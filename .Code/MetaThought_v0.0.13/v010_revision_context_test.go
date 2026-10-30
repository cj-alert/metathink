package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestV010RevisionContextSeparatesOldGuidanceFromCurrentAuthority(t *testing.T) {
	h, _, _ := v10Fixture(t, 2, nil)
	v10ConvergenceTeam(t, h, 2)
	oldWork := "Original inquiry contribution cites the earlier novelty score of 0.7; retain this historical text."
	workID := v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,1,'contribute','CONTRIBUTE',?,0.8,0.7,?)", oldWork, now())
	research := `{"status":"completed","answer":"Original research remains inspectable.","search_calls":[{"type":"web_search_call","status":"completed","results":[{"url":"https://research.invalid/original"}]}]}`
	boundary := v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,2,'contribute','RESEARCH',?,0.8,0.7,?)", research, now())
	oldGuidance := "OLD_GUIDANCE_AUTHORITY: use the earlier 0.7 novelty score as the current gate."
	oldGuidanceID := v10Exec(t, h, "INSERT INTO hive_worker_guidance(request_id,inquiry_id,body,relevance,inquiry_value,created) VALUES(?,1,?,0.8,0.7,?)", h.requestID, oldGuidance, "2026-10-28T15:59:59.999999999Z")
	feedback := "Novelty concerns this EXACT formulation.\r\nA unique combination is 1.0. Preserve α and these spaces.  "
	handoff := strings.Replace(v9Handoff, "Novelty >= 1.0", "Novelty > 0.5", 1)
	revisionID := v10Exec(t, h, "INSERT INTO hive_guidance(request_id,tick,body,created) VALUES(?,0,?,?)", h.requestID, jsonText(map[string]any{
		"kind": "contract_revision", "through_work_id": boundary, "user_feedback": feedback,
		"old_contract": "OLD_CONTRACT_NOT_CURRENT_CONTEXT", "new_contract": handoff,
	}), "2026-10-28T16:00:00Z")
	// Ordinary later guidance must not hide the latest matching revision.
	v10Exec(t, h, "INSERT INTO hive_guidance(request_id,tick,body,created) VALUES(?,0,?,?)", h.requestID, "Ordinary administrative guidance.", "2026-10-28T16:00:00.000000001Z")
	v10Exec(t, h, "UPDATE hive_requests SET contract=?,status='working' WHERE id=?", handoff, h.requestID)
	claim := v10Claim(t, h, "revision-context-fixture")
	decode := func(raw string, err error) map[string]any {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		var input map[string]any
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			t.Fatal(err)
		}
		if str(input["handoff"]) != handoff || strings.Contains(raw, "OLD_CONTRACT_NOT_CURRENT_CONTEXT") {
			t.Fatal("current handoff was changed or the administrative old contract leaked into context")
		}
		revision, ok := input["contract_revision"].(map[string]any)
		if !ok || len(revision) != 3 || id(revision["revision_id"]) != revisionID || id(revision["through_work_id"]) != boundary || str(revision["user_feedback"]) != feedback {
			t.Fatalf("compact exact revision feedback/boundary missing: %+v", input["contract_revision"])
		}
		return input
	}
	drone := decode(h.WorkerInput(v10Context(t), claim))
	if drone["queen_guidance"] != nil || str(drone["own_latest_work"].(map[string]any)["body"]) != oldWork || str(drone["research_work"].(map[string]any)["body"]) != research {
		t.Fatal("old guidance remained authoritative or original work/research was discarded")
	}
	selected, err := h.query(v10Context(t), "fixture_original_work", "hive_inquiry_work", "SELECT * FROM hive_inquiry_work WHERE id=?", workID)
	if err != nil {
		t.Fatal(err)
	}
	queen := decode(h.workerQueenInput(v10Context(t), 1, selected, "User-confirmed contract revision is current."))
	if str(queen["work"].([]any)[0].(map[string]any)["body"]) != oldWork || str(queen["research_work"].(map[string]any)["body"]) != research {
		t.Fatal("Queen lost original operational evidence while receiving the revision boundary")
	}
	newGuidance := "CURRENT_GUIDANCE: use the exact current contract and preserve earlier work as history."
	newGuidanceID := v10Exec(t, h, "INSERT INTO hive_worker_guidance(request_id,inquiry_id,body,relevance,inquiry_value,created) VALUES(?,1,?,0.8,0.7,?)", h.requestID, newGuidance, "2026-10-28T16:00:00.000000001Z")
	drone = decode(h.WorkerInput(v10Context(t), claim))
	guidance, ok := drone["queen_guidance"].(map[string]any)
	if !ok || id(guidance["id"]) != newGuidanceID || str(guidance["body"]) != newGuidance {
		t.Fatal("valid post-revision guidance was hidden or rewritten")
	}
	if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_guidance WHERE id=? AND body=?", oldGuidanceID, oldGuidance) != 1 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE id=? AND body=?", workID, oldWork) != 1 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE id=? AND body=?", boundary, research) != 1 {
		t.Fatal("context filtering modified the original guidance, work, or research")
	}
}
