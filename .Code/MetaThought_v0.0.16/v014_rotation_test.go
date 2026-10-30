package main

import (
	"fmt"
	"strings"
	"testing"
)

func v14Inquiry(t *testing.T, h *Hive, request int64, question string) int64 {
	t.Helper()
	return v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,1,?)", request, question, now())
}

// Acquire a named fixture actor through the actual queue. Temporarily held
// earlier actors are released unchanged, rather than rewriting queue order.
func v14ClaimActor(t *testing.T, h *Hive, drone int64) WorkerClaim {
	t.Helper()
	var owner string
	if err := h.Store.db.QueryRow("SELECT owner FROM hive_worker_queue WHERE drone_id=?", drone).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != "" {
		claim := v10Claim(t, h, owner)
		if claim.DroneID != drone {
			t.Fatalf("saved owner claimed wrong actor: %+v", claim)
		}
		return claim
	}
	var parked []WorkerClaim
	defer func() {
		for _, claim := range parked {
			if err := h.ReleaseWorker(v10Context(t), claim); err != nil {
				t.Error(err)
			}
		}
	}()
	population := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_queue WHERE request_id=?", h.requestID)
	for turn := 0; turn < population; turn++ {
		claim := v10Claim(t, h, fmt.Sprintf("v014-select-%d-%d", drone, turn))
		if claim.DroneID == drone {
			return claim
		}
		parked = append(parked, claim)
	}
	t.Fatalf("actor %d was not available in fixture queue", drone)
	return WorkerClaim{}
}

func v14AssertPlacement(t *testing.T, h *Hive, child, inquiry int64) {
	t.Helper()
	var focus, membership int64
	var phase, commitment, owner string
	err := h.Store.db.QueryRow(`SELECT q.focus_id,q.phase,q.owner,m.inquiry_id,m.commitment
 FROM hive_worker_queue q JOIN hive_inquiry_members m ON m.drone_id=q.drone_id
 WHERE q.drone_id=? AND q.request_id=? AND m.commitment='initial_rotation'`, child, h.requestID).
		Scan(&focus, &phase, &owner, &membership, &commitment)
	if err != nil || focus != inquiry || membership != inquiry || phase != "contribute" || owner != "" {
		t.Fatalf("newborn placement not atomic/coherent: child=%d focus=%d membership=%d phase=%q owner=%q error=%v", child, focus, membership, phase, owner, err)
	}
	if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=?", child) != 1 {
		t.Fatal("initial placement created extra memberships")
	}
	if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE drone_id=?", child) != 0 {
		t.Fatal("machine placement fabricated a child JOIN or contribution")
	}
}

func v14Spawn(t *testing.T, h *Hive, creator, invitation int64) int64 {
	t.Helper()
	claim := v14ClaimActor(t, h, creator)
	before := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work")
	body := "Explore an additional perspective; the original invitation stays preserved."
	if err := h.AdvanceWorker(v10Context(t), claim, v10Action("SPAWN", invitation, body)); err != nil {
		t.Fatal(err)
	}
	var child int64
	if err := h.Store.db.QueryRow("SELECT MAX(id) FROM hive_drones").Scan(&child); err != nil {
		t.Fatal(err)
	}
	if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work") != before+1 ||
		v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE drone_id=?", child) != 0 {
		t.Fatal("SPAWN must save only the creator's actual action")
	}
	var kind, savedBody string
	var target int64
	if err := h.Store.db.QueryRow("SELECT kind,body,inquiry_id FROM hive_inquiry_work ORDER BY id DESC LIMIT 1").Scan(&kind, &savedBody, &target); err != nil {
		t.Fatal(err)
	}
	if kind != "SPAWN" || savedBody != body || target != invitation {
		t.Fatal("placement rewrote the creator's original SPAWN output")
	}
	var sameGenes bool
	if err := h.Store.db.QueryRow("SELECT child.contribute=parent.contribute AND child.expand=parent.expand FROM hive_drones child,hive_drones parent WHERE child.id=? AND parent.id=?", child, creator).Scan(&sameGenes); err != nil || !sameGenes {
		t.Fatalf("rotation changed inherited chromosomes: same=%v error=%v", sameGenes, err)
	}
	return child
}

func TestV014RotationWrapsIncludingSingletonsAndSurvivesLaterMovement(t *testing.T) {
	h, _, _ := v10Fixture(t, 1, nil)
	h.Config.Workers.NewbornMode = "rotation"
	first := v14Inquiry(t, h, h.requestID, "First potential inquiry")
	second := v14Inquiry(t, h, h.requestID, "Second singleton inquiry")
	third := v14Inquiry(t, h, h.requestID, "Third potential inquiry")
	// Membership size is not a routing factor: the first/third have nobody,
	// while the middle candidate has one explicit existing commitment.
	v10Exec(t, h, "INSERT INTO hive_inquiry_members(inquiry_id,drone_id,commitment,created) VALUES(?,1,'existing singleton',?)", second, now())
	var children []int64
	for _, expected := range []int64{first, second, third, first} {
		child := v14Spawn(t, h, 1, third)
		v14AssertPlacement(t, h, child, expected)
		children = append(children, child)
	}
	teams, err := h.observedInquiryTeams(v10Context(t))
	if err != nil || len(teams) != 0 {
		t.Fatalf("forced members were counted as working contributors: teams=%+v error=%v", teams, err)
	}

	last := children[len(children)-1]
	claim := v14ClaimActor(t, h, last)
	if err = h.AdvanceWorker(v10Context(t), claim, v10Action("JOIN", second, "I choose the second inquiry after inspecting the available work.")); err != nil {
		t.Fatal(err)
	}
	var currentFocus, originalPlacement int64
	if err = h.Store.db.QueryRow("SELECT focus_id FROM hive_worker_queue WHERE drone_id=?", last).Scan(&currentFocus); err != nil {
		t.Fatal(err)
	}
	if err = h.Store.db.QueryRow("SELECT inquiry_id FROM hive_inquiry_members WHERE drone_id=? AND commitment='initial_rotation'", last).Scan(&originalPlacement); err != nil {
		t.Fatal(err)
	}
	if currentFocus != second || originalPlacement != first || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=?", last) != 2 {
		t.Fatal("later explicit movement lost original placement or overlapping membership")
	}
	// If rotation incorrectly used the child's new focus, this would choose
	// third. It must follow the durable initial placement at first instead.
	next := v14Spawn(t, h, 1, third)
	v14AssertPlacement(t, h, next, second)
	claim = v14ClaimActor(t, h, last)
	if err = h.AdvanceWorker(v10Context(t), claim, v10Action("CONTRIBUTE", first, "I can still develop the inquiry I originally joined by placement.")); err != nil {
		t.Fatalf("original membership stopped permitting later contributions: %v", err)
	}
	if v9Count(t, h.Store.db, "SELECT COUNT(DISTINCT drone_id) FROM hive_inquiry_work WHERE inquiry_id=? AND kind IN ('PROPOSE','JOIN','CONTRIBUTE')", first) != 1 {
		t.Fatal("contributor count did not distinguish actual output from placement")
	}
}

func TestV014RotationExcludesOtherRequestsAndTheirLatestPlacement(t *testing.T) {
	h, _, _ := v10Fixture(t, 1, nil)
	h.Config.Workers.NewbornMode = "rotation"
	first := v14Inquiry(t, h, h.requestID, "Current request first")
	second := v14Inquiry(t, h, h.requestID, "Current request second")
	child := v14Spawn(t, h, 1, second)
	v14AssertPlacement(t, h, child, first)
	otherRequest := v10Exec(t, h, "INSERT INTO hive_requests(run_id,contract,status,created) VALUES(?,?,?,?)", h.runID, "Other request", "working", now())
	otherInquiry := v14Inquiry(t, h, otherRequest, "Foreign inquiry must not become eligible")
	foreign := v10Exec(t, h, "INSERT INTO hive_drones(name,contribute,expand,signal,created) VALUES('foreign','original contribution','original expansion','foreign',?)", now())
	v10Exec(t, h, "INSERT INTO hive_inquiry_members(inquiry_id,drone_id,commitment,created) VALUES(?,?,'initial_rotation',?)", otherInquiry, foreign, now())
	next := v14Spawn(t, h, 1, first)
	v14AssertPlacement(t, h, next, second)
	wrapped := v14Spawn(t, h, 1, second)
	v14AssertPlacement(t, h, wrapped, first)
	if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE inquiry_id=?", otherInquiry) != 1 {
		t.Fatal("current-request rotation changed foreign memberships")
	}
}

func TestV014OriginalSixSeedsRemainUncommittedAndNoEligibleInquiryUsesRoot(t *testing.T) {
	h, _, _ := v10Fixture(t, 6, nil)
	h.Config.Workers.NewbornMode = "rotation"
	var root int64
	if err := h.Store.db.QueryRow("SELECT MIN(id) FROM hive_inquiries WHERE request_id=?", h.requestID).Scan(&root); err != nil {
		t.Fatal(err)
	}
	if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_queue WHERE request_id=? AND focus_id=?", h.requestID, root) != 6 ||
		v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members") != 0 ||
		v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work") != 0 {
		t.Fatal("rotation changed the original bootstrap seeds")
	}
	otherRequest := v10Exec(t, h, "INSERT INTO hive_requests(run_id,contract,status,created) VALUES(?,?,?,?)", h.runID, "Other request", "working", now())
	v14Inquiry(t, h, otherRequest, "Foreign root")
	v14Inquiry(t, h, otherRequest, "Foreign nonroot")
	child := v14Spawn(t, h, 1, root)
	v14AssertPlacement(t, h, child, root)
	if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id<=6") != 0 {
		t.Fatal("newborn fallback force-committed an original seed")
	}
	if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiries WHERE request_id=?", h.requestID) != 1 {
		t.Fatal("root fallback fabricated an inquiry")
	}
}

func TestV014RotationPlacementRollsBackWithFailedQueueInsertion(t *testing.T) {
	h, _, _ := v10Fixture(t, 1, nil)
	h.Config.Workers.NewbornMode = "rotation"
	target := v14Inquiry(t, h, h.requestID, "Eligible inquiry")
	claim := v14ClaimActor(t, h, 1)
	before := map[string]int{}
	for _, table := range []string{"hive_drones", "hive_worker_queue", "hive_inquiry_members", "hive_inquiry_work"} {
		before[table] = v9Count(t, h.Store.db, "SELECT COUNT(*) FROM "+table)
	}
	v10Exec(t, h, "CREATE TRIGGER v014_fail_queue BEFORE INSERT ON hive_worker_queue WHEN NEW.drone_id>1 BEGIN SELECT RAISE(ABORT,'v014 queue rejected'); END")
	err := h.AdvanceWorker(v10Context(t), claim, v10Action("SPAWN", target, "This attempt must roll back completely."))
	if err == nil || !strings.Contains(err.Error(), "v014 queue rejected") {
		t.Fatalf("expected injected queue failure, got %v", err)
	}
	for table, count := range before {
		if got := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM "+table); got != count {
			t.Fatalf("failed placement leaked %s rows: before=%d after=%d", table, count, got)
		}
	}
	still := v14ClaimActor(t, h, 1)
	if still.Phase != claim.Phase || still.FocusID != claim.FocusID || still.Owner != claim.Owner {
		t.Fatalf("failed placement advanced its creator: before=%+v after=%+v", claim, still)
	}
	v10Exec(t, h, "DROP TRIGGER v014_fail_queue")
	child := v14Spawn(t, h, 1, target)
	v14AssertPlacement(t, h, child, target)
}
