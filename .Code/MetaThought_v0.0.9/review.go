package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// QueenReview is a sufficiency judgment, never another numerical grade.
type QueenReview struct {
	Decision             string  `json:"decision"`
	SelectedCandidateIDs []int64 `json:"selected_candidate_ids,omitempty"`
	Criteria             string  `json:"criteria_status"`
	Reason               string  `json:"reason"`
	answer               string
}

func parseQueenReview(text string) (QueenReview, error) {
	fields := map[string]string{}
	current := ""
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			current = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			if current != "DECISION" && current != "CANDIDATES" && current != "CRITERIA" && current != "REASON" {
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
	r := QueenReview{Decision: fields["DECISION"], SelectedCandidateIDs: []int64{}, Criteria: fields["CRITERIA"], Reason: fields["REASON"]}
	if r.Decision != "RETURN" && r.Decision != "CONTINUE" {
		return r, fmt.Errorf("review requires RETURN or CONTINUE")
	}
	if r.Criteria != "MET" && r.Criteria != "UNMET" && r.Criteria != "UNCERTAIN" {
		return r, fmt.Errorf("review requires MET, UNMET or UNCERTAIN")
	}
	if r.Reason == "" {
		return r, fmt.Errorf("review requires a reason")
	}
	if r.Decision == "CONTINUE" {
		if fields["CANDIDATES"] != "NONE" {
			return r, fmt.Errorf("CONTINUE review requires CANDIDATES to be NONE")
		}
		return r, nil
	}
	for _, value := range strings.Split(fields["CANDIDATES"], ",") {
		candidateID, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || candidateID <= 0 {
			return r, fmt.Errorf("RETURN review requires positive saved candidate IDs separated by commas")
		}
		for _, existing := range r.SelectedCandidateIDs {
			if existing == candidateID {
				return r, fmt.Errorf("RETURN review repeats candidate %d", candidateID)
			}
		}
		r.SelectedCandidateIDs = append(r.SelectedCandidateIDs, candidateID)
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
	reviews, err := h.query(ctx, "review_history", "hive_reviews", "SELECT candidate_id,selected_candidate_ids,tick,decision,criteria_status,reason FROM hive_reviews WHERE request_id=? ORDER BY id", h.requestID)
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
	// Methods may evolve the behavioral instruction. The separately configured
	// reply format remains attached to the owning return parser.
	source := h.Config.Prompts["collapse"] + "\n\n" + h.Config.Prompts["collapse_format"]
	text, err := h.generatePrompt(ctx, "queen", h.Config.Models.Queen, "collapse", source, input, "")
	if err != nil {
		return QueenReview{}, err
	}
	review, err := parseQueenReview(text)
	if err != nil {
		h.Log.Log("queen", "review_rejected", map[string]any{"candidate_id": candidateID, "outcome": "format_error", "error": err.Error(), "response": text})
		return review, err
	}
	if review.Decision == "RETURN" {
		review.answer, err = h.selectedAnswers(ctx, h.requestID, review.SelectedCandidateIDs)
		if err != nil {
			h.Log.Log("queen", "review_rejected", map[string]any{"candidate_id": candidateID, "outcome": "invalid_selection", "error": err.Error(), "response": text})
			return review, err
		}
	}
	err = h.Store.Transaction(ctx, h.Log.Fields, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
		if _, err := exec("review", "hive_reviews", "INSERT INTO hive_reviews(request_id,candidate_id,selected_candidate_ids,tick,decision,criteria_status,reason,created) VALUES(?,?,?,?,?,?,?,?)",
			h.requestID, candidateID, jsonText(review.SelectedCandidateIDs), tick, review.Decision, review.Criteria, review.Reason, now()); err != nil {
			return err
		}
		if review.Decision == "RETURN" {
			_, err := exec("return", "hive_requests", "UPDATE hive_requests SET status='returned',result=?,finish_reason=?,criteria_met=? WHERE id=?",
				review.answer, "queen_return", review.Criteria == "MET", h.requestID)
			return err
		}
		return nil
	})
	if err != nil {
		return review, err
	}
	h.Log.Log("queen", "critical_mass_judgment", map[string]any{"candidate_id": candidateID, "selected_candidate_ids": review.SelectedCandidateIDs, "decision": review.Decision, "criteria_status": review.Criteria, "reason": review.Reason})
	return review, nil
}

// selectedAnswers binds a return to ordinary saved candidate identities. One
// answer is returned verbatim; multiple answers retain their exact bodies under
// deterministic candidate headings, in the Queen's selected order.
func (h *Hive) selectedAnswers(ctx context.Context, requestID int64, candidateIDs []int64) (string, error) {
	if len(candidateIDs) == 0 {
		return "", fmt.Errorf("saved RETURN has no explicit selected candidate IDs; legacy selection cannot be inferred")
	}
	seen := map[int64]bool{}
	bodies := make([]string, 0, len(candidateIDs))
	for _, candidateID := range candidateIDs {
		if candidateID <= 0 || seen[candidateID] {
			return "", fmt.Errorf("invalid or duplicate selected candidate ID %d", candidateID)
		}
		seen[candidateID] = true
		// A parsed score vector is written atomically. Its presence supplies
		// assessment evidence without imposing a reviewer count or score gate.
		rows, err := h.query(ctx, "selected_candidate", "hive_contributions,hive_assessments", "SELECT body FROM hive_contributions c WHERE request_id=? AND id=? AND kind='candidate' AND EXISTS (SELECT 1 FROM hive_assessments a WHERE a.request_id=c.request_id AND a.candidate_id=c.id)", requestID, candidateID)
		if err != nil {
			return "", err
		}
		if len(rows) != 1 {
			return "", fmt.Errorf("selected candidate %d is not a saved assessed candidate for request %d", candidateID, requestID)
		}
		bodies = append(bodies, str(rows[0]["body"]))
	}
	if len(bodies) == 1 {
		return bodies[0], nil
	}
	var answer strings.Builder
	for index, body := range bodies {
		if index > 0 {
			answer.WriteString("\n\n")
		}
		fmt.Fprintf(&answer, "# Candidate %d\n\n", candidateIDs[index])
		answer.WriteString(body)
	}
	return answer.String(), nil
}

// recoverReturn reads a durable judgment before scheduling any new inquiry.
// Legacy returns without explicit selected IDs fail rather than guessing. The
// recovery itself makes no model call and never changes the selected answers.
func (h *Hive) recoverReturn(ctx context.Context, requestID int64) (HiveResult, bool, error) {
	rows, err := h.query(ctx, "saved_return", "hive_reviews",
		"SELECT q.status,q.team_id,r.selected_candidate_ids,r.criteria_status,r.reason FROM hive_requests q JOIN hive_reviews r ON r.request_id=q.id WHERE q.id=? AND r.decision='RETURN' ORDER BY r.id DESC LIMIT 1", requestID)
	if err != nil {
		return HiveResult{}, false, fmt.Errorf("read durable return: %w", err)
	}
	if len(rows) == 0 {
		return HiveResult{}, false, nil
	}
	row := rows[0]
	var selected []int64
	if err := json.Unmarshal([]byte(str(row["selected_candidate_ids"])), &selected); err != nil {
		return HiveResult{}, true, fmt.Errorf("saved RETURN selected candidate IDs: %w", err)
	}
	answer, err := h.selectedAnswers(ctx, requestID, selected)
	if err != nil {
		return HiveResult{}, true, err
	}
	criteriaStatus, reason := str(row["criteria_status"]), str(row["reason"])
	if (criteriaStatus != "MET" && criteriaStatus != "UNMET" && criteriaStatus != "UNCERTAIN") || strings.TrimSpace(reason) == "" {
		return HiveResult{}, true, fmt.Errorf("saved RETURN has incomplete criteria status or rationale")
	}
	latestRuns, err := h.query(ctx, "latest_return_run", "hive_runs", "SELECT id,status FROM hive_runs WHERE request_id=? ORDER BY id DESC LIMIT 1", requestID)
	if err != nil {
		return HiveResult{}, true, err
	}
	unfinishedRun := len(latestRuns) != 0 && str(latestRuns[0]["status"]) == "running"
	if str(row["status"]) != "returned" || unfinishedRun {
		if err := h.Store.Transaction(ctx, h.Log.Fields, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
			if str(row["status"]) != "returned" {
				if _, err := exec("recover_return", "hive_requests", "UPDATE hive_requests SET status='returned',result=?,finish_reason=?,criteria_met=? WHERE id=?", answer, "queen_return", criteriaStatus == "MET", requestID); err != nil {
					return err
				}
			}
			if unfinishedRun {
				// Stored call counts may be incomplete after a crash. Preserve
				// those measurements instead of inventing missing usage.
				_, err := exec("recover_return_run", "hive_runs", "UPDATE hive_runs SET status='completed',finished=? WHERE id=? AND status='running'", now(), id(latestRuns[0]["id"]))
				return err
			}
			return nil
		}); err != nil {
			return HiveResult{}, true, err
		}
	}
	result := HiveResult{Status: "returned", RequestID: requestID, TeamID: id(row["team_id"]), Reason: "queen_return",
		Answer: answer, SelectedCandidateIDs: selected, CriteriaStatus: criteriaStatus, CriteriaMet: criteriaStatus == "MET", Rationale: reason}
	h.Log.Log("queen", "return_recovered", map[string]any{"request_id": requestID, "selected_candidate_ids": selected, "criteria_status": criteriaStatus, "outcome": "returned"})
	return result, true, nil
}
