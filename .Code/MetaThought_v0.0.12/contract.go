package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Contract is parsed from the user's reviewed text, without configuration
// defaults. Targets describe desired outcomes; they are not actual scores.
type Contract struct {
	Objective string              `json:"objective"`
	Intent    string              `json:"intent"`
	Context   string              `json:"context"`
	Return    string              `json:"return"`
	Criteria  []ContractCriterion `json:"criteria"`
}

type ContractCriterion struct {
	Name    string       `json:"name"`
	Meaning string       `json:"meaning"`
	Zero    string       `json:"zero"`
	One     string       `json:"one"`
	Target  *ScoreTarget `json:"target,omitempty"`
	Weight  *float64     `json:"weight,omitempty"`
}

type ScoreTarget struct {
	Operator string  `json:"operator"`
	Value    float64 `json:"value"`
}

// Met reports only whether this criterion's target is met. It does not decide
// whether an assessment is complete, combine scores, or trigger queen collapse.
func (target ScoreTarget) Met(score float64) bool {
	if !contractUnitValue(score) || !contractUnitValue(target.Value) {
		return false
	}
	switch target.Operator {
	case "=":
		return score == target.Value
	case ">":
		return score > target.Value
	case ">=":
		return score >= target.Value
	case "<":
		return score < target.Value
	case "<=":
		return score <= target.Value
	default:
		return false
	}
}

// ContractFormat describes the plain-text protocol for configurable drafting
// prompts. Criterion order is preserved; optional targets/weights stay absent.
func ContractFormat() string {
	return "Use plain text with # OBJECTIVE, # INTENT, # CONTEXT, # RETURN, and # CRITERIA headings. Give the first four a nonempty free-text body. Under # CRITERIA, put each criterion in a ## name block with Meaning:, Zero:, One:, Target:, and Weight: on separate single lines. Meaning defines what is assessed; Zero and One define the score endpoints. Target is none or an exact comparison (=, >, >=, <, <=) followed by a finite number in [0,1]. Weight is none or a finite nonnegative number. Criterion names must be unique. Do not supply actual outcome scores in this draft."
}

// ParseContract accepts the reviewed contract as the sole authority. Unknown or
// duplicate structure is rejected rather than silently omitted or overwritten.
func ParseContract(text string) (Contract, error) {
	var contract Contract
	sections := map[string][]string{}
	seenSections := map[string]bool{}
	var section string
	var criterion *ContractCriterion
	var fields map[string]bool
	lines := strings.Split(strings.TrimPrefix(text, "\uFEFF"), "\n")
	for index, original := range lines {
		line := strings.TrimSuffix(original, "\r")
		trimmed := strings.TrimSpace(line)
		lineError := func(message string) error {
			return fmt.Errorf("contract line %d: %s", index+1, message)
		}
		if strings.HasPrefix(trimmed, "# ") {
			name := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(trimmed, "# ")))
			switch name {
			case "OBJECTIVE", "INTENT", "CONTEXT", "RETURN", "CRITERIA":
			default:
				return contract, lineError("unknown section " + name)
			}
			if seenSections[name] {
				return contract, lineError("duplicate section " + name)
			}
			seenSections[name] = true
			section, criterion = name, nil
			continue
		}
		if section == "" {
			if trimmed != "" {
				return contract, lineError("content must follow a contract section heading")
			}
			continue
		}
		if section != "CRITERIA" {
			sections[section] = append(sections[section], line)
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			name := strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			if name == "" {
				return contract, lineError("criterion name is required")
			}
			for _, existing := range contract.Criteria {
				if strings.EqualFold(existing.Name, name) {
					return contract, lineError("duplicate criterion " + name)
				}
			}
			contract.Criteria = append(contract.Criteria, ContractCriterion{Name: name})
			criterion = &contract.Criteria[len(contract.Criteria)-1]
			fields = map[string]bool{}
			continue
		}
		if trimmed == "" {
			continue
		}
		if criterion == nil {
			return contract, lineError("criterion fields must follow a ## name heading")
		}
		key, value, found := strings.Cut(trimmed, ":")
		key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)
		if !found {
			return contract, lineError("criterion fields must use Field: value on one line")
		}
		if fields[key] {
			return contract, lineError("duplicate field " + key + " for " + criterion.Name)
		}
		fields[key] = true
		switch key {
		case "meaning":
			criterion.Meaning = value
		case "zero":
			criterion.Zero = value
		case "one":
			criterion.One = value
		case "target":
			target, err := parseContractTarget(value)
			if err != nil {
				return contract, lineError(err.Error())
			}
			criterion.Target = target
		case "weight":
			if strings.EqualFold(value, "none") {
				continue
			}
			weight, err := strconv.ParseFloat(value, 64)
			if err != nil || math.IsNaN(weight) || math.IsInf(weight, 0) || weight < 0 {
				return contract, lineError("weight must be none or a finite nonnegative number")
			}
			criterion.Weight = &weight
		default:
			return contract, lineError("unknown criterion field " + key)
		}
	}
	for _, name := range []string{"OBJECTIVE", "INTENT", "CONTEXT", "RETURN", "CRITERIA"} {
		if !seenSections[name] {
			return contract, fmt.Errorf("contract requires # %s", name)
		}
	}
	contract.Objective = strings.TrimSpace(strings.Join(sections["OBJECTIVE"], "\n"))
	contract.Intent = strings.TrimSpace(strings.Join(sections["INTENT"], "\n"))
	contract.Context = strings.TrimSpace(strings.Join(sections["CONTEXT"], "\n"))
	contract.Return = strings.TrimSpace(strings.Join(sections["RETURN"], "\n"))
	for _, name := range []string{"OBJECTIVE", "INTENT", "CONTEXT", "RETURN"} {
		if strings.TrimSpace(strings.Join(sections[name], "\n")) == "" {
			return contract, fmt.Errorf("contract section %s requires a body", name)
		}
	}
	if len(contract.Criteria) == 0 {
		return contract, fmt.Errorf("contract requires at least one named criterion")
	}
	for _, criterion := range contract.Criteria {
		if criterion.Meaning == "" || criterion.Zero == "" || criterion.One == "" {
			return contract, fmt.Errorf("contract criterion %q requires Meaning, Zero, and One", criterion.Name)
		}
	}
	return contract, nil
}

func parseContractTarget(text string) (*ScoreTarget, error) {
	if strings.EqualFold(text, "none") {
		return nil, nil
	}
	for _, operator := range []string{">=", "<=", "=", ">", "<"} {
		if strings.HasPrefix(text, operator) {
			value, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(text, operator)), 64)
			if err != nil || !contractUnitValue(value) {
				return nil, fmt.Errorf("target comparison requires a finite number in [0,1]")
			}
			return &ScoreTarget{Operator: operator, Value: value}, nil
		}
	}
	return nil, fmt.Errorf("target must be none or a comparison (=, >, >=, <, <=) followed by a number in [0,1]")
}

func contractUnitValue(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}
