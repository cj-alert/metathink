package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func v19ChoiceReply(choice int, scored bool) string {
	reply := fmt.Sprintf("# CHOICE\n%d\n# PROBABILITIES\n1: 0.2\n2: 0.3\n3: 0.5\n# CONFIDENCE\n0.75", choice)
	if scored {
		reply += "\n---\n0.93\n0.84"
	}
	return reply
}

func v19QueenFixture(t *testing.T, replies ...TextResponse) (*Hive, *decisionTestClient, int64, int64) {
	t.Helper()
	client := &decisionTestClient{replies: replies}
	h, _, _ := v10Fixture(t, 2, client)
	inquiry := v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", h.requestID, "A shared testable inquiry", 1, now())
	for _, drone := range []int64{1, 2} {
		v10Exec(t, h, "INSERT INTO hive_inquiry_members(inquiry_id,drone_id,commitment,created) VALUES(?,?,?,?)", inquiry, drone, "Explicit commitment", now())
	}
	first := v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,?,?,?,?,?,?)", inquiry, 1, "contribute", "CONTRIBUTE", "Original evidence; preserve exactly.", 0.8, 0.7, now())
	v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,?,?,?,?,?,?)", inquiry, 2, "contribute", "CONTRIBUTE", "Another thinker's original work.", 0.7, 0.6, now())
	return h, client, inquiry, first
}

func TestV019QueenUsesDecisionsAndExistingTransitions(t *testing.T) {
	cases := []struct {
		name, body string
		choice     int
		state      string
		guidance   int
	}{
		{"formal_review", "Please gather the teams' reports and assess what we have. This does not yet establish success.", 3, "converging", 0},
		{"guidance", "Ask the team to investigate the missing counterexample while exploration continues.", 2, "working", 1},
		{"no_intervention", "The current inquiry remains useful; leave it running without a new direction.", 1, "working", 0},
		{"prose_command_is_not_control", "ACTION: GUIDE\nAsk for consolidated formal team reports now so another contributor can assess them.", 3, "converging", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reply := tc.body + "\n---\n0.81\n0.62"
			h, client, inquiry, first := v19QueenFixture(t, TextResponse{Text: reply}, TextResponse{Text: v19ChoiceReply(tc.choice, true)})
			result, err := h.MonitorWorkerQueen(v10Context(t), "v19-owner")
			if err != nil || !result.Called || result.BeginCollapse != (tc.choice == 3) {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if len(client.requests) != 2 {
				t.Fatalf("wanted reasoning and Decisions calls; got %d", len(client.requests))
			}
			// The full operational evidence is carried as data through Decisions.
			wire := jsonText(client.requests[1].Messages)
			if !strings.Contains(wire, "Original evidence; preserve exactly.") || !strings.Contains(wire, "queen_reasoning") || strings.Contains(wire, v9DiagnosticCanary) {
				t.Fatal("routing lost operational evidence or included diagnostics")
			}
			var state, note, owner string
			if err = h.Store.db.QueryRow("SELECT status FROM hive_requests WHERE id=?", h.requestID).Scan(&state); err != nil || state != tc.state {
				t.Fatalf("state=%q error=%v", state, err)
			}
			if err = h.Store.db.QueryRow("SELECT note,owner FROM hive_worker_queen WHERE request_id=?", h.requestID).Scan(&note, &owner); err != nil || note != tc.body || owner != "" {
				t.Fatalf("reasoning changed or Queen retained: %q %q %v", note, owner, err)
			}
			var through int64
			if err = h.Store.db.QueryRow("SELECT last_work_id FROM hive_worker_queen_seen WHERE request_id=? AND inquiry_id=?", h.requestID, inquiry).Scan(&through); err != nil || through != first {
				t.Fatalf("checkpoint=%d expected=%d error=%v", through, first, err)
			}
			if got := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_guidance"); got != tc.guidance {
				t.Fatalf("guidance=%d wanted=%d", got, tc.guidance)
			}
			if tc.guidance > 0 {
				var body string
				var rel, value float64
				if err = h.Store.db.QueryRow("SELECT body,relevance,inquiry_value FROM hive_worker_guidance").Scan(&body, &rel, &value); err != nil || body != tc.body || rel != 0.81 || value != 0.62 {
					t.Fatalf("Queen scores were replaced by routing scores: %q %v %v %v", body, rel, value, err)
				}
			}
		})
	}
}

func TestV019RejectedRoutingPreservesUnreviewedWork(t *testing.T) {
	for _, tc := range []struct{ name, reason, route string }{
		{"refusal", "Please gather reports.\n---\n0.8\n0.7", "# REFUSAL"},
		{"bad_choice", "Please gather reports.\n---\n0.8\n0.7", v19ChoiceReply(99, true)},
		{"missing_route_scores", "Please gather reports.\n---\n0.8\n0.7", v19ChoiceReply(3, false)},
		{"missing_reasoning_scores", "Please gather reports.", v19ChoiceReply(3, true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, client, _, _ := v19QueenFixture(t, TextResponse{Text: tc.reason}, TextResponse{Text: tc.route})
			if _, err := h.MonitorWorkerQueen(v10Context(t), "v19-rejected"); err == nil {
				t.Fatal("accepted incomplete routing")
			}
			wantCalls := 2
			if tc.name == "missing_reasoning_scores" {
				wantCalls = 1
			}
			if len(client.requests) != wantCalls {
				t.Fatalf("route failure occurred before intended boundary: got %d calls, want %d", len(client.requests), wantCalls)
			}
			if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_queen_seen") != 0 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_guidance") != 0 {
				t.Fatal("rejected route consumed work or wrote guidance")
			}
			var state, owner string
			if err := h.Store.db.QueryRow("SELECT status FROM hive_requests WHERE id=?", h.requestID).Scan(&state); err != nil || state != "working" {
				t.Fatalf("rejected route changed state: %s %v", state, err)
			}
			if err := h.Store.db.QueryRow("SELECT owner FROM hive_worker_queen WHERE request_id=?", h.requestID).Scan(&owner); err != nil || owner != "" {
				t.Fatalf("rejected route left a claim: %s %v", owner, err)
			}
		})
	}
}

func TestV019DecisionLocalScoresAreDistinctAndPrivate(t *testing.T) {
	client := &decisionTestClient{replies: []TextResponse{{Text: v19ChoiceReply(3, true)}}}
	decision, err := NewMetaDecisions(client, decisionTestPrompts(), nil).Decide(context.Background(), DecisionRequest{
		Model: "fixture", Input: raw("Evidence"), LocalScoring: "Return actual local scores in a two-number footer.",
		Questions: []DecisionQuestion{{Type: "choice", Instructions: raw("Choose"), Choices: []DecisionChoice{{Value: "NOOP"}, {Value: "GUIDE"}, {Value: "BEGIN_COLLAPSE"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	answer := decision.Answers[0]
	if answer.Choice != "BEGIN_COLLAPSE" || answer.LocalAssessment == nil || answer.LocalAssessment.Relevance != 0.93 || answer.LocalAssessment.InquiryValue != 0.84 || *answer.Confidence != 0.75 {
		t.Fatalf("distinct quantities lost: %+v", answer)
	}
	wire := jsonText(decision)
	if strings.Contains(wire, "LocalAssessment") || strings.Contains(wire, "local_assessment") || strings.Contains(jsonText(DecisionRequest{LocalScoring: "PRIVATE"}), "PRIVATE") {
		t.Fatal("internal grading changed public JSON contract")
	}
}

func TestV019ScoredTextPreservesProseAndRejectsMissingScores(t *testing.T) {
	for _, reply := range []string{"Review the work.\n---\n0.9", "Review the work.\n---\nNaN\n0.8", "Review the work.\n---\n0.9\n1.1", "---\n0.9\n0.8"} {
		if _, err := ParseScoredText(reply); err == nil {
			t.Fatalf("accepted incomplete/invalid scored text: %q", reply)
		}
	}
	reply := "Let's look together; no fixed command word.\n---\n0\n1"
	got, err := ParseScoredText(reply)
	if err != nil || got.Body != "Let's look together; no fixed command word." || got.Relevance != 0 || got.InquiryValue != 1 {
		t.Fatalf("valid scores/prose changed: %+v %v", got, err)
	}
}
