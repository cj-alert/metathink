package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// InquiryAction is one proposed operation and its local scores. The scores do
// not assess a parent hypothesis, and neither score is a missing-value default.
type InquiryAction struct {
	Kind         string  `json:"kind"`
	InquiryID    int64   `json:"inquiry_id,omitempty"`
	TargetID     int64   `json:"target_id,omitempty"`
	Body         string  `json:"body"`
	Relevance    float64 `json:"relevance"`
	InquiryValue float64 `json:"inquiry_value"`
}

// ParseInquiryAction parses the three plain-text sections. Inquiry existence,
// membership and the meaning of the answer belong to the runner and its evidence;
// parsing cannot establish the quality or novelty of an otherwise valid action.
func ParseInquiryAction(text string) (InquiryAction, error) {
	sections := map[string][]string{}
	current := ""
	for index, original := range strings.Split(text, "\n") {
		line := strings.TrimSpace(original)
		if strings.HasPrefix(line, "#") {
			var name string
			switch line {
			case "# ACTION":
				name = "ACTION"
			case "# YOUR ANSWER":
				name = "YOUR ANSWER"
			case "# YOUR SCORE":
				name = "YOUR SCORE"
			default:
				return InquiryAction{}, fmt.Errorf("action line %d: unknown heading %q", index+1, line)
			}
			if _, exists := sections[name]; exists {
				return InquiryAction{}, fmt.Errorf("action line %d: duplicate heading %q", index+1, name)
			}
			sections[name], current = []string{}, name
			continue
		}
		if current == "" {
			if line != "" {
				return InquiryAction{}, fmt.Errorf("action line %d: expected a section heading", index+1)
			}
			continue
		}
		sections[current] = append(sections[current], original)
	}
	for _, name := range []string{"ACTION", "YOUR ANSWER", "YOUR SCORE"} {
		if _, exists := sections[name]; !exists {
			return InquiryAction{}, fmt.Errorf("action missing heading %q", name)
		}
	}

	command := ""
	for _, line := range sections["ACTION"] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if command != "" {
			return InquiryAction{}, fmt.Errorf("ACTION must contain exactly one command line")
		}
		command = line
	}
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return InquiryAction{}, fmt.Errorf("ACTION requires a command")
	}
	result := InquiryAction{Kind: parts[0]}
	arguments := 0
	switch result.Kind {
	case "PROPOSE", "PASS":
	case "JOIN", "CONTRIBUTE":
		arguments = 1
	case "CONNECT":
		arguments = 2
	default:
		return InquiryAction{}, fmt.Errorf("unknown action %q", result.Kind)
	}
	if len(parts) != arguments+1 {
		return InquiryAction{}, fmt.Errorf("%s requires %d inquiry IDs", result.Kind, arguments)
	}
	for index, value := range parts[1:] {
		for _, digit := range value {
			if digit < '0' || digit > '9' {
				return InquiryAction{}, fmt.Errorf("action inquiry ID %q must be a positive integer", value)
			}
		}
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return InquiryAction{}, fmt.Errorf("action inquiry ID %q must be a positive integer", value)
		}
		if index == 0 {
			result.InquiryID = id
		} else {
			result.TargetID = id
		}
	}
	result.Body = strings.TrimSpace(strings.Join(sections["YOUR ANSWER"], "\n"))
	if result.Body == "" {
		return InquiryAction{}, fmt.Errorf("YOUR ANSWER requires nonempty text")
	}

	scores := map[string]float64{}
	for _, original := range sections["YOUR SCORE"] {
		line := strings.TrimSpace(original)
		if line == "" {
			continue
		}
		name, value, found := strings.Cut(line, ":")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !found || (name != "relevance" && name != "inquiry_value") {
			return InquiryAction{}, fmt.Errorf("YOUR SCORE requires relevance: and inquiry_value: lines; unexpected %q", line)
		}
		if _, exists := scores[name]; exists {
			return InquiryAction{}, fmt.Errorf("duplicate action score %q", name)
		}
		score, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
			return InquiryAction{}, fmt.Errorf("action score %s must be one finite number in [0,1]", name)
		}
		scores[name] = score
	}
	for _, name := range []string{"relevance", "inquiry_value"} {
		if _, exists := scores[name]; !exists {
			return InquiryAction{}, fmt.Errorf("missing action score %q", name)
		}
	}
	result.Relevance, result.InquiryValue = scores["relevance"], scores["inquiry_value"]
	return result, nil
}
