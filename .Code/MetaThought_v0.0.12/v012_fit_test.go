package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const v12InvitedQuestion = "Invited inquiry\nV012_INVITER_FULL_QUESTION"
const v12AlternativeQuestion = "Alternative inquiry\nV012_ALTERNATIVE_ORIGINAL α\r\nExact original spacing.  "

type v12FitFixture struct {
	h                            *Hive
	child, seed                  WorkerClaim
	invited, alternative, third  int64
	invitedWork, alternativeWork int64
	connection                   int64
}

func v12NewFitFixture(t *testing.T, client TextClient) v12FitFixture {
	t.Helper()
	h, _, _ := v10Fixture(t, 3, client)
	inviter := v10Claim(t, h, "v12-inviter")
	if err := h.AdvanceWorker(v10Context(t), inviter, v10Action("PROPOSE", 0, v12InvitedQuestion)); err != nil {
		t.Fatal(err)
	}
	inviter = v10Claim(t, h, "v12-inviter")
	invited := inviter.FocusID
	var invitedWork int64
	if err := h.Store.db.QueryRow("SELECT id FROM hive_inquiry_work WHERE inquiry_id=? AND kind='PROPOSE'", invited).Scan(&invitedWork); err != nil {
		t.Fatal(err)
	}
	if err := h.AdvanceWorker(v10Context(t), inviter, v10Action("SPAWN", invited, "V012_INVITATION_SIGNAL: investigate the inviter's preferred direction")); err != nil {
		t.Fatal(err)
	}
	author := v10Claim(t, h, "v12-alternative-author")
	if err := h.AdvanceWorker(v10Context(t), author, v10Action("PROPOSE", 0, v12AlternativeQuestion)); err != nil {
		t.Fatal(err)
	}
	author = v10Claim(t, h, "v12-alternative-author")
	alternative := author.FocusID
	var alternativeWork int64
	if err := h.Store.db.QueryRow("SELECT id FROM hive_inquiry_work WHERE inquiry_id=? AND kind='PROPOSE'", alternative).Scan(&alternativeWork); err != nil {
		t.Fatal(err)
	}
	seed := v10Claim(t, h, "v12-bootstrap-seed")
	third := v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", h.requestID, "Third inquiry\nV012_THIRD_DETAIL", author.DroneID, now())
	connection := v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,target_inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,?,'expand','CONNECT',?,0.8,0.7,?)", alternative, invited, author.DroneID, "V012_INVITER_CONNECTION", now())
	for _, item := range []struct{ kind, body string }{
		{"TEAM_REPORT", "V012_INVITER_REPORT"},
		{"TEAM_ASSESSMENT", "V012_INVITER_ASSESSMENT"},
		{"RESEARCH", "V012_INVITER_RESEARCH"},
	} {
		v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,'convergence',?,?,0.8,0.7,?)", invited, inviter.DroneID, item.kind, item.body, now())
	}
	v10Exec(t, h, "INSERT INTO hive_worker_guidance(request_id,inquiry_id,body,relevance,inquiry_value,created) VALUES(?,?,?,0.8,0.7,?)", h.requestID, invited, "V012_INVITER_QUEEN_GUIDANCE", now())
	v10Exec(t, h, "INSERT INTO hive_worker_guidance(request_id,inquiry_id,body,relevance,inquiry_value,created) VALUES(?,?,?,0.8,0.7,?)", h.requestID, alternative, "V012_ALTERNATIVE_QUEEN_GUIDANCE", now())
	child := v10Claim(t, h, "v12-child")
	if child.DroneID <= 3 || child.FocusID != invited {
		t.Fatalf("SPAWN fixture did not create an uncommitted child at invitation: %+v", child)
	}
	// Hold the parent so the child's next ordinary turn is deterministic; all
	// claims still use the production queue and remain within its ten slots.
	parked := v10Claim(t, h, "v12-park-parent")
	if parked.DroneID != inviter.DroneID {
		t.Fatalf("expected released inviter, got %+v", parked)
	}
	return v12FitFixture{h: h, child: child, seed: seed, invited: invited, alternative: alternative, third: third,
		invitedWork: invitedWork, alternativeWork: alternativeWork, connection: connection}
}

func v12Input(t *testing.T, h *Hive, claim WorkerClaim) (map[string]any, string) {
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

func v12AssertSignals(t *testing.T, input map[string]any, want map[int64]string) {
	t.Helper()
	if input["needs_fit_choice"] != true {
		t.Fatalf("uncommitted child did not receive fit choice: %#v", input)
	}
	signals, ok := input["inquiry_signals"].([]any)
	if !ok || len(signals) != len(want) {
		t.Fatalf("signals=%#v, want %d balanced cards", input["inquiry_signals"], len(want))
	}
	seen := map[int64]bool{}
	for _, value := range signals {
		card, ok := value.(map[string]any)
		if !ok || len(card) != 3 {
			t.Fatalf("card has unequal or preference-bearing fields: %#v", value)
		}
		for _, key := range []string{"id", "question_first_line", "latest_work_id"} {
			if _, exists := card[key]; !exists {
				t.Fatalf("card lacks %s: %#v", key, card)
			}
		}
		inquiry := id(card["id"])
		question, exists := want[inquiry]
		if !exists || seen[inquiry] || card["question_first_line"] != question {
			t.Fatalf("card changed original signal or repeated an inquiry: %#v", card)
		}
		seen[inquiry] = true
	}
}

func v12AssertNoRichContext(t *testing.T, input map[string]any, raw string) {
	t.Helper()
	for _, key := range []string{"focus", "own_latest_work", "peer_work", "connection_offer", "directory_offer", "queen_guidance", "team_report", "team_assessment", "research_work"} {
		if _, exists := input[key]; exists {
			t.Fatalf("fit choice exposes rich/default routing field %q", key)
		}
	}
	drone, ok := input["drone"].(map[string]any)
	if !ok || len(drone) != 4 || drone["id"] == nil || drone["name"] == nil || drone["contribute"] == nil || drone["expand"] == nil {
		t.Fatalf("fit choice did not preserve only identity and chromosomes: %#v", input["drone"])
	}
	members, ok := input["memberships"].([]any)
	if !ok || len(members) != 0 || input["handoff"] != v9Handoff {
		t.Fatal("fit choice invented membership or changed governing handoff")
	}
	for _, canary := range []string{"V012_INVITER_FULL_QUESTION", "V012_INVITATION_SIGNAL", "V012_INVITER_CONNECTION", "V012_INVITER_REPORT", "V012_INVITER_ASSESSMENT", "V012_INVITER_RESEARCH", "V012_INVITER_QUEEN_GUIDANCE", v9DiagnosticCanary, v9OldSeedCanary} {
		if strings.Contains(raw, canary) {
			t.Fatalf("fit choice leaked suppressed context %q", canary)
		}
	}
}

func TestV012SpawnFitUsesEqualSignalsWithoutConsumingSuppressedEvidence(t *testing.T) {
	f := v12NewFitFixture(t, nil)
	// Simulate an old prepared context: the new fit input must clear these
	// offers so accepting PASS cannot acknowledge evidence it never displayed.
	v10Exec(t, f.h, "UPDATE hive_worker_queue SET offered_peer_id=?,offered_connection_id=? WHERE drone_id=?", f.invitedWork, f.connection, f.child.DroneID)
	input, raw := v12Input(t, f.h, f.child)
	v12AssertSignals(t, input, map[int64]string{f.invited: "Invited inquiry", f.alternative: "Alternative inquiry"})
	v12AssertNoRichContext(t, input, raw)
	for _, value := range input["inquiry_signals"].([]any) {
		card := value.(map[string]any)
		want := f.invitedWork
		if id(card["id"]) == f.alternative {
			want = f.alternativeWork
		}
		if id(card["latest_work_id"]) != want {
			t.Fatalf("compact card lost original READ address: %#v", card)
		}
	}
	var offeredPeer, offeredConnection int64
	if err := f.h.Store.db.QueryRow("SELECT offered_peer_id,offered_connection_id FROM hive_worker_queue WHERE drone_id=?", f.child.DroneID).Scan(&offeredPeer, &offeredConnection); err != nil {
		t.Fatal(err)
	}
	if offeredPeer != 0 || offeredConnection != 0 {
		t.Fatal("suppressed artifacts remained offered")
	}
	if err := f.h.AdvanceWorker(v10Context(t), f.child, v10Action("PASS", 0, "Neither signal yet supports a useful contribution.")); err != nil {
		t.Fatal(err)
	}
	var peerAfter, connectionAfter int64
	if err := f.h.Store.db.QueryRow("SELECT peer_after,connection_after FROM hive_worker_queue WHERE drone_id=?", f.child.DroneID).Scan(&peerAfter, &connectionAfter); err != nil {
		t.Fatal(err)
	}
	if peerAfter != 0 || connectionAfter != 0 || v9Count(t, f.h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=?", f.child.DroneID) != 0 {
		t.Fatal("fit PASS consumed hidden evidence or committed the child")
	}
}

func TestV012ReadThenJoinAlternativeUsesTwoOrdinaryCallsAndRestoresContext(t *testing.T) {
	var f v12FitFixture
	calls := 0
	client := v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
		calls++
		if request.Scope != "drone" || request.Model != f.h.Config.Models.Drone || len(request.Messages) != 2 {
			t.Fatalf("unexpected routing/model call: %+v", request)
		}
		raw := str(request.Messages[1]["content"])
		var input map[string]any
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			t.Fatal(err)
		}
		v12AssertNoRichContext(t, input, raw)
		if calls == 1 {
			v12AssertSignals(t, input, map[int64]string{f.invited: "Invited inquiry", f.alternative: "Alternative inquiry"})
			if input["read_work"] != nil {
				t.Fatal("first choice inherited an explicit READ")
			}
			return TextResponse{Text: v9Reply(fmt.Sprintf("READ %d", f.alternativeWork), "Read the alternative's original before choosing fit.", 0.8, 0.7)}, nil
		}
		if calls != 2 {
			t.Fatal("fit introduced an extra model call")
		}
		read, ok := input["read_work"].(map[string]any)
		if !ok || id(read["id"]) != f.alternativeWork || read["body"] != v12AlternativeQuestion || id(read["inquiry_id"]) != f.alternative {
			t.Fatalf("READ lost the exact chosen original: %#v", input["read_work"])
		}
		if v9Count(t, f.h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=?", f.child.DroneID) != 0 {
			t.Fatal("READ silently committed the child")
		}
		return TextResponse{Text: v9Reply(fmt.Sprintf("JOIN %d", f.alternative), "The alternative's original reveals the specific question I can test.", 0.8, 0.7)}, nil
	})
	f = v12NewFitFixture(t, client)
	claim := f.child
	for phase := 0; phase < 2; phase++ {
		_, input := v12Input(t, f.h, claim)
		drone, err := f.h.drone(v10Context(t), claim.DroneID)
		if err != nil {
			t.Fatal(err)
		}
		prompt := f.h.Config.Prompts["worker_contract_authority"] + "\n\n" + f.h.Config.Prompts["inquiry_"+claim.Phase] + "\n\n" + f.h.Config.Prompts["inquiry_actions"] + "\n\n" + f.h.Config.Prompts["inquiry_scores"] + "\n\n" + f.h.Config.Prompts["inquiry_format"]
		reply, err := f.h.generatePrompt(v10Context(t), "drone", f.h.Config.Models.Drone, "inquiry_"+claim.Phase, prompt, input, str(drone[claim.Phase]))
		if err != nil {
			t.Fatal(err)
		}
		action, err := ParseInquiryAction(reply)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.h.AdvanceWorker(v10Context(t), claim, action); err != nil {
			t.Fatal(err)
		}
		claim = v10Claim(t, f.h, "v12-child")
	}
	input, raw := v12Input(t, f.h, claim)
	if calls != 2 || claim.FocusID != f.alternative || input["needs_fit_choice"] == true {
		t.Fatalf("explicit alternative JOIN did not end fit state: calls=%d claim=%+v", calls, claim)
	}
	focus, ok := input["focus"].(map[string]any)
	if !ok || focus["question"] != v12AlternativeQuestion || !strings.Contains(raw, "V012_ALTERNATIVE_QUEEN_GUIDANCE") {
		t.Fatal("ordinary full chosen-team context was not restored")
	}
	if v9Count(t, f.h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=? AND inquiry_id=?", claim.DroneID, f.alternative) != 1 ||
		v9Count(t, f.h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=? AND inquiry_id=?", claim.DroneID, f.invited) != 0 {
		t.Fatal("alternative choice added unwanted inviter membership")
	}
}

func TestV012BootstrapUnchangedAndFitAlternativesRotateWithoutCommitment(t *testing.T) {
	f := v12NewFitFixture(t, nil)
	seedInput, _ := v12Input(t, f.h, f.seed)
	focus, ok := seedInput["focus"].(map[string]any)
	if !ok || id(focus["proposed_by"]) != 0 || seedInput["needs_fit_choice"] == true {
		t.Fatal("bootstrap seed was incorrectly converted to an invitation fit choice")
	}
	input, _ := v12Input(t, f.h, f.child)
	v12AssertSignals(t, input, map[int64]string{f.invited: "Invited inquiry", f.alternative: "Alternative inquiry"})
	if err := f.h.AdvanceWorker(v10Context(t), f.child, v10Action("DISCOVER", 0, "Consider another inquiry without committing.")); err != nil {
		t.Fatal(err)
	}
	claim := v10Claim(t, f.h, "v12-child")
	input, raw := v12Input(t, f.h, claim)
	v12AssertSignals(t, input, map[int64]string{f.invited: "Invited inquiry", f.third: "Third inquiry"})
	v12AssertNoRichContext(t, input, raw)
	if claim.FocusID != f.invited || strings.Contains(raw, "V012_THIRD_DETAIL") || v9Count(t, f.h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=?", claim.DroneID) != 0 {
		t.Fatal("browsing changed focus/membership or exposed a full alternative")
	}
}
