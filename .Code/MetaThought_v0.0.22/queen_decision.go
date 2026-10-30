package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// The explanation is data for the existing Decisions port. Its wording is never
// executed as a command. The existing Queen transaction remains the only writer.
func (h *Hive) routeWorkerQueen(ctx context.Context, input, reply string) (QueenWorkerAction, error) {
	reasoning, err := ParseScoredText(reply)
	if err != nil {
		return QueenWorkerAction{}, err
	}
	if h.Decisions == nil {
		return QueenWorkerAction{}, fmt.Errorf("Queen action requires the Decisions client")
	}
	priorPhase := h.Log.Fields["phase"]
	h.Log.Fields["phase"] = "queen_route"
	defer func() { h.Log.Fields["phase"] = priorPhase }()
	decision, err := h.Decisions.Decide(ctx, DecisionRequest{
		Model: h.Config.Models.Queen,
		Input: raw(jsonText(map[string]any{"operational_evidence": json.RawMessage(input), "queen_reasoning": reasoning})),
		Questions: []DecisionQuestion{{
			Type: "choice", Name: ptr("worker_queen_action"),
			Instructions: raw(h.Config.Prompts["worker_queen_route"]),
			Choices:      []DecisionChoice{{Value: "NOOP"}, {Value: "GUIDE"}, {Value: "BEGIN_COLLAPSE"}},
		}},
		LocalScoring: h.Config.Prompts["worker_queen_route_scores"],
		ContextKind:  "worker_queen_route",
	})
	if err != nil {
		return QueenWorkerAction{}, err
	}
	if len(decision.Answers) != 1 || decision.Answers[0].Type != "choice" {
		return QueenWorkerAction{}, fmt.Errorf("Queen routing requires one Decisions choice")
	}
	answer := decision.Answers[0]
	if answer.LocalAssessment == nil {
		return QueenWorkerAction{}, fmt.Errorf("Queen routing is missing its local actual scores")
	}
	kind, ok := answer.Choice.(string)
	if !ok || (kind != "NOOP" && kind != "GUIDE" && kind != "BEGIN_COLLAPSE") {
		return QueenWorkerAction{}, fmt.Errorf("Decisions returned an unsupported Queen action")
	}
	action := QueenWorkerAction{Kind: kind, Body: reasoning.Body,
		Relevance: reasoning.Relevance, InquiryValue: reasoning.InquiryValue}
	h.Log.Log("queen", "worker_queen_routed", map[string]any{
		"action": action, "routing_assessment": answer.LocalAssessment,
		"choice_confidence": answer.Confidence, "choice_probabilities": answer.Probabilities,
	})
	return action, nil
}
