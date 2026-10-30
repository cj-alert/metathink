package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// CriterionAssessment is one drone's judgment of one declared criterion.
// Targets, weights and other drones' scores never replace this actual score.
type CriterionAssessment struct {
	Name        string  `json:"name"`
	Score       float64 `json:"score"`
	Incomplete  bool    `json:"incomplete,omitempty"`
	Reason      string  `json:"reason,omitempty"`
	Evidence    string  `json:"evidence"`
	Uncertainty string  `json:"uncertainty"`
}

// ScoreValue preserves a scored zero while representing a missing assessment
// as null at the model/UI boundary. Legacy numeric storage remains unchanged.
func (a CriterionAssessment) ScoreValue() any {
	if a.Incomplete {
		return nil
	}
	return a.Score
}

func (a CriterionAssessment) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name        string `json:"name"`
		Score       any    `json:"score"`
		Incomplete  bool   `json:"incomplete,omitempty"`
		Reason      string `json:"reason,omitempty"`
		Evidence    string `json:"evidence"`
		Uncertainty string `json:"uncertainty"`
	}{a.Name, a.ScoreValue(), a.Incomplete, a.Reason, a.Evidence, a.Uncertainty})
}

// ParseAssessment accepts one # exact-criterion-name section per criterion,
// containing numeric Score:, Evidence: and Uncertainty: fields. Reason: is
// supported but optional for legacy replies. Text fields may continue onto
// subsequent lines. Indent a continuation that
// contains a colon so it cannot be mistaken for an additional field.
// Incomplete or invalid replies return an error; numeric-table callers cannot
// accidentally persist a missing assessment as zero.
func ParseAssessment(text string, criteria []ContractCriterion) ([]CriterionAssessment, error) {
	return parseCriterionAssessments(text, criteria, false, false)
}

// Worker assessment records can explicitly retain a missing prerequisite.
// All declared sections are still required; INCOMPLETE is a stated result,
// never a default for a missing or malformed score. New live worker replies
// require Reason; old saved numeric replies remain readable without it.
func parseCriterionAssessments(text string, criteria []ContractCriterion, allowIncomplete, requireReason bool) ([]CriterionAssessment, error) {
	if len(criteria) == 0 {
		return nil, fmt.Errorf("assessment requires declared criteria")
	}
	expected := make(map[string]bool, len(criteria))
	for _, criterion := range criteria {
		if criterion.Name == "" || strings.TrimSpace(criterion.Name) != criterion.Name {
			return nil, fmt.Errorf("assessment criterion has an empty or untrimmed name")
		}
		if expected[criterion.Name] {
			return nil, fmt.Errorf("assessment criterion %q is declared twice", criterion.Name)
		}
		expected[criterion.Name] = true
	}

	type section struct {
		assessment CriterionAssessment
		fields     map[string]bool
		active     string
	}
	sections := make(map[string]*section, len(criteria))
	var current *section
	for index, original := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(original)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "# ") {
			name := strings.TrimSpace(strings.TrimPrefix(line, "# "))
			if !expected[name] {
				return nil, fmt.Errorf("assessment line %d: unknown criterion %q", index+1, name)
			}
			if sections[name] != nil {
				return nil, fmt.Errorf("assessment line %d: duplicate criterion %q", index+1, name)
			}
			current = &section{assessment: CriterionAssessment{Name: name}, fields: map[string]bool{}}
			sections[name] = current
			continue
		}
		if strings.HasPrefix(line, "#") {
			return nil, fmt.Errorf("assessment line %d: expected # followed by an exact criterion name", index+1)
		}
		if current == nil {
			return nil, fmt.Errorf("assessment line %d: expected a criterion heading", index+1)
		}

		label, value, hasColon := strings.Cut(line, ":")
		knownField := hasColon && (label == "Score" || label == "Reason" || label == "Evidence" || label == "Uncertainty")
		if knownField {
			if current.fields[label] {
				return nil, fmt.Errorf("assessment criterion %q: duplicate %s field", current.assessment.Name, label)
			}
			current.fields[label], current.active = true, label
			value = strings.TrimSpace(value)
			switch label {
			case "Score":
				if allowIncomplete && value == "INCOMPLETE" {
					current.assessment.Incomplete = true
					break
				}
				score, err := strconv.ParseFloat(value, 64)
				if err != nil || math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
					return nil, fmt.Errorf("assessment criterion %q: Score must be one finite number in [0,1]", current.assessment.Name)
				}
				current.assessment.Score = score
			case "Reason":
				current.assessment.Reason = value
			case "Evidence":
				current.assessment.Evidence = value
			case "Uncertainty":
				current.assessment.Uncertainty = value
			}
			continue
		}
		indented := strings.HasPrefix(original, " ") || strings.HasPrefix(original, "\t")
		if hasColon && !indented {
			return nil, fmt.Errorf("assessment line %d: unknown field %q", index+1, label)
		}
		switch current.active {
		case "Reason":
			current.assessment.Reason = strings.TrimSpace(current.assessment.Reason + "\n" + line)
		case "Evidence":
			current.assessment.Evidence = strings.TrimSpace(current.assessment.Evidence + "\n" + line)
		case "Uncertainty":
			current.assessment.Uncertainty = strings.TrimSpace(current.assessment.Uncertainty + "\n" + line)
		default:
			return nil, fmt.Errorf("assessment line %d: expected Score, Reason, Evidence or Uncertainty", index+1)
		}
	}

	assessments := make([]CriterionAssessment, 0, len(criteria))
	for _, criterion := range criteria {
		entry := sections[criterion.Name]
		if entry == nil {
			return nil, fmt.Errorf("assessment missing criterion %q", criterion.Name)
		}
		for _, field := range []string{"Score", "Evidence", "Uncertainty"} {
			if !entry.fields[field] {
				return nil, fmt.Errorf("assessment criterion %q: missing %s", criterion.Name, field)
			}
		}
		if entry.assessment.Evidence == "" || entry.assessment.Uncertainty == "" {
			return nil, fmt.Errorf("assessment criterion %q: Evidence and Uncertainty must be explicit nonempty text", criterion.Name)
		}
		if (requireReason || entry.assessment.Incomplete || entry.fields["Reason"]) && entry.assessment.Reason == "" {
			return nil, fmt.Errorf("assessment criterion %q: Reason must be explicit nonempty text", criterion.Name)
		}
		assessments = append(assessments, entry.assessment)
	}
	return assessments, nil
}

// assessCandidate asks each committed drone for its own complete assessment.
// Earlier assessment rows remain intact if a later call or parse fails. No
// averaging, voting, target gate, retry, or evaluator-of-evaluator call occurs.
func (h *Hive) assessCandidate(ctx context.Context, contract string, candidateID, teamID int64, tick int, members []map[string]any) error {
	if len(h.Contract.Criteria) == 0 {
		return fmt.Errorf("candidate assessment requires confirmed criteria")
	}
	previousDrone, hadDrone := h.Log.Fields["drone_id"]
	previousCandidate, hadCandidate := h.Log.Fields["candidate_id"]
	h.Log.Fields["candidate_id"] = candidateID
	defer func() {
		if hadDrone {
			h.Log.Fields["drone_id"] = previousDrone
		} else {
			delete(h.Log.Fields, "drone_id")
		}
		if hadCandidate {
			h.Log.Fields["candidate_id"] = previousCandidate
		} else {
			delete(h.Log.Fields, "candidate_id")
		}
	}()
	candidates, err := h.query(ctx, "assessment_subject", "hive_contributions",
		"SELECT body FROM hive_contributions WHERE id=? AND request_id=? AND kind='candidate'", candidateID, h.requestID)
	if err != nil {
		return err
	}
	if len(candidates) != 1 {
		return fmt.Errorf("candidate %d does not belong to request %d", candidateID, h.requestID)
	}
	for _, member := range members {
		droneID := id(member["id"])
		h.Log.Fields["drone_id"] = droneID
		operationalContext, err := h.context(ctx, contract, droneID)
		if err != nil {
			return err
		}
		input := jsonText(map[string]any{
			"candidate_id": candidateID, "candidate": str(candidates[0]["body"]),
			"scoring_elements":    h.Contract.Criteria,
			"operational_context": json.RawMessage(operationalContext),
		})
		reply, err := h.generate(ctx, "drone", h.Config.Models.Drone, "assess", input, "")
		if err != nil {
			return err
		}
		assessments, parseErr := ParseAssessment(reply, h.Contract.Criteria)
		fields := map[string]any{
			"candidate_id": candidateID, "team_id": teamID, "drone_id": droneID, "tick": tick,
			"reply": reply, "criterion_count": len(h.Contract.Criteria), "outcome": "parsed",
		}
		if parseErr != nil {
			fields["outcome"], fields["error"] = "error", parseErr.Error()
		}
		h.Log.Log("drone", "assessment_parse", fields)
		if parseErr != nil {
			return fmt.Errorf("drone %d candidate %d: %w", droneID, candidateID, parseErr)
		}
		if err := h.Store.Transaction(ctx, h.Log.Fields, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
			for _, assessment := range assessments {
				if _, err := exec("assess", "hive_assessments",
					"INSERT INTO hive_assessments(request_id,team_id,drone_id,candidate_id,tick,criterion,score,evidence,uncertainty,created) VALUES(?,?,?,?,?,?,?,?,?,?)",
					h.requestID, teamID, droneID, candidateID, tick, assessment.Name, assessment.Score, assessment.Evidence, assessment.Uncertainty, now()); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
