package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// This version's local runner compiles production files plus v009_*_test.go.
// Historical fixtures have their own TestMain and are deliberately excluded.
var v9NetworkAttempts atomic.Int64

type v9BlockedHTTP struct{}

func (v9BlockedHTTP) RoundTrip(*http.Request) (*http.Response, error) {
	v9NetworkAttempts.Add(1)
	return nil, errors.New("HTTP is forbidden in local v009 tests")
}

func TestMain(m *testing.M) {
	http.DefaultTransport = v9BlockedHTTP{}
	http.DefaultClient = &http.Client{Transport: v9BlockedHTTP{}}
	code := m.Run()
	attempts := v9NetworkAttempts.Load()
	fmt.Fprintf(os.Stdout, "focused_local_network_attempts=%d\n", attempts)
	if attempts != 0 {
		code = 1
	}
	os.Exit(code)
}

type v9TextFunc func(context.Context, TextRequest) (TextResponse, error)

func (f v9TextFunc) Complete(ctx context.Context, request TextRequest) (TextResponse, error) {
	return f(ctx, request)
}

const v9DiagnosticCanary = "V009_DIAGNOSTIC_ONLY_NOT_MODEL_CONTEXT"
const v9OldSeedCanary = "V009_OLD_SEED_NOT_GOVERNING_CONTEXT"

const v9Handoff = "queen_request:\r\nFind one or more novel, testable hypotheses to solve/cure Sudden Infant Death Syndrome (SIDS).\r\nNovelty >= 1.0\r\nTestability_Human_Trials > 0.35\r\nChance_of_Successfully_Solves_SIDS > 0.35\r\nUnanswered: what does solve/cure mean? Preserve α and uncertainty.  \r\n"

func v9Config(t *testing.T, drones int) Config {
	t.Helper()
	c, err := LoadConfig("config.json")
	if err != nil {
		t.Fatal(err)
	}
	c.Meta.APIKeyFile = ""
	c.Meta.APIKeyEnv = "METATHOUGHT_V009_LOCAL_TEST_KEY"
	t.Setenv(c.Meta.APIKeyEnv, "")
	c.Meta.BaseURL = "https://v009-fixture.invalid/v1"
	c.Models.Drone, c.Models.Queen = "v009-fixture-drone", "v009-forbidden-queen"
	c.Hive.DroneCount = drones
	c.Experiment.Objective, c.Experiment.Intent, c.Experiment.Context = v9OldSeedCanary, v9OldSeedCanary, v9OldSeedCanary
	c.Experiment.Criteria = []Criterion{{Name: v9OldSeedCanary, Description: v9OldSeedCanary}}
	c.MetaQueen.Objective, c.MetaQueen.Context = v9OldSeedCanary, v9OldSeedCanary
	c.MetaQueen.Criteria = []Criterion{{Name: v9OldSeedCanary, Description: v9OldSeedCanary}}
	// Put an actual canary in the diagnostic database without seeding operational
	// history. Every later model request must still contain operational data only.
	c.Schema.Logs = append(c.Schema.Logs, "INSERT INTO hive_log(ts,scope,event,details) VALUES('fixture','developer','fixture_canary','{\"canary\":\""+v9DiagnosticCanary+"\"}')")
	return c
}

type v9Input struct {
	Handoff string `json:"handoff"`
	Drone   struct {
		ID int64 `json:"id"`
	} `json:"drone"`
	Phase     string `json:"phase"`
	Inquiries []struct {
		ID         int64  `json:"id"`
		Question   string `json:"question"`
		ProposedBy int64  `json:"proposed_by"`
	} `json:"inquiries"`
	Memberships []struct {
		InquiryID int64 `json:"inquiry_id"`
		DroneID   int64 `json:"drone_id"`
	} `json:"memberships"`
	Work []struct {
		InquiryID       int64  `json:"inquiry_id"`
		TargetInquiryID int64  `json:"target_inquiry_id"`
		DroneID         int64  `json:"drone_id"`
		Kind            string `json:"kind"`
		Body            string `json:"body"`
	} `json:"work"`
}

func v9AssertInput(t *testing.T, c Config, request TextRequest, call int) v9Input {
	t.Helper()
	if request.Model != c.Models.Drone || request.Scope != "drone" || len(request.Messages) != 2 {
		t.Fatalf("call %d was not a single drone operation: %+v", call, request)
	}
	if request.Messages[0]["role"] != "developer" || request.Messages[1]["role"] != "user" {
		t.Fatalf("call %d changed message roles", call)
	}
	serialized, err := json.Marshal(request.Messages)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{v9DiagnosticCanary, v9OldSeedCanary, "observed_multiple_teams"} {
		if strings.Contains(string(serialized), forbidden) {
			t.Fatalf("call %d leaked non-operational context %q", call, forbidden)
		}
	}
	var input v9Input
	if err = json.Unmarshal([]byte(str(request.Messages[1]["content"])), &input); err != nil {
		t.Fatalf("call %d input: %v", call, err)
	}
	if input.Handoff != v9Handoff {
		t.Fatalf("call %d changed exact handoff: got %q", call, input.Handoff)
	}
	wantDrone := int64(((call - 1) / 2 % c.Hive.DroneCount) + 1)
	wantPhase := "contribute"
	if call%2 == 0 {
		wantPhase = "expand"
	}
	if input.Drone.ID != wantDrone || input.Phase != wantPhase {
		t.Fatalf("call %d got drone=%d phase=%s; want drone=%d phase=%s", call, input.Drone.ID, input.Phase, wantDrone, wantPhase)
	}
	if call == 1 {
		if len(input.Inquiries) != 1 || input.Inquiries[0].ID != 1 || input.Inquiries[0].Question != c.Prompts["inquiry_root"] || input.Inquiries[0].ProposedBy != 0 || len(input.Memberships) != 0 || len(input.Work) != 0 {
			t.Fatalf("root inquiry inherited work, invented an author, or changed its configured question: %+v", input)
		}
	}
	return input
}

func v9Reply(action, body string, relevance, inquiryValue float64) string {
	return fmt.Sprintf("# ACTION\n%s\n# YOUR ANSWER\n%s\n# YOUR SCORE\nrelevance: %g\ninquiry_value: %g", action, body, relevance, inquiryValue)
}

func v9ReadDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func v9Count(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func v9AssertSavedRequest(t *testing.T, c Config, dir string, result InquiryExperimentResult) *sql.DB {
	t.Helper()
	db := v9ReadDB(t, filepath.Join(dir, c.Files.State))
	var handoff, status, reason string
	if err := db.QueryRow("SELECT contract,status,finish_reason FROM hive_requests WHERE id=?", result.RequestID).Scan(&handoff, &status, &reason); err != nil {
		t.Fatal(err)
	}
	if handoff != v9Handoff || status != "paused" || reason != result.Status {
		t.Fatalf("request authority/outcome changed: contract=%q status=%q reason=%q result=%+v", handoff, status, reason, result)
	}
	var runStatus string
	if err := db.QueryRow("SELECT status FROM hive_runs WHERE request_id=?", result.RequestID).Scan(&runStatus); err != nil || runStatus != result.Status {
		t.Fatalf("run outcome not saved: got %q error=%v want %q", runStatus, err, result.Status)
	}
	if count := v9Count(t, db, "SELECT COUNT(*) FROM hive_requests"); count != 1 {
		t.Fatalf("unexpected additional root obligations: %d", count)
	}
	diagnostic := v9ReadDB(t, filepath.Join(dir, c.Files.Debug))
	if count := v9Count(t, diagnostic, "SELECT COUNT(*) FROM hive_log WHERE event='fixture_canary' AND details LIKE ?", "%"+v9DiagnosticCanary+"%"); count != 1 {
		t.Fatalf("diagnostic-only canary was not actually present: %d", count)
	}
	return db
}

func TestV009InquiryObservesOverlappingTeamsAndStopsBetweenCalls(t *testing.T) {
	c, dir := v9Config(t, 3), t.TempDir()
	replies := []string{
		v9Reply("JOIN 1", "First contributor exposes an unresolved assumption.", 0, 0.7),
		v9Reply("PROPOSE", "Compare whether two explanations make distinguishable predictions.", 0.8, 0.9),
		v9Reply("JOIN 1", "Second contributor changes the first assumption using a counterexample.", 0.9, 0.8),
		v9Reply("CONNECT 1 2", "The counterexample bears on the competing predictions; membership remains separate.", 0.8, 0.7),
		v9Reply("JOIN 2", "Third contributor develops a distinguishing observation for the proposed inquiry.", 0.9, 0.8),
	}
	calls := 0
	client := v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
		calls++
		input := v9AssertInput(t, c, request, calls)
		if calls > len(replies) {
			return TextResponse{}, fmt.Errorf("unexpected call %d after two teams actually contributed", calls)
		}
		if calls == 5 {
			if len(input.Memberships) != 3 || len(input.Work) != 4 {
				t.Fatalf("prior operational actions not visible: %+v", input)
			}
			for _, member := range input.Memberships {
				if member.InquiryID == 2 && member.DroneID == 2 {
					t.Fatal("CONNECT silently joined its target inquiry")
				}
			}
			found := false
			for _, work := range input.Work {
				found = found || work.Kind == "CONNECT" && work.InquiryID == 1 && work.TargetInquiryID == 2 && work.DroneID == 2
			}
			if !found {
				t.Fatal("accepted connection was absent from the next drone's operational board")
			}
		}
		return TextResponse{Text: replies[calls-1], Model: request.Model}, nil
	})
	result, err := RunInquiryExperiment(context.Background(), c, dir, v9Handoff, client)
	if err != nil {
		t.Fatal(err)
	}
	want := []InquiryTeam{
		{InquiryID: 1, Members: []int64{1, 2}, Contributors: []int64{1, 2}},
		{InquiryID: 2, Members: []int64{1, 3}, Contributors: []int64{1, 3}},
	}
	if result.Status != "observed_multiple_teams" || calls != 5 || result.Calls != 5 || !reflect.DeepEqual(result.ActiveTeams, want) {
		t.Fatalf("wrong observation boundary: calls=%d result=%+v want teams=%+v", calls, result, want)
	}
	db := v9AssertSavedRequest(t, c, dir, result)
	if v9Count(t, db, "SELECT COUNT(*) FROM hive_inquiry_work") != 5 || v9Count(t, db, "SELECT COUNT(*) FROM hive_inquiry_members") != 4 {
		t.Fatal("accepted work or overlapping commitments were not persisted")
	}
	if v9Count(t, db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE inquiry_id=2 AND drone_id=2") != 0 {
		t.Fatal("CONNECT persisted an unauthorized target membership")
	}
	var relevance, value float64
	if err = db.QueryRow("SELECT relevance,inquiry_value FROM hive_inquiry_work WHERE id=1").Scan(&relevance, &value); err != nil || relevance != 0 || value != 0.7 {
		t.Fatalf("valid zero or separate local score was lost: relevance=%v value=%v error=%v", relevance, value, err)
	}
}

func TestV009InquiryOneTeamDoesNotSatisfyObservationBoundary(t *testing.T) {
	c, dir := v9Config(t, 2), t.TempDir()
	calls := 0
	client := v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
		calls++
		v9AssertInput(t, c, request, calls)
		if calls > 8 {
			return TextResponse{}, errors.New("did not stop after a complete inactive cycle")
		}
		action := "PASS"
		if calls == 1 || calls == 3 {
			action = "JOIN 1"
		}
		return TextResponse{Text: v9Reply(action, "Local fixture contribution or explicit no useful addition.", 0.6, 0.6)}, nil
	})
	result, err := RunInquiryExperiment(context.Background(), c, dir, v9Handoff, client)
	if err != nil {
		t.Fatal(err)
	}
	want := []InquiryTeam{{InquiryID: 1, Members: []int64{1, 2}, Contributors: []int64{1, 2}}}
	if result.Status != "idle" || calls != 8 || result.Calls != 8 || !reflect.DeepEqual(result.ActiveTeams, want) {
		t.Fatalf("one team falsely met the stop boundary or idle cycle was ignored: calls=%d result=%+v", calls, result)
	}
	v9AssertSavedRequest(t, c, dir, result)
}

func TestV009InquiryRepeatedWorkDoesNotInventDistinctContributors(t *testing.T) {
	c, dir := v9Config(t, 4), t.TempDir()
	actions := []string{"PROPOSE", "CONTRIBUTE 2", "PROPOSE", "CONTRIBUTE 3", "JOIN 2", "CONNECT 2 3", "JOIN 3"}
	calls := 0
	client := v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
		calls++
		v9AssertInput(t, c, request, calls)
		if calls > len(actions) {
			return TextResponse{}, errors.New("continued after both independently proposed teams contributed")
		}
		return TextResponse{Text: v9Reply(actions[calls-1], fmt.Sprintf("Distinct accepted work %d; authorship must remain explicit.", calls), 0.8, 0.7)}, nil
	})
	result, err := RunInquiryExperiment(context.Background(), c, dir, v9Handoff, client)
	if err != nil {
		t.Fatal(err)
	}
	want := []InquiryTeam{
		{InquiryID: 2, Members: []int64{1, 3}, Contributors: []int64{1, 3}},
		{InquiryID: 3, Members: []int64{2, 4}, Contributors: []int64{2, 4}},
	}
	if result.Status != "observed_multiple_teams" || calls != 7 || result.Calls != 7 || !reflect.DeepEqual(result.ActiveTeams, want) {
		t.Fatalf("repeated work counted as distinct people, root counted as a team, or later work continued: calls=%d result=%+v", calls, result)
	}
	db := v9AssertSavedRequest(t, c, dir, result)
	if v9Count(t, db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE inquiry_id=1") != 0 || v9Count(t, db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE kind='CONTRIBUTE'") != 2 {
		t.Fatal("root gained implicit members or member contributions were not preserved")
	}
}

func TestV009InquiryAllUncommittedDronesParticipateBeforeIdle(t *testing.T) {
	c, dir := v9Config(t, 3), t.TempDir()
	calls := 0
	client := v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
		calls++
		v9AssertInput(t, c, request, calls)
		if calls > 6 {
			return TextResponse{}, errors.New("repeated an already idle full cycle")
		}
		return TextResponse{Text: v9Reply("PASS", "No justified contribution available in this fixture.", 0, 0)}, nil
	})
	result, err := RunInquiryExperiment(context.Background(), c, dir, v9Handoff, client)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "idle" || calls != 6 || result.Calls != 6 || len(result.ActiveTeams) != 0 {
		t.Fatalf("idle excluded uncommitted drones or invented a team: calls=%d result=%+v", calls, result)
	}
	db := v9AssertSavedRequest(t, c, dir, result)
	if v9Count(t, db, "SELECT COUNT(*) FROM hive_inquiries") != 1 || v9Count(t, db, "SELECT COUNT(*) FROM hive_inquiry_members") != 0 {
		t.Fatal("an idle pass invented an inquiry or commitment")
	}
}

func TestV009InquiryRejectsInvalidActionsWithoutAcceptedMutation(t *testing.T) {
	cases := []struct {
		name       string
		prior      string
		rejected   string
		inquiries  int
		members    int
		priorWorks int
	}{
		{"missing_join_target", "", v9Reply("JOIN 99", "REJECT_ME", 0.8, 0.8), 1, 0, 0},
		{"uncommitted_contribution", "PROPOSE", v9Reply("CONTRIBUTE 1", "REJECT_ME", 0.8, 0.8), 2, 1, 1},
		{"uncommitted_connection_source", "PROPOSE", v9Reply("CONNECT 1 2", "REJECT_ME", 0.8, 0.8), 2, 1, 1},
		{"missing_connection_target", "PROPOSE", v9Reply("CONNECT 2 99", "REJECT_ME", 0.8, 0.8), 2, 1, 1},
		{"self_connection", "PROPOSE", v9Reply("CONNECT 2 2", "REJECT_ME", 0.8, 0.8), 2, 1, 1},
		{"missing_score", "PROPOSE", "# ACTION\nJOIN 1\n# YOUR ANSWER\nREJECT_ME\n# YOUR SCORE\nrelevance: 0.8", 2, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, dir := v9Config(t, 3), t.TempDir()
			calls := 0
			client := v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
				calls++
				v9AssertInput(t, c, request, calls)
				if tc.prior != "" && calls == 1 {
					return TextResponse{Text: v9Reply(tc.prior, "Preserve this prior accepted inquiry and contribution.", 0.7, 0.8)}, nil
				}
				if calls > tc.priorWorks+1 {
					return TextResponse{}, errors.New("retried after rejected action")
				}
				return TextResponse{Text: tc.rejected}, nil
			})
			result, err := RunInquiryExperiment(context.Background(), c, dir, v9Handoff, client)
			if err == nil || result.Status != "failed" || calls != tc.priorWorks+1 || result.Calls != calls {
				t.Fatalf("invalid action did not fail once: calls=%d result=%+v error=%v", calls, result, err)
			}
			db := v9AssertSavedRequest(t, c, dir, result)
			if v9Count(t, db, "SELECT COUNT(*) FROM hive_inquiries") != tc.inquiries || v9Count(t, db, "SELECT COUNT(*) FROM hive_inquiry_members") != tc.members || v9Count(t, db, "SELECT COUNT(*) FROM hive_inquiry_work") != tc.priorWorks {
				t.Fatal("rejected action mutated operational state or removed prior accepted work")
			}
			if v9Count(t, db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE body LIKE '%REJECT_ME%'") != 0 {
				t.Fatal("rejected response was saved as accepted work")
			}
		})
	}
}

func TestV009InquiryProviderFailurePreservesEarlierWorkWithoutRetry(t *testing.T) {
	for _, refusal := range []bool{false, true} {
		name := "provider_error"
		if refusal {
			name = "refusal"
		}
		t.Run(name, func(t *testing.T) {
			c, dir := v9Config(t, 3), t.TempDir()
			calls := 0
			client := v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
				calls++
				v9AssertInput(t, c, request, calls)
				if calls == 1 {
					return TextResponse{Text: v9Reply("PROPOSE", "Preserve the accepted first inquiry.", 0.8, 0.7)}, nil
				}
				if calls > 2 {
					return TextResponse{}, errors.New("unexpected retry after provider failure")
				}
				if refusal {
					return TextResponse{Refusal: "Fixture refusal; this is not an accepted action."}, nil
				}
				return TextResponse{Text: "Partial provider output must remain rejected."}, errors.New("fixture provider failure")
			})
			result, err := RunInquiryExperiment(context.Background(), c, dir, v9Handoff, client)
			if err == nil || result.Status != "failed" || calls != 2 || result.Calls != 2 {
				t.Fatalf("provider failure was concealed or retried: calls=%d result=%+v error=%v", calls, result, err)
			}
			db := v9AssertSavedRequest(t, c, dir, result)
			if v9Count(t, db, "SELECT COUNT(*) FROM hive_inquiries") != 2 || v9Count(t, db, "SELECT COUNT(*) FROM hive_inquiry_members") != 1 || v9Count(t, db, "SELECT COUNT(*) FROM hive_inquiry_work") != 1 {
				t.Fatal("provider failure lost prior accepted work or accepted a failed response")
			}
		})
	}
}
