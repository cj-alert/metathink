package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestV010ContractRevisionExcludesOldProfilesAndReusesNewOnes(t *testing.T) {
	revised := strings.Replace(v9Handoff, "Novelty >= 1.0", "Novelty > 0.5", 1) + "\nNovelty asks whether this exact presented combination exists in the searched literature.\n"
	reportCalls, assessmentCalls := 0, 0
	client := v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
		var input map[string]any
		if err := json.Unmarshal([]byte(str(request.Messages[1]["content"])), &input); err != nil {
			return TextResponse{}, err
		}
		if str(input["handoff"]) != revised {
			return TextResponse{}, fmt.Errorf("team did not receive the exact revised handoff")
		}
		if input["prior_report"] != nil || input["prior_assessment"] != nil {
			return TextResponse{}, fmt.Errorf("pre-revision profile was presented as the current prior profile")
		}
		switch str(input["purpose"]) {
		case "team_report":
			reportCalls++
			return TextResponse{Text: v9Reply("CONTRIBUTE 1", "Revised-rubric synthetic team report.", 0.8, 0.7)}, nil
		case "team_assessment":
			assessmentCalls++
			return TextResponse{Text: v10ConvergenceAssessment("0.8")}, nil
		default:
			return TextResponse{}, fmt.Errorf("unexpected model purpose %q", str(input["purpose"]))
		}
	})
	h, config, dir := v10Fixture(t, 2, client)
	v10ConvergenceTeam(t, h, 2)
	oldReport := v9Reply("CONTRIBUTE 1", "Original report under the old rubric; retain verbatim.", 0.8, 0.7)
	oldAssessment := v10ConvergenceAssessment("0.2")
	oldReportID := v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,1,'convergence','TEAM_REPORT',?,0.8,0.7,?)", oldReport, now())
	oldAssessmentID := v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,2,'convergence','TEAM_ASSESSMENT',?,0.8,0.7,?)", oldAssessment, now())
	report, assessment, err := h.latestWorkerTeamRecords(v10Context(t), 1)
	if err != nil || id(report["id"]) != oldReportID || id(assessment["id"]) != oldAssessmentID {
		t.Fatalf("original profile was not current before revision: %v %v error=%v", report, assessment, err)
	}
	v10Exec(t, h, "UPDATE hive_requests SET status='paused' WHERE id=?", h.requestID)
	revision, err := h.reviseWorkerContract(v10Context(t), revised, "Use the exact presented combination for novelty; retain prior work.")
	if err != nil || revision.ThroughWorkID != oldAssessmentID {
		t.Fatalf("revision did not capture the saved work boundary: %+v error=%v", revision, err)
	}
	// A later ordinary guidance message must not hide the last revision.
	v10Exec(t, h, "INSERT INTO hive_guidance(request_id,tick,body,created) VALUES(?,0,?,?)", h.requestID, "Ordinary guidance without a JSON envelope.", now())
	peer := v10Peer(t, h, config, dir)
	cutoff, err := peer.workerContractCutoff(v10Context(t))
	if err != nil || cutoff != oldAssessmentID {
		t.Fatalf("reopened view lost the persisted revision boundary: %d error=%v", cutoff, err)
	}
	report, assessment, err = peer.latestWorkerTeamRecords(v10Context(t), 1)
	if err != nil || report != nil || assessment != nil {
		t.Fatalf("old profile remained current after revision: %v %v error=%v", report, assessment, err)
	}
	criteria, err := workerHandoffCriteria(revised)
	if err != nil {
		t.Fatal(err)
	}
	team := InquiryTeam{InquiryID: 1, Members: []int64{1, 2}, Contributors: []int64{1, 2}}
	v10Exec(t, h, "UPDATE hive_requests SET status='converging' WHERE id=?", h.requestID)
	fresh, err := h.completeWorkerTeam(v10Context(t), revised, criteria, team)
	if err != nil || fresh.ID <= cutoff || fresh.AssessmentID <= fresh.ID || reportCalls != 1 || assessmentCalls != 1 {
		t.Fatalf("revision did not create a fresh attributed profile: %+v calls=%d/%d error=%v", fresh, reportCalls, assessmentCalls, err)
	}
	if len(fresh.TargetStatuses) != 3 || fresh.TargetStatuses[0]["met"] != true {
		t.Fatalf("fresh score was not evaluated against the revised target: %+v", fresh.TargetStatuses)
	}
	report, assessment, err = peer.latestWorkerTeamRecords(v10Context(t), 1)
	if err != nil || id(report["id"]) != fresh.ID || id(assessment["id"]) != fresh.AssessmentID {
		t.Fatalf("post-revision profile was not available as current: %v %v error=%v", report, assessment, err)
	}
	reused, err := h.completeWorkerTeam(v10Context(t), revised, criteria, team)
	if err != nil || reused.ID != fresh.ID || reused.AssessmentID != fresh.AssessmentID || reportCalls != 1 || assessmentCalls != 1 {
		t.Fatalf("successful new profile was replayed: %+v calls=%d/%d error=%v", reused, reportCalls, assessmentCalls, err)
	}
	if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE id=? AND body=?", oldReportID, oldReport) != 1 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE id=? AND body=?", oldAssessmentID, oldAssessment) != 1 {
		t.Fatal("rubric revision altered original report or score history")
	}
	if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE kind IN ('TEAM_REPORT','TEAM_ASSESSMENT')") != 4 {
		t.Fatal("expected the original and revised report/assessment pairs")
	}
}
