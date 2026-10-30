package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func v10ResearchReply() TextResponse {
	return TextResponse{Text: "# ACTION\nRESEARCH 1\n# YOUR ANSWER\nThe retrieved paper describes the mechanism; compare the intervention before judging novelty.\n# YOUR SCORE\nrelevance: 0.9\ninquiry_value: 0.6\n",
		SearchCalls: []json.RawMessage{json.RawMessage(`{"type":"web_search_call","id":"ws_local","status":"completed","results":[{"type":"text_result","title":"Fixture paper","url":"https://research.invalid/paper","snippet":"Original research evidence."}]}`)},
		Citations:   []json.RawMessage{json.RawMessage(`{"type":"url_citation","url":"https://research.invalid/paper","title":"Fixture paper","start_index":0,"end_index":19}`)}}
}

func v10ResearchMember(t *testing.T, h *Hive, claim WorkerClaim) {
	t.Helper()
	v10Exec(t, h, "INSERT INTO hive_inquiry_members(inquiry_id,drone_id,commitment,created) VALUES(1,?,'I commit',?)", claim.DroneID, now())
}

func TestV010ResearchWorkerRoutesAndSharesOriginalEvidence(t *testing.T) {
	calls := 0
	client := v9TextFunc(func(_ context.Context, req TextRequest) (TextResponse, error) {
		calls++
		if !req.WebSearch || req.Model != "v009-fixture-drone" {
			t.Fatalf("research route/model lost: %+v", req)
		}
		input := str(req.Messages[1]["content"])
		if !strings.Contains(input, "Find prior published mechanisms") || !strings.Contains(input, "queen_request") || strings.Contains(input, v9DiagnosticCanary) {
			t.Fatalf("research input lacks question/contract or leaks diagnostics: %s", input)
		}
		return v10ResearchReply(), nil
	})
	h, _, _ := v10Fixture(t, 2, client)
	claim := v10Claim(t, h, "researcher")
	v10ResearchMember(t, h, claim)
	action := v10Action("RESEARCH", 1, "Find prior published mechanisms for this candidate.")
	action, err := h.ResearchWorker(context.Background(), claim, action)
	if err != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	var record ResearchRecord
	if err := json.Unmarshal([]byte(action.Body), &record); err != nil || record.Status != "completed" || record.LocalScoreSubject != "research_result" || action.Relevance != 0.9 {
		t.Fatalf("research=%+v action=%+v err=%v", record, action, err)
	}
	if string(record.SearchCalls[0]) != string(v10ResearchReply().SearchCalls[0]) || string(record.Citations[0]) != string(v10ResearchReply().Citations[0]) || record.Answer != v10ResearchReply().Text {
		t.Fatal("provider evidence was rewritten or lost")
	}
	if err := h.AdvanceWorker(context.Background(), claim, action); err != nil {
		t.Fatal(err)
	}
	next := v10Claim(t, h, "researcher")
	input, err := h.WorkerInput(context.Background(), next)
	if err != nil || !strings.Contains(input, "research_work") || !strings.Contains(input, "research.invalid/paper") || strings.Contains(input, v9DiagnosticCanary) {
		t.Fatalf("next phase cannot inspect research: %s err=%v", input, err)
	}
	queen, err := h.workerQueenInput(context.Background(), 1, nil, "")
	if err != nil || !strings.Contains(queen, "Original research evidence.") {
		t.Fatalf("Queen lost research: %s err=%v", queen, err)
	}
	rows, err := h.query(context.Background(), "assert_research", "hive_inquiry_work", "SELECT body FROM hive_inquiry_work WHERE kind='RESEARCH'")
	if err != nil || len(rows) != 1 || str(rows[0]["body"]) != action.Body {
		t.Fatal("research was not persisted intact as operational work")
	}
}

func TestV010ResearchFailuresBecomeOperationalNeedsWithoutRetry(t *testing.T) {
	cases := []struct {
		name, status  string
		response      TextResponse
		err           error
		missingPrompt bool
	}{
		{name: "no search", status: "not_performed", response: TextResponse{Text: "I know the answer from memory."}},
		{name: "provider failure", status: "unavailable", err: errors.New("fixture search unavailable")},
		{name: "refusal", status: "unavailable", response: TextResponse{Refusal: "fixture refusal"}},
		{name: "search failed", status: "incomplete", response: TextResponse{Text: "No findings.", SearchCalls: []json.RawMessage{json.RawMessage(`{"type":"web_search_call","status":"failed"}`)}}},
		{name: "no sources", status: "insufficient_evidence", response: TextResponse{Text: "No match", SearchCalls: []json.RawMessage{json.RawMessage(`{"type":"web_search_call","status":"completed","results":[]}`)}}},
		{name: "missing configuration", status: "unavailable", missingPrompt: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h, _, _ := v10Fixture(t, 2, v9TextFunc(func(context.Context, TextRequest) (TextResponse, error) { calls++; return tc.response, tc.err }))
			if tc.missingPrompt {
				delete(h.Config.Prompts, "worker_research")
			}
			claim := v10Claim(t, h, "researcher")
			v10ResearchMember(t, h, claim)
			action, err := h.ResearchWorker(context.Background(), claim, v10Action("RESEARCH", 1, "Find needed evidence."))
			if err != nil {
				t.Fatal(err)
			}
			var record ResearchRecord
			if err := json.Unmarshal([]byte(action.Body), &record); err != nil || record.Status != tc.status || record.Need == "" || record.LocalScoreSubject != "research_request" {
				t.Fatalf("missing research not reported honestly: %+v err=%v", record, err)
			}
			if action.Relevance != 0.8 || action.InquiryValue != 0.7 || (tc.missingPrompt && calls != 0) || (!tc.missingPrompt && calls != 1) {
				t.Fatalf("request scores changed or call retried: action=%+v calls=%d", action, calls)
			}
			if err := h.AdvanceWorker(context.Background(), claim, action); err != nil {
				t.Fatal(err)
			}
			input, err := h.workerQueenInput(context.Background(), 1, nil, "")
			if err != nil || !strings.Contains(input, "research_request") || !strings.Contains(input, tc.status) {
				t.Fatalf("Queen never received missing-research complaint: %s %v", input, err)
			}
		})
	}
}

func TestV010ResearchRequiresMembershipAndStopsBeforeCallingPausedRequest(t *testing.T) {
	calls := 0
	h, _, _ := v10Fixture(t, 2, v9TextFunc(func(context.Context, TextRequest) (TextResponse, error) { calls++; return v10ResearchReply(), nil }))
	claim := v10Claim(t, h, "researcher")
	action := v10Action("RESEARCH", 1, "Find needed evidence.")
	if _, err := h.ResearchWorker(context.Background(), claim, action); err == nil || calls != 0 {
		t.Fatal("nonmember research called provider")
	}
	v10ResearchMember(t, h, claim)
	stale := claim
	stale.Owner = "another-owner"
	if _, err := h.ResearchWorker(context.Background(), stale, action); err == nil || calls != 0 {
		t.Fatal("unowned research called provider")
	}
	v10Exec(t, h, "UPDATE hive_requests SET status='paused' WHERE id=?", h.requestID)
	result, err := h.ResearchWorker(context.Background(), claim, action)
	if err != nil || calls != 0 || !strings.Contains(result.Body, "not_started") {
		t.Fatalf("paused research dispatched: %+v %v calls=%d", result, err, calls)
	}
}

func TestV010ResearchWakesSleepingPoolAfterQueenObservedIt(t *testing.T) {
	h, _, _ := v10Fixture(t, 2, nil)
	v10Exec(t, h, "UPDATE hive_worker_queue SET asleep_work_id=0,asleep_guidance_id=0,turn_activity=0")
	work := v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,1,'expand','RESEARCH','New source evidence',0.8,0.7,?)", now())
	v10Exec(t, h, "INSERT INTO hive_worker_queen_seen(request_id,inquiry_id,last_work_id) VALUES(?,1,?)", h.requestID, work)
	idle, err := h.workerPoolIdle(context.Background())
	if err != nil || idle {
		t.Fatalf("new research was mistaken for idle: idle=%v err=%v", idle, err)
	}
	claim, err := h.ClaimDrone(context.Background(), "reader", 2)
	if err != nil || claim == nil {
		t.Fatalf("sleeping drone was not awakened by research: %+v %v", claim, err)
	}
}
