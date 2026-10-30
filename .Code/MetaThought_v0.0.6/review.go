package main

import (
	"context"
	"fmt"
	"strings"
)

// QueenReview is a sufficiency judgment, never another numerical grade.
type QueenReview struct {
	Decision string `json:"decision"`
	Criteria string `json:"criteria_status"`
	Reason   string `json:"reason"`
}

func parseQueenReview(text string) (QueenReview, error) {
	fields := map[string]string{}
	current := ""
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			current = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			if current != "DECISION" && current != "CRITERIA" && current != "REASON" {
				return QueenReview{}, fmt.Errorf("unknown review heading %q", current)
			}
			if _, exists := fields[current]; exists {
				return QueenReview{}, fmt.Errorf("duplicate review heading %s", current)
			}
			fields[current] = ""
		} else if line != "" {
			if current == "" {
				return QueenReview{}, fmt.Errorf("review text before first heading")
			}
			if fields[current] != "" {
				fields[current] += "\n"
			}
			fields[current] += line
		}
	}
	r := QueenReview{Decision: fields["DECISION"], Criteria: fields["CRITERIA"], Reason: fields["REASON"]}
	if r.Decision != "RETURN" && r.Decision != "CONTINUE" {
		return r, fmt.Errorf("review requires RETURN or CONTINUE")
	}
	if r.Criteria != "MET" && r.Criteria != "UNMET" && r.Criteria != "UNCERTAIN" {
		return r, fmt.Errorf("review requires MET, UNMET or UNCERTAIN")
	}
	if r.Reason == "" {
		return r, fmt.Errorf("review requires a reason")
	}
	return r, nil
}

func (h *Hive) queenEvidence(ctx context.Context, contract string, candidateID int64) (string, error) {
	work, err := h.query(ctx, "review_inquiry", "hive_contributions", "SELECT id,team_id,drone_id,tick,kind,body FROM hive_contributions WHERE request_id=? ORDER BY id", h.requestID)
	if err != nil {
		return "", err
	}
	assessments, err := h.query(ctx, "review_assessments", "hive_assessments", "SELECT id,team_id,drone_id,candidate_id,tick,criterion,score,evidence,uncertainty FROM hive_assessments WHERE request_id=? ORDER BY id", h.requestID)
	if err != nil {
		return "", err
	}
	// Exact target comparisons annotate each observation; they neither aggregate
	// opinions nor determine whether exploration continues.
	for _, assessment := range assessments {
		for _, criterion := range h.Contract.Criteria {
			if criterion.Name != str(assessment["criterion"]) {
				continue
			}
			if criterion.Target != nil {
				if score, ok := assessment["score"].(float64); ok {
					assessment["target_met"] = criterion.Target.Met(score)
				}
			}
		}
	}
	offers, err := h.query(ctx, "review_offers", "hive_links", "SELECT l.to_drone,l.from_drone,c.id AS contribution_id,c.request_id,c.kind,c.body FROM hive_links l JOIN hive_contributions c ON c.id=l.contribution_id WHERE l.to_drone IN (SELECT drone_id FROM hive_commitments WHERE request_id=?) ORDER BY l.id", h.requestID)
	if err != nil {
		return "", err
	}
	reviews, err := h.query(ctx, "review_history", "hive_reviews", "SELECT candidate_id,tick,decision,criteria_status,reason FROM hive_reviews WHERE request_id=? ORDER BY id", h.requestID)
	if err != nil {
		return "", err
	}
	guidance, err := h.query(ctx, "review_guidance", "hive_guidance", "SELECT tick,body FROM hive_guidance WHERE request_id=? ORDER BY id", h.requestID)
	if err != nil {
		return "", err
	}
	return jsonText(map[string]any{"contract": h.Contract, "current_candidate_id": candidateID, "inquiry": work, "received_offers": offers, "drone_assessments": assessments, "queen_reviews": reviews, "guidance": guidance}), nil
}

func (h *Hive) reviewCandidate(ctx context.Context, contract string, candidateID int64, tick int) (QueenReview, error) {
	input, err := h.queenEvidence(ctx, contract, candidateID)
	if err != nil {
		return QueenReview{}, err
	}
	text, err := h.generate(ctx, "queen", h.Config.Models.Queen, "collapse", input, "")
	if err != nil {
		return QueenReview{}, err
	}
	review, err := parseQueenReview(text)
	if err != nil {
		h.Log.Log("queen", "review_rejected", map[string]any{"candidate_id": candidateID, "outcome": "format_error", "error": err.Error(), "response": text})
		return review, err
	}
	_, err = h.exec(ctx, "review", "hive_reviews", "INSERT INTO hive_reviews(request_id,candidate_id,tick,decision,criteria_status,reason,created) VALUES(?,?,?,?,?,?,?)", h.requestID, candidateID, tick, review.Decision, review.Criteria, review.Reason, now())
	if err != nil {
		return review, err
	}
	h.Log.Log("queen", "critical_mass_judgment", map[string]any{"candidate_id": candidateID, "decision": review.Decision, "criteria_status": review.Criteria, "reason": review.Reason})
	return review, nil
}
