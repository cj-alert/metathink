package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Research is ordinary inquiry evidence, never a view into diagnostic logs.
// The request and provider answer stay intact even when the search cannot run.
type ResearchRecord struct {
	Question          string            `json:"question"`
	Status            string            `json:"status"`
	Need              string            `json:"need,omitempty"`
	Answer            string            `json:"answer,omitempty"`
	SearchCalls       []json.RawMessage `json:"search_calls"`
	Citations         []json.RawMessage `json:"citations"`
	LocalScoreSubject string            `json:"local_score_subject"`
}

func (h *Hive) latestWorkerResearch(ctx context.Context, inquiryID int64) (map[string]any, error) {
	rows, err := h.query(ctx, "latest_research", "hive_inquiry_work",
		`SELECT w.* FROM hive_inquiry_work w JOIN hive_inquiries i ON i.id=w.inquiry_id
		 WHERE w.inquiry_id=? AND i.request_id=? AND w.kind='RESEARCH' ORDER BY w.id DESC LIMIT 1`, inquiryID, h.requestID)
	if err != nil {
		return nil, err
	}
	return workerOne(rows), nil
}

// ResearchWorker checks the existing claim and membership before spending a
// provider call. No separate research queue, retry budget or router is needed.
func (h *Hive) ResearchWorker(ctx context.Context, claim WorkerClaim, action InquiryAction) (InquiryAction, error) {
	if action.Kind != "RESEARCH" || claim.RequestID != h.requestID {
		return action, fmt.Errorf("research requires an action for the current request")
	}
	if err := h.workerInitialFocusAction(ctx, claim, action); err != nil {
		return action, err
	}
	rows, err := h.workerQuery(ctx, claim, "research_authority", "hive_worker_queue",
		`SELECT r.status FROM hive_worker_queue q JOIN hive_requests r ON r.id=q.request_id
		 JOIN hive_inquiries i ON i.id=? AND i.request_id=r.id
		 JOIN hive_inquiry_members m ON m.inquiry_id=i.id AND m.drone_id=q.drone_id
		 WHERE q.drone_id=? AND q.request_id=? AND q.owner=? AND q.phase=?`,
		action.InquiryID, claim.DroneID, claim.RequestID, claim.Owner, claim.Phase)
	if err != nil {
		return action, err
	}
	if len(rows) != 1 {
		return action, fmt.Errorf("research requires the current worker claim and inquiry membership")
	}
	if str(rows[0]["status"]) != "working" {
		action.Body = jsonText(ResearchRecord{Question: action.Body, Status: "not_started", Need: "The request stopped before research could begin.", LocalScoreSubject: "research_request"})
		return action, nil
	}
	return h.performResearch(ctx, action)
}

func (h *Hive) performResearch(ctx context.Context, action InquiryAction) (InquiryAction, error) {
	record := ResearchRecord{Question: action.Body, Status: "unavailable", LocalScoreSubject: "research_request",
		SearchCalls: []json.RawMessage{}, Citations: []json.RawMessage{}}
	source := h.Config.Prompts["worker_research"]
	if strings.TrimSpace(source) == "" {
		record.Need = "The research prompt is missing from this run's configuration; internet research was not attempted."
	} else {
		rows, err := h.query(ctx, "research_handoff", "hive_requests", "SELECT contract FROM hive_requests WHERE id=?", h.requestID)
		if err != nil {
			return action, err
		}
		if len(rows) != 1 {
			return action, fmt.Errorf("research request is missing")
		}
		input := jsonText(map[string]any{"purpose": "research", "inquiry_id": action.InquiryID, "question": action.Body, "handoff": rows[0]["contract"]})
		response, callErr := h.Text.Complete(ctx, TextRequest{Scope: "drone", Model: h.Config.Models.Drone, WebSearch: true,
			Messages: []Message{{"role": "developer", "content": source}, {"role": "user", "content": input}}})
		if workerRetryExhausted(callErr) {
			return action, callErr
		}
		record.Answer, record.SearchCalls, record.Citations = response.Text, response.SearchCalls, response.Citations
		switch {
		case callErr != nil:
			record.Need = "Internet research could not complete: " + callErr.Error()
		case response.Refusal != "":
			record.Need = "The research request was refused: " + response.Refusal
		default:
			record.Status, record.Need = researchSearchState(response)
			if record.Status == "completed" {
				findings, parseErr := ParseScoredText(response.Text)
				if parseErr != nil {
					record.Status = "response_invalid"
					record.Need = "The research answer did not follow the requested findings and local-score format."
					if parseErr != nil {
						record.Need += " " + parseErr.Error()
					}
				} else {
					action.Relevance, action.InquiryValue = findings.Relevance, findings.InquiryValue
					record.LocalScoreSubject = "research_result"
				}
			}
		}
	}
	// Missing research is an operational complaint, not an invented parent
	// score or an automatic retry. The original request's local scores describe
	// the requested investigation when no valid scored findings were returned.
	action.Body = jsonText(record)
	h.Log.Log("hive", "research_result", map[string]any{"inquiry_id": action.InquiryID, "status": record.Status,
		"need": record.Need, "search_calls": len(record.SearchCalls), "citations": len(record.Citations), "body": action.Body})
	return action, nil
}

func researchSearchState(response TextResponse) (string, string) {
	if len(response.SearchCalls) == 0 {
		return "not_performed", "The provider returned no search execution. Research evidence is still needed; an answer from model memory cannot establish the required search."
	}
	hasSource := len(response.Citations) > 0
	for _, raw := range response.SearchCalls {
		var call struct {
			Type    string            `json:"type"`
			Status  string            `json:"status"`
			Results []json.RawMessage `json:"results"`
		}
		if err := json.Unmarshal(raw, &call); err != nil || call.Type != "web_search_call" || call.Status != "completed" {
			return "incomplete", "The provider did not report completed research for every returned search call; the required investigation remains incomplete."
		}
		hasSource = hasSource || len(call.Results) > 0
	}
	if !hasSource {
		return "insufficient_evidence", "Search ran but returned no inspectable sources. This does not establish absence of prior art; further evidence or a different query is needed."
	}
	if strings.TrimSpace(response.Text) == "" {
		return "incomplete", "Search returned sources but no findings. The research question still needs an evidence-supported answer."
	}
	return "completed", ""
}
