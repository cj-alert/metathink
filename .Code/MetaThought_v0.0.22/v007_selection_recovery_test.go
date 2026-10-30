package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func v7SelectionReply(ids []int64, status, reason string) string {
	parts := make([]string, len(ids))
	for n, candidateID := range ids {
		parts[n] = strconv.FormatInt(candidateID, 10)
	}
	return fmt.Sprintf("# DECISION\nRETURN\n# CANDIDATES\n%s\n# CRITERIA\n%s\n# REASON\n%s", strings.Join(parts, ", "), status, reason)
}

func v7SelectionRows(t *testing.T, h *Hive, table, query string, args ...any) []map[string]any {
	t.Helper()
	rows, err := h.Store.Query(context.Background(), "test_observation", table, h.Log.Fields, query, args...)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestV007ReturnSelectsExactEarlierAndMultipleAnswers(t *testing.T) {
	for _, multiple := range []bool{false, true} {
		name := "earlier"
		if multiple {
			name = "multiple_in_selected_order"
		}
		t.Run(name, func(t *testing.T) {
			var reply string
			var observed TextRequest
			calls := 0
			h := v7OpenHive(t, v7TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
				calls++
				observed = request
				return TextResponse{Text: reply}, nil
			}))
			contract := v7ContractText("selected answers", "support", "testability")
			requestID, teamID := v7SeedRequest(t, h, contract)
			firstBody := "  Earlier α hypothesis.\n\nKeep this exact tail.\n"
			secondBody := "Alternative β answer.\r\nPreserve its line endings."
			first := v7AddCandidate(t, h, teamID, firstBody, true)
			second := v7AddCandidate(t, h, teamID, secondBody, true)
			latest := v7AddCandidate(t, h, teamID, "Latest answer must not replace the selected work.", true)
			selected, expected := []int64{first}, firstBody
			if multiple {
				selected = []int64{second, first}
				expected = fmt.Sprintf("# Candidate %d\n\n%s\n\n# Candidate %d\n\n%s", second, secondBody, first, firstBody)
			}
			reply = v7SelectionReply(selected, "UNCERTAIN", "Return the selected saved evidence with its unresolved objections.")
			review, err := h.reviewCandidate(context.Background(), contract, latest, 3)
			if err != nil {
				t.Fatal(err)
			}
			if review.answer != expected || !reflect.DeepEqual(review.SelectedCandidateIDs, selected) {
				t.Fatalf("selected answer mismatch: ids=%v answer=%q; want ids=%v answer=%q", review.SelectedCandidateIDs, review.answer, selected, expected)
			}
			if calls != 1 || observed.Scope != "queen" || observed.Model != h.Config.Models.Queen {
				t.Fatalf("review dispatch: calls=%d scope=%q model=%q", calls, observed.Scope, observed.Model)
			}
			if len(observed.Messages) != 2 || !strings.Contains(str(observed.Messages[0]["content"]), h.Config.Prompts["collapse_format"]) {
				t.Fatal("review did not receive the fixed selection reply protocol")
			}
			requests := v7SelectionRows(t, h, "hive_requests", "SELECT status,result,finish_reason,criteria_met FROM hive_requests WHERE id=?", requestID)
			if len(requests) != 1 || str(requests[0]["status"]) != "returned" || str(requests[0]["result"]) != expected || id(requests[0]["criteria_met"]) != 0 {
				t.Fatalf("stored request does not preserve selected answer/uncertainty: %s", jsonText(requests))
			}
			reviews := v7SelectionRows(t, h, "hive_reviews", "SELECT candidate_id,selected_candidate_ids,decision,criteria_status,reason FROM hive_reviews WHERE request_id=?", requestID)
			var savedIDs []int64
			if len(reviews) != 1 {
				t.Fatalf("want one saved review, got %s", jsonText(reviews))
			}
			if err := json.Unmarshal([]byte(str(reviews[0]["selected_candidate_ids"])), &savedIDs); err != nil {
				t.Fatal(err)
			}
			if id(reviews[0]["candidate_id"]) != latest || !reflect.DeepEqual(savedIDs, selected) || str(reviews[0]["criteria_status"]) != "UNCERTAIN" {
				t.Fatalf("review lost current versus selected attribution: %s", jsonText(reviews))
			}
			t.Logf("selection_observation=%s", jsonText(map[string]any{"requests": requests, "reviews": reviews, "model_calls": calls}))
		})
	}
}

func TestV007InvalidSelectionsDoNotFinalizeRequest(t *testing.T) {
	for _, kind := range []string{"unassessed", "wrong_request", "duplicate", "missing"} {
		t.Run(kind, func(t *testing.T) {
			var reply string
			calls := 0
			h := v7OpenHive(t, v7TextFunc(func(context.Context, TextRequest) (TextResponse, error) {
				calls++
				return TextResponse{Text: reply}, nil
			}))
			contract := v7ContractText("invalid selection")
			requestID, teamID := v7SeedRequest(t, h, contract)
			current := v7AddCandidate(t, h, teamID, "Current assessed candidate.", true)
			selected := []int64{current}
			switch kind {
			case "unassessed":
				selected = []int64{v7AddCandidate(t, h, teamID, "Interrupted before any assessment.", false)}
			case "wrong_request":
				foreignRequest := v7Exec(t, h, "INSERT INTO hive_requests(run_id,contract,status,team_id,created) VALUES(?,?,?,?,?)", h.runID, contract, "active", teamID, now())
				h.requestID = foreignRequest
				selected = []int64{v7AddCandidate(t, h, teamID, "Assessed, but owned by another request.", true)}
				h.requestID = requestID
			case "duplicate":
				selected = []int64{current, current}
			case "missing":
				selected = []int64{current + 1000}
			}
			before := v7SelectionRows(t, h, "hive_requests", "SELECT status,result,finish_reason,criteria_met FROM hive_requests WHERE id=?", requestID)
			if _, err := h.selectedAnswers(context.Background(), requestID, selected); err == nil {
				t.Fatalf("selectedAnswers accepted %s selection %v", kind, selected)
			}
			reply = v7SelectionReply(selected, "MET", "An invalid ID must prevent finalization even when the reply claims MET.")
			if _, err := h.reviewCandidate(context.Background(), contract, current, 2); err == nil {
				t.Fatalf("reviewCandidate accepted %s selection %v", kind, selected)
			} else {
				t.Logf("rejected_selection=%s error=%q", kind, err)
			}
			after := v7SelectionRows(t, h, "hive_requests", "SELECT status,result,finish_reason,criteria_met FROM hive_requests WHERE id=?", requestID)
			reviews := v7SelectionRows(t, h, "hive_reviews", "SELECT id FROM hive_reviews WHERE request_id=?", requestID)
			if !reflect.DeepEqual(before, after) || len(reviews) != 0 || calls != 1 {
				t.Fatalf("invalid selection changed state or retried: before=%s after=%s reviews=%s calls=%d", jsonText(before), jsonText(after), jsonText(reviews), calls)
			}
		})
	}
}

func TestV007ReturnRequestFailureRollsBackReview(t *testing.T) {
	var reply string
	calls := 0
	h := v7OpenHive(t, v7TextFunc(func(context.Context, TextRequest) (TextResponse, error) {
		calls++
		return TextResponse{Text: reply}, nil
	}))
	contract := v7ContractText("return rollback")
	requestID, teamID := v7SeedRequest(t, h, contract)
	candidateID := v7AddCandidate(t, h, teamID, "Retain this candidate after the return write fails.", true)
	v7Exec(t, h, "INSERT INTO hive_reviews(request_id,candidate_id,selected_candidate_ids,tick,decision,criteria_status,reason,created) VALUES(?,?,?,?,?,?,?,?)", requestID, candidateID, "[]", 1, "CONTINUE", "UNCERTAIN", "Earlier durable review.", now())
	requestQuery := "SELECT status,result,finish_reason,criteria_met FROM hive_requests WHERE id=?"
	reviewQuery := "SELECT id,candidate_id,selected_candidate_ids,decision,criteria_status,reason FROM hive_reviews WHERE request_id=? ORDER BY id"
	beforeRequest := v7SelectionRows(t, h, "hive_requests", requestQuery, requestID)
	beforeReviews := v7SelectionRows(t, h, "hive_reviews", reviewQuery, requestID)
	v7Exec(t, h, "CREATE TRIGGER v007_reject_return BEFORE UPDATE OF status ON hive_requests WHEN NEW.status='returned' BEGIN SELECT RAISE(ABORT,'v007 forced return update failure'); END")
	reply = v7SelectionReply([]int64{candidateID}, "MET", "The request update will fail after review insertion.")
	_, err := h.reviewCandidate(context.Background(), contract, candidateID, 2)
	if err == nil || !strings.Contains(err.Error(), "v007 forced return update failure") {
		t.Fatalf("expected injected SQL failure, got %v", err)
	}
	afterRequest := v7SelectionRows(t, h, "hive_requests", requestQuery, requestID)
	afterReviews := v7SelectionRows(t, h, "hive_reviews", reviewQuery, requestID)
	if !reflect.DeepEqual(beforeRequest, afterRequest) || !reflect.DeepEqual(beforeReviews, afterReviews) || calls != 1 {
		t.Fatalf("return transaction was partial or retried: request=%s reviews=%s calls=%d", jsonText(afterRequest), jsonText(afterReviews), calls)
	}
	t.Logf("return_rollback_observation=%s", jsonText(map[string]any{"request": afterRequest, "reviews": afterReviews, "model_calls": calls, "error": err.Error()}))
}

func TestV007ResumeSavedReturnPreservesAnswerAndRunHistory(t *testing.T) {
	var reply string
	initialCalls := 0
	h := v7OpenHive(t, v7TextFunc(func(context.Context, TextRequest) (TextResponse, error) {
		initialCalls++
		return TextResponse{Text: reply}, nil
	}))
	contract := v7ContractText("saved return recovery")
	requestID, teamID := v7SeedRequest(t, h, contract)
	oldRunning := h.runID
	answer := "  Exact selected return.\nThe newest candidate is deliberately different.\n"
	selected := v7AddCandidate(t, h, teamID, answer, true)
	latestCandidate := v7AddCandidate(t, h, teamID, "A later unselected answer.", true)
	v7Exec(t, h, "UPDATE hive_runs SET calls=? WHERE id=?", 11, oldRunning)
	failed := v7Exec(t, h, "INSERT INTO hive_runs(started,finished,status,version,calls,config,request_id) VALUES(?,?,?,?,?,?,?)", "prior-start", "prior-finish", "failed", Version, 22, jsonText(h.Config), requestID)
	latestRun := v7Exec(t, h, "INSERT INTO hive_runs(started,status,version,calls,config,request_id) VALUES(?,?,?,?,?,?)", "latest-start", "running", Version, 33, jsonText(h.Config), requestID)
	reason := "The selected answer is useful but unresolved objections remain."
	reply = v7SelectionReply([]int64{selected}, "UNCERTAIN", reason)
	if _, err := h.reviewCandidate(context.Background(), contract, latestCandidate, 3); err != nil {
		t.Fatal(err)
	}
	if initialCalls != 1 {
		t.Fatalf("initial review calls=%d", initialCalls)
	}
	runQuery := "SELECT id,status,calls,started,finished,config,request_id FROM hive_runs WHERE request_id=? ORDER BY id"
	beforeRuns := v7SelectionRows(t, h, "hive_runs", runQuery, requestID)
	config, dir := h.Config, h.Dir
	h.Close()

	resumeCalls := 0
	noCalls := v7TextFunc(func(context.Context, TextRequest) (TextResponse, error) {
		resumeCalls++
		return TextResponse{}, errors.New("saved RETURN must not call a model")
	})
	resumed, err := OpenHive(config, dir, noCalls)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resumed.Close)
	result, err := resumed.Resume(context.Background(), requestID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "returned" || result.Answer != answer || result.Rationale != reason || result.CriteriaStatus != "UNCERTAIN" || result.CriteriaMet || !reflect.DeepEqual(result.SelectedCandidateIDs, []int64{selected}) {
		t.Fatalf("recovered result changed saved return: %s", jsonText(result))
	}
	if resumeCalls != 0 || resumed.Text.Calls != 0 || result.Calls != 0 {
		t.Fatalf("recovery issued or invented calls: provider=%d observed=%d result=%d", resumeCalls, resumed.Text.Calls, result.Calls)
	}
	afterRuns := v7SelectionRows(t, resumed, "hive_runs", runQuery, requestID)
	if len(beforeRuns) != 3 || len(afterRuns) != 3 {
		t.Fatalf("recovery added or removed runs: before=%s after=%s", jsonText(beforeRuns), jsonText(afterRuns))
	}
	for n, row := range afterRuns {
		if id(row["id"]) == latestRun {
			if str(row["status"]) != "completed" || str(row["finished"]) == "" || id(row["calls"]) != 33 {
				t.Fatalf("latest run not finalized with saved calls: %s", jsonText(row))
			}
			for _, key := range []string{"id", "calls", "started", "config", "request_id"} {
				if !reflect.DeepEqual(row[key], beforeRuns[n][key]) {
					t.Fatalf("recovery changed latest run %s", key)
				}
			}
		} else if !reflect.DeepEqual(row, beforeRuns[n]) {
			t.Fatalf("recovery rewrote historical run: before=%s after=%s", jsonText(beforeRuns[n]), jsonText(row))
		}
	}
	request := v7SelectionRows(t, resumed, "hive_requests", "SELECT status,result,finish_reason,criteria_met FROM hive_requests WHERE id=?", requestID)
	if len(request) != 1 || str(request[0]["result"]) != answer || str(request[0]["status"]) != "returned" {
		t.Fatalf("recovery changed stored selected answer: %s", jsonText(request))
	}
	resumed.Close()
	// Reopening again must not skip the completed latest run and finalize an
	// older abandoned running attempt instead.
	completed, err := OpenHive(config, dir, noCalls)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(completed.Close)
	if _, err := completed.Resume(context.Background(), requestID); err != nil {
		t.Fatal(err)
	}
	secondRuns := v7SelectionRows(t, completed, "hive_runs", runQuery, requestID)
	if !reflect.DeepEqual(afterRuns, secondRuns) || resumeCalls != 0 || completed.Text.Calls != 0 {
		t.Fatalf("second recovery changed completed/historical work: runs=%s calls=%d", jsonText(secondRuns), resumeCalls)
	}
	observedRuns := make([]map[string]any, 0, len(secondRuns))
	for _, row := range secondRuns {
		observedRuns = append(observedRuns, map[string]any{"id": row["id"], "status": row["status"], "calls": row["calls"], "finished": row["finished"]})
	}
	t.Logf("return_recovery_observation=%s", jsonText(map[string]any{"result": result, "old_running_id": oldRunning, "failed_id": failed, "latest_id": latestRun, "runs": observedRuns, "resume_model_calls": resumeCalls}))
}
