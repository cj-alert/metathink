package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// WorkerResourceAction selects inquiries for a temporary newborn-menu omission.
// Its scores describe the Queen's decision, not a parent hypothesis.
type WorkerResourceAction struct {
	Kind         string  `json:"kind"`
	InquiryIDs   []int64 `json:"inquiry_ids"`
	Body         string  `json:"body"`
	Relevance    float64 `json:"relevance"`
	InquiryValue float64 `json:"inquiry_value"`
}

func ParseWorkerResourceAction(text string) (WorkerResourceAction, error) {
	lines := strings.Split(text, "\n")
	arities := map[string]int{"SELECT": 0, "CLEAR": 0}
	line, command, plain := workerPlainActionStart(lines, arities)
	if !plain {
		return WorkerResourceAction{}, errors.New("resource review requires a plain SELECT or CLEAR action")
	}
	parts := strings.Fields(command)
	for index := range parts {
		parts[index] = workerUnwrapControlText(parts[index])
	}
	if len(parts) == 0 || (parts[0] != "SELECT" && parts[0] != "CLEAR") {
		return WorkerResourceAction{}, errors.New("resource review action must be SELECT or CLEAR")
	}
	if (parts[0] == "SELECT" && len(parts) < 2) || (parts[0] == "CLEAR" && len(parts) != 1) {
		return WorkerResourceAction{}, errors.New("SELECT requires inquiry IDs; CLEAR takes none")
	}
	ids := []int64{}
	seen := map[int64]bool{}
	for _, value := range parts[1:] {
		for _, char := range value {
			if char < '0' || char > '9' {
				return WorkerResourceAction{}, fmt.Errorf("resource inquiry ID %q must be a positive integer", value)
			}
		}
		inquiryID, err := strconv.ParseInt(value, 10, 64)
		if err != nil || inquiryID <= 0 || seen[inquiryID] {
			return WorkerResourceAction{}, fmt.Errorf("resource inquiry ID %q must be positive and distinct", value)
		}
		seen[inquiryID] = true
		ids = append(ids, inquiryID)
	}
	// Only the command's variable-length ID list is extracted here. The common
	// parser still validates the original body, duplicate controls and scores.
	lines[line] = "ACTION: " + parts[0]
	parsed, err := parseScoredWorkerAction(strings.Join(lines, "\n"), arities)
	if err != nil {
		return WorkerResourceAction{}, err
	}
	return WorkerResourceAction{Kind: parsed.Kind, InquiryIDs: ids, Body: parsed.Body,
		Relevance: parsed.Relevance, InquiryValue: parsed.InquiryValue}, nil
}

// The expression is supplied only by the two internal scheduling queries.
// Both count committed SPAWN records, never attempts or model calls.
func workerResourceDueSQL(requestExpression string) string {
	return `(? > 0 AND (SELECT COUNT(*) FROM hive_inquiry_work rw JOIN hive_inquiries ri ON ri.id=rw.inquiry_id
 WHERE ri.request_id=` + requestExpression + ` AND rw.kind='SPAWN')-
 COALESCE((SELECT births_reviewed FROM hive_worker_resources rr WHERE rr.request_id=` + requestExpression + `),0)>=?)`
}

func (h *Hive) workerResourceInput(ctx context.Context) (string, int64, map[int64]bool, error) {
	rows, err := h.query(ctx, "resource_snapshot", "hive_worker_resources", `WITH request_inquiries AS (
 SELECT id,question FROM hive_inquiries WHERE request_id=?
 ), births AS (
 SELECT COUNT(*) AS total FROM hive_inquiry_work w JOIN request_inquiries i ON i.id=w.inquiry_id WHERE w.kind='SPAWN'
 ) SELECT r.contract,i.id,i.question,(SELECT MIN(id) FROM request_inquiries) AS root_id,b.total AS successful_births,
 COALESCE(s.births_reviewed,0) AS births_reviewed,COALESCE(s.selected_ids,'[]') AS selected_ids,
 COALESCE(s.note,'') AS prior_note,COALESCE(s.relevance,0) AS prior_relevance,
 COALESCE(s.inquiry_value,0) AS prior_inquiry_value,COALESCE(s.created,'') AS prior_created,
 (SELECT COUNT(*) FROM hive_inquiry_members m WHERE m.inquiry_id=i.id) AS committed_member_count,
 (SELECT COUNT(*) FROM hive_inquiry_members m WHERE m.inquiry_id=i.id AND EXISTS(
 SELECT 1 FROM hive_inquiry_work w WHERE w.inquiry_id=i.id AND w.drone_id=m.drone_id AND w.kind IN ('PROPOSE','JOIN','CONTRIBUTE'))) AS contributing_member_count,
 (SELECT COUNT(*) FROM hive_inquiry_work w WHERE w.inquiry_id=i.id) AS work_record_count,
 (SELECT COUNT(*) FROM hive_inquiry_work w WHERE w.inquiry_id=i.id AND w.created>COALESCE(s.created,'')) AS work_since_review,
 latest.id AS latest_id,latest.drone_id AS latest_drone,latest.kind AS latest_kind,latest.body AS latest_body,latest.created AS latest_created
 FROM hive_requests r CROSS JOIN request_inquiries i CROSS JOIN births b
 LEFT JOIN hive_worker_resources s ON s.request_id=r.id
 LEFT JOIN hive_inquiry_work latest ON latest.id=(SELECT MAX(w.id) FROM hive_inquiry_work w
 WHERE w.inquiry_id=i.id AND w.kind IN ('PROPOSE','JOIN','CONTRIBUTE','CONNECT'))
 WHERE r.id=? ORDER BY i.id`, h.requestID, h.requestID)
	if err != nil {
		return "", 0, nil, err
	}
	if len(rows) == 0 {
		return "", 0, nil, errors.New("resource review request or inquiries missing")
	}
	revision, err := h.currentWorkerRevision(ctx, h.requestID)
	if err != nil {
		return "", 0, nil, err
	}
	first := rows[0]
	var selected []int64
	if err := json.Unmarshal([]byte(str(first["selected_ids"])), &selected); err != nil {
		return "", 0, nil, fmt.Errorf("saved resource selection: %w", err)
	}
	var prior map[string]any
	if str(first["prior_created"]) != "" {
		prior = map[string]any{"selected_ids": selected, "note": first["prior_note"],
			"relevance": first["prior_relevance"], "inquiry_value": first["prior_inquiry_value"], "created": first["prior_created"]}
	}
	inquiries := []map[string]any{}
	eligible := map[int64]bool{}
	for _, row := range rows {
		inquiryID := id(row["id"])
		if inquiryID != id(row["root_id"]) {
			eligible[inquiryID] = true
		}
		var latest map[string]any
		if row["latest_id"] != nil {
			latest = map[string]any{"id": row["latest_id"], "drone_id": row["latest_drone"], "kind": row["latest_kind"], "body": row["latest_body"], "created": row["latest_created"]}
		}
		inquiries = append(inquiries, map[string]any{"id": inquiryID,
			"question_first_line":    strings.SplitN(str(row["question"]), "\n", 2)[0],
			"committed_member_count": row["committed_member_count"], "contributing_member_count": row["contributing_member_count"],
			"work_record_count": row["work_record_count"], "work_since_review": row["work_since_review"], "latest_work": latest})
	}
	births := id(first["successful_births"])
	return jsonText(map[string]any{"purpose": "resource_review", "handoff": first["contract"], "contract_revision": revision,
		"root_inquiry_id":   first["root_id"],
		"successful_births": births, "births_reviewed": first["births_reviewed"],
		"prior_selection": prior,
		"inquiries":       inquiries}), births, eligible, nil
}

// Called only while holding the ordinary Queen owner and its model slot.
func (h *Hive) reviewWorkerResources(ctx context.Context, owner string) (bool, WorkerQueenResult, error) {
	result := WorkerQueenResult{}
	every := h.Config.Workers.ResourceReviewEvery
	if every == 0 {
		return false, result, nil
	}
	rows, err := h.query(ctx, "resource_due", "hive_worker_resources", `SELECT
 (SELECT COUNT(*) FROM hive_inquiry_work w JOIN hive_inquiries i ON i.id=w.inquiry_id WHERE i.request_id=? AND w.kind='SPAWN')-
 COALESCE((SELECT births_reviewed FROM hive_worker_resources WHERE request_id=?),0) AS pending_births`, h.requestID, h.requestID)
	if err != nil {
		return false, result, err
	}
	if id(rows[0]["pending_births"]) < int64(every) {
		return false, result, nil
	}
	input, births, eligible, err := h.workerResourceInput(ctx)
	if err != nil {
		return true, result, err
	}
	prompt := h.Config.Prompts["queen_role"] + "\n\n" + h.Config.Prompts["worker_resource_review"] + "\n\n" +
		h.Config.Prompts["worker_resource_scores"] + "\n\n" + h.Config.Prompts["worker_resource_format"] + "\n\n" + h.Config.Prompts["worker_contract_authority"]
	result.Called = true
	reply, err := h.generatePrompt(ctx, "queen", h.Config.Models.Queen, "worker_resource_review", prompt, input, "")
	if err != nil {
		if workerRetryExhausted(err) {
			_, pauseErr := h.pauseWorkerProvider(context.Background(), err, false)
			_, releaseErr := h.exec(context.Background(), "release_exhausted_resource_queen", "hive_worker_queen", "UPDATE hive_worker_queen SET owner='' WHERE request_id=? AND owner=?", h.requestID, owner)
			err = errors.Join(err, pauseErr, releaseErr)
		}
		return true, result, err
	}
	h.Log.Log("hive", "worker_resource_reply", map[string]any{"body": reply, "successful_births": births})
	action, err := ParseWorkerResourceAction(reply)
	if err != nil {
		return true, result, err
	}
	for _, inquiryID := range action.InquiryIDs {
		if !eligible[inquiryID] {
			return true, result, fmt.Errorf("resource inquiry %d is not a current-request nonroot inquiry", inquiryID)
		}
	}
	err = h.Store.Transaction(ctx, h.Log.Fields, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
		changed, err := exec("finish_resource_queen", "hive_worker_queen", "UPDATE hive_worker_queen SET owner='' WHERE request_id=? AND owner=?", h.requestID, owner)
		if err != nil {
			return err
		}
		count, err := changed.RowsAffected()
		if err != nil || count != 1 {
			return fmt.Errorf("resource Queen ownership changed: rows=%d error=%v", count, err)
		}
		_, err = exec("save_resource_review", "hive_worker_resources", `INSERT INTO hive_worker_resources(request_id,births_reviewed,selected_ids,note,relevance,inquiry_value,created)
 VALUES(?,?,?,?,?,?,?) ON CONFLICT(request_id) DO UPDATE SET births_reviewed=excluded.births_reviewed,selected_ids=excluded.selected_ids,
 note=excluded.note,relevance=excluded.relevance,inquiry_value=excluded.inquiry_value,created=excluded.created`,
			h.requestID, births, jsonText(action.InquiryIDs), action.Body, action.Relevance, action.InquiryValue, now())
		return err
	})
	if err != nil {
		return true, result, err
	}
	h.Log.Log("hive", "worker_resource_decision", map[string]any{"action": action, "successful_births": births, "outcome": "saved"})
	return true, result, nil
}
