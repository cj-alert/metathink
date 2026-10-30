package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type workerRevisionContext struct {
	RevisionID    int64     `json:"revision_id"`
	ThroughWorkID int64     `json:"through_work_id"`
	UserFeedback  string    `json:"user_feedback"`
	Created       time.Time `json:"-"`
}

// Only the revision boundary and exact user feedback enter agent context.
// Earlier contracts remain in the saved administrative record, not this view.
func (h *Hive) currentWorkerRevision(ctx context.Context, requestID int64) (*workerRevisionContext, error) {
	rows, err := h.query(ctx, "worker_contract_revision_context", "hive_guidance", `SELECT id,body,created FROM hive_guidance WHERE request_id=?
 AND CASE WHEN json_valid(body) THEN json_extract(body,'$.kind') END='contract_revision' ORDER BY id DESC LIMIT 1`, requestID)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	var record struct {
		ThroughWorkID *int64  `json:"through_work_id"`
		UserFeedback  *string `json:"user_feedback"`
	}
	if err := json.Unmarshal([]byte(str(rows[0]["body"])), &record); err != nil {
		return nil, fmt.Errorf("saved contract revision context: %w", err)
	}
	if record.ThroughWorkID == nil || *record.ThroughWorkID < 0 || record.UserFeedback == nil {
		return nil, errors.New("saved contract revision is missing its work boundary or user feedback")
	}
	created, err := time.Parse(time.RFC3339Nano, str(rows[0]["created"]))
	if err != nil {
		return nil, fmt.Errorf("saved contract revision time: %w", err)
	}
	return &workerRevisionContext{RevisionID: id(rows[0]["id"]), ThroughWorkID: *record.ThroughWorkID, UserFeedback: *record.UserFeedback, Created: created}, nil
}
