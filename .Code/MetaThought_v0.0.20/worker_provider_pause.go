package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Every leaf must be an exhausted, fully received HTTP 500. A joined SQL,
// transport or parsing error must not make an uncertain claim safe to replay.
func workerRetryExhausted(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !workerRetryExhausted(child) {
				return false
			}
		}
		return true
	}
	if wrapped := errors.Unwrap(err); wrapped != nil {
		return workerRetryExhausted(wrapped)
	}
	provider, ok := err.(*ProviderError)
	return ok && provider.Kind == "retry_exhausted" && provider.StatusCode == 500
}

type workerProviderPause struct {
	ResumePhase        string `json:"resume_phase"`
	Error              string `json:"error"`
	ProviderStatusCode int    `json:"provider_status_code"`
	ProviderAttempts   int    `json:"provider_attempts"`
	ReadyForResume     bool   `json:"ready_for_resume"`
}

func (p workerProviderPause) apply(result *InquiryExperimentResult) {
	result.Status, result.Error = "provider_unavailable", p.Error
	result.ProviderStatusCode, result.ProviderAttempts = p.ProviderStatusCode, p.ProviderAttempts
	result.ReadyForResume = p.ReadyForResume
}

// The existing request result holds administrative continuation state only
// while provider_unavailable. Completed/blocked user answers are not replaced.
func (h *Hive) pauseWorkerProvider(ctx context.Context, cause error, drained bool) (workerProviderPause, error) {
	var provider *ProviderError
	if !workerRetryExhausted(cause) || !errors.As(cause, &provider) {
		return workerProviderPause{}, errors.New("provider pause requires exhausted HTTP 500 only")
	}
	rows, err := h.query(ctx, "pause_provider", "hive_requests", `UPDATE hive_requests SET
 result=json_object('kind','provider_unavailable','resume_phase',CASE
 WHEN status='converging' THEN 'converging'
 WHEN status='provider_unavailable' THEN json_extract(result,'$.resume_phase') ELSE 'working' END,
 'error',?,'provider_status_code',500,'provider_attempts',?,
 'ready_for_resume',json(CASE WHEN ? AND
 NOT EXISTS(SELECT 1 FROM hive_worker_queue WHERE request_id=hive_requests.id AND owner<>'') AND
 NOT EXISTS(SELECT 1 FROM hive_worker_queen WHERE request_id=hive_requests.id AND owner<>'')
 THEN 'true' ELSE 'false' END)),status='provider_unavailable',finish_reason='provider_unavailable'
 WHERE id=? AND status IN ('working','converging','paused','provider_unavailable') RETURNING result`, cause.Error(), provider.Attempts, drained, h.requestID)
	if err != nil {
		return workerProviderPause{}, err
	}
	if len(rows) != 1 {
		return workerProviderPause{}, errors.New("request state changed before provider pause; preserved its saved result")
	}
	var saved workerProviderPause
	if err := json.Unmarshal([]byte(str(rows[0]["result"])), &saved); err != nil {
		return saved, err
	}
	h.Log.Log("hive", "worker_provider_paused", map[string]any{"error": saved.Error, "resume_phase": saved.ResumePhase, "ready_for_resume": saved.ReadyForResume, "provider_attempts": saved.ProviderAttempts})
	return saved, nil
}

func (h *Hive) savedWorkerProviderPause(ctx context.Context) (workerProviderPause, error) {
	rows, err := h.query(ctx, "provider_pause_state", "hive_requests", `SELECT result,
 NOT EXISTS(SELECT 1 FROM hive_worker_queue WHERE request_id=r.id AND owner<>'') AND
 NOT EXISTS(SELECT 1 FROM hive_worker_queen WHERE request_id=r.id AND owner<>'') AS ready
 FROM hive_requests r WHERE id=? AND status='provider_unavailable'`, h.requestID)
	if err != nil {
		return workerProviderPause{}, err
	}
	if len(rows) != 1 {
		return workerProviderPause{}, fmt.Errorf("request %d has no provider pause", h.requestID)
	}
	var saved workerProviderPause
	if err := json.Unmarshal([]byte(str(rows[0]["result"])), &saved); err != nil {
		return saved, err
	}
	saved.ReadyForResume = saved.ReadyForResume && id(rows[0]["ready"]) != 0
	return saved, nil
}
