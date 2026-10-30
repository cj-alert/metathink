package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// The scoped loader includes v009_inquiry_test.go for its HTTP-blocking TestMain
// and local helpers, but selects only TestV010. Watchdogs below detect a local
// concurrency deadlock; they are not limits imposed on the application.
func v10Context(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func v10Config(t *testing.T, drones, workers int) Config {
	t.Helper()
	c := v9Config(t, drones)
	c.Workers.Count, c.Workers.Observe = workers, "none"
	// Historical worker scenarios predate forced newborn placement.
	c.Workers.NewbornFocusProbability = 0
	return c
}

func v10Exec(t *testing.T, h *Hive, query string, args ...any) int64 {
	t.Helper()
	row, err := h.exec(context.Background(), "fixture", schemaTarget(query), query, args...)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func v10Fixture(t *testing.T, drones int, client TextClient) (*Hive, Config, string) {
	t.Helper()
	c, dir := v10Config(t, drones, 10), t.TempDir()
	if client == nil {
		client = v9TextFunc(func(context.Context, TextRequest) (TextResponse, error) {
			return TextResponse{}, errors.New("storage fixture must not call a model")
		})
	}
	h, err := OpenHive(c, dir, client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	h.runID = v10Exec(t, h, "INSERT INTO hive_runs(started,status,version,config) VALUES(?,?,?,?)", now(), "working", Version, jsonText(c))
	h.requestID = v10Exec(t, h, "INSERT INTO hive_requests(run_id,contract,status,created) VALUES(?,?,?,?)", h.runID, v9Handoff, "working", now())
	h.Log.Fields["run_id"], h.Log.Fields["request_id"] = h.runID, h.requestID
	v10Exec(t, h, "UPDATE hive_runs SET request_id=? WHERE id=?", h.requestID, h.runID)
	if err = h.seed(context.Background()); err != nil {
		t.Fatal(err)
	}
	v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", h.requestID, c.Prompts["inquiry_root"], 0, now())
	if err = h.InitWorkerQueue(context.Background(), h.requestID); err != nil {
		t.Fatal(err)
	}
	v10Exec(t, h, "INSERT OR IGNORE INTO hive_worker_queen(request_id) VALUES(?)", h.requestID)
	return h, c, dir
}

func v10Peer(t *testing.T, h *Hive, c Config, dir string) *Hive {
	t.Helper()
	peer, err := OpenHive(c, dir, v9TextFunc(func(context.Context, TextRequest) (TextResponse, error) {
		return TextResponse{}, errors.New("storage peer must not call a model")
	}))
	if err != nil {
		t.Fatal(err)
	}
	peer.runID, peer.requestID = h.runID, h.requestID
	peer.Log.Fields["run_id"], peer.Log.Fields["request_id"] = h.runID, h.requestID
	t.Cleanup(peer.Close)
	return peer
}

func v10Claim(t *testing.T, h *Hive, owner string) WorkerClaim {
	t.Helper()
	claim, err := h.ClaimDrone(v10Context(t), owner, 10)
	if err != nil || claim == nil {
		t.Fatalf("claim %q: claim=%+v error=%v", owner, claim, err)
	}
	return *claim
}

func v10Action(kind string, inquiry int64, body string) InquiryAction {
	return InquiryAction{Kind: kind, InquiryID: inquiry, Body: body, Relevance: 0.8, InquiryValue: 0.7}
}

func TestV010ClaimsAreExclusiveAcrossIndependentSQLiteConnections(t *testing.T) {
	h, c, dir := v10Fixture(t, 12, nil)
	peers := make([]*Hive, 16)
	for i := range peers {
		peers[i] = v10Peer(t, h, c, dir)
	}
	ctx := v10Context(t)
	type answer struct {
		claim *WorkerClaim
		err   error
	}
	start, answers := make(chan struct{}), make(chan answer, len(peers))
	for i, peer := range peers {
		go func(i int, peer *Hive) {
			<-start
			claim, err := peer.ClaimDrone(ctx, fmt.Sprintf("process-%d", i), 10)
			answers <- answer{claim, err}
		}(i, peer)
	}
	close(start)
	owners := map[int64]string{}
	for range peers {
		select {
		case result := <-answers:
			if result.err != nil {
				t.Fatalf("concurrent claim failed: %v", result.err)
			}
			if result.claim != nil {
				if previous, exists := owners[result.claim.DroneID]; exists {
					t.Fatalf("drone %d claimed by both %s and %s", result.claim.DroneID, previous, result.claim.Owner)
				}
				owners[result.claim.DroneID] = result.claim.Owner
			}
		case <-ctx.Done():
			t.Fatal("concurrent SQLite claims did not finish")
		}
	}
	if len(owners) != 10 {
		t.Fatalf("claimed %d distinct actors, want the configured 10 slots", len(owners))
	}
	if claim, err := h.ClaimDrone(ctx, "late-process", 10); err != nil || claim != nil {
		t.Fatalf("live claims were reassigned: claim=%+v error=%v", claim, err)
	}
}

func TestV010ActionAndPhaseAreAtomicAndStaleSubmissionCannotRepeat(t *testing.T) {
	t.Run("committed_once", func(t *testing.T) {
		h, _, _ := v10Fixture(t, 2, nil)
		claim := v10Claim(t, h, "worker")
		action := v10Action("JOIN", 1, "A real local contribution with a commitment.")
		if err := h.AdvanceWorker(v10Context(t), claim, action); err != nil {
			t.Fatal(err)
		}
		if err := h.AdvanceWorker(v10Context(t), claim, action); err == nil {
			t.Fatal("stale Contribute submission was accepted twice")
		}
		next := v10Claim(t, h, "worker")
		if next.DroneID != claim.DroneID || next.Phase != "expand" {
			t.Fatalf("completed Contribute was repeated or actor changed: %+v", next)
		}
		if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work") != 1 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members") != 1 {
			t.Fatal("duplicate submission duplicated accepted action or membership")
		}
		wrongOwner := next
		wrongOwner.Owner = "different-worker"
		_ = h.ReleaseWorker(v10Context(t), wrongOwner)
		var owner string
		if err := h.Store.db.QueryRow("SELECT owner FROM hive_worker_queue WHERE drone_id=?", next.DroneID).Scan(&owner); err != nil || owner != next.Owner {
			t.Fatalf("another owner released a live actor: owner=%q error=%v", owner, err)
		}
		if err := h.ReleaseWorker(v10Context(t), next); err != nil {
			t.Fatal(err)
		}
		recovered := v10Claim(t, h, "explicit-recovery-owner")
		if recovered.DroneID != next.DroneID || recovered.Phase != "expand" {
			t.Fatalf("explicit release lost the durable next phase: %+v", recovered)
		}
	})
	t.Run("work_failure_rolls_back_membership_and_phase", func(t *testing.T) {
		h, _, _ := v10Fixture(t, 2, nil)
		claim := v10Claim(t, h, "worker")
		v10Exec(t, h, "CREATE TRIGGER v010_fail_work BEFORE INSERT ON hive_inquiry_work BEGIN SELECT RAISE(ABORT,'fixture work failure'); END")
		if err := h.AdvanceWorker(v10Context(t), claim, v10Action("JOIN", 1, "Must not partially commit.")); err == nil {
			t.Fatal("injected action persistence failure was concealed")
		}
		next := v10Claim(t, h, "worker")
		if next.DroneID != claim.DroneID || next.Phase != "contribute" || next.Owner != claim.Owner {
			t.Fatalf("failed action changed its claim or phase: %+v", next)
		}
		if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work") != 0 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members") != 0 {
			t.Fatal("failed action left a commitment or work behind")
		}
	})
}

func TestV010SpawnQueuesAtTailWithoutInventingMembership(t *testing.T) {
	h, _, _ := v10Fixture(t, 6, nil)
	first := v10Claim(t, h, "worker")
	if first.DroneID != 1 {
		t.Fatalf("unexpected first actor: %+v", first)
	}
	if err := h.AdvanceWorker(v10Context(t), first, v10Action("SPAWN", 1, "An additional perspective could examine this question.")); err != nil {
		t.Fatal(err)
	}
	expand := v10Claim(t, h, "worker")
	if expand.DroneID != 1 || expand.Phase != "expand" {
		t.Fatalf("spawn displaced its parent's unfinished turn: %+v", expand)
	}
	if err := h.AdvanceWorker(v10Context(t), expand, v10Action("PASS", 0, "No additional offer.")); err != nil {
		t.Fatal(err)
	}
	for want := int64(2); want <= 7; want++ {
		claim := v10Claim(t, h, "worker")
		if claim.DroneID != want || claim.Phase != "contribute" {
			t.Fatalf("spawn starved a preexisting actor or was not queued: got %+v want drone %d", claim, want)
		}
		if err := h.AdvanceWorker(v10Context(t), claim, v10Action("PASS", 0, "No useful contribution yet.")); err != nil {
			t.Fatal(err)
		}
		claim = v10Claim(t, h, "worker")
		if err := h.AdvanceWorker(v10Context(t), claim, v10Action("PASS", 0, "No useful expansion yet.")); err != nil {
			t.Fatal(err)
		}
	}
	if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_drones") != 7 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE drone_id=7") != 0 {
		t.Fatal("spawn failed to grow the population or silently committed the child")
	}
}

func TestV010ConcurrentModelCallsLeaveSQLiteAvailable(t *testing.T) {
	c, dir := v10Config(t, 2, 2), t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	started := make(chan int64, 4)
	var mu sync.Mutex
	active := map[int64]bool{}
	var problems []string
	client := v9TextFunc(func(callCtx context.Context, request TextRequest) (TextResponse, error) {
		if request.Model != c.Models.Drone {
			return TextResponse{}, errors.New("Queen called without accepted operational work")
		}
		var input v9Input
		if err := json.Unmarshal([]byte(str(request.Messages[len(request.Messages)-1]["content"])), &input); err != nil {
			return TextResponse{}, err
		}
		mu.Lock()
		if active[input.Drone.ID] {
			problems = append(problems, fmt.Sprintf("actor %d overlaps itself", input.Drone.ID))
		}
		active[input.Drone.ID] = true
		mu.Unlock()
		started <- input.Drone.ID
		<-callCtx.Done()
		mu.Lock()
		delete(active, input.Drone.ID)
		mu.Unlock()
		return TextResponse{}, callCtx.Err()
	})
	type completion struct {
		result InquiryExperimentResult
		err    error
	}
	finished := make(chan completion, 1)
	go func() {
		result, err := RunWorkerExperiment(ctx, c, dir, v9Handoff, client)
		finished <- completion{result, err}
	}()
	seen := map[int64]bool{}
	for len(seen) < 2 {
		select {
		case drone := <-started:
			seen[drone] = true
		case early := <-finished:
			t.Fatalf("runner ended before two calls overlapped: %+v", early)
		case <-ctx.Done():
			t.Fatal("model calls were serialized or actor identity was not supplied")
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, c.Files.State))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.ExecContext(ctx, "UPDATE hive_drones SET signal=signal WHERE id=1"); err != nil {
		t.Fatalf("a model call held the database write transaction: %v", err)
	}
	cancel()
	select {
	case result := <-finished:
		if result.err == nil {
			t.Fatalf("cancelled provider work was reported successful: %+v", result.result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not release cancelled worker calls")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(problems) != 0 {
		t.Fatal(problems)
	}
}

func TestV010WorkerContextIsScopedAndRemoteHistoryRequiresExplicitRead(t *testing.T) {
	h, c, dir := v10Fixture(t, 3, nil)
	focus := v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", h.requestID, "FOCUSED_QUESTION", 2, now())
	remote := v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", h.requestID, "REMOTE_DIRECTORY_QUESTION", 2, now())
	for _, inquiry := range []int64{focus, remote} {
		v10Exec(t, h, "INSERT INTO hive_inquiry_members(inquiry_id,drone_id,commitment,created) VALUES(?,?,?,?)", inquiry, 2, "Fixture peer commitment.", now())
	}
	for _, body := range []string{"FOCUS_PEER_ONE", "FOCUS_PEER_TWO"} {
		v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,?,?,?,?,?,?)", focus, 2, "contribute", "CONTRIBUTE", body, 0.8, 0.7, now())
	}
	remoteWork := v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,?,?,?,?,?,?)", remote, 2, "contribute", "CONTRIBUTE", "REMOTE_HISTORY_REQUIRES_READ", 0.8, 0.7, now())
	v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,target_inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,?,?,?,?,?,?,?)", remote, focus, 2, "expand", "CONNECT", "RELEVANT_INBOUND_CONNECTION", 0.8, 0.7, now())
	claim := v10Claim(t, h, "worker")
	if err := h.AdvanceWorker(v10Context(t), claim, v10Action("JOIN", focus, "MY_CURRENT_WORK")); err != nil {
		t.Fatal(err)
	}
	claim = v10Claim(t, h, "worker")
	diagnostic := v9ReadDB(t, filepath.Join(dir, c.Files.Debug))
	var before int64
	if err := diagnostic.QueryRow("SELECT COALESCE(MAX(id),0) FROM hive_log").Scan(&before); err != nil {
		t.Fatal(err)
	}
	input, err := h.WorkerInput(v10Context(t), claim)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"FOCUSED_QUESTION", "MY_CURRENT_WORK", "RELEVANT_INBOUND_CONNECTION"} {
		if !strings.Contains(input, required) {
			t.Fatalf("context omitted relevant operational material %q: %s", required, input)
		}
	}
	peerCount := 0
	for _, body := range []string{"FOCUS_PEER_ONE", "FOCUS_PEER_TWO"} {
		if strings.Contains(input, body) {
			peerCount++
		}
	}
	if peerCount != 1 {
		t.Fatalf("context loaded %d peer artifacts, want one current offer", peerCount)
	}
	for _, excluded := range []string{"REMOTE_HISTORY_REQUIRES_READ", v9DiagnosticCanary, v9OldSeedCanary} {
		if strings.Contains(input, excluded) {
			t.Fatalf("context leaked unrelated history or diagnostics %q", excluded)
		}
	}
	var decoded map[string]any
	if err = json.Unmarshal([]byte(input), &decoded); err != nil || decoded["handoff"] != v9Handoff {
		t.Fatalf("scoped context changed the exact handoff: error=%v", err)
	}
	if v9Count(t, diagnostic, "SELECT COUNT(*) FROM hive_log WHERE id>? AND event='sql_done' AND table_name='hive_drones' AND rows_count=1", before) == 0 || v9Count(t, diagnostic, "SELECT COUNT(*) FROM hive_log WHERE id>? AND event='sql_done' AND table_name='hive_drones' AND rows_count>1", before) != 0 {
		t.Fatal("WorkerInput did not load exactly one actor at a time")
	}
	if err = h.AdvanceWorker(v10Context(t), claim, v10Action("READ", remoteWork, "Inspect the original linked record before drawing a conclusion.")); err != nil {
		t.Fatal(err)
	}
	// Let the already queued peers finish their turns, preserving normal fairness.
	for want := int64(2); want <= 3; want++ {
		for phase := 0; phase < 2; phase++ {
			peer := v10Claim(t, h, "worker")
			if peer.DroneID != want {
				t.Fatalf("unexpected queue order while awaiting explicit read: %+v", peer)
			}
			if err = h.AdvanceWorker(v10Context(t), peer, v10Action("PASS", 0, "No new contribution.")); err != nil {
				t.Fatal(err)
			}
		}
	}
	claim = v10Claim(t, h, "worker")
	if claim.DroneID != 1 || claim.ReadWorkID != remoteWork {
		t.Fatalf("explicit read was lost across the turn boundary: %+v", claim)
	}
	input, err = h.WorkerInput(v10Context(t), claim)
	if err != nil || !strings.Contains(input, "REMOTE_HISTORY_REQUIRES_READ") {
		t.Fatalf("requested source record was not available: input=%s error=%v", input, err)
	}
	if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members WHERE inquiry_id=? AND drone_id=1", remote) != 0 {
		t.Fatal("reading a record silently joined its inquiry")
	}
}

func TestV010GrowthObservationRequiresSpawnedContributorAndMultipleTeams(t *testing.T) {
	c, dir := v10Config(t, 6, 1), t.TempDir()
	c.Workers.Observe = "growth"
	var mu sync.Mutex
	joinedChild := false
	droneCalls, queenCalls := 0, 0
	client := v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
		mu.Lock()
		defer mu.Unlock()
		if request.Model == c.Models.Queen {
			queenCalls++
			return TextResponse{Text: v9Reply("NOOP", "The work may continue; no intervention needed.", 0.8, 0.7)}, nil
		}
		if request.Model != c.Models.Drone {
			return TextResponse{}, fmt.Errorf("unexpected model %q", request.Model)
		}
		var input v9Input
		if err := json.Unmarshal([]byte(str(request.Messages[len(request.Messages)-1]["content"])), &input); err != nil {
			return TextResponse{}, err
		}
		if input.Handoff != v9Handoff {
			return TextResponse{}, errors.New("worker changed the saved handoff")
		}
		if joinedChild {
			return TextResponse{}, errors.New("made another drone call after the growth observation boundary")
		}
		droneCalls++
		action := "PASS"
		switch {
		case input.Drone.ID == 1 && input.Phase == "contribute":
			action = "JOIN 1"
		case input.Drone.ID == 1 && input.Phase == "expand":
			action = "PROPOSE"
		case input.Drone.ID == 2 && input.Phase == "contribute":
			action = "JOIN 2"
		case input.Drone.ID == 2 && input.Phase == "expand":
			action = "SPAWN 2"
		case input.Drone.ID == 3 && input.Phase == "contribute":
			action = "JOIN 1"
		case input.Drone.ID == 7 && input.Phase == "contribute":
			action, joinedChild = "JOIN 2", true
		}
		return TextResponse{Text: v9Reply(action, "Fixture inquiry contribution with evidence and uncertainty.", 0.8, 0.7)}, nil
	})
	result, err := RunWorkerExperiment(v10Context(t), c, dir, v9Handoff, client)
	if err != nil {
		t.Fatal(err)
	}
	want := []InquiryTeam{
		{InquiryID: 1, Members: []int64{1, 3}, Contributors: []int64{1, 3}},
		{InquiryID: 2, Members: []int64{1, 2, 7}, Contributors: []int64{1, 2, 7}},
	}
	if result.Status != "observed_growth" || !joinedChild || result.Calls != droneCalls+queenCalls || !reflect.DeepEqual(result.ActiveTeams, want) {
		t.Fatalf("growth stopped at population alone, failed to grow, or continued too far: drone=%d queen=%d result=%+v", droneCalls, queenCalls, result)
	}
	db := v9ReadDB(t, filepath.Join(dir, c.Files.State))
	if v9Count(t, db, "SELECT COUNT(*) FROM hive_drones") != 7 {
		t.Fatal("initial population was treated as a permanent ceiling")
	}
	if v9Count(t, db, "SELECT COUNT(*) FROM hive_worker_queue WHERE owner<>''") != 0 {
		t.Fatal("successful growth stop left a drone claimed with no call in flight")
	}
}

func TestV010UncommittedContributionCannotMutateWorkMembershipOrPhase(t *testing.T) {
	h, _, _ := v10Fixture(t, 2, nil)
	claim := v10Claim(t, h, "uncommitted-fixture")
	err := h.AdvanceWorker(v10Context(t), claim, v10Action("CONTRIBUTE", 1, "This actor has not explicitly joined the inquiry."))
	if err == nil {
		t.Fatal("CONTRIBUTE was accepted before an explicit commitment")
	}
	if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work") != 0 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members") != 0 {
		t.Fatal("rejected contribution created work or silently joined its author")
	}
	var owner, phase string
	var focus int64
	if err = h.Store.db.QueryRow("SELECT owner,phase,focus_id FROM hive_worker_queue WHERE request_id=? AND drone_id=?", claim.RequestID, claim.DroneID).Scan(&owner, &phase, &focus); err != nil {
		t.Fatal(err)
	}
	if owner != claim.Owner || phase != claim.Phase || focus != claim.FocusID {
		t.Fatalf("rejected contribution changed its durable actor state: owner=%q phase=%q focus=%d claim=%+v", owner, phase, focus, claim)
	}
}

func TestV010IdleRequiresNoReadyActorsOwnersOrUnseenQueenWork(t *testing.T) {
	h, _, _ := v10Fixture(t, 2, nil)
	ctx := v10Context(t)
	assertIdle := func(want bool) {
		t.Helper()
		idle, err := h.workerPoolIdle(ctx)
		if err != nil || idle != want {
			t.Fatalf("idle=%t want=%t error=%v", idle, want, err)
		}
	}
	// An unowned actor still has work available; ownership count alone is not idle.
	assertIdle(false)
	consumePasses := func() {
		t.Helper()
		for actor := 0; actor < 2; actor++ {
			for phase := 0; phase < 2; phase++ {
				claim := v10Claim(t, h, "idle-fixture")
				assertIdle(false)
				if _, err := h.WorkerInput(ctx, claim); err != nil {
					t.Fatal(err)
				}
				if err := h.AdvanceWorker(ctx, claim, v10Action("PASS", 0, "Inspected available work; no useful addition.")); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	consumePasses()
	assertIdle(true)
	// Both actors consume the arrival and become idle again, while the Queen's
	// independent unseen-work checkpoint remains outstanding.
	v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,?,?,?,?,?,?)", 1, 1, "contribute", "CONTRIBUTE", "New operational work awaiting Queen review.", 0.8, 0.7, now())
	consumePasses()
	assertIdle(false)
}

func TestV010QueenNoopSkipsUnchangedWorkAndKeepsArrivalsDuringReview(t *testing.T) {
	ctx := v10Context(t)
	started, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var inputs []string
	client := v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
		if request.Scope == "hive" {
			return TextResponse{Text: v19ChoiceReply(1, true), Model: request.Model}, nil
		}
		input := str(request.Messages[len(request.Messages)-1]["content"])
		mu.Lock()
		inputs = append(inputs, input)
		n := len(inputs)
		mu.Unlock()
		if n == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return TextResponse{}, ctx.Err()
			}
		}
		return TextResponse{Text: "No guidance needed for the inspected work.\n---\n0.8\n0.7"}, nil
	})
	h, c, dir := v10Fixture(t, 2, client)
	peer := v10Peer(t, h, c, dir)
	firstWork := v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,?,?,?,?,?,?)", 1, 1, "contribute", "CONTRIBUTE", "FIRST_REVIEW_WORK", 0.8, 0.7, now())
	type answer struct {
		result WorkerQueenResult
		err    error
	}
	done := make(chan answer, 1)
	go func() {
		result, err := h.MonitorWorkerQueen(ctx, "queen-owner")
		done <- answer{result, err}
	}()
	select {
	case <-started:
	case early := <-done:
		t.Fatalf("Queen did not inspect unseen work: %+v", early)
	case <-ctx.Done():
		t.Fatal("Queen did not start")
	}
	other, err := peer.MonitorWorkerQueen(ctx, "competing-owner")
	if err != nil || other.Called {
		t.Fatalf("second process concurrently called the same Queen: %+v error=%v", other, err)
	}
	secondWork := v10Exec(t, peer, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,?,?,?,?,?,?)", 1, 2, "contribute", "CONTRIBUTE", "ARRIVED_DURING_REVIEW", 0.8, 0.7, now())
	close(release)
	select {
	case first := <-done:
		if first.err != nil || !first.result.Called || first.result.BeginCollapse {
			t.Fatalf("first NOOP failed: %+v", first)
		}
	case <-ctx.Done():
		t.Fatal("Queen did not finish after release")
	}
	var through int64
	if err = h.Store.db.QueryRow("SELECT last_work_id FROM hive_worker_queen_seen WHERE request_id=? AND inquiry_id=1", h.requestID).Scan(&through); err != nil || through != firstWork {
		t.Fatalf("NOOP consumed work that arrived after its snapshot: got %d want %d error=%v", through, firstWork, err)
	}
	second, err := h.MonitorWorkerQueen(ctx, "queen-owner")
	if err != nil || !second.Called || second.BeginCollapse {
		t.Fatalf("new arrival was lost: result=%+v error=%v", second, err)
	}
	third, err := h.MonitorWorkerQueen(ctx, "queen-owner")
	if err != nil || third.Called {
		t.Fatalf("unchanged work caused another Queen call: result=%+v error=%v", third, err)
	}
	if err = h.Store.db.QueryRow("SELECT last_work_id FROM hive_worker_queen_seen WHERE request_id=? AND inquiry_id=1", h.requestID).Scan(&through); err != nil || through != secondWork {
		t.Fatalf("second NOOP did not advance its snapshot: got %d want %d error=%v", through, secondWork, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 2 || !strings.Contains(inputs[0], "FIRST_REVIEW_WORK") || strings.Contains(inputs[0], "ARRIVED_DURING_REVIEW") || !strings.Contains(inputs[1], "ARRIVED_DURING_REVIEW") {
		t.Fatalf("Queen input snapshots did not match actual review boundaries: %#v", inputs)
	}
}

func TestV010CapacitySupportsDozensOfTeamsWithDozensOfContributors(t *testing.T) {
	// Capacity only: the fixture supplies 24 inquiries. No inference about
	// spontaneous formation, intellectual influence or novelty follows from it.
	const size = 24
	h, _, _ := v10Fixture(t, size, nil)
	inquiries := []int64{1}
	for i := 1; i < size; i++ {
		inquiries = append(inquiries, v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", h.requestID, fmt.Sprintf("Capacity fixture inquiry %d", i+1), 0, now()))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for cycle := 0; cycle < size/2; cycle++ {
		for droneID := int64(1); droneID <= size; droneID++ {
			for phase := 0; phase < 2; phase++ {
				claim, err := h.ClaimDrone(ctx, "capacity-worker", 1)
				wantPhase := "contribute"
				if phase == 1 {
					wantPhase = "expand"
				}
				if err != nil || claim == nil || claim.DroneID != droneID || claim.Phase != wantPhase {
					t.Fatalf("capacity scheduling cycle=%d drone=%d phase=%s claim=%+v error=%v", cycle, droneID, wantPhase, claim, err)
				}
				inquiryID := inquiries[cycle*2+phase]
				action := v10Action("JOIN", inquiryID, fmt.Sprintf("Capacity fixture contribution by drone %d to inquiry %d.", droneID, inquiryID))
				if err = h.AdvanceWorker(ctx, *claim, action); err != nil {
					t.Fatalf("capacity join drone=%d inquiry=%d: %v", droneID, inquiryID, err)
				}
			}
		}
	}
	teams, err := h.observedInquiryTeams(ctx)
	if err != nil || len(teams) != size {
		t.Fatalf("capacity produced %d qualifying teams, want24: %v", len(teams), err)
	}
	wantMembers := make([]int64, size)
	for i := range wantMembers {
		wantMembers[i] = int64(i + 1)
	}
	for _, team := range teams {
		if !reflect.DeepEqual(team.Members, wantMembers) || !reflect.DeepEqual(team.Contributors, wantMembers) {
			t.Fatalf("team %d lacks the 24 independently committed contributors: %+v", team.InquiryID, team)
		}
	}
	if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_members") != size*size || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_inquiry_work WHERE kind='JOIN'") != size*size {
		t.Fatal("capacity memberships or actual accepted contributions were lost")
	}
	if h.Text.Calls != 0 {
		t.Fatal("capacity proof unexpectedly called a model")
	}
}
