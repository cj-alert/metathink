package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestV010ConvergenceHandoffCriteriaPreserveDeclaredTargets(t *testing.T) {
	text := "queen_request:\n1. Novelty >= 1.0\n2. Human trials > 0.35\nA. Novelty >= 1.0\nB. Human trials > 0.35\nUnanswered: Does success mean mortality reduction > 0.35?\n"
	criteria, err := workerHandoffCriteria(text)
	if err != nil || len(criteria) != 2 {
		t.Fatalf("criteria=%+v err=%v", criteria, err)
	}
	if criteria[0].Name != "Novelty" || criteria[0].Target.Operator != ">=" || criteria[0].Target.Value != 1 || criteria[1].Name != "Human trials" || criteria[1].Target.Operator != ">" || criteria[1].Target.Met(0.35) {
		t.Fatalf("declared names or comparisons changed: %+v", criteria)
	}
	for name, bad := range map[string]string{
		"missing":       "queen_request:\nFind something useful.",
		"conflict":      text + "Novelty > 0.5\n",
		"case conflict": text + "novelty >= 1.0\n",
		"nonfinite":     "Novelty >= NaN\n",
		"outside scale": "Novelty > 1.1\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := workerHandoffCriteria(bad); err == nil {
				t.Fatal("ambiguous or invalid handoff accepted")
			}
		})
	}
}

func v10ConvergenceAssessment(score string) string {
	var reply strings.Builder
	for _, name := range []string{"Novelty", "Testability_Human_Trials", "Chance_of_Successfully_Solves_SIDS"} {
		fmt.Fprintf(&reply, "# %s\nScore: %s\nReason: This synthetic score exercises the declared comparison; it is not a research finding.\nEvidence: Fixture contains no external research or experiment.\nUncertainty: Clinical validity and novelty are unverified.\n", name, score)
	}
	reply.WriteString("# YOUR SCORE\nrelevance: 0.8\ninquiry_value: 0.7\n")
	return reply.String()
}

func v10ConvergenceReturn(reportID int64, criteria string) string {
	return fmt.Sprintf("# ACTION\nRETURN\n# REPORTS\n%d\n# CRITERIA\n%s\n# YOUR ANSWER\nThe team has an unverified proposal; the evidence does not establish a SIDS solution.\n# YOUR SCORE\nrelevance: 0.8\ninquiry_value: 0.7\n", reportID, criteria)
}

func v10ConvergenceBlocked(reportID int64, criteria string) string {
	return fmt.Sprintf("# ACTION\nBLOCKED\n# REPORTS\n%d\n# CRITERIA\n%s\n# YOUR ANSWER\nBlocker: The required external fixture evidence is unavailable.\nWhy alternatives cannot progress: The remaining alternatives depend on that same unavailable measurement.\nNeeded: Supply the external measurement before reassessment.\n# YOUR SCORE\nrelevance: 0.8\ninquiry_value: 0.7\n", reportID, criteria)
}

func v10ConvergenceTeam(t *testing.T, h *Hive, count int) {
	t.Helper()
	for drone := 1; drone <= count; drone++ {
		v10Exec(t, h, "INSERT INTO hive_inquiry_members(inquiry_id,drone_id,commitment,created) VALUES(1,?,?,?)", drone, fmt.Sprintf("I commit original thinking %d", drone), now())
		v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,?,'contribute','JOIN',?,0.7,0.6,?)", drone, fmt.Sprintf("Original attributed observation %d", drone), now())
	}
	v10Exec(t, h, "UPDATE hive_requests SET status='converging' WHERE id=?", h.requestID)
}

func TestV010ConvergenceReusesSavedReportAfterIncompleteAssessment(t *testing.T) {
	h, _, _ := v10Fixture(t, 2, nil)
	v10ConvergenceTeam(t, h, 2)
	counts := map[string]int{}
	var reporter, assessor int64
	var reportID int64
	client := v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
		input := str(request.Messages[1]["content"])
		if strings.Contains(input, v9DiagnosticCanary) || strings.Contains(input, v9OldSeedCanary) || strings.Contains(input, "debug_log") {
			t.Fatal("preset or diagnostic information entered convergence context")
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(input), &data); err != nil {
			t.Fatal(err)
		}
		if str(data["handoff"]) != v9Handoff {
			t.Fatal("exact accepted handoff was changed")
		}
		purpose := str(data["purpose"])
		counts[purpose]++
		switch purpose {
		case "team_report":
			reporter = id(data["drone_id"])
			if len(data["committed_members"].([]any)) != 2 || !strings.Contains(input, "Original attributed observation 1") || !strings.Contains(input, "Original attributed observation 2") {
				t.Fatal("report was not grounded in both committed contributors")
			}
			return TextResponse{Text: v9Reply("CONTRIBUTE 1", "A team proposal with two attributed observations; no claim of proven novelty.", 0.8, 0.7)}, nil
		case "team_assessment":
			assessor = id(data["drone_id"])
			if assessor == reporter {
				t.Fatal("reporter assessed its own report")
			}
			if counts[purpose] == 1 {
				return TextResponse{Text: strings.Replace(v10ConvergenceAssessment("1"), "Score: 1", "Score: NaN", 1)}, nil
			}
			return TextResponse{Text: v10ConvergenceAssessment("1")}, nil
		case "queen_return":
			reports := data["reports"].([]any)
			if len(reports) != 1 {
				t.Fatal("expected the one completed team report")
			}
			report := reports[0].(map[string]any)
			reportID = id(report["id"])
			if len(report["assessments"].([]any)) != 3 || id(report["assessor_id"]) == id(report["drone_id"]) {
				t.Fatal("Queen did not receive attributed complete parent scores")
			}
			return TextResponse{Text: v10ConvergenceReturn(reportID, "MET")}, nil
		default:
			return TextResponse{}, fmt.Errorf("unexpected purpose %q", purpose)
		}
	})
	first, err := h.CompleteWorkerRequest(v10Context(t), client)
	if err == nil || first.Calls != 2 {
		t.Fatalf("incomplete assessment should stop after report: %+v error=%v", first, err)
	}
	if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE kind='TEAM_REPORT'") != 1 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE kind='TEAM_ASSESSMENT'") != 0 {
		t.Fatal("incomplete assessment was persisted or successful report was discarded")
	}
	result, err := h.CompleteWorkerRequest(v10Context(t), client)
	if err != nil || result.Status != "returned" || result.Calls != 2 || result.CriteriaMet == nil || !*result.CriteriaMet {
		t.Fatalf("completion=%+v error=%v", result, err)
	}
	if counts["team_report"] != 1 || counts["team_assessment"] != 2 || counts["queen_return"] != 1 {
		t.Fatalf("completed report was replayed: %v", counts)
	}
	rows, err := h.query(context.Background(), "fixture_read", "hive_requests", "SELECT result,status,criteria_met FROM hive_requests WHERE id=?", h.requestID)
	if err != nil || str(rows[0]["result"]) != v10ConvergenceReturn(reportID, "MET") || id(rows[0]["criteria_met"]) != 1 {
		t.Fatalf("exact return/status not saved: %v %v", rows, err)
	}
	reused, err := h.CompleteWorkerRequest(v10Context(t), v9TextFunc(func(context.Context, TextRequest) (TextResponse, error) {
		t.Fatal("durable completed request called a model")
		return TextResponse{}, errors.New("must not call")
	}))
	if err != nil || reused.Calls != 0 || reused.Answer != result.Answer {
		t.Fatalf("saved return reuse=%+v error=%v", reused, err)
	}
}

func TestV010ConvergenceRejectsFalseMetAndReusesBothArtifacts(t *testing.T) {
	h, _, _ := v10Fixture(t, 2, nil)
	v10ConvergenceTeam(t, h, 2)
	queenCalls := 0
	client := v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
		var data map[string]any
		if err := json.Unmarshal([]byte(str(request.Messages[1]["content"])), &data); err != nil {
			t.Fatal(err)
		}
		switch str(data["purpose"]) {
		case "team_report":
			return TextResponse{Text: v9Reply("CONTRIBUTE 1", "Unverified team conclusion.", 0.8, 0.6)}, nil
		case "team_assessment":
			return TextResponse{Text: v10ConvergenceAssessment("0.2")}, nil
		case "queen_return":
			queenCalls++
			reportID := id(data["reports"].([]any)[0].(map[string]any)["id"])
			criteria := "MET"
			if queenCalls == 2 {
				return TextResponse{Text: v10ConvergenceReturn(reportID, "UNMET")}, nil
			}
			if queenCalls > 2 {
				return TextResponse{Text: v10ConvergenceBlocked(reportID, "UNCERTAIN")}, nil
			}
			return TextResponse{Text: v10ConvergenceReturn(reportID, criteria)}, nil
		}
		return TextResponse{}, errors.New("unexpected purpose")
	})
	first, err := h.CompleteWorkerRequest(v10Context(t), client)
	if err == nil || !strings.Contains(err.Error(), "misses target") || first.Calls != 3 {
		t.Fatalf("false success was accepted: %+v %v", first, err)
	}
	if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_requests WHERE status='returned'") != 0 {
		t.Fatal("false MET was persisted as returned")
	}
	second, err := h.CompleteWorkerRequest(v10Context(t), client)
	if err == nil || !strings.Contains(err.Error(), "new RETURN requires MET") || second.Calls != 1 {
		t.Fatalf("new unmet RETURN incorrectly ended the inquiry: %+v %v", second, err)
	}
	third, err := h.CompleteWorkerRequest(v10Context(t), client)
	if err != nil || third.Calls != 1 || third.CriteriaMet != nil || third.Status != "blocked" {
		t.Fatalf("assessment reuse/unknown status: %+v %v", third, err)
	}
}

func TestV010ConvergenceRequiresTeamAndDrainedWorkers(t *testing.T) {
	for _, variant := range []string{"single contributor", "active worker"} {
		t.Run(variant, func(t *testing.T) {
			h, _, _ := v10Fixture(t, 2, nil)
			count := 1
			if variant == "active worker" {
				count = 2
			}
			v10ConvergenceTeam(t, h, count)
			if variant == "active worker" {
				v10Exec(t, h, "UPDATE hive_worker_queue SET owner='still-running' WHERE drone_id=1")
			}
			result, err := h.CompleteWorkerRequest(v10Context(t), v9TextFunc(func(context.Context, TextRequest) (TextResponse, error) {
				t.Fatal("called a model before a real team/drained pool")
				return TextResponse{}, errors.New("must not call")
			}))
			if err == nil || result.Calls != 0 {
				t.Fatalf("invalid convergence accepted: %+v %v", result, err)
			}
		})
	}
}
