package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type v17VisibilityFixture struct {
	h                 *Hive
	dir               string
	child             WorkerClaim
	invited, other    int64
	third, foreign    int64
	foreignRequest    int64
	invitedWork       int64
	otherWork         int64
}

// All setup uses local SQLite. SPAWN and subsequent decisions use the real
// worker transaction; the fixture's TextClient rejects any model call.
func v17Visibility(t *testing.T, probability float64) v17VisibilityFixture {
	t.Helper()
	h, _, dir := v10Fixture(t, 3, nil)
	h.Config.Workers.NewbornOmitProbability = probability
	parent := v10Claim(t, h, "v17-parent")
	if err := h.AdvanceWorker(v10Context(t), parent, v10Action("PROPOSE", 0, "Invited inquiry\nPRIVATE_INVITER_QUESTION")); err != nil {
		t.Fatal(err)
	}
	parent = v10Claim(t, h, parent.Owner)
	f := v17VisibilityFixture{h: h, dir: dir, invited: parent.FocusID}
	f.other = v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", h.requestID, "Other inquiry\nPRIVATE_OTHER_QUESTION", 2, now())
	// A non-root inquiry is eligible even when proposed_by is zero.
	f.third = v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", h.requestID, "Third inquiry\nPRIVATE_THIRD_QUESTION", 0, now())
	v10Exec(t, h, "INSERT INTO hive_inquiry_members(inquiry_id,drone_id,commitment,created) VALUES(?,?,?,?)", f.other, 2, "Existing commitment", now())
	f.otherWork = v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,?,?,?,?,?,?)", f.other, 2, "contribute", "CONTRIBUTE", "EXACT_OTHER_ORIGINAL\nA second line.", .8, .7, now())
	v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,target_inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,?,?,?,?,?,?,?)", f.other, f.invited, 2, "expand", "CONNECT", "PRIVATE_CONNECTION", .8, .7, now())
	v10Exec(t, h, "INSERT INTO hive_worker_guidance(request_id,inquiry_id,body,relevance,inquiry_value,created) VALUES(?,?,?,?,?,?)", h.requestID, f.invited, "PRIVATE_QUEEN_GUIDANCE", .8, .7, now())
	f.foreignRequest = v10Exec(t, h, "INSERT INTO hive_requests(run_id,contract,status,created) VALUES(?,?,?,?)", h.runID, "FOREIGN_HANDOFF", "paused", now())
	v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", f.foreignRequest, "FOREIGN_ROOT", 0, now())
	f.foreign = v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", f.foreignRequest, "FOREIGN_INQUIRY", 2, now())
	if err := h.Store.db.QueryRow("SELECT id FROM hive_inquiry_work WHERE inquiry_id=? AND kind='PROPOSE'", f.invited).Scan(&f.invitedWork); err != nil {
		t.Fatal(err)
	}
	if err := h.AdvanceWorker(v10Context(t), parent, v10Action("SPAWN", f.invited, "PRIVATE_INVITATION_SIGNAL")); err != nil {
		t.Fatal(err)
	}
	// Park the two original root actors. The next fair-queue actor is the child.
	for _, owner := range []string{"v17-seed-2", "v17-seed-3"} {
		seed := v10Claim(t, h, owner)
		input, err := h.WorkerInput(v10Context(t), seed)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(input, "needs_fit_choice") || strings.Contains(input, "inquiry_signals") {
			t.Fatalf("root seed was routed through the newborn menu: %s", input)
		}
	}
	f.child = v10Claim(t, h, "v17-child")
	if f.child.DroneID != 4 || f.child.FocusID != f.invited {
		t.Fatalf("spawn queue: %+v", f.child)
	}
	return f
}

func v17Select(t *testing.T, h *Hive, request int64, selected []int64) {
	t.Helper()
	v10Exec(t, h, "INSERT INTO hive_worker_resources(request_id,selected_ids) VALUES(?,?) ON CONFLICT(request_id) DO UPDATE SET selected_ids=excluded.selected_ids", request, jsonText(selected))
}

func v17Menu(t *testing.T, h *Hive, claim WorkerClaim) (map[string]any, []int64) {
	t.Helper()
	raw, err := h.WorkerInput(v10Context(t), claim)
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	if input["needs_fit_choice"] != true || input["handoff"] != v9Handoff {
		t.Fatalf("missing newborn choice or changed handoff: %s", raw)
	}
	for _, key := range []string{"focus", "peer_work", "connection_offer", "queen_guidance", "team_report", "team_assessment", "research_work", "directory_offer", "newborn_mode", "omit_oversized", "selected_ids", "shown_inquiry_ids", "omitted_inquiry_ids"} {
		if _, exists := input[key]; exists {
			t.Fatalf("newborn input leaked %s", key)
		}
	}
	actor := input["drone"].(map[string]any)
	if len(actor) != 4 || actor["signal"] != nil {
		t.Fatalf("invitation leaked through actor: %v", actor)
	}
	ids := []int64{}
	for _, item := range input["inquiry_signals"].([]any) {
		card := item.(map[string]any)
		if len(card) != 3 || card["question_first_line"] == nil {
			t.Fatalf("unequal or rich card: %v", card)
		}
		if strings.Contains(fmt.Sprint(card), "PRIVATE_") || strings.Contains(fmt.Sprint(card), "FOREIGN_") {
			t.Fatalf("full question or different request in menu: %v", card)
		}
		ids = append(ids, int64(card["id"].(float64)))
	}
	return input, ids
}

func v17WantMenu(t *testing.T, got, want []int64) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("menu %v, want %v in inquiry-ID order", got, want)
	}
}

func TestV017NewbornSavedCohortAndCurrentSelection(t *testing.T) {
	for _, probability := range []float64{0, 1} {
		t.Run(fmt.Sprint(probability), func(t *testing.T) {
			f := v17Visibility(t, probability)
			h := f.h
			if got := v9Count(t, h.Store.db, "SELECT omit_oversized FROM hive_worker_queue WHERE drone_id=?", f.child.DroneID); got != int(probability) {
				t.Fatalf("birth cohort=%d, probability=%v", got, probability)
			}
			if n := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_queue WHERE drone_id<=3 AND omit_oversized IS NOT NULL"); n != 0 {
				t.Fatal("seed cohort changed")
			}
			if n := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=?", f.child.DroneID); n != 0 {
				t.Fatal("SPAWN fabricated membership")
			}
			if n := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE drone_id=?", f.child.DroneID); n != 0 {
				t.Fatal("SPAWN fabricated child work")
			}
			// No resource row means no omission. A different request's row cannot apply.
			v17Select(t, h, f.foreignRequest, []int64{f.invited, f.other})
			_, got := v17Menu(t, h, f.child)
			v17WantMenu(t, got, []int64{f.invited, f.other, f.third})
			v17Select(t, h, h.requestID, []int64{f.invited})
			want := []int64{f.invited, f.other, f.third}
			if probability == 1 {
				want = []int64{f.other, f.third}
			}
			_, got = v17Menu(t, h, f.child)
			v17WantMenu(t, got, want)
			// Change the configured probability and reopen through an independent
			// SQLite connection: the existing actor's cohort must not be redrawn.
			h.Config.Workers.NewbornOmitProbability = 1 - probability
			peer := v10Peer(t, h, h.Config, f.dir)
			_, got = v17Menu(t, peer, f.child)
			v17WantMenu(t, got, want)
			v17Select(t, h, h.requestID, []int64{f.invited, f.third})
			_, got = v17Menu(t, peer, f.child)
			if probability == 1 {
				want = []int64{f.other}
			}
			v17WantMenu(t, got, want)
		})
	}
}

func TestV017NewbornReadAndVoluntaryJoinRemainAvailable(t *testing.T) {
	f := v17Visibility(t, 1)
	h := f.h
	v17Select(t, h, h.requestID, []int64{f.invited, f.other})
	_, menu := v17Menu(t, h, f.child)
	v17WantMenu(t, menu, []int64{f.third})
	if err := h.AdvanceWorker(v10Context(t), f.child, v10Action("READ", f.invitedWork, "Read an original from outside this menu.")); err != nil {
		t.Fatal(err)
	}
	child := v10Claim(t, h, f.child.Owner)
	input, menu := v17Menu(t, h, child)
	v17WantMenu(t, menu, []int64{f.third})
	read := input["read_work"].(map[string]any)
	if int64(read["id"].(float64)) != f.invitedWork || read["body"] != "Invited inquiry\nPRIVATE_INVITER_QUESTION" {
		t.Fatalf("READ did not retain exact omitted original: %v", read)
	}
	if n := v9Count(t, h.Store.db, "SELECT peer_after+connection_after+offered_peer_id+offered_connection_id FROM hive_worker_queue WHERE drone_id=?", child.DroneID); n != 0 {
		t.Fatal("hidden peer/connection was acknowledged")
	}
	if err := h.AdvanceWorker(v10Context(t), child, v10Action("JOIN", f.other, "A voluntary contribution to an omitted inquiry.")); err != nil {
		t.Fatal(err)
	}
	// The parent was already waiting at the queue tail; park it, then reclaim child.
	_ = v10Claim(t, h, "v17-parent-parked")
	child = v10Claim(t, h, "v17-child-after-join")
	if child.DroneID != f.child.DroneID || child.FocusID != f.other {
		t.Fatalf("voluntary JOIN focus was replaced: %+v", child)
	}
	raw, err := h.WorkerInput(v10Context(t), child)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "needs_fit_choice") || !strings.Contains(raw, "PRIVATE_OTHER_QUESTION") {
		t.Fatalf("committed member did not receive normal context: %s", raw)
	}
	if n := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE inquiry_id=?", f.other); n != 2 {
		t.Fatalf("existing member or voluntary new member lost: %d", n)
	}
}

func TestV017NewbornEmptyMenuCanDiscoverOrPropose(t *testing.T) {
	for _, action := range []string{"DISCOVER", "PROPOSE"} {
		t.Run(action, func(t *testing.T) {
			f := v17Visibility(t, 1)
			h := f.h
			v17Select(t, h, h.requestID, []int64{f.invited, f.other, f.third})
			_, menu := v17Menu(t, h, f.child)
			v17WantMenu(t, menu, []int64{})
			if err := h.AdvanceWorker(v10Context(t), f.child, v10Action(action, 0, "An independent investigation or discovery.")); err != nil {
				t.Fatal(err)
			}
			child := v10Claim(t, h, f.child.Owner)
			if action == "DISCOVER" {
				_, menu = v17Menu(t, h, child)
				v17WantMenu(t, menu, []int64{f.invited, f.other, f.third})
				peer := v10Peer(t, h, h.Config, f.dir)
				_, menu = v17Menu(t, peer, child)
				v17WantMenu(t, menu, []int64{f.invited, f.other, f.third})
				if n := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=?", child.DroneID); n != 0 {
					t.Fatal("DISCOVER added membership")
				}
			} else {
				raw, err := h.WorkerInput(v10Context(t), child)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(raw, "needs_fit_choice") || child.FocusID == f.invited {
					t.Fatalf("PROPOSE did not restore normal context: %s", raw)
				}
			}
		})
	}
}

func TestV017NewbornMalformedSelectionFailsExplicitly(t *testing.T) {
	f := v17Visibility(t, 1)
	v10Exec(t, f.h, "INSERT INTO hive_worker_resources(request_id,selected_ids) VALUES(?,?)", f.h.requestID, "not-json")
	if _, err := f.h.WorkerInput(v10Context(t), f.child); err == nil || !strings.Contains(err.Error(), "saved resource inquiry selection") {
		t.Fatalf("bad saved selection silently ignored: %v", err)
	}
	if n := v9Count(t, f.h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=?", f.child.DroneID); n != 0 {
		t.Fatal("failed input changed membership")
	}
}

func TestV017RootTargetSpawnReceivesMenuAndSeedsRemainNormal(t *testing.T) {
	for _, probability := range []float64{0, 1} {
		t.Run(fmt.Sprint(probability), func(t *testing.T) {
			h, _, _ := v10Fixture(t, 2, nil)
			h.Config.Workers.NewbornOmitProbability = probability
			selected := v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", h.requestID, "Selected inquiry", 1, now())
			other := v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", h.requestID, "Other inquiry", 2, now())
			v17Select(t, h, h.requestID, []int64{selected})
			parent := v10Claim(t, h, "root-parent")
			if parent.FocusID != 1 {
				t.Fatalf("fixture parent focus=%d, want root", parent.FocusID)
			}
			if err := h.AdvanceWorker(v10Context(t), parent, v10Action("SPAWN", 1, "An invitation to think about the root request.")); err != nil {
				t.Fatal(err)
			}
			seed := v10Claim(t, h, "original-seed")
			for _, focus := range []int64{1, selected} {
				// Original actors stay on the ordinary path at either focus. Only
				// SPAWN writes a non-NULL cohort, not a later focus change.
				v10Exec(t, h, "UPDATE hive_worker_queue SET focus_id=? WHERE drone_id=?", focus, seed.DroneID)
				seed = v10Claim(t, h, seed.Owner)
				input, err := h.WorkerInput(v10Context(t), seed)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(input, "needs_fit_choice") || strings.Contains(input, "inquiry_signals") {
					t.Fatalf("original seed received newborn menu at focus %d: %s", focus, input)
				}
			}
			child := v10Claim(t, h, "root-child")
			if child.DroneID != 3 || child.FocusID != 1 {
				t.Fatalf("root spawn was relocated: %+v", child)
			}
			_, menu := v17Menu(t, h, child)
			want := []int64{selected, other}
			if probability == 1 {
				want = []int64{other}
			}
			v17WantMenu(t, menu, want)
			if got := v9Count(t, h.Store.db, "SELECT omit_oversized FROM hive_worker_queue WHERE drone_id=?", child.DroneID); got != int(probability) {
				t.Fatalf("root child's saved cohort=%d, want %v", got, probability)
			}
			if n := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_queue WHERE drone_id<=2 AND omit_oversized IS NOT NULL"); n != 0 {
				t.Fatal("original seeds acquired a newborn cohort")
			}
			if n := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=?", child.DroneID); n != 0 {
				t.Fatal("root spawn fabricated membership")
			}
		})
	}
}
