package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestV010ContractAuthorityReachesEveryWorkerThinkingPhase(t *testing.T) {
	h, _, _ := v10Fixture(t, 2, nil)
	h.Config.Workers.Count = 1
	h.Config.Dashboard.Enabled = false
	configured := h.Config.Prompts["worker_contract_authority"]
	if strings.TrimSpace(configured) == "" {
		t.Fatal("atomic contract authority instruction is missing from configuration")
	}
	// A configuration variation proves assembly uses the saved key rather than
	// an independently hardcoded copy of its current wording.
	authority := configured + " CONFIGURED_AUTHORITY_FIXTURE"
	h.Config.Prompts["worker_contract_authority"] = authority
	v10ConvergenceTeam(t, h, 2)
	v10Exec(t, h, "UPDATE hive_requests SET status='working' WHERE id=?", h.requestID)
	seen := map[string]int{}
	client := v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
		if len(request.Messages) < 2 || strings.Count(str(request.Messages[0]["content"]), authority) != 1 {
			return TextResponse{}, fmt.Errorf("configured authority was missing or duplicated in a worker developer prompt")
		}
		var input map[string]any
		if err := json.Unmarshal([]byte(str(request.Messages[1]["content"])), &input); err != nil {
			return TextResponse{}, err
		}
		if str(input["handoff"]) != v9Handoff {
			return TextResponse{}, fmt.Errorf("authority assembly changed the exact governing handoff")
		}
		phase := str(input["purpose"])
		if phase == "" {
			phase = str(input["phase"])
			if phase == "" {
				phase = "queen_monitor"
			}
		}
		seen[phase]++
		switch phase {
		case "contribute":
			return TextResponse{Text: v9Reply("CONTRIBUTE 1", "Synthetic fresh contribution under the current definitions.", 0.8, 0.7)}, nil
		case "expand":
			return TextResponse{Text: v9Reply("PASS", "No additional fixture connection is needed.", 0.8, 0.7)}, nil
		case "queen_monitor":
			return TextResponse{Text: v9Reply("BEGIN_COLLAPSE", "Assess the synthetic current team evidence.", 0.8, 0.7)}, nil
		case "team_report":
			return TextResponse{Text: v9Reply("CONTRIBUTE 1", "Synthetic report retains current definitions and attributed evidence.", 0.8, 0.7)}, nil
		case "team_assessment":
			return TextResponse{Text: v10ConvergenceAssessment("1")}, nil
		case "queen_return":
			reports, ok := input["reports"].([]any)
			if !ok || len(reports) != 1 {
				return TextResponse{}, fmt.Errorf("fixture lost its sole current team report")
			}
			report := reports[0].(map[string]any)
			return TextResponse{Text: v10ConvergenceReturn(id(report["id"]), "MET")}, nil
		default:
			return TextResponse{}, fmt.Errorf("unexpected worker phase %q", phase)
		}
	})
	result, err := runWorkerPool(v10Context(t), h, client)
	if err != nil || result.Status != "returned" {
		t.Fatalf("authority propagation fixture did not complete: %+v error=%v", result, err)
	}
	for _, phase := range []string{"contribute", "expand", "queen_monitor", "team_report", "team_assessment", "queen_return"} {
		if seen[phase] != 1 {
			t.Fatalf("expected one observed %s assembly, got %d", phase, seen[phase])
		}
	}
	if result.Calls != 6 {
		t.Fatalf("prompt propagation added a model call: %+v", result)
	}
	t.Log("one configured authority instruction reaches both drone phases, monitoring, reporting, assessment and final Queen decision; exact handoff is preserved")
}
