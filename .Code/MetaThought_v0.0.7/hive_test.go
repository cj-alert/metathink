package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

type hiveFixture struct {
	prompts       []string
	contributions int
	sawPeerWork   bool
	fail          bool
	failAfter     int
}

func (f *hiveFixture) Complete(_ context.Context, r TextRequest) (TextResponse, error) {
	if f.fail || (f.failAfter > 0 && len(f.prompts) >= f.failAfter) {
		return TextResponse{}, errors.New("fixture provider unavailable")
	}
	system := fmt.Sprint(r.Messages[0]["content"])
	input := fmt.Sprint(r.Messages[1]["content"])
	f.prompts = append(f.prompts, system+input)
	answer := "A new representation that might distinguish the hypotheses."
	switch {
	case strings.Contains(system, "Return one concise contribution"):
		f.contributions++
		if strings.Contains(input, "peer-work-") {
			f.sawPeerWork = true
		}
		answer = fmt.Sprintf("peer-work-%d: test the other model at input 5; compare its predicted reading with the offset model.", f.contributions)
	case strings.Contains(system, "current best answer"):
		answer = "The observations fit y=x+2. A competing nonlinear interpolation fits both points; measure at input 5 to distinguish their predictions. No claim of novelty."
	case strings.Contains(system, "Give one concise question"):
		answer = "Which new measurement distinguishes the two models?"
	case strings.Contains(system, "Write a concise contract"):
		answer = "Explain the two readings and propose a discriminating measurement; report uncertainties."
	}
	return TextResponse{Text: answer, Model: r.Model}, nil
}

type hiveDecisionsFixture struct{ rounds int }

func (f *hiveDecisionsFixture) Decide(_ context.Context, r DecisionRequest) (Decision, error) {
	result := Decision{Model: r.Model}
	for _, q := range r.Questions {
		a := DecisionAnswer{Type: q.Type, Name: q.Name}
		switch q.Type {
		case "choice":
			key := ""
			if q.Name != nil {
				key = *q.Name
			}
			selected := q.Choices[0].Value
			switch key {
			case "commit":
				selected = "ACCEPT"
			case "collapse":
				f.rounds++
				selected = "RETURN"
				if f.rounds == 1 {
					selected = "CONTINUE"
				}
			case "fit", "invite", "recipient":
				if len(q.Choices) > 1 {
					selected = q.Choices[1].Value
				}
			}
			a.Choice = selected
			a.Confidence = ptr(0.9)
		case "score":
			a.Score = ptr(float64(len(q.Levels) - 1))
			a.Confidence = ptr(0.9)
		default:
			return Decision{}, fmt.Errorf("unexpected fixture question %s", q.Type)
		}
		result.Answers = append(result.Answers, a)
	}
	return result, nil
}

func testHiveConfig(t *testing.T) Config {
	t.Helper()
	c, e := LoadConfig("config.json")
	if e != nil {
		t.Fatal(e)
	}
	c.Meta.APIKeyFile = ""
	return c
}

func TestHiveSharesThinkingAndRetainsTeamsAcrossRequests(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	c := testHiveConfig(t)
	model := &hiveFixture{}
	h, e := OpenHive(c, dir, model)
	if e != nil {
		t.Fatal(e)
	}
	h.Decisions = &hiveDecisionsFixture{}
	h.Logger.Log("developer", "canary", map[string]any{"secret_debug_marker": "DO_NOT_FEED_DIAGNOSTICS_TO_AGENTS"})
	first, e := h.Run(ctx, "Explain the supplied sensor readings and one discriminating test.", 0)
	if e != nil {
		h.Close()
		t.Fatal(e)
	}
	if first.TeamID == 0 || first.RequestID == 0 || first.Ticks != 2 || !first.CriteriaMet || first.Answer == "" {
		t.Fatalf("bad return: %+v", first)
	}
	if !model.sawPeerWork {
		t.Fatal("later contributions never received another contribution")
	}
	counts, e := h.query(ctx, "test", "hive_contributions", "SELECT drone_id,kind,COUNT(*) AS n FROM hive_contributions WHERE request_id=? AND kind IN ('contribute','expand') GROUP BY drone_id,kind", first.RequestID)
	if e != nil {
		t.Fatal(e)
	}
	if len(counts) != 6 {
		t.Fatalf("expected contribute and expand from three committed drones; got %v", counts)
	}
	links, e := h.query(ctx, "test", "hive_links", "SELECT COUNT(*) AS n FROM hive_links")
	if e != nil || id(links[0]["n"]) == 0 {
		t.Fatalf("no information exchanges: %v %v", links, e)
	}
	h.Close()
	h, e = OpenHive(c, dir, model)
	if e != nil {
		t.Fatal(e)
	}
	defer h.Close()
	h.Decisions = &hiveDecisionsFixture{}
	second, e := h.Run(ctx, "Reconsider the sensor model using the earlier inquiry.", first.RequestID)
	if e != nil {
		t.Fatal(e)
	}
	if second.RequestID == first.RequestID || second.TeamID != first.TeamID {
		t.Fatalf("fresh obligation or persistent team lost: first=%+v second=%+v", first, second)
	}
	rows, e := h.query(ctx, "test", "hive_requests", "SELECT id,status,parent_id FROM hive_requests ORDER BY id")
	if e != nil || len(rows) != 2 {
		t.Fatalf("requests missing: %v %v", rows, e)
	}
	if str(rows[0]["status"]) != "returned" || id(rows[1]["parent_id"]) != first.RequestID {
		t.Fatalf("prior obligation was changed: %v", rows)
	}
	for _, p := range model.prompts {
		if strings.Contains(p, "DO_NOT_FEED_DIAGNOSTICS_TO_AGENTS") {
			t.Fatal("diagnostic content entered model context")
		}
	}
	if filepath.Base(c.Files.State) == filepath.Base(c.Files.Debug) {
		t.Fatal("state/debug files overlap")
	}
}

func TestHiveFailureLeavesObligationOpen(t *testing.T) {
	c := testHiveConfig(t)
	h, e := OpenHive(c, t.TempDir(), &hiveFixture{fail: true})
	if e != nil {
		t.Fatal(e)
	}
	defer h.Close()
	h.Decisions = &hiveDecisionsFixture{}
	result, e := h.Run(context.Background(), "A request whose provider fails.", 0)
	if e == nil {
		t.Fatal("provider failure was hidden")
	}
	rows, e := h.query(context.Background(), "test", "hive_requests", "SELECT status,result FROM hive_requests WHERE id=?", result.RequestID)
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 1 || str(rows[0]["status"]) != "paused" || rows[0]["result"] != nil {
		t.Fatalf("failure falsely returned an answer: %v", rows)
	}
}

func TestCallObservationDoesNotEnforceQuota(t *testing.T) {
	f := &hiveFixture{}
	b := &LoggingClient{Inner: f, Log: &recordingHiveLog{}}
	r := TextRequest{Messages: []Message{{"content": "test"}, {"content": "data"}}}
	for i := 0; i < 100; i++ {
		if _, e := b.Complete(context.Background(), r); e != nil {
			t.Fatal(e)
		}
	}
	if len(f.prompts) != 100 || b.Calls != 100 {
		t.Fatal("calls were gated or not observed")
	}
}

type recordingHiveLog struct{}

func (*recordingHiveLog) Log(string, string, map[string]any) {}

func TestProviderFailureResumePreservesCompletedWork(t *testing.T) {
	ctx := context.Background()
	c := testHiveConfig(t)
	dir := t.TempDir()
	h, e := OpenHive(c, dir, &hiveFixture{failAfter: 1})
	if e != nil {
		t.Fatal(e)
	}
	h.Decisions = &hiveDecisionsFixture{}
	partial, e := h.Run(ctx, "An inquiry interrupted by a provider failure.", 0)
	if e == nil || partial.Status != "paused" {
		h.Close()
		t.Fatalf("expected retained paused request: %+v %v", partial, e)
	}
	h.Close()
	h, e = OpenHive(c, dir, &hiveFixture{})
	if e != nil {
		t.Fatal(e)
	}
	defer h.Close()
	h.Decisions = &hiveDecisionsFixture{}
	resumed, e := h.Resume(ctx, partial.RequestID)
	if e != nil {
		t.Fatal(e)
	}
	if resumed.RequestID != partial.RequestID || resumed.TeamID != partial.TeamID || resumed.Status != "returned" {
		t.Fatalf("resume changed obligation: %+v", resumed)
	}
	requests, e := h.query(ctx, "test", "hive_requests", "SELECT COUNT(*) AS n FROM hive_requests")
	if e != nil || id(requests[0]["n"]) != 1 {
		t.Fatalf("resume added an obligation: %v %v", requests, e)
	}
	oldWork, e := h.query(ctx, "test", "hive_contributions", "SELECT COUNT(*) AS n FROM hive_contributions WHERE request_id=? AND tick=1", partial.RequestID)
	if e != nil || id(oldWork[0]["n"]) != 1 {
		t.Fatalf("completed work was replayed: %v %v", oldWork, e)
	}
	if _, e = h.Resume(ctx, partial.RequestID); e == nil {
		t.Fatal("resumed an already returned request")
	}
}
