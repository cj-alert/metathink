package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func v10Exhausted500() error {
	return &ProviderError{Kind: "retry_exhausted", StatusCode: 500, Attempts: 3, Message: "Original provider internal error"}
}

func TestV010ProviderPauseClassifiesOnlyExhausted500(t *testing.T) {
	if !workerRetryExhausted(fmt.Errorf("wrapped: %w", v10Exhausted500())) || !workerRetryExhausted(errors.Join(v10Exhausted500(), v10Exhausted500())) {
		t.Fatal("known fully received exhaustion lost its classification")
	}
	for _, err := range []error{nil, errors.New("HTTP 500 text is not classification"), &ProviderError{Kind: "http_error", StatusCode: 500}, &ProviderError{Kind: "retry_exhausted", StatusCode: 503}, errors.Join(v10Exhausted500(), errors.New("SQLite write failed"))} {
		if workerRetryExhausted(err) {
			t.Fatalf("uncertain/mixed failure classified safe to replay: %v", err)
		}
	}
}

func TestV010ProviderPauseRequiresSavedDrainCompletionBeforeResume(t *testing.T) {
	h, config, dir := v10Fixture(t, 1, nil)
	config.Dashboard.Enabled = false
	paused, err := h.pauseWorkerProvider(v10Context(t), v10Exhausted500(), false)
	if err != nil || paused.ReadyForResume {
		t.Fatalf("pause=%+v error=%v", paused, err)
	}
	read, err := h.savedWorkerProviderPause(v10Context(t))
	if err != nil || read.ReadyForResume {
		t.Fatalf("empty claims incorrectly erased unfinished-drain flag: %+v error=%v", read, err)
	}
	_, err = ResumeWorkerExperiment(v10Context(t), config, dir, v9TextFunc(func(context.Context, TextRequest) (TextResponse, error) {
		t.Fatal("resume called a model before the saved drain finished")
		return TextResponse{}, errors.New("must not call")
	}))
	if err == nil || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_requests WHERE status='provider_unavailable'") != 1 {
		t.Fatalf("premature resume changed phase: %v", err)
	}
}

func TestV010ProviderPauseDrainsConcurrentWorkAndDirectResumePreservesPhase(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprintf("mixed_failure_%t", mixed), func(t *testing.T) {
			h, config, dir := v10Fixture(t, 2, nil)
			v10ConvergenceTeam(t, h, 2)
			v10Exec(t, h, "UPDATE hive_requests SET status='working' WHERE id=?", h.requestID)
			v10Exec(t, h, "UPDATE hive_worker_queue SET phase='expand' WHERE drone_id=1")
			h.Config.Workers.Count, h.Config.Dashboard.Enabled = 2, false
			priorWork := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work")
			started, paused := make(chan struct{}), make(chan struct{})
			var once sync.Once
			sink := h.Store.log
			h.Store.log = v10PhaseLogFunc(func(scope, event string, fields map[string]any) {
				sink.Log(scope, event, fields)
				if event == "sql_done" && fields["sql_op"] == "pause_provider" {
					once.Do(func() { close(paused) })
				}
			})
			client := v9TextFunc(func(ctx context.Context, request TextRequest) (TextResponse, error) {
				var input map[string]any
				if err := json.Unmarshal([]byte(str(request.Messages[1]["content"])), &input); err != nil {
					return TextResponse{}, err
				}
				drone, ok := input["drone"].(map[string]any)
				if !ok {
					return TextResponse{}, errors.New("unexpected Queen dispatch during drain fixture")
				}
				if id(drone["id"]) == 1 {
					select {
					case <-started:
					case <-ctx.Done():
						return TextResponse{}, ctx.Err()
					}
					return TextResponse{}, v10Exhausted500()
				}
				close(started)
				select {
				case <-paused:
				case <-ctx.Done():
					return TextResponse{}, ctx.Err()
				}
				if mixed {
					return TextResponse{}, errors.New("uncertain ordinary transport failure while draining")
				}
				return TextResponse{Text: v9Reply("CONTRIBUTE 1", "DRAINED_SUCCESSFUL_WORK", 0.8, 0.7)}, nil
			})
			result, err := runWorkerPool(v10Context(t), h, client)
			if err == nil || result.Calls != 2 {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if mixed {
				if result.Status != "failed" || result.ReadyForResume || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_queue WHERE drone_id=2 AND owner<>''") != 1 {
					t.Fatalf("mixed error released an ambiguous claim or advertised clean resume: %+v", result)
				}
				return
			}
			if result.Status != "provider_unavailable" || !result.ReadyForResume || result.ProviderStatusCode != 500 || result.ProviderAttempts != 3 || !strings.Contains(result.Error, "Original provider internal error") {
				t.Fatalf("clean exhausted result=%+v", result)
			}
			if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_queue WHERE owner<>''") != 0 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_queue WHERE phase='expand'") != 2 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work") != priorWork+1 {
				t.Fatal("drain lost completed work, retained claims or changed the failed actor phase")
			}
			config.Workers.Count, config.Dashboard.Enabled = 1, false
			resumedCalls := 0
			resumed, err := ResumeWorkerExperiment(v10Context(t), config, dir, v9TextFunc(func(ctx context.Context, request TextRequest) (TextResponse, error) {
				resumedCalls++
				var input map[string]any
				if e := json.Unmarshal([]byte(str(request.Messages[1]["content"])), &input); e != nil {
					return TextResponse{}, e
				}
				drone, ok := input["drone"].(map[string]any)
				if !ok || id(drone["id"]) != 1 || str(input["phase"]) != "expand" {
					return TextResponse{}, errors.New("resume replayed the wrong actor or phase")
				}
				_, e := h.exec(ctx, "fixture_operator_pause", "hive_requests", "UPDATE hive_requests SET status='paused',finish_reason='observation_paused' WHERE id=?", h.requestID)
				if e != nil {
					return TextResponse{}, e
				}
				return TextResponse{Text: v9Reply("CONTRIBUTE 1", "RESUMED_EXACT_EXPAND_PHASE", 0.8, 0.7)}, nil
			}))
			if err != nil || resumedCalls != 1 || resumed.Calls != 1 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE body='DRAINED_SUCCESSFUL_WORK'") != 1 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work") != priorWork+2 {
				t.Fatalf("direct resume replayed or discarded work: %+v calls=%d error=%v", resumed, resumedCalls, err)
			}
			t.Log("HTTP500 exhaustion stopped dispatch; concurrent successful work committed; direct resume completed only the failed Expand phase")
		})
	}
}

func TestV010ProviderPauseQueenDoesNotAdvanceReviewOrReleaseOrdinaryFailure(t *testing.T) {
	for _, exhausted := range []bool{true, false} {
		t.Run(fmt.Sprintf("exhausted_%t", exhausted), func(t *testing.T) {
			h, _, _ := v10Fixture(t, 1, v9TextFunc(func(context.Context, TextRequest) (TextResponse, error) {
				if exhausted {
					return TextResponse{}, v10Exhausted500()
				}
				return TextResponse{}, &ProviderError{Kind: "transport_error", Message: "uncertain response"}
			}))
			v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,1,'contribute','JOIN','original item',0.8,0.7,?)", now())
			result, err := h.MonitorWorkerQueen(v10Context(t), "queen-exhaustion")
			if err == nil || !result.Called || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_queen_seen") != 0 {
				t.Fatalf("monitor=%+v error=%v", result, err)
			}
			owners := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_queen WHERE owner<>''")
			if exhausted && (owners != 0 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_requests WHERE status='provider_unavailable'") != 1) {
				t.Fatal("exhausted Queen retained claim or dispatch stayed active")
			}
			if !exhausted && owners != 1 {
				t.Fatal("ordinary uncertain Queen failure was made replayable")
			}
		})
	}
}

func TestV010ProviderPauseResearchDoesNotCommitFakeOutcome(t *testing.T) {
	h, _, _ := v10Fixture(t, 1, nil)
	h.Config.Workers.Count, h.Config.Dashboard.Enabled = 1, false
	v10Exec(t, h, "INSERT INTO hive_inquiry_members(inquiry_id,drone_id,commitment,created) VALUES(1,1,'Research commitment',?)", now())
	result, err := runWorkerPool(v10Context(t), h, v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
		if request.WebSearch {
			return TextResponse{}, v10Exhausted500()
		}
		return TextResponse{Text: v9Reply("RESEARCH 1", "Find the necessary source.", 0.8, 0.7)}, nil
	}))
	if err == nil || result.Status != "provider_unavailable" || !result.ReadyForResume || result.Calls != 2 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE kind='RESEARCH'") != 0 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_queue WHERE owner='' AND phase='contribute'") != 1 {
		t.Fatalf("research exhaustion became fake evidence or advanced phase: %+v error=%v", result, err)
	}
}

func TestV010ProviderPauseConvergenceResumesOnlyUnfinishedCall(t *testing.T) {
	for _, failedPurpose := range []string{"team_report", "team_assessment", "queen_return"} {
		t.Run(failedPurpose, func(t *testing.T) {
			h, config, dir := v10Fixture(t, 2, nil)
			v10ConvergenceTeam(t, h, 2)
			h.Config.Dashboard.Enabled, config.Dashboard.Enabled = false, false
			counts := map[string]int{}
			client := v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
				var input map[string]any
				if e := json.Unmarshal([]byte(str(request.Messages[1]["content"])), &input); e != nil {
					return TextResponse{}, e
				}
				purpose := str(input["purpose"])
				counts[purpose]++
				if purpose == failedPurpose && counts[purpose] == 1 {
					return TextResponse{}, v10Exhausted500()
				}
				switch purpose {
				case "team_report":
					return TextResponse{Text: v9Reply("CONTRIBUTE 1", "Preserved team report.", 0.8, 0.7)}, nil
				case "team_assessment":
					return TextResponse{Text: v10ConvergenceAssessment("1")}, nil
				case "queen_return":
					report := input["reports"].([]any)[0].(map[string]any)
					return TextResponse{Text: v10ConvergenceReturn(id(report["id"]), "MET")}, nil
				}
				return TextResponse{}, fmt.Errorf("convergence resume unexpectedly explored: %q", purpose)
			})
			first, err := runWorkerPool(v10Context(t), h, client)
			if err == nil || first.Status != "provider_unavailable" || !first.ReadyForResume {
				t.Fatalf("first=%+v error=%v", first, err)
			}
			var saved string
			if e := h.Store.db.QueryRow("SELECT result FROM hive_requests WHERE id=?", h.requestID).Scan(&saved); e != nil {
				t.Fatal(e)
			}
			var pause workerProviderPause
			if e := json.Unmarshal([]byte(saved), &pause); e != nil || pause.ResumePhase != "converging" {
				t.Fatalf("pause=%+v error=%v", pause, e)
			}
			second, err := ResumeWorkerExperiment(v10Context(t), config, dir, client)
			if err != nil || second.Status != "returned" || second.CriteriaMet == nil || !*second.CriteriaMet {
				t.Fatalf("resume=%+v error=%v", second, err)
			}
			for _, purpose := range []string{"team_report", "team_assessment", "queen_return"} {
				want := 1
				if purpose == failedPurpose {
					want = 2
				}
				if counts[purpose] != want {
					t.Fatalf("completed artifact replay: counts=%v failed=%s", counts, failedPurpose)
				}
			}
			if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE kind='TEAM_REPORT'") != 1 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE kind='TEAM_ASSESSMENT'") != 1 {
				t.Fatal("resume duplicated completed report/assessment")
			}
			t.Logf("failed %s resumed in convergence; counts=%v", failedPurpose, counts)
		})
	}
}
