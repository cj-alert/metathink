package main

import (
	"strings"
	"testing"
)

func TestV010DashboardContractRevisionSeparatesCurrentGradesAndOriginalHistory(t *testing.T) {
	h, _, _ := v10Fixture(t, 2, nil)
	ctx := v10Context(t)
	insert := func(body string) int64 {
		t.Helper()
		return v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,2,'convergence','TEAM_ASSESSMENT',?,0.8,0.7,?)", body, now())
	}
	original := v10ConvergenceAssessment("0.2")
	originalID := insert(original)
	revised := strings.Replace(v9Handoff, "Novelty >= 1.0", "Novelty > 0.5", 1)
	v10Exec(t, h, "UPDATE hive_requests SET status='paused' WHERE id=?", h.requestID)
	if _, err := h.reviseWorkerContract(ctx, revised, "Use the revised target; preserve all previous evidence."); err != nil {
		t.Fatal(err)
	}
	// An unrelated later guidance record must not hide the revision boundary.
	v10Exec(t, h, "INSERT INTO hive_guidance(request_id,tick,body,created) VALUES(?,0,?,?)", h.requestID, "Ordinary guidance", now())
	pending, err := h.dashboardSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending.Assessments) != 0 || len(pending.AssessmentHistory) != 3 {
		t.Fatalf("pre-revision grades remained current: current=%#v history=%#v", pending.Assessments, pending.AssessmentHistory)
	}
	vm := v10DashboardScript(t, pending, `{"selection":{"kind":"team","id":1},"tab":"objective"}`)
	_, err = vm.RunString(`
function revisionText(e){return [e.textContent||""].concat(e.children.map(revisionText)).join("\n");}
const area=document.getElementById("detail-content");
if(area.children.some(function(e){return e.className==="criterion-score";}))throw new Error("Earlier-contract grade became a current card");
if(!revisionText(area).includes("Parent criterion assessment pending")||!revisionText(area).includes("Earlier contract assessment evidence")||!revisionText(area).includes("Target: >= 1"))throw new Error("Pending current rubric or original history/target is missing");
`)
	if err != nil {
		t.Fatal(err)
	}
	middle := v10ConvergenceAssessment("0.6")
	middleID := insert(middle)
	renamed := strings.Replace(revised, "Novelty > 0.5", "CombinationNovelty > 0.75", 1)
	if _, err = h.reviseWorkerContract(ctx, renamed, "Rename this criterion and preserve its earlier versions."); err != nil {
		t.Fatal(err)
	}
	fresh := strings.Replace(v10ConvergenceAssessment("0.8"), "# Novelty\n", "# CombinationNovelty\n", 1)
	freshID := insert(fresh)
	snapshot, err := h.dashboardSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Assessments) != 3 || len(snapshot.AssessmentHistory) != 6 {
		t.Fatalf("revision profiles were lost or mixed: %#v %#v", snapshot.Assessments, snapshot.AssessmentHistory)
	}
	for _, row := range snapshot.Assessments {
		if row["parse_error"] != nil || id(row["work_id"]) != freshID {
			t.Fatalf("current profile includes stale or falsely invalid evidence: %#v", row)
		}
		if row["criterion"] == "CombinationNovelty" {
			target, ok := row["target"].(*ScoreTarget)
			if !ok || target.Operator != ">" || target.Value != 0.75 || row["score"] != float64(0.8) {
				t.Fatalf("fresh criterion lost its own score/target: %#v", row)
			}
		}
	}
	for _, row := range snapshot.AssessmentHistory {
		if row["parse_error"] != nil {
			t.Fatalf("renamed current criteria falsely invalidated an original assessment: %#v", row)
		}
		if row["criterion"] != "Novelty" {
			continue
		}
		target, ok := row["target"].(*ScoreTarget)
		if !ok {
			t.Fatalf("original target absent: %#v", row)
		}
		switch id(row["work_id"]) {
		case originalID:
			if target.Operator != ">=" || target.Value != 1 || row["score"] != float64(0.2) || row["assessed_contract"] != v9Handoff {
				t.Fatalf("first original was reassigned a later target or meaning: %#v", row)
			}
		case middleID:
			if target.Operator != ">" || target.Value != 0.5 || row["score"] != float64(0.6) || row["assessed_contract"] != revised {
				t.Fatalf("middle original was reassigned a later target or meaning: %#v", row)
			}
		default:
			t.Fatalf("unexpected historical source: %#v", row)
		}
	}
	if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE id=? AND body=?", originalID, original) != 1 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE id=? AND body=?", middleID, middle) != 1 {
		t.Fatal("dashboard changed original assessment bodies")
	}
	if h.Text.Calls != 0 {
		t.Fatalf("dashboard/revision fixture made %d model calls", h.Text.Calls)
	}
	t.Log("two confirmed revisions retain original scores, targets, exact contracts and evidence as history; current cards use only the latest rubric's profile")
}
