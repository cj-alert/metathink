package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const v10RevisedContract = "queen_request:\r\nInvestigate a different useful approach.\r\nUsefulness > 0.5\r\nTestability >= 0.7\r\nKeep uncertainty explicit.  \r\n"
const v10RevisionFeedback = "Change direction—not the saved evidence.\r\n\tAssess usefulness and testability independently.  \n"

func v10RevisionRows(t *testing.T, h *Hive, table string) []map[string]any {
	t.Helper()
	rows, err := h.query(context.Background(), "revision_fixture_read", table, "SELECT * FROM "+table+" ORDER BY 1")
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestV010ContractRevisionPreservesHiveAndExactAuthority(t *testing.T) {
	h, config, dir := v10Fixture(t, 2, nil)
	v10ConvergenceTeam(t, h, 2)
	boundary := v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,2,'expand','CONTRIBUTE',?,0.8,0.7,?)", "ORIGINAL work remains unchanged under the old contract.\r\n", now())
	v10Exec(t, h, "UPDATE hive_requests SET status='paused',result=?,criteria_met=0,finish_reason='observation_paused' WHERE id=?", "Original previous decision", h.requestID)
	v10Exec(t, h, "UPDATE hive_worker_queen SET note=?,through_id=? WHERE request_id=?", "Original Queen note", boundary, h.requestID)
	v10Exec(t, h, "UPDATE hive_worker_queue SET phase='expand',asleep_work_id=?,asleep_guidance_id=0,peer_after=?,read_work_id=? WHERE drone_id=1", boundary, boundary, boundary)
	before := map[string]string{}
	for _, table := range []string{"hive_drones", "hive_inquiries", "hive_inquiry_members", "hive_inquiry_work", "hive_runs", "hive_worker_guidance"} {
		before[table] = jsonText(v10RevisionRows(t, h, table))
	}
	queue := v10RevisionRows(t, h, "hive_worker_queue")
	for _, row := range queue {
		row["asleep_work_id"], row["asleep_guidance_id"] = nil, nil
	}
	result, err := ReviseWorkerContract(v10Context(t), config, dir, v10RevisedContract, v10RevisionFeedback)
	if err != nil || result.Status != "paused" || result.RequestID != h.requestID || result.RevisionID < 1 || result.ThroughWorkID != boundary || result.Calls != 0 || len(result.OldCriteria) != 3 || len(result.NewCriteria) != 2 || result.NewCriteria[0].Name != "Usefulness" {
		t.Fatalf("revision=%+v error=%v", result, err)
	}
	for table, original := range before {
		if actual := jsonText(v10RevisionRows(t, h, table)); actual != original {
			t.Fatalf("revision changed %s\nbefore=%s\nafter=%s", table, original, actual)
		}
	}
	if actual := jsonText(v10RevisionRows(t, h, "hive_worker_queue")); actual != jsonText(queue) {
		t.Fatalf("queue identities/phase/cursors changed beyond waking\nexpected=%s\nactual=%s", jsonText(queue), actual)
	}
	request := v10RevisionRows(t, h, "hive_requests")[0]
	if str(request["contract"]) != v10RevisedContract || str(request["status"]) != "paused" || request["criteria_met"] != nil || request["result"] != nil {
		t.Fatalf("replacement authority/status=%v", request)
	}
	guidance := v10RevisionRows(t, h, "hive_guidance")
	if len(guidance) != 1 {
		t.Fatalf("want exactly one original revision record, got %v", guidance)
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(str(guidance[0]["body"])), &record); err != nil {
		t.Fatal(err)
	}
	if record["kind"] != "contract_revision" || record["old_contract"] != v9Handoff || record["new_contract"] != v10RevisedContract || record["user_feedback"] != v10RevisionFeedback || record["previous_queen_note"] != "Original Queen note" || record["old_result"] != "Original previous decision" || record["old_criteria_met"] != float64(0) || id(record["through_work_id"]) != boundary {
		t.Fatalf("original revision evidence was changed or omitted: %v", record)
	}
	queen := v10RevisionRows(t, h, "hive_worker_queen")[0]
	if str(queen["note"]) != "User-confirmed contract revision. User feedback (verbatim):\n"+v10RevisionFeedback || id(queen["through_id"]) != boundary {
		t.Fatalf("Queen revision guidance or review cursor=%v", queen)
	}
	if v9Count(t, h.Logger.db, "SELECT COUNT(*) FROM hive_log WHERE event='llm.start'") != 0 {
		t.Fatal("administrative contract revision made a model call")
	}
	t.Logf("request=%d revision=%d through_work=%d; exact contracts/feedback retained; same drones, teams, phase and work; no provider calls", result.RequestID, result.RevisionID, result.ThroughWorkID)
}

func TestV010ContractRevisionRejectsActiveOrReturnedRequest(t *testing.T) {
	for _, scenario := range []string{"working", "converging", "returned", "drone claim", "Queen claim"} {
		t.Run(scenario, func(t *testing.T) {
			h, _, _ := v10Fixture(t, 2, nil)
			v10Exec(t, h, "UPDATE hive_requests SET status='paused' WHERE id=?", h.requestID)
			switch scenario {
			case "drone claim":
				v10Exec(t, h, "UPDATE hive_worker_queue SET owner='in-flight' WHERE drone_id=1")
			case "Queen claim":
				v10Exec(t, h, "UPDATE hive_worker_queen SET owner='in-flight' WHERE request_id=?", h.requestID)
			default:
				v10Exec(t, h, "UPDATE hive_requests SET status=? WHERE id=?", scenario, h.requestID)
			}
			before := map[string]string{}
			for _, table := range []string{"hive_requests", "hive_guidance", "hive_worker_queen", "hive_worker_queue"} {
				before[table] = jsonText(v10RevisionRows(t, h, table))
			}
			if _, err := h.reviseWorkerContract(v10Context(t), v10RevisedContract, v10RevisionFeedback); err == nil || !strings.Contains(err.Error(), "claim released") {
				t.Fatalf("unsafe revision accepted: %v", err)
			}
			for table, original := range before {
				if actual := jsonText(v10RevisionRows(t, h, table)); actual != original {
					t.Fatalf("rejected revision changed %s: %s", table, actual)
				}
			}
		})
	}
}

func TestV010ContractRevisionValidationAndLateFailureAreAtomic(t *testing.T) {
	for _, scenario := range []string{"invalid criteria", "empty feedback", "wake write failure"} {
		t.Run(scenario, func(t *testing.T) {
			h, _, _ := v10Fixture(t, 2, nil)
			v10Exec(t, h, "UPDATE hive_requests SET status='blocked',result='Original blocked decision',criteria_met=0 WHERE id=?", h.requestID)
			v10Exec(t, h, "UPDATE hive_worker_queen SET note='Original Queen note' WHERE request_id=?", h.requestID)
			v10Exec(t, h, "UPDATE hive_worker_queue SET phase='expand',asleep_work_id=0,asleep_guidance_id=0")
			handoff, feedback := v10RevisedContract, v10RevisionFeedback
			if scenario == "invalid criteria" {
				handoff = "queen_request:\nUsefulness > 1.5\n"
			}
			if scenario == "empty feedback" {
				feedback = " \r\n"
			}
			if scenario == "wake write failure" {
				v10Exec(t, h, "CREATE TRIGGER reject_revision_wake BEFORE UPDATE OF asleep_work_id ON hive_worker_queue BEGIN SELECT RAISE(ABORT,'fixture revision wake failed'); END")
			}
			before := map[string]string{}
			for _, table := range []string{"hive_requests", "hive_guidance", "hive_worker_queen", "hive_worker_queue"} {
				before[table] = jsonText(v10RevisionRows(t, h, table))
			}
			if _, err := h.reviseWorkerContract(v10Context(t), handoff, feedback); err == nil {
				t.Fatal("invalid or failed revision accepted")
			}
			for table, original := range before {
				if actual := jsonText(v10RevisionRows(t, h, table)); actual != original {
					t.Fatalf("failed revision partially changed %s: %s", table, actual)
				}
			}
		})
	}
}

func TestV010ContractRevisionCLIRequiresConfirmationBeforeOpeningRun(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent-run")
	err := execute([]string{"worker-revise", "-dir", dir, "-handoff", "revised.md", "-feedback", "feedback.txt"})
	if err == nil || !strings.Contains(err.Error(), "-confirm") {
		t.Fatalf("revision CLI did not reject missing confirmation: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("unconfirmed revision touched run folder: %v", err)
	}
}
