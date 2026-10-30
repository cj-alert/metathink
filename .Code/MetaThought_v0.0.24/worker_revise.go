package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type WorkerRevisionResult struct {
	Status           string              `json:"status"`
	RequestID        int64               `json:"request_id"`
	RevisionID       int64               `json:"revision_id"`
	ThroughWorkID    int64               `json:"through_work_id"`
	OldCriteria      []ContractCriterion `json:"old_criteria"`
	OldCriteriaError string              `json:"old_criteria_error,omitempty"`
	NewCriteria      []ContractCriterion `json:"new_criteria"`
	Calls            int                 `json:"calls"`
}

// ReviseWorkerContract changes the governing direction of an existing stopped
// inquiry. It opens only local state and diagnostics; no provider is constructed.
// The caller confirms the complete replacement contract before invoking this.
func ReviseWorkerContract(ctx context.Context, c Config, dir, handoff, feedback string) (WorkerRevisionResult, error) {
	statePath := filepath.Join(dir, c.Files.State)
	if _, err := os.Stat(statePath); err != nil {
		return WorkerRevisionResult{}, fmt.Errorf("existing worker state: %w", err)
	}
	logger, err := OpenLogger(filepath.Join(dir, c.Files.Debug), c.Schema.Logs)
	if err != nil {
		return WorkerRevisionResult{}, err
	}
	defer logger.Close()
	log := &ContextLog{Sink: logger, Fields: map[string]any{}}
	store, err := OpenStore(statePath, c.Schema.State, log)
	if err != nil {
		return WorkerRevisionResult{}, err
	}
	defer store.Close()
	h := &Hive{Config: c, Dir: dir, Store: store, Logger: logger, Log: log}
	return h.reviseWorkerContract(ctx, handoff, feedback)
}

func (h *Hive) reviseWorkerContract(ctx context.Context, handoff, feedback string) (result WorkerRevisionResult, err error) {
	criteria, err := workerHandoffCriteria(handoff)
	if err != nil {
		return result, fmt.Errorf("revised handoff: %w", err)
	}
	if strings.TrimSpace(feedback) == "" {
		return result, errors.New("contract revision requires explicit user feedback")
	}
	requests, err := h.query(ctx, "revision_request", "hive_requests", "SELECT id,run_id FROM hive_requests ORDER BY id DESC LIMIT 1")
	if err != nil {
		return result, err
	}
	if len(requests) != 1 {
		return result, errors.New("no saved worker request to revise")
	}
	h.requestID, h.runID = id(requests[0]["id"]), id(requests[0]["run_id"])
	h.Log.Fields["request_id"], h.Log.Fields["run_id"] = h.requestID, h.runID
	result.RequestID, result.NewCriteria = h.requestID, criteria
	// INSERT SELECT takes the write lock and captures the exact old authority,
	// prior Queen note and request-scoped work boundary in the same transaction
	// as replacement. Concurrent claims/resume cannot slip between these steps.
	err = h.Store.Transaction(ctx, h.Log.Fields, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
		saved, e := exec("save_contract_revision", "hive_guidance", `INSERT INTO hive_guidance(request_id,tick,body,created)
 SELECT r.id,0,json_object('kind','contract_revision','old_contract',r.contract,'new_contract',?,
 'user_feedback',?,'previous_queen_note',q.note,'old_status',r.status,
 'old_finish_reason',r.finish_reason,'old_result',r.result,'old_criteria_met',r.criteria_met,
 'through_work_id',COALESCE((SELECT MAX(w.id) FROM hive_inquiry_work w JOIN hive_inquiries i ON i.id=w.inquiry_id WHERE i.request_id=r.id),0)),?
 FROM hive_requests r JOIN hive_worker_queen q ON q.request_id=r.id
 WHERE r.id=? AND r.id=(SELECT MAX(id) FROM hive_requests) AND r.status IN ('paused','failed','blocked')
 AND NOT EXISTS(SELECT 1 FROM hive_worker_queue WHERE owner<>'')
 AND NOT EXISTS(SELECT 1 FROM hive_worker_queen WHERE owner<>'')`, handoff, feedback, now(), h.requestID)
		if e != nil {
			return e
		}
		n, e := saved.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return errors.New("contract revision requires the current unfinished request paused, failed or blocked, with every worker and Queen claim released")
		}
		result.RevisionID, e = saved.LastInsertId()
		if e != nil {
			return e
		}
		if _, e = exec("replace_contract", "hive_requests", "UPDATE hive_requests SET contract=?,status='paused',finish_reason='contract_revised',result=NULL,criteria_met=NULL WHERE id=?", handoff, h.requestID); e != nil {
			return e
		}
		if _, e = exec("revision_queen_note", "hive_worker_queen", "UPDATE hive_worker_queen SET note=? WHERE request_id=?", "User-confirmed contract revision. User feedback (verbatim):\n"+feedback, h.requestID); e != nil {
			return e
		}
		_, e = exec("wake_revised_contract", "hive_worker_queue", "UPDATE hive_worker_queue SET asleep_work_id=NULL,asleep_guidance_id=NULL WHERE request_id=?", h.requestID)
		return e
	})
	if err != nil {
		return result, err
	}
	result.Status = "paused"
	// Read the saved administrative record to produce the receipt's actual old
	// criteria and boundary. This is not a comparison or replay gate.
	saved, err := h.query(ctx, "contract_revision_receipt", "hive_guidance", "SELECT body FROM hive_guidance WHERE id=? AND request_id=?", result.RevisionID, h.requestID)
	if err != nil {
		return result, err
	}
	if len(saved) != 1 {
		return result, errors.New("saved contract revision receipt is missing")
	}
	var record struct {
		OldContract   string `json:"old_contract"`
		ThroughWorkID int64  `json:"through_work_id"`
	}
	if err = json.Unmarshal([]byte(str(saved[0]["body"])), &record); err != nil {
		return result, err
	}
	result.ThroughWorkID = record.ThroughWorkID
	result.OldCriteria, err = workerHandoffCriteria(record.OldContract)
	if err != nil {
		// A valid replacement may repair an older malformed handoff. Preserve
		// that original and report its parse failure instead of inventing anchors.
		result.OldCriteriaError = err.Error()
	}
	h.Log.Log("developer", "worker_contract_revised", map[string]any{"result": result, "outcome": "paused"})
	return result, nil
}
