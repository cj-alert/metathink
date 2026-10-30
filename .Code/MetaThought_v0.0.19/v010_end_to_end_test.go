package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// These markers are synthetic test evidence, not research findings. The
// watchdog and barriers control only injected local calls, never live policy.
const v10EndLate = "LATE_ACCEPTED_CONTRIBUTION_AFTER_COLLAPSE_REQUEST"
const v10EndUptake = "UPTAKE_OF_ORIGINAL_CONNECTED_CONTRIBUTION"

func TestV010EndToEndTeamsDrainAndReturn(t *testing.T) {
	c, dir := v10Config(t, 10, 10), t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	initialStarted := make(chan int64, 10)
	initialGate, lateGate, queenReturned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var initialOnce, lateOnce, queenOnce sync.Once
	var mu sync.Mutex
	initial := map[int64]bool{}
	activeDrones := map[int64]bool{}
	active, peak := 0, 0
	proposed, connected, spawned, uptake := false, false, false, false
	sawConnection, sawChild, sawUptake := false, false, false
	var reportCalls, assessmentCalls, returnCalls int
	assertDrained := func() error {
		db, err := sql.Open("sqlite", filepath.Join(dir, c.Files.State))
		if err != nil {
			return err
		}
		defer db.Close()
		var owned, late int
		if err := db.QueryRow("SELECT (SELECT COUNT(*) FROM hive_worker_queue WHERE owner<>'')+(SELECT COUNT(*) FROM hive_worker_queen WHERE owner<>'' AND owner NOT LIKE 'convergence-%')").Scan(&owned); err != nil {
			return err
		}
		if err := db.QueryRow("SELECT COUNT(*) FROM hive_inquiry_work WHERE body=?", v10EndLate).Scan(&late); err != nil {
			return err
		}
		if owned != 0 || late != 1 {
			return fmt.Errorf("convergence began before in-flight work drained: owners=%d late_work=%d", owned, late)
		}
		return nil
	}

	// The worker portion is driven by the actor's real input, not a
	// predetermined call order.
	client := v9TextFunc(func(callCtx context.Context, request TextRequest) (TextResponse, error) {
		mu.Lock()
		active++
		if active > peak {
			peak = active
		}
		mu.Unlock()
		defer func() { mu.Lock(); active--; mu.Unlock() }()
		if len(request.Messages) != 2 {
			return TextResponse{}, errors.New("fixture expected exact role plus operational input")
		}
		text := str(request.Messages[1]["content"])
		var input map[string]any
		if err := json.Unmarshal([]byte(text), &input); err != nil {
			return TextResponse{}, err
		}
		if input["handoff"] != v9Handoff || strings.Contains(text, v9DiagnosticCanary) || strings.Contains(text, v9OldSeedCanary) {
			return TextResponse{}, errors.New("governing handoff changed or diagnostic/config seed leaked")
		}
		if phase, isWorker := input["phase"].(string); isWorker && (phase == "contribute" || phase == "expand") {
			if request.Model != c.Models.Drone {
				return TextResponse{}, errors.New("worker used the wrong configured model")
			}
			drone, ok := input["drone"].(map[string]any)
			if !ok {
				return TextResponse{}, errors.New("worker actor missing")
			}
			actor := id(drone["id"])
			mu.Lock()
			if activeDrones[actor] {
				mu.Unlock()
				return TextResponse{}, fmt.Errorf("drone %d has overlapping calls", actor)
			}
			activeDrones[actor] = true
			first := actor <= 10 && phase == "contribute" && !initial[actor]
			if first {
				initial[actor] = true
			}
			mu.Unlock()
			defer func() { mu.Lock(); delete(activeDrones, actor); mu.Unlock() }()
			if first {
				initialStarted <- actor
				select {
				case <-initialGate:
				case <-callCtx.Done():
					return TextResponse{}, callCtx.Err()
				}
			}
			if actor == 10 && first {
				select {
				case <-lateGate:
					return TextResponse{Text: v9Reply("JOIN 1", v10EndLate, 0.8, 0.7)}, nil
				case <-callCtx.Done():
					return TextResponse{}, callCtx.Err()
				}
			}
			member := map[int64]bool{}
			if rows, ok := input["memberships"].([]any); ok {
				for _, raw := range rows {
					if row, ok := raw.(map[string]any); ok {
						member[id(row["inquiry_id"])] = true
					}
				}
			}
			offer, _ := input["directory_offer"].(map[string]any)
			connection, _ := input["connection_offer"].(map[string]any)
			action, body := "PASS", "No new contribution in this fixture context."
			mu.Lock()
			switch {
			case actor == 1 && !member[1]:
				action, body = "JOIN 1", "Initial committed inquiry contribution."
			case actor == 1 && phase == "expand" && !proposed:
				proposed = true
				action, body = "PROPOSE", "A second inquiry develops an alternative representation."
			case actor == 1 && member[2] && !connected:
				connected = true
				action, body = "CONNECT 2 1", "CONNECTED_ORIGINAL: compare the two representations before inferring a shared conclusion."
			case actor == 2 && !member[2] && id(offer["id"]) == 2:
				action, body = "JOIN 2", "A second thinker develops the alternative representation."
			case actor == 2 && member[2] && !spawned:
				spawned = true
				action, body = "SPAWN 2", "Invite an additional perspective to examine the alternative."
			case actor == 3 && !member[1]:
				action, body = "JOIN 1", "Another committed contribution to the original inquiry."
			case actor == 3 && member[1] && connection != nil && !uptake:
				if id(connection["inquiry_id"]) != 2 || id(connection["target_inquiry_id"]) != 1 {
					mu.Unlock()
					return TextResponse{}, errors.New("wrong original CONNECT delivered to receiving inquiry")
				}
				uptake = true
				action, body = "CONTRIBUTE 1", fmt.Sprintf("%s; source work %d changes the representation, with uncertainty retained.", v10EndUptake, id(connection["id"]))
			case actor == 11 && !member[2]:
				action, body = "JOIN 2", "The spawned thinker contributes a distinct uncertainty to the alternative."
			}
			mu.Unlock()
			return TextResponse{Text: v9Reply(action, body, 0.8, 0.7)}, nil
		}
		if _, monitor := input["current_facts"]; monitor {
			if request.Model != c.Models.Queen {
				return TextResponse{}, errors.New("monitor used the wrong configured model")
			}
			var snapshot v10QueenContext
			if err := json.Unmarshal([]byte(text), &snapshot); err != nil {
				return TextResponse{}, err
			}
			if len(snapshot.Work) != 1 {
				return TextResponse{}, errors.New("Queen incremental sample changed")
			}
			w := snapshot.Work[0]
			mu.Lock()
			sawConnection = sawConnection || str(w["kind"]) == "CONNECT"
			sawChild = sawChild || (id(w["drone_id"]) == 11 && str(w["kind"]) == "JOIN")
			sawUptake = sawUptake || strings.Contains(str(w["body"]), v10EndUptake)
			ready := sawConnection && sawChild && sawUptake && snapshot.CurrentFacts.Request.ObservedTeamCount >= 2
			mu.Unlock()
			if ready {
				queenOnce.Do(func() { close(queenReturned) })
				return TextResponse{Text: v9Reply("BEGIN_COLLAPSE", "Return the teams' supported fixture conclusions and unmet criteria after accepting in-flight work.", 0.8, 0.7)}, nil
			}
			return TextResponse{Text: v9Reply("NOOP", "The fixture inquiry is still developing; no intervention.", 0.8, 0.7)}, nil
		}
		if err := assertDrained(); err != nil {
			return TextResponse{}, err
		}
		switch input["purpose"] {
		case "team_report":
			inquiry, ok := input["inquiry"].(map[string]any)
			if !ok || request.Model != c.Models.Drone {
				return TextResponse{}, errors.New("team report lacks its inquiry or configured drone model")
			}
			inquiryID := id(inquiry["id"])
			if inquiryID == 1 && (!strings.Contains(text, v10EndLate) || !strings.Contains(text, v10EndUptake)) {
				return TextResponse{}, errors.New("team report omitted accepted late work or the recipient's connected contribution")
			}
			mu.Lock()
			reportCalls++
			mu.Unlock()
			body := fmt.Sprintf("TEAM_%d_SUPPORTED_FIXTURE_REPORT. Evidence: saved member contributions and their source IDs. The fixture does not establish a research hypothesis; all parent targets remain unmet. Uncertainty: medical validity and novelty untested.", inquiryID)
			return TextResponse{Text: v9Reply(fmt.Sprintf("CONTRIBUTE %d", inquiryID), body, 0.8, 0.7)}, nil
		case "team_assessment":
			report, ok := input["report"].(map[string]any)
			if !ok || id(input["drone_id"]) <= 0 || id(input["drone_id"]) == id(report["drone_id"]) {
				return TextResponse{}, errors.New("team report did not receive a different drone's assessment")
			}
			elements, ok := input["scoring_elements"].([]any)
			if !ok || len(elements) != 3 || request.Model != c.Models.Drone {
				return TextResponse{}, errors.New("team assessment lacks parent scoring elements or configured drone model")
			}
			wanted := map[string]bool{"Novelty": true, "Testability_Human_Trials": true, "Chance_of_Successfully_Solves_SIDS": true}
			var reply strings.Builder
			for _, raw := range elements {
				element, ok := raw.(map[string]any)
				if !ok || !wanted[str(element["name"])] {
					return TextResponse{}, errors.New("parent scoring element missing, repeated, or replaced")
				}
				delete(wanted, str(element["name"]))
				fmt.Fprintf(&reply, "# %s\nScore: 0\nReason: The synthetic fixture deliberately assigns a genuine low score for the continuation check.\nEvidence: Synthetic fixture evidence supports no research success for this element.\nUncertainty: No empirical validation or novelty search.\n\n", str(element["name"]))
			}
			reply.WriteString("# YOUR SCORE\nrelevance: 0.8\ninquiry_value: 0.7")
			mu.Lock()
			assessmentCalls++
			mu.Unlock()
			return TextResponse{Text: reply.String()}, nil
		case "queen_return":
			reports, ok := input["reports"].([]any)
			if !ok || len(reports) != 2 || request.Model != c.Models.Queen {
				return TextResponse{}, errors.New("Queen return lacks both actual team reports or configured Queen model")
			}
			var ids []string
			for _, raw := range reports {
				report, ok := raw.(map[string]any)
				if !ok || id(report["id"]) <= 0 {
					return TextResponse{}, errors.New("saved team report ID missing")
				}
				ids = append(ids, fmt.Sprint(id(report["id"])))
			}
			for _, required := range []string{"TEAM_1_SUPPORTED_FIXTURE_REPORT", "TEAM_2_SUPPORTED_FIXTURE_REPORT"} {
				if !strings.Contains(text, required) {
					return TextResponse{}, errors.New("Queen return omitted an original team report")
				}
			}
			mu.Lock()
			returnCalls++
			mu.Unlock()
			return TextResponse{Text: "# ACTION\nBLOCKED\n# REPORTS\n" + strings.Join(ids, ",") + "\n# CRITERIA\nUNMET\n# YOUR ANSWER\nSUPPORTED_FINAL_FIXTURE_BLOCKED: both teams' contributions are retained; no research success is established and the parent criteria remain unmet.\nBlocker: Required external measurements are unavailable in this fixture.\nWhy alternatives cannot progress: Both remaining paths depend on these same measurements.\nNeeded: Provide the external measurements before reassessment.\n# YOUR SCORE\nrelevance: 0.8\ninquiry_value: 0.7"}, nil
		default:
			return TextResponse{}, fmt.Errorf("unexpected convergence purpose %v", input["purpose"])
		}
	})

	type completion struct {
		result InquiryExperimentResult
		err    error
	}
	finished := make(chan completion, 1)
	done := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		initialOnce.Do(func() { close(initialGate) })
		lateOnce.Do(func() { close(lateGate) })
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("local worker fixture did not drain after cleanup cancellation")
		}
	})
	go func() {
		result, err := RunWorkerExperiment(ctx, c, dir, v9Handoff, client)
		finished <- completion{result, err}
		close(done)
	}()
	started := map[int64]bool{}
	for len(started) < 10 {
		select {
		case actor := <-initialStarted:
			started[actor] = true
		case early := <-finished:
			t.Fatalf("runner stopped before ten configured slots entered calls: %+v", early)
		case <-ctx.Done():
			t.Fatal("ten configured slots did not enter independent calls")
		}
	}
	initialOnce.Do(func() { close(initialGate) })
	db := v9ReadDB(t, filepath.Join(dir, c.Files.State))
	select {
	case <-queenReturned:
	case early := <-finished:
		t.Fatalf("runner ended before Queen observed connected multi-team work: %+v", early)
	case <-ctx.Done():
		t.Fatal("connected teams did not reach the injected Queen convergence decision")
	}
	// Keep D10's call open until the stop is durable, then let its original
	// response finish. This is a drain proof, not a new post-stop dispatch.
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for {
		var status string
		if err := db.QueryRow("SELECT status FROM hive_requests ORDER BY id DESC LIMIT 1").Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status == "converging" {
			break
		}
		select {
		case early := <-finished:
			t.Fatalf("runner returned before its accepted in-flight response: %+v", early)
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("Queen convergence was not persisted")
		}
	}
	lateOnce.Do(func() { close(lateGate) })
	var final completion
	select {
	case final = <-finished:
	case <-ctx.Done():
		t.Fatal("worker convergence did not finish after its in-flight call drained")
	}
	if final.err != nil {
		t.Fatal(final.err)
	}
	wantTeams := []InquiryTeam{
		{InquiryID: 1, Members: []int64{1, 3, 10}, Contributors: []int64{1, 3, 10}},
		{InquiryID: 2, Members: []int64{1, 2, 11}, Contributors: []int64{1, 2, 11}},
	}
	if final.result.Status != "blocked" || !reflect.DeepEqual(final.result.ActiveTeams, wantTeams) || !strings.Contains(final.result.Answer, "SUPPORTED_FINAL_FIXTURE_BLOCKED") || final.result.CriteriaMet == nil || *final.result.CriteriaMet {
		t.Fatalf("request did not return from the actual overlapping teams: %+v", final.result)
	}
	mu.Lock()
	gotPeak, gotActive, reports, assessments, returns := peak, active, reportCalls, assessmentCalls, returnCalls
	mu.Unlock()
	if gotPeak != 10 || gotActive != 0 {
		t.Fatalf("configured slot or drain behavior changed: peak=%d remaining=%d", gotPeak, gotActive)
	}
	if reports != 2 || assessments != 2 || returns != 1 {
		t.Fatalf("expected two teams' completed reporting and one final return: reports=%d assessments=%d returns=%d", reports, assessments, returns)
	}
	if v9Count(t, db, "SELECT COUNT(*) FROM hive_drones") != 11 || v9Count(t, db, "SELECT COUNT(*) FROM hive_worker_queue WHERE owner<>''") != 0 || v9Count(t, db, "SELECT COUNT(*) FROM hive_worker_queen WHERE owner<>''") != 0 {
		t.Fatal("spawned actor missing or completed run retains an in-flight owner")
	}
	if v9Count(t, db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE body=?", v10EndLate) != 1 || v9Count(t, db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE kind='CONNECT' AND inquiry_id=2 AND target_inquiry_id=1") != 1 || v9Count(t, db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE drone_id=3 AND kind='CONTRIBUTE' AND body LIKE ?", v10EndUptake+"%") != 1 {
		t.Fatal("late response, original connection, or actual recipient uptake was not persisted exactly once")
	}
	if v9Count(t, db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE kind='TEAM_REPORT' AND phase='convergence'") != 2 || v9Count(t, db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE kind='TEAM_ASSESSMENT' AND phase='convergence'") != 2 {
		t.Fatal("team reports or their separate assessments were not persisted")
	}
	var status, answer string
	var criteriaMet sql.NullBool
	if err := db.QueryRow("SELECT status,result,criteria_met FROM hive_requests WHERE id=?", final.result.RequestID).Scan(&status, &answer, &criteriaMet); err != nil {
		t.Fatal(err)
	}
	if status != "blocked" || !strings.Contains(answer, "SUPPORTED_FINAL_FIXTURE_BLOCKED") || !criteriaMet.Valid || criteriaMet.Bool {
		t.Fatalf("persisted return lost its answer or honest unmet status: status=%s criteria=%+v answer=%s", status, criteriaMet, answer)
	}
}
