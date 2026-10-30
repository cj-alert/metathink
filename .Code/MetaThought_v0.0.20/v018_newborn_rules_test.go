package main

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

type v18BirthFixture struct {
	h                                      *Hive
	dir                                    string
	parent                                 WorkerClaim
	invited, least, quiet, foreign, readID int64
}

func v18Birth(t *testing.T, omit, focus float64) v18BirthFixture {
	t.Helper()
	h, _, dir := v10Fixture(t, 6, nil)
	h.Config.Workers.NewbornOmitProbability, h.Config.Workers.NewbornFocusProbability = omit, focus
	h.Config.Workers.FocusBasis = "members"
	parent := v10Claim(t, h, "v18-parent")
	if err := h.AdvanceWorker(v10Context(t), parent, v10Action("PROPOSE", 0, "Invited question\nINVITER_DETAIL")); err != nil {
		t.Fatal(err)
	}
	parent = v10Claim(t, h, parent.Owner)
	f := v18BirthFixture{h: h, dir: dir, parent: parent, invited: parent.FocusID}
	f.least = v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", h.requestID, "Smallest member question\nASSIGNED_DETAIL", 4, now())
	f.quiet = v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", h.requestID, "Fewest contributor question", 6, now())
	for inquiry, members := range map[int64][]int64{f.invited: {2, 3}, f.least: {4, 5}, f.quiet: {2, 3, 6}} {
		for _, drone := range members {
			v10Exec(t, h, "INSERT INTO hive_inquiry_members(inquiry_id,drone_id,commitment,created) VALUES(?,?,?,?)", inquiry, drone, "fixture commitment", now())
		}
	}
	// A has three members/two contributors, B two/two, C three/zero.
	// Contributor counts therefore cannot substitute for member counts.
	for inquiry, authors := range map[int64][]int64{f.invited: {2}, f.least: {4, 5}} {
		for _, drone := range authors {
			work := v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,'contribute','CONTRIBUTE',?,.8,.7,?)", inquiry, drone, fmt.Sprintf("Exact original %d/%d", inquiry, drone), now())
			if inquiry == f.least {
				f.readID = work
			}
		}
	}
	foreignRequest := v10Exec(t, h, "INSERT INTO hive_requests(run_id,contract,status,created) VALUES(?,?,?,?)", h.runID, "FOREIGN_HANDOFF", "paused", now())
	v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,0,?)", foreignRequest, "FOREIGN_ROOT", now())
	f.foreign = v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,2,?)", foreignRequest, "FOREIGN_INQUIRY", now())
	v10Exec(t, h, "INSERT INTO hive_worker_resources(request_id,selected_ids) VALUES(?,?)", h.requestID, jsonText([]int64{f.invited}))
	t.Cleanup(func() {
		if h.Text.Calls != 0 {
			t.Fatalf("newborn storage fixture made %d model calls", h.Text.Calls)
		}
	})
	return f
}

func v18Spawn(t *testing.T, f v18BirthFixture, parent WorkerClaim) int64 {
	t.Helper()
	if err := f.h.AdvanceWorker(v10Context(t), parent, v10Action("SPAWN", f.invited, "EXACT_ORIGINAL_INVITATION")); err != nil {
		t.Fatal(err)
	}
	return int64(v9Count(t, f.h.Store.db, "SELECT MAX(id) FROM hive_drones"))
}

func v18ClaimActor(t *testing.T, h *Hive, drone int64, prefix string) WorkerClaim {
	t.Helper()
	// Hold earlier fair-queue actors; no scripted work or model calls are needed.
	for n := 0; n < 10; n++ {
		claim := v10Claim(t, h, fmt.Sprintf("%s-%d", prefix, n))
		if claim.DroneID == drone {
			return claim
		}
	}
	t.Fatalf("actor %d was not reachable through the local worker queue", drone)
	return WorkerClaim{}
}

func v18Input(t *testing.T, h *Hive, claim WorkerClaim) (map[string]any, string) {
	t.Helper()
	raw, err := h.WorkerInput(v10Context(t), claim)
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	if err = json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	if input["handoff"] != v9Handoff || strings.Contains(raw, "FOREIGN_") {
		t.Fatalf("wrong governing/scope context: %s", raw)
	}
	return input, raw
}

func v18CardIDs(t *testing.T, input map[string]any, key string) []int64 {
	t.Helper()
	items, ok := input[key].([]any)
	if !ok {
		t.Fatalf("missing %s array: %v", key, input)
	}
	ids := []int64{}
	for _, item := range items {
		card, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("invalid card: %v", item)
		}
		ids = append(ids, int64(card["id"].(float64)))
	}
	return ids
}

func v18Assignment(t *testing.T, h *Hive, child int64) (int64, map[string]any) {
	t.Helper()
	var inquiry int64
	var commitment string
	if err := h.Store.db.QueryRow("SELECT inquiry_id,commitment FROM hive_inquiry_members WHERE drone_id=?", child).Scan(&inquiry, &commitment); err != nil {
		t.Fatal(err)
	}
	var marker map[string]any
	if err := json.Unmarshal([]byte(commitment), &marker); err != nil {
		t.Fatalf("machine assignment is not explicit JSON: %v", err)
	}
	if marker["kind"] != "initial_focus" {
		t.Fatalf("wrong assignment origin: %v", marker)
	}
	return inquiry, marker
}

func TestV018DisjointCohortsPreserveLegacyMenus(t *testing.T) {
	for _, tc := range []struct {
		name        string
		omit, focus float64
		cohort      int
	}{{"open", 0, 0, 0}, {"omit", 1, 0, 1}, {"focus", 0, 1, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			f := v18Birth(t, tc.omit, tc.focus)
			child := v18Spawn(t, f, f.parent)
			claim := v18ClaimActor(t, f.h, child, "child")
			if got := v9Count(t, f.h.Store.db, "SELECT omit_oversized FROM hive_worker_queue WHERE drone_id=?", child); got != tc.cohort {
				t.Fatalf("cohort=%d want integer %d", got, tc.cohort)
			}
			if n := v9Count(t, f.h.Store.db, "SELECT COUNT(*) FROM hive_worker_queue WHERE drone_id<=6 AND omit_oversized IS NOT NULL"); n != 0 {
				t.Fatal("original seeds acquired a newborn cohort")
			}
			if n := v9Count(t, f.h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE drone_id=?", child); n != 0 {
				t.Fatal("birth fabricated child thinking")
			}
			input, _ := v18Input(t, f.h, claim)
			if tc.cohort == 2 {
				if input["initial_focus_pending"] != true || input["needs_fit_choice"] == true || input["inquiry_signals"] != nil {
					t.Fatal("focused birth was given a free-choice menu")
				}
				if got := v18CardIDs(t, input, "hive_background"); !reflect.DeepEqual(got, []int64{f.invited, f.least, f.quiet}) {
					t.Fatalf("forced context was masked or included root/foreign inquiries: %v", got)
				}
				assigned, _ := v18Assignment(t, f.h, child)
				if assigned != f.least {
					t.Fatalf("assigned=%d want least members %d", assigned, f.least)
				}
			} else {
				if input["needs_fit_choice"] != true || input["initial_focus_pending"] == true {
					t.Fatal("legacy cohort was forced")
				}
				want := []int64{f.invited, f.least, f.quiet}
				if tc.cohort == 1 {
					want = want[1:]
				}
				if got := v18CardIDs(t, input, "inquiry_signals"); !reflect.DeepEqual(got, want) {
					t.Fatalf("menu=%v want %v", got, want)
				}
				if n := v9Count(t, f.h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=?", child); n != 0 {
					t.Fatal("legacy birth was automatically committed")
				}
			}
		})
	}
}

func TestV018FocusAssignmentRequiresContributionThenAllowsOverlap(t *testing.T) {
	f := v18Birth(t, 0, 1)
	h := f.h
	child := v18Spawn(t, f, f.parent)
	claim := v18ClaimActor(t, h, child, "focused")
	assigned, marker := v18Assignment(t, h, child)
	if assigned != f.least || claim.FocusID != assigned || marker["invited_inquiry_id"] != float64(f.invited) || marker["focus_basis"] != "members" || marker["population_before_assignment"] != float64(2) {
		t.Fatalf("invitation/least-member assignment lost: id=%d claim=%+v marker=%v", assigned, claim, marker)
	}
	if n := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE inquiry_id=?", assigned); n != 3 {
		t.Fatalf("forced membership count=%d", n)
	}
	if n := v9Count(t, h.Store.db, "SELECT COUNT(DISTINCT w.drone_id) FROM hive_inquiry_work w JOIN hive_inquiry_members m ON m.inquiry_id=w.inquiry_id AND m.drone_id=w.drone_id WHERE w.inquiry_id=? AND w.kind IN ('PROPOSE','CONTRIBUTE')", assigned); n != 2 {
		t.Fatalf("assignment was counted as actual thinking: %d", n)
	}
	input, raw := v18Input(t, h, claim)
	if int64(input["assigned_inquiry"].(map[string]any)["id"].(float64)) != assigned || strings.Contains(raw, "EXACT_ORIGINAL_INVITATION") {
		t.Fatalf("assigned context privileged the original invitation: %s", raw)
	}
	for _, action := range []InquiryAction{v10Action("JOIN", f.invited, "Rejected early switch"), v10Action("PROPOSE", 0, "Rejected early new inquiry"), v10Action("CONTRIBUTE", f.invited, "Rejected wrong target")} {
		before := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work")
		if err := h.AdvanceWorker(v10Context(t), claim, action); err == nil {
			t.Fatalf("accepted action before assigned contribution: %+v", action)
		}
		if got := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work"); got != before {
			t.Fatal("rejected action changed operational work")
		}
		if n := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=?", child); n != 1 {
			t.Fatal("rejected switch changed memberships")
		}
	}
	if err := h.AdvanceWorker(v10Context(t), claim, v10Action("READ", f.readID, "Read assigned original first")); err != nil {
		t.Fatal(err)
	}
	claim = v10Claim(t, h, claim.Owner)
	h.Config.Workers.NewbornFocusProbability = 0
	h.Config.Workers.NewbornOmitProbability = 1
	peer := v10Peer(t, h, h.Config, f.dir)
	input, _ = v18Input(t, peer, claim)
	if input["initial_focus_pending"] != true || int64(input["read_work"].(map[string]any)["id"].(float64)) != f.readID {
		t.Fatal("READ/reopen lost the original or redrew the assignment")
	}
	if err := h.AdvanceWorker(v10Context(t), claim, v10Action("CONTRIBUTE", assigned, "Exact first assigned contribution")); err != nil {
		t.Fatal(err)
	}
	claim = v18ClaimActor(t, h, child, "after-first")
	input, _ = v18Input(t, h, claim)
	if input["initial_focus_pending"] == true {
		t.Fatal("saved assigned contribution did not release the initial constraint")
	}
	if err := h.AdvanceWorker(v10Context(t), claim, v10Action("JOIN", f.invited, "Voluntary later overlap")); err != nil {
		t.Fatal(err)
	}
	if n := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=?", child); n != 2 {
		t.Fatal("later JOIN failed to preserve the initial membership")
	}
	if focus := v9Count(t, h.Store.db, "SELECT focus_id FROM hive_worker_queue WHERE drone_id=?", child); int64(focus) != f.invited {
		t.Fatal("later voluntary JOIN did not change focus")
	}
}

func TestV018FocusBirthAssignmentIsAtomic(t *testing.T) {
	f := v18Birth(t, 0, 1)
	h := f.h
	beforeD := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_drones")
	beforeM := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members")
	beforeW := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work")
	v10Exec(t, h, "CREATE TRIGGER v18_reject_focus_queue BEFORE INSERT ON hive_worker_queue WHEN NEW.omit_oversized=2 BEGIN SELECT RAISE(ABORT,'fixture queue failure'); END")
	if err := h.AdvanceWorker(v10Context(t), f.parent, v10Action("SPAWN", f.invited, "Rejected atomic birth")); err == nil {
		t.Fatal("expected queue insertion failure")
	}
	if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_drones") != beforeD || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members") != beforeM || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work") != beforeW {
		t.Fatal("failed birth left a child, membership, or accepted action behind")
	}
	var owner, phase string
	if err := h.Store.db.QueryRow("SELECT owner,phase FROM hive_worker_queue WHERE drone_id=?", f.parent.DroneID).Scan(&owner, &phase); err != nil {
		t.Fatal(err)
	}
	if owner != f.parent.Owner || phase != f.parent.Phase {
		t.Fatal("failed birth advanced the parent claim")
	}
	v10Exec(t, h, "DROP TRIGGER v18_reject_focus_queue")
	child := v18Spawn(t, f, f.parent)
	assigned, _ := v18Assignment(t, h, child)
	if assigned != f.least {
		t.Fatal("retry did not persist the complete least-member assignment")
	}
}

func TestV018FocusTiesAndContributorBasis(t *testing.T) {
	t.Run("ties_recount_after_each_birth", func(t *testing.T) {
		f := v18Birth(t, 0, 1)
		h := f.h
		a := v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,0,?)", h.requestID, "Empty eligible A", now())
		b := v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,2,?)", h.requestID, "Empty eligible B", now())
		first := v18Spawn(t, f, f.parent)
		picked, _ := v18Assignment(t, h, first)
		if picked != a && picked != b {
			t.Fatalf("random tie chose ineligible inquiry %d", picked)
		}
		parent := v18ClaimActor(t, h, f.parent.DroneID, "second-parent")
		second := v18Spawn(t, f, parent)
		next, _ := v18Assignment(t, h, second)
		if next == picked || (next != a && next != b) {
			t.Fatalf("second assignment ignored current minimum counts: first=%d next=%d", picked, next)
		}
	})
	t.Run("contributors_is_explicit_and_excludes_join_only", func(t *testing.T) {
		f := v18Birth(t, 0, 1)
		f.h.Config.Workers.FocusBasis = "contributors"
		v10Exec(t, f.h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,6,'contribute','JOIN','Join-only evidence',.8,.7,?)", f.quiet, now())
		child := v18Spawn(t, f, f.parent)
		assigned, marker := v18Assignment(t, f.h, child)
		if assigned != f.quiet || marker["focus_basis"] != "contributors" || marker["population_before_assignment"] != float64(0) {
			t.Fatalf("contributor basis counted JOIN or used member count: %d %v", assigned, marker)
		}
	})
}

func TestV018FocusWithoutResearchInquiryKeepsBootstrap(t *testing.T) {
	h, _, _ := v10Fixture(t, 2, nil)
	h.Config.Workers.NewbornOmitProbability = 0
	h.Config.Workers.NewbornFocusProbability = 1
	parent := v10Claim(t, h, "root-parent")
	if err := h.AdvanceWorker(v10Context(t), parent, v10Action("SPAWN", 1, "Bootstrap invitation")); err != nil {
		t.Fatal(err)
	}
	child := int64(v9Count(t, h.Store.db, "SELECT MAX(id) FROM hive_drones"))
	claim := v18ClaimActor(t, h, child, "bootstrap")
	if got := v9Count(t, h.Store.db, "SELECT omit_oversized FROM hive_worker_queue WHERE drone_id=?", child); got != 2 {
		t.Fatal("ineligible birth redrew its cohort")
	}
	if got := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=?", child); got != 0 {
		t.Fatal("bootstrap forced a root membership")
	}
	input, _ := v18Input(t, h, claim)
	if input["initial_focus_pending"] == true {
		t.Fatal("unapplied focus created a mandatory contribution")
	}
	if err := h.AdvanceWorker(v10Context(t), claim, v10Action("PROPOSE", 0, "First real investigation")); err != nil {
		t.Fatal(err)
	}
	if n := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE inquiry_id=1"); n != 0 {
		t.Fatal("root became a forced research team")
	}
	if h.Text.Calls != 0 {
		t.Fatal("bootstrap storage check called a model")
	}
}

func TestV018CohortConfigurationIsDisjointAndFinite(t *testing.T) {
	c := v9Config(t, 6)
	if math.Abs(c.Workers.NewbornOmitProbability-1.0/3) > 1e-12 || math.Abs(c.Workers.NewbornFocusProbability-1.0/3) > 1e-12 || c.Workers.FocusBasis != "members" {
		t.Fatalf("default is not the three-way member-based split: %+v", c.Workers)
	}
	if err := c.Workers.validateResourceSettings(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		omit, focus float64
		basis       string
	}{{"overlap", .6, .5, "members"}, {"negative_focus", 0, -.1, "members"}, {"nan_focus", 0, math.NaN(), "members"}, {"infinite_omit", math.Inf(1), 0, "members"}, {"unknown_basis", 0, 0, "capacity"}} {
		t.Run(tc.name, func(t *testing.T) {
			s := c.Workers
			s.NewbornOmitProbability, s.NewbornFocusProbability, s.FocusBasis = tc.omit, tc.focus, tc.basis
			if err := s.validateResourceSettings(); err == nil {
				t.Fatal("invalid cohort configuration accepted")
			}
		})
	}
}

func TestV018DashboardSeparatesMembersFromContributingThinkers(t *testing.T) {
	h, _, _ := v10Fixture(t, 3, nil)
	parent := v10Claim(t, h, "dashboard-author")
	if err := h.AdvanceWorker(v10Context(t), parent, v10Action("PROPOSE", 0, "A real research inquiry")); err != nil {
		t.Fatal(err)
	}
	inquiry := int64(v9Count(t, h.Store.db, "SELECT MAX(id) FROM hive_inquiries"))
	readTeam := func() DashboardTeam {
		t.Helper()
		snapshot, err := h.dashboardSnapshot(v10Context(t))
		if err != nil {
			t.Fatal(err)
		}
		for _, team := range snapshot.Teams {
			if team.ID == inquiry {
				return team
			}
		}
		t.Fatal("dashboard omitted the saved research inquiry")
		return DashboardTeam{}
	}
	before := readTeam()
	joiner := v10Claim(t, h, "dashboard-joiner")
	if err := h.AdvanceWorker(v10Context(t), joiner, v10Action("JOIN", inquiry, "An explicit commitment, not a later contribution")); err != nil {
		t.Fatal(err)
	}
	afterJoin := readTeam()
	if len(before.Members) != 1 || len(afterJoin.Members) != 2 || before.ActualContributorCount == nil || afterJoin.ActualContributorCount == nil || *before.ActualContributorCount != 1 || *afterJoin.ActualContributorCount != 1 {
		t.Fatalf("JOIN-only membership inflated actual thinking: before=%+v after=%+v", before, afterJoin)
	}
	if afterJoin.ContributorCount != 2 {
		t.Fatal("historical JOIN-inclusive formation evidence was changed")
	}
	joiner = v10Claim(t, h, joiner.Owner)
	if err := h.AdvanceWorker(v10Context(t), joiner, v10Action("CONTRIBUTE", inquiry, "A separately recorded substantive contribution")); err != nil {
		t.Fatal(err)
	}
	afterWork := readTeam()
	if afterWork.ActualContributorCount == nil || *afterWork.ActualContributorCount != 2 || len(afterWork.Members) != 2 {
		t.Fatal("saved substantive thinking did not increase the separate contributor count")
	}
	if h.Text.Calls != 0 {
		t.Fatal("dashboard fixture called a model")
	}
}
