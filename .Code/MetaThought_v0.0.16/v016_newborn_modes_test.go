package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const v16Alternative = "Alternative inquiry\nV016_ALTERNATIVE_ORIGINAL α\nExact original spacing.  "

type v16ModeFixture struct {
	h                                                                      *Hive
	child                                                                  WorkerClaim
	invited, alternative, singleton, alternativeWork, peerWork, connection int64
}

func v16NewModeFixture(t *testing.T, mode string) v16ModeFixture {
	t.Helper()
	h, _, _ := v10Fixture(t, 8, nil)
	h.Config.Workers.NewbornMode = mode
	invited := v14Inquiry(t, h, h.requestID, "Invited inquiry\nV016_INVITER_FULL_QUESTION")
	alternative := v14Inquiry(t, h, h.requestID, v16Alternative)
	// A non-root inquiry stays eligible even if it has no proposer or members.
	singleton := v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,0,?)", h.requestID, "Unstaffed inquiry\nV016_UNSTAFFED_DETAIL", now())
	for drone := 1; drone <= 8; drone++ {
		v10Exec(t, h, "INSERT INTO hive_inquiry_members(inquiry_id,drone_id,commitment,created) VALUES(?,?,'existing commitment',?)", invited, drone, now())
	}
	work := func(inquiry, drone int64, kind, body string) int64 {
		return v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,'contribute',?,?,0.8,0.7,?)", inquiry, drone, kind, body, now())
	}
	peerWork := work(invited, 2, "CONTRIBUTE", "V016_RICH_PEER")
	alternativeWork := work(alternative, 3, "PROPOSE", v16Alternative)
	connection := v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,target_inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,3,'expand','CONNECT','V016_RICH_CONNECTION',0.8,0.7,?)", alternative, invited, now())
	for _, kind := range []string{"TEAM_REPORT", "TEAM_ASSESSMENT", "RESEARCH"} {
		work(invited, 2, kind, "V016_RICH_"+kind)
	}
	v10Exec(t, h, "INSERT INTO hive_worker_guidance(request_id,inquiry_id,body,relevance,inquiry_value,created) VALUES(?,?,'V016_RICH_QUEEN',0.8,0.7,?)", h.requestID, invited, now())
	foreignRequest := v10Exec(t, h, "INSERT INTO hive_requests(run_id,contract,status,created) VALUES(?,?,?,?)", h.runID, "Foreign obligation", "working", now())
	v14Inquiry(t, h, foreignRequest, "V016_FOREIGN_ROOT")
	v14Inquiry(t, h, foreignRequest, "V016_FOREIGN_NONROOT")
	childID := v14Spawn(t, h, 1, invited)
	return v16ModeFixture{h: h, child: v14ClaimActor(t, h, childID), invited: invited, alternative: alternative,
		singleton: singleton, alternativeWork: alternativeWork, peerWork: peerWork, connection: connection}
}

func v16Input(t *testing.T, h *Hive, claim WorkerClaim) (map[string]any, string) {
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

func v16AssertChoice(t *testing.T, f v16ModeFixture, input map[string]any, raw, mode string) {
	t.Helper()
	if input["needs_fit_choice"] != true || input["newborn_mode"] != mode || input["handoff"] != v9Handoff {
		t.Fatalf("choice mode or exact handoff missing: %#v", input)
	}
	want := map[int64]string{f.invited: "Invited inquiry", f.alternative: "Alternative inquiry"}
	if mode == "hive_choice" {
		want[f.singleton] = "Unstaffed inquiry"
	}
	cards, ok := input["inquiry_signals"].([]any)
	if !ok || len(cards) != len(want) {
		t.Fatalf("got %#v signals, want %d", input["inquiry_signals"], len(want))
	}
	seen := map[int64]bool{}
	for _, value := range cards {
		card, ok := value.(map[string]any)
		if !ok || len(card) != 3 {
			t.Fatalf("unequal or preference-bearing card: %#v", value)
		}
		for _, key := range []string{"id", "question_first_line", "latest_work_id"} {
			if _, exists := card[key]; !exists {
				t.Fatalf("card lacks %s: %#v", key, card)
			}
		}
		inquiry := id(card["id"])
		question, exists := want[inquiry]
		if !exists || seen[inquiry] || card["question_first_line"] != question {
			t.Fatalf("wrong/duplicate signal: %#v", card)
		}
		seen[inquiry] = true
		if inquiry == f.alternative && id(card["latest_work_id"]) != f.alternativeWork {
			t.Fatal("alternative signal lost its original READ address")
		}
	}
	for _, key := range []string{"focus", "own_latest_work", "peer_work", "connection_offer", "directory_offer", "queen_guidance", "team_report", "team_assessment", "research_work"} {
		if _, exists := input[key]; exists {
			t.Fatalf("choice exposes rich/default context %q", key)
		}
	}
	drone, ok := input["drone"].(map[string]any)
	if !ok || len(drone) != 4 || drone["id"] == nil || drone["name"] == nil || drone["contribute"] == nil || drone["expand"] == nil {
		t.Fatalf("choice actor exposes invitation or loses genes: %#v", input["drone"])
	}
	members, ok := input["memberships"].([]any)
	if !ok || len(members) != 0 {
		t.Fatal("choice invented membership")
	}
	for _, canary := range []string{"V016_INVITER_FULL_QUESTION", "V016_UNSTAFFED_DETAIL", "V016_RICH_", "V016_FOREIGN_", "Explore an additional perspective", v9DiagnosticCanary, v9OldSeedCanary} {
		if strings.Contains(raw, canary) {
			t.Fatalf("choice leaked suppressed context %q", canary)
		}
	}
}

func TestV016NewbornModesIsolateAutomaticMembershipAndChoiceContext(t *testing.T) {
	for _, mode := range []string{"", "invitation", "fit_pair", "hive_choice", "rotation"} {
		t.Run("mode_"+mode, func(t *testing.T) {
			f := v16NewModeFixture(t, mode)
			members := v9Count(t, f.h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=?", f.child.DroneID)
			if mode == "rotation" {
				if members != 1 || v9Count(t, f.h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=? AND commitment='initial_rotation'", f.child.DroneID) != 1 {
					t.Fatal("rotation did not own the only automatic placement")
				}
			} else if members != 0 {
				t.Fatalf("mode %q silently committed its newborn", mode)
			}
			if v9Count(t, f.h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE drone_id=?", f.child.DroneID) != 0 {
				t.Fatal("admission fabricated child output")
			}
			v10Exec(t, f.h, "UPDATE hive_worker_queue SET offered_peer_id=?,offered_connection_id=? WHERE drone_id=?", f.peerWork, f.connection, f.child.DroneID)
			input, raw := v16Input(t, f.h, f.child)
			if mode == "fit_pair" || mode == "hive_choice" {
				v16AssertChoice(t, f, input, raw, mode)
				var offeredPeer, offeredConnection int64
				if err := f.h.Store.db.QueryRow("SELECT offered_peer_id,offered_connection_id FROM hive_worker_queue WHERE drone_id=?", f.child.DroneID).Scan(&offeredPeer, &offeredConnection); err != nil {
					t.Fatal(err)
				}
				if offeredPeer != 0 || offeredConnection != 0 {
					t.Fatal("hidden peer/connection remained offered")
				}
				if err := f.h.AdvanceWorker(v10Context(t), f.child, v10Action("PASS", 0, "No commitment yet.")); err != nil {
					t.Fatal(err)
				}
				var peerAfter, connectionAfter int64
				if err := f.h.Store.db.QueryRow("SELECT peer_after,connection_after FROM hive_worker_queue WHERE drone_id=?", f.child.DroneID).Scan(&peerAfter, &connectionAfter); err != nil {
					t.Fatal(err)
				}
				if peerAfter != 0 || connectionAfter != 0 {
					t.Fatal("choice consumed undisplayed evidence")
				}
			} else if input["needs_fit_choice"] == true || input["focus"] == nil || !strings.Contains(raw, "V016_INVITER_FULL_QUESTION") {
				t.Fatal("normal invitation/rotation context was replaced by a choice-mode payload")
			}
		})
	}
}

func TestV016HiveChoiceOrdersLiveMembershipCountsWithoutChangingPairMode(t *testing.T) {
	for _, mode := range []string{"hive_choice", "fit_pair"} {
		t.Run(mode, func(t *testing.T) {
			f := v16NewModeFixture(t, mode)
			assertOrder := func(want []int64) {
				t.Helper()
				input, raw := v16Input(t, f.h, f.child)
				v16AssertChoice(t, f, input, raw, mode)
				var actual []int64
				for _, value := range input["inquiry_signals"].([]any) {
					actual = append(actual, id(value.(map[string]any)["id"]))
				}
				if !reflect.DeepEqual(actual, want) {
					t.Fatalf("%s signal order=%v, want %v", mode, actual, want)
				}
			}
			// Counts are 8,0,0. Equal-size inquiries use their IDs; the large
			// inviter remains available at the end without a count field.
			if mode == "hive_choice" {
				assertOrder([]int64{f.alternative, f.singleton, f.invited})
			} else {
				assertOrder([]int64{f.invited, f.alternative})
			}
			v10Exec(t, f.h, "INSERT INTO hive_inquiry_members(inquiry_id,drone_id,commitment,created) VALUES(?,1,'new explicit commitment',?)", f.alternative, now())
			// A fresh input reads counts 8,1,0. No action by this newborn,
			// cached ranking, or change of its membership is required.
			if mode == "hive_choice" {
				assertOrder([]int64{f.singleton, f.alternative, f.invited})
			} else {
				assertOrder([]int64{f.invited, f.alternative})
			}
			if v9Count(t, f.h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=?", f.child.DroneID) != 0 {
				t.Fatal("ordering inquiry cards committed the newborn")
			}
		})
	}
}

func TestV016ExplicitReadAndFreeCommitmentEndNewbornChoice(t *testing.T) {
	for _, mode := range []string{"fit_pair", "hive_choice"} {
		for _, decision := range []string{"JOIN", "PROPOSE"} {
			t.Run(mode+"_"+decision, func(t *testing.T) {
				f := v16NewModeFixture(t, mode)
				input, raw := v16Input(t, f.h, f.child)
				v16AssertChoice(t, f, input, raw, mode)
				if err := f.h.AdvanceWorker(v10Context(t), f.child, v10Action("READ", f.alternativeWork, "Read the offered alternative before choosing.")); err != nil {
					t.Fatal(err)
				}
				claim := v14ClaimActor(t, f.h, f.child.DroneID)
				input, raw = v16Input(t, f.h, claim)
				read, ok := input["read_work"].(map[string]any)
				if !ok || id(read["id"]) != f.alternativeWork || read["body"] != v16Alternative || id(read["inquiry_id"]) != f.alternative {
					t.Fatalf("READ did not preserve the requested original: %#v", input["read_work"])
				}
				if input["needs_fit_choice"] != true || v9Count(t, f.h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=?", claim.DroneID) != 0 {
					t.Fatal("READ silently chose membership")
				}
				action := v10Action("JOIN", f.alternative, "I can develop this alternative's question.")
				if decision == "PROPOSE" {
					action = v10Action("PROPOSE", 0, "Independent inquiry\nA distinct original question.")
				}
				if err := f.h.AdvanceWorker(v10Context(t), claim, action); err != nil {
					t.Fatal(err)
				}
				claim = v14ClaimActor(t, f.h, f.child.DroneID)
				input, _ = v16Input(t, f.h, claim)
				focus, ok := input["focus"].(map[string]any)
				wantQuestion := v16Alternative
				if decision == "PROPOSE" {
					wantQuestion = action.Body
				}
				if !ok || input["needs_fit_choice"] == true || focus["question"] != wantQuestion || id(focus["id"]) != claim.FocusID {
					t.Fatalf("explicit %s did not restore chosen inquiry context: %#v", decision, input)
				}
				if v9Count(t, f.h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=?", claim.DroneID) != 1 || v9Count(t, f.h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=? AND inquiry_id=?", claim.DroneID, f.invited) != 0 {
					t.Fatal("explicit choice added inviter membership")
				}
			})
		}
	}
}

func TestV016OriginalSeedsKeepRootBootstrapInEveryMode(t *testing.T) {
	for _, mode := range []string{"invitation", "fit_pair", "hive_choice", "rotation"} {
		t.Run(mode, func(t *testing.T) {
			h, _, _ := v10Fixture(t, 6, nil)
			h.Config.Workers.NewbornMode = mode
			claim := v10Claim(t, h, "root-seed")
			input, _ := v16Input(t, h, claim)
			focus, ok := input["focus"].(map[string]any)
			if !ok || id(focus["id"]) != 1 || input["needs_fit_choice"] == true {
				t.Fatal("root seed was treated as an invited newcomer")
			}
			if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_queue WHERE focus_id=1") != 6 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members") != 0 {
				t.Fatal("mode changed original six seeds' placement")
			}
		})
	}
}

func TestV016InvalidNewbornModeFailsBeforeStateOrModelCalls(t *testing.T) {
	c := v10Config(t, 2, 1)
	for _, mode := range []string{"", "invitation", "fit_pair", "rotation", "hive_choice"} {
		c.Workers.NewbornMode = mode
		if err := c.Validate(); err != nil {
			t.Fatalf("valid mode %q rejected by Config.Validate: %v", mode, err)
		}
		if err := validateWorkers(c); err != nil {
			t.Fatalf("valid mode %q rejected by runner: %v", mode, err)
		}
	}
	c.Workers.NewbornMode = "invented_mode"
	if err := c.Validate(); err == nil {
		t.Fatal("invalid configured mode was accepted")
	}
	dir, calls := t.TempDir(), 0
	client := v9TextFunc(func(context.Context, TextRequest) (TextResponse, error) {
		calls++
		return TextResponse{}, errors.New("invalid mode must not reach a model")
	})
	if _, err := RunWorkerExperiment(v10Context(t), c, dir, v9Handoff, client); err == nil || calls != 0 {
		t.Fatalf("invalid mode reached execution: calls=%d error=%v", calls, err)
	}
	if _, err := os.Stat(filepath.Join(dir, c.Files.State)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid mode opened operational state: %v", err)
	}
}
