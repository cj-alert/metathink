package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestV020OrderedDecisionValuesPreserveResponseTypes(t *testing.T) {
	cases := []struct {
		name  string
		q     DecisionQuestion
		reply string
		want  any
	}{
		{"predicate", DecisionQuestion{Type: "predicate"}, "0.7", 0.7},
		{"choice", DecisionQuestion{Type: "choice", Choices: []DecisionChoice{{Value: false}, {Value: "false"}}}, "1\n0.8\n0.2\n0.75", false},
		{"score", DecisionQuestion{Type: "score", Levels: []DecisionLevel{{Label: "Low"}, {Label: "Middle"}, {Label: "High"}}}, "0.25\n0.5\n0.25\n0.8", 1.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := parseDecisionReply(tc.q, tc.reply)
			if err != nil {
				t.Fatal(err)
			}
			switch tc.q.Type {
			case "predicate":
				if a.Probability == nil || *a.Probability != tc.want.(float64) {
					t.Fatalf("%+v", a)
				}
			case "choice":
				if a.Choice != tc.want {
					t.Fatalf("typed choice lost: %+v", a)
				}
			case "score":
				if a.Score == nil || *a.Score != tc.want.(float64) {
					t.Fatalf("%+v", a)
				}
			}
		})
	}
}

func TestV020OriginalRejectedProbabilityLayoutIsAccepted(t *testing.T) {
	reply := "# CHOICE\n2\n# PROBABILITIES\n0.07\n0.88\n0.05\n# CONFIDENCE\n0.88\n---\n0.92\n0.78"
	client := &decisionTestClient{replies: []TextResponse{{Text: reply}}}
	c := v10Config(t, 2, 4)
	d, err := NewMetaDecisions(client, c.Decisions, nil).Decide(context.Background(), DecisionRequest{
		Model: "fixture", Input: raw("Original evidence"), Questions: []DecisionQuestion{{Type: "choice", Instructions: raw("Choose action"),
			Choices: []DecisionChoice{{Value: "NOOP"}, {Value: "GUIDE"}, {Value: "BEGIN_COLLAPSE"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := d.Answers[0]
	if a.Choice != "GUIDE" || *a.Confidence != 0.88 || a.Probabilities[0].Probability != 0.07 || a.LocalAssessment == nil || a.LocalAssessment.Relevance != 0.92 || a.LocalAssessment.InquiryValue != 0.78 {
		t.Fatalf("values changed: %+v", a)
	}
}

func TestV020DefaultDecisionPromptsRequireSeparateActualScores(t *testing.T) {
	c := v10Config(t, 2, 4)
	for _, tc := range []struct {
		kind, reply string
		choices     []DecisionChoice
		levels      []DecisionLevel
	}{
		{"predicate", "0.7\n0.91\n0.82", nil, nil},
		{"choice", "2\n0.2\n0.8\n0.75\n0.91\n0.82", []DecisionChoice{{Value: "A"}, {Value: "B"}}, nil},
		{"score", "0.2\n0.8\n0.75\n0.91\n0.82", nil, []DecisionLevel{{Label: "Low"}, {Label: "High"}}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			client := &decisionTestClient{replies: []TextResponse{{Text: tc.reply}}}
			d, err := NewMetaDecisions(client, c.Decisions, nil).Decide(context.Background(), DecisionRequest{Model: "fixture", Input: raw("Evidence"), Questions: []DecisionQuestion{{Type: tc.kind, Instructions: raw("A local decision"), Choices: tc.choices, Levels: tc.levels}}})
			if err != nil {
				t.Fatal(err)
			}
			if d.Answers[0].LocalAssessment == nil || d.Answers[0].LocalAssessment.Relevance != 0.91 || d.Answers[0].LocalAssessment.InquiryValue != 0.82 {
				t.Fatalf("missing actual local scores: %+v", d)
			}
		})
	}
	client := &decisionTestClient{replies: []TextResponse{{Text: "0.7"}}}
	if _, err := NewMetaDecisions(client, c.Decisions, nil).Decide(context.Background(), DecisionRequest{Model: "fixture", Input: raw("Evidence"), Questions: []DecisionQuestion{{Type: "predicate", Instructions: raw("Assess")}}}); err == nil {
		t.Fatal("missing scores were treated as complete")
	}
}

func TestV020OrderedValuesStillRejectInvalidData(t *testing.T) {
	q := DecisionQuestion{Type: "choice", Choices: []DecisionChoice{{Value: "A"}, {Value: "B"}}}
	for _, reply := range []string{"2\n0.2\n0.8", "2\n0.2\n0.8\n1.1", "3\n0.2\n0.8\n0.9", "2\n0.2\n0.2\n0.9", "2\nNaN\n0.8\n0.9", "2\n0.2\n0.8\n0.9\n0.5"} {
		if _, err := parseDecisionReply(q, reply); err == nil {
			t.Fatalf("accepted invalid values %q", reply)
		}
	}
}

func TestV020ResearchUsesOneQuestionPerCallWithoutCommandMetadata(t *testing.T) {
	var questions []string
	client := v9TextFunc(func(_ context.Context, req TextRequest) (TextResponse, error) {
		if !req.WebSearch {
			t.Fatal("research did not enable web search")
		}
		var input struct {
			Question string `json:"question"`
		}
		if err := json.Unmarshal([]byte(str(req.Messages[1]["content"])), &input); err != nil {
			t.Fatal(err)
		}
		questions = append(questions, input.Question)
		response := v10ResearchReply()
		response.Text = "Related sources describe ingredients, but do not disclose the complete formulation. This answers only the supplied question; scope remains limited.\n0.9\n0.7"
		return response, nil
	})
	h, _, _ := v10Fixture(t, 2, client)
	claim := v10Claim(t, h, "web-series")
	v10ResearchMember(t, h, claim)
	for _, question := range []string{"Is this complete formulation already in clinical trials?", "Is this complete formulation already described in academic research?"} {
		action, err := h.ResearchWorker(context.Background(), claim, v10Action("RESEARCH", 1, question))
		if err != nil {
			t.Fatal(err)
		}
		var record ResearchRecord
		if err = json.Unmarshal([]byte(action.Body), &record); err != nil || record.Status != "completed" || record.Question != question || record.Answer == "" {
			t.Fatalf("research not retained: %+v %v", record, err)
		}
		if action.Kind != "RESEARCH" || action.InquiryID != 1 || action.Relevance != 0.9 || action.InquiryValue != 0.7 {
			t.Fatalf("findings changed known routing/scores: %+v", action)
		}
		if err = h.AdvanceWorker(context.Background(), claim, action); err != nil {
			t.Fatal(err)
		}
		claim = v10Claim(t, h, "web-series")
		input, err := h.WorkerInput(context.Background(), claim)
		if err != nil {
			t.Fatal(err)
		}
		var next struct {
			ResearchWork map[string]any `json:"research_work"`
		}
		if err = json.Unmarshal([]byte(input), &next); err != nil {
			t.Fatal(err)
		}
		var seen ResearchRecord
		if err = json.Unmarshal([]byte(str(next.ResearchWork["body"])), &seen); err != nil || seen.Answer != record.Answer {
			t.Fatalf("next turn lost the prior finding: %v", err)
		}
	}
	if len(questions) != 2 || questions[0] == questions[1] {
		t.Fatalf("atomic research questions lost: %v", questions)
	}
	if got := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE kind='RESEARCH'"); got != 2 {
		t.Fatalf("saved research=%d", got)
	}
}
