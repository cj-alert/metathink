package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestV010ResumedCompletionFailureRetainsFailedStatusAndCallCount(t *testing.T) {
	h, config, dir := v10Fixture(t, 2, nil)
	config.Dashboard.Enabled = false
	v10ConvergenceTeam(t, h, 2)
	reportBody := v9Reply("CONTRIBUTE 1", "Saved team report; uncertainty remains.", 0.8, 0.7)
	reportID := v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,1,'convergence','TEAM_REPORT',?,0.8,0.7,?)", reportBody, now())
	assessmentBody := v10ConvergenceAssessment("0.2")
	assessmentID := v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,2,'convergence','TEAM_ASSESSMENT',?,0.8,0.7,?)", assessmentBody, now())
	calls := 0
	client := v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
		calls++
		var input map[string]any
		if err := json.Unmarshal([]byte(str(request.Messages[1]["content"])), &input); err != nil { t.Fatal(err) }
		if str(input["purpose"]) != "queen_return" { t.Fatalf("resume replayed completed team work: purpose=%v", input["purpose"]) }
		return TextResponse{Text: v10ConvergenceReturn(reportID, "UNMET") + "Basis: forbidden extra score-section line.\n"}, nil
	})
	result, err := ResumeWorkerExperiment(v10Context(t), config, dir, client)
	if err == nil || !strings.Contains(err.Error(), "unexpected") || result.Status != "failed" || result.Calls != 1 || calls != 1 || result.RequestID != h.requestID {
		t.Fatalf("resumed failure lost status, call accounting or original error: result=%+v calls=%d error=%v", result, calls, err)
	}
	if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_requests WHERE id=? AND status='converging' AND result IS NULL", h.requestID) != 1 {
		t.Fatal("format rejection changed the recoverable request state")
	}
	if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE id=? AND body=?", reportID, reportBody) != 1 ||
		v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE id=? AND body=?", assessmentID, assessmentBody) != 1 ||
		v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE kind IN ('TEAM_REPORT','TEAM_ASSESSMENT')") != 2 {
		t.Fatal("failed final return altered or replayed saved completion artifacts")
	}
	t.Logf("resumed Queen rejection: status=%s calls=%d; saved report %d and assessment %d retained", result.Status, result.Calls, reportID, assessmentID)
}
