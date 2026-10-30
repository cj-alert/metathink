package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const v10MissingResearchReason = "I cannot assess novelty until the missing prior-art search is performed; no equivalent prior art has been found."

func v10IncompleteProfile() string {
	return "# Novelty\nScore: INCOMPLETE\nReason: " + v10MissingResearchReason + "\nEvidence: The saved research record says the required search was unavailable.\nUncertainty: Novelty is unknown.\n" +
		"# Testability_Human_Trials\nScore: 0.7\nReason: A specified synthetic trial design supports this independent criterion.\nEvidence: The fixture contains the proposed controlled measurement.\nUncertainty: Actual feasibility remains unverified.\n" +
		"# Chance_of_Successfully_Solves_SIDS\nScore: 0\nReason: Synthetic counterevidence supports the declared zero anchor for this criterion.\nEvidence: The fixture explicitly rules out its proposed causal link.\nUncertainty: This says nothing about other hypotheses.\n" +
		"# YOUR SCORE\nrelevance: 0.8\ninquiry_value: 0.7\n"
}

func TestV010IncompleteAssessmentRetainsSupportedScoresWithoutInventingZero(t *testing.T) {
	criteria, err := workerHandoffCriteria(v9Handoff)
	if err != nil {
		t.Fatal(err)
	}
	profile := v10IncompleteProfile()
	assessments, local, err := parseWorkerAssessmentReply(profile, criteria, true)
	if err != nil || len(assessments) != 3 || !assessments[0].Incomplete || assessments[0].Reason != v10MissingResearchReason || assessments[1].Score != 0.7 || assessments[2].Incomplete || assessments[2].Score != 0 || local.Relevance != 0.8 {
		t.Fatalf("profile=%+v local=%+v error=%v", assessments, local, err)
	}
	encoded, err := json.Marshal(assessments)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err = json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded[0]["score"] != nil || decoded[0]["incomplete"] != true || decoded[1]["score"] != 0.7 || decoded[2]["score"] != float64(0) {
		t.Fatalf("missing assessment was confused with a genuine numeric zero: %s", encoded)
	}
	body, _, err := workerExtraSections(profile, "YOUR SCORE")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ParseAssessment(body, criteria); err == nil {
		t.Fatal("legacy numeric-table parser accepted INCOMPLETE")
	}
	for name, bad := range map[string]string{
		"missing reason":         strings.Replace(profile, "Reason: "+v10MissingResearchReason+"\n", "", 1),
		"empty reason":           strings.Replace(profile, v10MissingResearchReason, "", 1),
		"missing numeric reason": strings.Replace(profile, "Reason: A specified synthetic trial design supports this independent criterion.\n", "", 1),
		"missing score":          strings.Replace(profile, "Score: INCOMPLETE\n", "", 1),
		"missing evidence":       strings.Replace(profile, "Evidence: The saved research record says the required search was unavailable.\n", "", 1),
		"missing uncertainty":    strings.Replace(profile, "Uncertainty: Novelty is unknown.\n", "", 1),
		"nonfinite":              strings.Replace(profile, "Score: INCOMPLETE", "Score: NaN", 1),
		"outside scale":          strings.Replace(profile, "Score: 0.7", "Score: 1.1", 1),
		"ambiguous score":        strings.Replace(profile, "Score: INCOMPLETE", "Score: INCOMPLETE 0", 1),
		"duplicate criterion":    profile + "# Novelty\nScore: INCOMPLETE\nReason: missing search\nEvidence: unavailable\nUncertainty: unknown\n",
		"unknown criterion":      strings.Replace(profile, "# Novelty", "# Unrequested", 1),
		"missing criterion":      profile[strings.Index(profile, "# Testability_Human_Trials"):],
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseWorkerAssessmentReply(bad, criteria, true); err == nil {
				t.Fatal("invalid live profile was accepted")
			}
		})
	}
	t.Logf("Explicit incomplete novelty serializes as null; independent scores remain 0.7 and 0: %s", encoded)
}

func TestV010IncompleteAssessmentReachesQueenAndCannotPassTargets(t *testing.T) {
	for _, nextAction := range []string{"CONTINUE", "BLOCKED"} {
		t.Run(nextAction, func(t *testing.T) {
			h, _, _ := v10Fixture(t, 2, nil)
			v10ConvergenceTeam(t, h, 2)
			counts := map[string]int{}
			client := v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
				var input map[string]any
				if err := json.Unmarshal([]byte(str(request.Messages[1]["content"])), &input); err != nil {
					return TextResponse{}, err
				}
				purpose := str(input["purpose"])
				counts[purpose]++
				switch purpose {
				case "team_report":
					return TextResponse{Text: v9Reply("CONTRIBUTE 1", "Original hypothesis with an unresolved search prerequisite.", 0.8, 0.7)}, nil
				case "team_assessment":
					return TextResponse{Text: v10IncompleteProfile()}, nil
				case "queen_return":
					report := input["reports"].([]any)[0].(map[string]any)
					profile := report["assessments"].([]any)
					missing := profile[0].(map[string]any)
					targets := report["target_statuses"].([]any)
					if missing["score"] != nil || missing["incomplete"] != true || missing["reason"] != v10MissingResearchReason || targets[0].(map[string]any)["met"] != nil || profile[1].(map[string]any)["score"] != 0.7 || targets[2].(map[string]any)["met"] != false {
						return TextResponse{}, errors.New("Queen received an invented score or lost the independently supported criteria")
					}
					reportID := id(report["id"])
					if counts[purpose] == 1 {
						return TextResponse{Text: v10ConvergenceReturn(reportID, "MET")}, nil
					}
					if nextAction == "BLOCKED" {
						return TextResponse{Text: v10ConvergenceBlocked(reportID, "UNCERTAIN")}, nil
					}
					return TextResponse{Text: strings.Replace(v10ContinueReply(reportID), "# CRITERIA\nUNMET", "# CRITERIA\nUNCERTAIN", 1)}, nil
				}
				return TextResponse{}, fmt.Errorf("unexpected purpose %q", purpose)
			})
			first, err := h.CompleteWorkerRequest(v10Context(t), client)
			if err == nil || !strings.Contains(err.Error(), "incomplete assessment") || first.Calls != 3 {
				t.Fatalf("incomplete MET result=%+v error=%v", first, err)
			}
			var saved string
			if err := h.Store.db.QueryRow("SELECT body FROM hive_inquiry_work WHERE kind='TEAM_ASSESSMENT'").Scan(&saved); err != nil || saved != v10IncompleteProfile() {
				t.Fatalf("original incomplete profile was not persisted exactly: %q error=%v", saved, err)
			}
			second, err := h.CompleteWorkerRequest(v10Context(t), client)
			want := "working"
			if nextAction == "BLOCKED" {
				want = "blocked"
			}
			if err != nil || second.Status != want || second.Calls != 1 || second.CriteriaMet != nil || counts["team_report"] != 1 || counts["team_assessment"] != 1 {
				t.Fatalf("explicit incomplete routing=%+v counts=%v error=%v", second, counts, err)
			}
			if nextAction == "CONTINUE" {
				claim := v10Claim(t, h, "incomplete-feedback")
				input, err := h.WorkerInput(v10Context(t), claim)
				if err != nil || !strings.Contains(input, v10MissingResearchReason) || !strings.Contains(input, "Score: INCOMPLETE") {
					t.Fatalf("continued worker lost actual missing prerequisite: %s error=%v", input, err)
				}
			} else {
				reused, err := h.CompleteWorkerRequest(v10Context(t), v9TextFunc(func(context.Context, TextRequest) (TextResponse, error) {
					t.Fatal("blocked readback retried a model without explicit resume")
					return TextResponse{}, errors.New("no retry")
				}))
				if err != nil || reused.Status != "blocked" || reused.Calls != 0 || reused.CriteriaMet != nil {
					t.Fatalf("blocked readback=%+v error=%v", reused, err)
				}
			}
			t.Logf("Incomplete profile persisted; false MET rejected; %s reused original profile with unknown target", nextAction)
		})
	}
}

func TestV010ResearchRefreshesIncompleteProfileWithOriginalEvidence(t *testing.T) {
	h, _, _ := v10Fixture(t, 2, nil)
	v10ConvergenceTeam(t, h, 2)
	criteria, err := workerHandoffCriteria(v9Handoff)
	if err != nil {
		t.Fatal(err)
	}
	team := InquiryTeam{InquiryID: 1, Members: []int64{1, 2}, Contributors: []int64{1, 2}}
	researchBody := "{\"question\":\"Find equivalent prior art\",\"status\":\"completed\",\"answer\":\"Synthetic equivalent mechanism found.\",\"search_calls\":[{\"id\":\"original-search\"}],\"citations\":[{\"url\":\"https://example.test/paper\",\"title\":\"Synthetic prior art\"}]}"
	var researchID int64
	counts := map[string]int{}
	h.Text.Inner = v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
		var input map[string]any
		if err := json.Unmarshal([]byte(str(request.Messages[1]["content"])), &input); err != nil {
			return TextResponse{}, err
		}
		purpose := str(input["purpose"])
		counts[purpose]++
		if researchID != 0 {
			research, ok := input["research_work"].(map[string]any)
			if !ok || id(research["id"]) != researchID || str(research["body"]) != researchBody {
				return TextResponse{}, errors.New("new research original was not supplied to reporter/assessor")
			}
		}
		switch purpose {
		case "team_report":
			return TextResponse{Text: v9Reply("CONTRIBUTE 1", fmt.Sprintf("Report generation %d.", counts[purpose]), 0.8, 0.7)}, nil
		case "team_assessment":
			if researchID == 0 {
				return TextResponse{Text: v10IncompleteProfile()}, nil
			}
			return TextResponse{Text: strings.Replace(v10IncompleteProfile(), "Score: INCOMPLETE\nReason: "+v10MissingResearchReason, "Score: 0\nReason: The original search now supplies equivalent prior art, supporting the declared zero anchor.", 1)}, nil
		}
		return TextResponse{}, errors.New("research refresh fixture must not call Queen")
	})
	first, err := h.completeWorkerTeam(v10Context(t), v9Handoff, criteria, team)
	if err != nil || !first.Assessments[0].Incomplete {
		t.Fatalf("first profile=%+v error=%v", first, err)
	}
	researchID = v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,1,'contribute','RESEARCH',?,0.8,0.7,?)", researchBody, now())
	updated, err := h.completeWorkerTeam(v10Context(t), v9Handoff, criteria, team)
	if err != nil || updated.ID == first.ID || updated.AssessmentID == first.AssessmentID || updated.Assessments[0].Incomplete || updated.Assessments[0].Score != 0 {
		t.Fatalf("research did not refresh assessment correctly: %+v error=%v", updated, err)
	}
	reused, err := h.completeWorkerTeam(v10Context(t), v9Handoff, criteria, team)
	if err != nil || reused.ID != updated.ID || reused.AssessmentID != updated.AssessmentID || counts["team_report"] != 2 || counts["team_assessment"] != 2 {
		t.Fatalf("unchanged research repeated completed work: %+v counts=%v error=%v", reused, counts, err)
	}
	var original string
	if err := h.Store.db.QueryRow("SELECT body FROM hive_inquiry_work WHERE id=?", first.AssessmentID).Scan(&original); err != nil || original != v10IncompleteProfile() {
		t.Fatalf("earlier incomplete profile changed: %q error=%v", original, err)
	}
	t.Logf("Research %d refreshed report %d to %d; original incomplete assessment %d retained; actual subsequent zero preserved", researchID, first.ID, updated.ID, first.AssessmentID)
}

func TestV010LegacyNumericAssessmentWithoutReasonRemainsReadable(t *testing.T) {
	h, _, _ := v10Fixture(t, 2, nil)
	v10ConvergenceTeam(t, h, 2)
	criteria, err := workerHandoffCriteria(v9Handoff)
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.ReplaceAll(v10ConvergenceAssessment("0"), "Reason: This synthetic score exercises the declared comparison; it is not a research finding.\n", "")
	if _, _, err := parseWorkerAssessmentReply(legacy, criteria, true); err == nil {
		t.Fatal("new live numeric reply silently accepted missing Reason")
	}
	reportID := v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,1,'convergence','TEAM_REPORT',?,0.8,0.7,?)", v9Reply("CONTRIBUTE 1", "Historical report.", 0.8, 0.7), now())
	assessmentID := v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,2,'convergence','TEAM_ASSESSMENT',?,0.8,0.7,?)", legacy, now())
	result, err := h.completeWorkerTeam(v10Context(t), v9Handoff, criteria, InquiryTeam{InquiryID: 1, Members: []int64{1, 2}, Contributors: []int64{1, 2}})
	if err != nil || result.ID != reportID || result.AssessmentID != assessmentID || result.Assessments[0].Incomplete || result.Assessments[0].Score != 0 || result.Assessments[0].Reason != "" {
		t.Fatalf("legacy profile was changed or rejected: %+v error=%v", result, err)
	}
	var saved string
	if err := h.Store.db.QueryRow("SELECT body FROM hive_inquiry_work WHERE id=?", assessmentID).Scan(&saved); err != nil || saved != legacy {
		t.Fatalf("legacy original changed: %q error=%v", saved, err)
	}
}
