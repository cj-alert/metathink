package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func v17ResourceReply(command string) string {
	return "ACTION: " + command + "\nThe observed concentration warrants this recruitment review; these are local decision scores.\n---\n0.8\n0.7"
}

func v17ResourceInquiry(t *testing.T, h *Hive, question string) int64 {
	t.Helper()
	return v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,1,?)", h.requestID, question, now())
}

func v17ResourceBirth(t *testing.T, h *Hive, inquiry int64) int64 {
	t.Helper()
	return v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,1,'expand','SPAWN','Saved successful birth',0.8,0.7,?)", inquiry, now())
}

func v17ResourceMarkSeen(t *testing.T, h *Hive) {
	t.Helper()
	v10Exec(t, h, `INSERT INTO hive_worker_queen_seen(request_id,inquiry_id,last_work_id)
 SELECT i.request_id,i.id,MAX(w.id) FROM hive_inquiries i JOIN hive_inquiry_work w ON w.inquiry_id=i.id
 WHERE i.request_id=? GROUP BY i.id
 ON CONFLICT(request_id,inquiry_id) DO UPDATE SET last_work_id=excluded.last_work_id`, h.requestID)
}

func TestV017ResourceActionParser(t *testing.T) {
	for _, test := range []struct {
		command string
		ids     []int64
	}{{"SELECT 2", []int64{2}}, {"SELECT 2 3 4", []int64{2, 3, 4}}, {"CLEAR", []int64{}}} {
		action, err := ParseWorkerResourceAction(v17ResourceReply(test.command))
		if err != nil || !reflect.DeepEqual(action.InquiryIDs, test.ids) || action.Relevance != 0.8 || action.InquiryValue != 0.7 || strings.Contains(action.Body, "---") {
			t.Fatalf("%s: action=%+v error=%v", test.command, action, err)
		}
	}
	for _, command := range []string{"SELECT", "SELECT 0", "SELECT -1", "SELECT 2 2", "SELECT 2,3", "SELECT 2 because", "CLEAR 2", "RETURN"} {
		if _, err := ParseWorkerResourceAction(v17ResourceReply(command)); err == nil {
			t.Fatalf("accepted invalid resource command %q", command)
		}
	}
	if _, err := ParseWorkerResourceAction(strings.Replace(v17ResourceReply("SELECT 2"), "0.8\n0.7", "NaN\n0.7", 1)); err == nil {
		t.Fatal("accepted nonfinite local score")
	}
}

func TestV017ResourceCadenceSelectionsAndRestart(t *testing.T) {
	h, _, dir := v10Fixture(t, 2, nil)
	h.Config.Workers.ResourceReviewEvery = 2
	first := v17ResourceInquiry(t, h, "First inquiry\nFull question remains saved.")
	second := v17ResourceInquiry(t, h, "Second inquiry")
	for _, inquiry := range []int64{first, second} {
		v10Exec(t, h, "INSERT INTO hive_inquiry_members(inquiry_id,drone_id,commitment,created) VALUES(?,1,'Actual shared membership',?)", inquiry, now())
		v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,1,'contribute','JOIN','Original operational contribution',0.8,0.7,?)", inquiry, now())
	}
	var resourceCalls, normalCalls int
	h.Text.Inner = v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
		var input map[string]any
		text := str(request.Messages[len(request.Messages)-1]["content"])
		if err := json.Unmarshal([]byte(text), &input); err != nil {
			return TextResponse{}, err
		}
		if strings.Contains(text, v9DiagnosticCanary) || strings.Contains(text, v9OldSeedCanary) || str(input["handoff"]) != v9Handoff {
			t.Error("resource input leaked diagnostics/presets or changed the accepted handoff")
		}
		if str(input["purpose"]) != "resource_review" {
			normalCalls++
			return TextResponse{Text: v9Reply("NOOP", "Ordinary work remains independently reviewed.", 0.8, 0.7)}, nil
		}
		resourceCalls++
		if request.Model != h.Config.Models.Queen || id(input["root_inquiry_id"]) != 1 || id(input["successful_births"]) != int64(resourceCalls*2) {
			t.Errorf("wrong model or birth snapshot: %s %s", request.Model, text)
		}
		sharedMemberships := 0
		for _, item := range input["inquiries"].([]any) {
			card := item.(map[string]any)
			if id(card["id"]) == first || id(card["id"]) == second {
				if id(card["committed_member_count"]) != 1 || id(card["contributing_member_count"]) != 1 {
					t.Errorf("overlapping memberships were not counted per inquiry: %s", text)
				}
				if _, present := card["work_since_review"]; !present || str(card["latest_work"].(map[string]any)["created"]) == "" {
					t.Error("recent activity count or original work timestamp missing")
				}
				sharedMemberships++
			}
		}
		if sharedMemberships != 2 || !strings.Contains(text, "Original operational contribution") {
			t.Error("resource review omitted population facts or original recent work")
		}
		if resourceCalls == 1 && input["prior_selection"] != nil {
			t.Error("first review invented prior decision scores")
		}
		if resourceCalls > 1 {
			prior := input["prior_selection"].(map[string]any)
			if str(prior["note"]) == "" || len(prior["selected_ids"].([]any)) != resourceCalls-1 {
				t.Errorf("prior review missing from new input: %s", text)
			}
		}
		switch resourceCalls {
		case 1:
			return TextResponse{Text: v17ResourceReply(fmt.Sprintf("SELECT %d", first))}, nil
		case 2:
			return TextResponse{Text: v17ResourceReply(fmt.Sprintf("SELECT %d %d", first, second))}, nil
		default:
			return TextResponse{Text: v17ResourceReply("CLEAR")}, nil
		}
	})
	v17ResourceMarkSeen(t, h)
	v17ResourceBirth(t, h, first)
	if result, err := h.MonitorWorkerQueen(v10Context(t), "ordinary-before-cadence"); err != nil || !result.Called || resourceCalls != 0 || normalCalls != 1 {
		t.Fatalf("review occurred below cadence: %+v error=%v", result, err)
	}
	for cycle := 1; cycle <= 3; cycle++ {
		if cycle > 1 {
			v17ResourceBirth(t, h, first)
		}
		v17ResourceBirth(t, h, second)
		v17ResourceMarkSeen(t, h)
		seenBefore := v9Count(t, h.Store.db, "SELECT SUM(last_work_id) FROM hive_worker_queen_seen")
		result, err := h.MonitorWorkerQueen(v10Context(t), fmt.Sprintf("resource-%d", cycle))
		if err != nil || !result.Called || result.BeginCollapse || resourceCalls != cycle {
			t.Fatalf("cycle %d: result=%+v error=%v", cycle, result, err)
		}
		if v9Count(t, h.Store.db, "SELECT SUM(last_work_id) FROM hive_worker_queen_seen") != seenBefore {
			t.Fatal("resource review advanced ordinary seen-work checkpoint")
		}
		var births int
		var selected string
		if err := h.Store.db.QueryRow("SELECT births_reviewed,selected_ids FROM hive_worker_resources WHERE request_id=?", h.requestID).Scan(&births, &selected); err != nil || births != cycle*2 {
			t.Fatalf("saved cadence=%d error=%v", births, err)
		}
		if cycle == 3 && selected != "[]" {
			t.Fatalf("CLEAR retained selection: %s", selected)
		}
	}
	c, requestID, runID := h.Config, h.requestID, h.runID
	h.Close()
	reopened, err := OpenHive(c, dir, v9TextFunc(func(context.Context, TextRequest) (TextResponse, error) {
		t.Error("restart replayed a completed resource review")
		return TextResponse{}, errors.New("unexpected replay")
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.Close)
	reopened.requestID, reopened.runID = requestID, runID
	if result, err := reopened.MonitorWorkerQueen(v10Context(t), "after-restart"); err != nil || result.Called {
		t.Fatalf("completed review replayed: %+v error=%v", result, err)
	}
}

func TestV017ResourceConcurrentOwnerAndCapturedBirths(t *testing.T) {
	h, _, dir := v10Fixture(t, 2, nil)
	h.Config.Workers.ResourceReviewEvery = 2
	inquiry := v17ResourceInquiry(t, h, "Population review subject")
	v17ResourceBirth(t, h, inquiry)
	v17ResourceBirth(t, h, inquiry)
	v17ResourceMarkSeen(t, h)
	ctx := v10Context(t)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	h.Text.Inner = v9TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return TextResponse{}, ctx.Err()
		}
		return TextResponse{Text: v17ResourceReply(fmt.Sprintf("SELECT %d", inquiry))}, nil
	})
	peer := v10Peer(t, h, h.Config, dir)
	v10Exec(t, peer, "UPDATE hive_worker_queue SET asleep_work_id=(SELECT MAX(id) FROM hive_inquiry_work),asleep_guidance_id=0,peer_after=(SELECT MAX(id) FROM hive_inquiry_work),connection_after=(SELECT MAX(id) FROM hive_inquiry_work)")
	if idle, err := peer.workerPoolIdle(ctx); err != nil || idle {
		t.Fatalf("due resource review was treated as idle: idle=%t error=%v", idle, err)
	}
	type outcome struct {
		result WorkerQueenResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := h.MonitorWorkerQueen(ctx, "resource-owner")
		done <- outcome{result, err}
	}()
	select {
	case <-started:
	case result := <-done:
		t.Fatalf("review failed before model call: %+v", result)
	case <-ctx.Done():
		t.Fatal("resource call did not start")
	}
	if result, err := peer.MonitorWorkerQueen(ctx, "other-owner"); err != nil || result.Called {
		t.Fatalf("competing Queen called: %+v error=%v", result, err)
	}
	v17ResourceBirth(t, peer, inquiry)
	close(release)
	select {
	case result := <-done:
		if result.err != nil || !result.result.Called {
			t.Fatalf("resource review failed: %+v", result)
		}
	case <-ctx.Done():
		t.Fatal("resource call did not finish")
	}
	if got := v9Count(t, peer.Store.db, "SELECT births_reviewed FROM hive_worker_resources WHERE request_id=?", h.requestID); got != 2 || calls.Load() != 1 {
		t.Fatalf("commit consumed a birth arriving during review: births=%d calls=%d", got, calls.Load())
	}
	v17ResourceMarkSeen(t, peer)
	if result, err := peer.MonitorWorkerQueen(ctx, "below-next-cadence"); err != nil || result.Called {
		t.Fatalf("one pending birth caused a second review: %+v error=%v", result, err)
	}
}

func TestV017ResourceInvalidReplyAndTransactionPreservePriorSelection(t *testing.T) {
	for _, mode := range []string{"root", "foreign", "malformed", "atomic_failure"} {
		t.Run(mode, func(t *testing.T) {
			h, _, _ := v10Fixture(t, 2, nil)
			h.Config.Workers.ResourceReviewEvery = 1
			inquiry := v17ResourceInquiry(t, h, "Eligible inquiry")
			foreignRequest := v10Exec(t, h, "INSERT INTO hive_requests(run_id,contract,status,created) VALUES(?,?,?,?)", h.runID, "Other request", "working", now())
			foreign := v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,1,?)", foreignRequest, "Foreign inquiry", now())
			v10Exec(t, h, "INSERT INTO hive_worker_resources(request_id,selected_ids,note,created) VALUES(?,?,?,?)", h.requestID, jsonText([]int64{inquiry}), "Previous selection remains authoritative", now())
			v17ResourceBirth(t, h, inquiry)
			reply := v17ResourceReply(fmt.Sprintf("SELECT %d", inquiry))
			switch mode {
			case "root":
				reply = v17ResourceReply("SELECT 1")
			case "foreign":
				reply = v17ResourceReply(fmt.Sprintf("SELECT %d", foreign))
			case "malformed":
				reply = strings.Replace(reply, "0.8\n0.7", "0.8\nnot a score", 1)
			case "atomic_failure":
				v10Exec(t, h, "CREATE TRIGGER reject_resource BEFORE UPDATE ON hive_worker_resources BEGIN SELECT RAISE(ABORT,'fixture persistence failure'); END")
			}
			h.Text.Inner = v9TextFunc(func(context.Context, TextRequest) (TextResponse, error) { return TextResponse{Text: reply}, nil })
			result, err := h.MonitorWorkerQueen(v10Context(t), "invalid-review")
			if err == nil || !result.Called {
				t.Fatalf("invalid review accepted: %+v error=%v", result, err)
			}
			if v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_resources WHERE births_reviewed=0 AND note='Previous selection remains authoritative'") != 1 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_queen WHERE owner='invalid-review'") != 1 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_queen_seen") != 0 {
				t.Fatal("failure changed prior selection, released uncertain owner, or advanced normal checkpoint")
			}
		})
	}
}

func TestV017ResourceHonorsSlotsAndProviderFailure(t *testing.T) {
	for _, exhausted := range []bool{false, true} {
		t.Run(fmt.Sprintf("exhausted_%t", exhausted), func(t *testing.T) {
			h, _, _ := v10Fixture(t, 2, nil)
			h.Config.Workers.ResourceReviewEvery, h.Config.Workers.Count = 1, 1
			inquiry := v17ResourceInquiry(t, h, "Resource subject")
			v17ResourceBirth(t, h, inquiry)
			var calls int
			h.Text.Inner = v9TextFunc(func(context.Context, TextRequest) (TextResponse, error) {
				calls++
				if exhausted {
					return TextResponse{}, &ProviderError{Kind: "retry_exhausted", StatusCode: 500, Attempts: 3, Message: "internal server error"}
				}
				return TextResponse{}, &ProviderError{Kind: "transport_error", Message: "uncertain response"}
			})
			claim := v10Claim(t, h, "occupied-slot")
			if result, err := h.MonitorWorkerQueen(v10Context(t), "waiting-resource"); err != nil || result.Called || calls != 0 {
				t.Fatalf("resource review exceeded global model slots: %+v %v", result, err)
			}
			if err := h.ReleaseWorker(v10Context(t), claim); err != nil {
				t.Fatal(err)
			}
			result, err := h.MonitorWorkerQueen(v10Context(t), "failing-resource")
			if err == nil || !result.Called || calls != 1 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_resources") != 0 {
				t.Fatalf("provider failure became a completed review: %+v %v calls=%d", result, err, calls)
			}
			owners := v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_worker_queen WHERE owner<>''")
			if exhausted && (owners != 0 || v9Count(t, h.Store.db, "SELECT COUNT(*) FROM hive_requests WHERE status='provider_unavailable'") != 1) {
				t.Fatal("exhaustion did not preserve controlled provider pause")
			}
			if !exhausted && owners != 1 {
				t.Fatal("uncertain provider failure released the live owner")
			}
		})
	}
}
