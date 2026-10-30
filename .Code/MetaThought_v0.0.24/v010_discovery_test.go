package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func v10DiscoveryInput(t *testing.T, h *Hive, claim WorkerClaim) (map[string]any, string) {
	t.Helper()
	raw, err := h.WorkerInput(v10Context(t), claim)
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	if err = json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	return input, raw
}

func v10DiscoveryOffer(t *testing.T, input map[string]any, want int64) map[string]any {
	t.Helper()
	if want == 0 {
		if input["directory_offer"] != nil {
			t.Fatalf("unexpected directory offer: %#v", input["directory_offer"])
		}
		return nil
	}
	offer, ok := input["directory_offer"].(map[string]any)
	if !ok || id(offer["id"]) != want {
		t.Fatalf("directory offer=%#v, want inquiry %d", input["directory_offer"], want)
	}
	return offer
}

func TestV010AutomaticDiscoverySeesNewInquiryAndReadKeepsOriginal(t *testing.T) {
	h, _, _ := v10Fixture(t, 2, nil)
	watcher := v10Claim(t, h, "watcher")
	input, _ := v10DiscoveryInput(t, h, watcher)
	v10DiscoveryOffer(t, input, 0) // The bootstrap is not a team directory offer.

	author := v10Claim(t, h, "author")
	question := "A newly proposed inquiry\nPRIVATE_FULL_QUESTION_AVAILABLE_BY_READ\n"
	if err := h.AdvanceWorker(v10Context(t), author, v10Action("PROPOSE", 0, question)); err != nil {
		t.Fatal(err)
	}
	author = v10Claim(t, h, "author")
	if author.FocusID == watcher.FocusID {
		t.Fatal("PROPOSE did not create and focus a distinct inquiry")
	}

	// The existing watcher claim sees the new inquiry without a DISCOVER action.
	input, raw := v10DiscoveryInput(t, h, watcher)
	offer := v10DiscoveryOffer(t, input, author.FocusID)
	if offer["question_first_line"] != "A newly proposed inquiry" || id(offer["committed_member_count"]) != 1 || id(offer["proposed_by"]) != author.DroneID {
		t.Fatalf("directory lost its original first-line or membership facts: %#v", offer)
	}
	if _, exists := offer["question"]; exists || strings.Contains(raw, "PRIVATE_FULL_QUESTION_AVAILABLE_BY_READ") {
		t.Fatal("automatic discovery exposed the full unrelated question/history")
	}
	workID := id(offer["latest_work_id"])
	if workID == 0 {
		t.Fatal("directory offer omitted the original record's READ address")
	}
	if err := h.AdvanceWorker(v10Context(t), watcher, v10Action("READ", workID, "Inspect the original before deciding whether to join.")); err != nil {
		t.Fatal(err)
	}
	watcher = v10Claim(t, h, "watcher")
	input, _ = v10DiscoveryInput(t, h, watcher)
	read, ok := input["read_work"].(map[string]any)
	if !ok || id(read["id"]) != workID || read["body"] != question || id(read["drone_id"]) != author.DroneID {
		t.Fatalf("READ did not preserve exact original and author: %#v", input["read_work"])
	}
	if watcher.FocusID == author.FocusID || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE inquiry_id=? AND drone_id=?", author.FocusID, watcher.DroneID) != 0 {
		t.Fatal("READ silently changed focus or committed the reader")
	}
	if err := h.AdvanceWorker(v10Context(t), watcher, v10Action("JOIN", author.FocusID, "The original inquiry fits my contribution.")); err != nil {
		t.Fatal(err)
	}
	watcher = v10Claim(t, h, "watcher") // The author still holds its own Expand claim.
	input, _ = v10DiscoveryInput(t, h, watcher)
	v10DiscoveryOffer(t, input, 0) // Neither current focus nor bootstrap is offered.
	if watcher.FocusID != author.FocusID || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE inquiry_id=? AND drone_id=?", author.FocusID, watcher.DroneID) != 1 {
		t.Fatal("explicit JOIN did not preserve the chosen inquiry membership")
	}
	t.Logf("new inquiry %d automatically offered with original work %d; READ preserved source before explicit JOIN", author.FocusID, workID)
}

func TestV010DiscoveryCursorCommitsWithActionAndWrapsWithoutJoining(t *testing.T) {
	h, _, _ := v10Fixture(t, 1, nil)
	first := v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", h.requestID, "First other inquiry", 1, now())
	second := v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", h.requestID, "Second other inquiry", 1, now())
	claim := v10Claim(t, h, "browser")
	originalFocus := claim.FocusID
	input, _ := v10DiscoveryInput(t, h, claim)
	v10DiscoveryOffer(t, input, first)
	input, _ = v10DiscoveryInput(t, h, claim)
	v10DiscoveryOffer(t, input, first) // Preparing or failing a call cannot consume it.
	if err := h.AdvanceWorker(v10Context(t), claim, v10Action("PASS", 0, "No contribution to the current inquiry.")); err != nil {
		t.Fatal(err)
	}
	claim = v10Claim(t, h, "browser")
	if claim.DiscoveryID != first {
		t.Fatalf("accepted phase did not save the offered cursor: %+v", claim)
	}
	claim.DiscoveryID = 0 // An old caller copy must not rewind the stored cursor.
	input, _ = v10DiscoveryInput(t, h, claim)
	v10DiscoveryOffer(t, input, second)

	workBefore := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work")
	v10Exec(t, h, `CREATE TRIGGER reject_discovery_advance BEFORE UPDATE ON hive_worker_queue
 WHEN OLD.drone_id=1 AND NEW.phase<>OLD.phase BEGIN SELECT RAISE(ABORT,'reject discovery advance'); END`)
	err := h.AdvanceWorker(v10Context(t), claim, v10Action("DISCOVER", 0, "Look at the next inquiry without joining."))
	if err == nil || !strings.Contains(err.Error(), "reject discovery advance") {
		t.Fatalf("expected injected atomic-action failure, got %v", err)
	}
	failed := v10Claim(t, h, "browser")
	if failed.DiscoveryID != first || failed.FocusID != originalFocus || failed.Phase != "expand" {
		t.Fatalf("failed action changed the saved turn: %+v", failed)
	}
	if got := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work"); got != workBefore {
		t.Fatalf("failed action leaked a work record: before=%d after=%d", workBefore, got)
	}
	input, _ = v10DiscoveryInput(t, h, failed)
	v10DiscoveryOffer(t, input, second)
	v10Exec(t, h, "DROP TRIGGER reject_discovery_advance")
	if err = h.AdvanceWorker(v10Context(t), claim, v10Action("DISCOVER", 0, "Browse another inquiry without assuming membership.")); err != nil {
		t.Fatal(err)
	}
	claim = v10Claim(t, h, "browser")
	if claim.DiscoveryID != second || claim.FocusID != originalFocus {
		t.Fatalf("DISCOVER did not advance only the directory cursor: %+v", claim)
	}
	if got := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=?", claim.DroneID); got != 0 {
		t.Fatalf("DISCOVER invented %d memberships", got)
	}
	input, _ = v10DiscoveryInput(t, h, claim)
	v10DiscoveryOffer(t, input, first) // Complete cyclic directory traversal.
	t.Logf("directory %d -> %d -> %d; injected failure retained Expand/focus/membership and rolled back work", first, second, first)
}
