package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// A worker is an execution slot, not a persistent drone or a team leader.
type workerCounter struct {
	inner TextClient
	calls *atomic.Int64
}

func (c workerCounter) Complete(ctx context.Context, req TextRequest) (TextResponse, error) {
	c.calls.Add(1)
	return c.inner.Complete(ctx, req)
}

func workerView(base *Hive, client TextClient, counter *atomic.Int64) (*Hive, error) {
	h := *base
	h.Log = &ContextLog{Sink: base.Logger, Fields: map[string]any{"run_id": base.runID, "request_id": base.requestID}}
	h.Store = &Store{db: base.Store.db, log: h.Log}
	if client == nil {
		var err error
		client, err = NewMetaClient(h.Config.Meta, h.Log)
		if err != nil {
			return nil, err
		}
	}
	h.Text = &LoggingClient{Inner: workerCounter{client, counter}, Log: h.Log}
	h.Decisions = NewMetaDecisions(h.Text, h.Config.Decisions, h.Log)
	return &h, nil
}

func validateWorkers(c Config) error {
	if err := c.Workers.validateResourceSettings(); err != nil {
		return err
	}
	if c.Workers.Count < 1 {
		return errors.New("workers.count must be positive")
	}
	if c.Workers.IdlePollMS < 1 {
		return errors.New("workers.idle_poll_ms must be positive")
	}
	if c.Workers.Observe != "growth" && c.Workers.Observe != "none" {
		return errors.New("workers.observe must be growth or none")
	}
	if c.Workers.ResourceReviewEvery > 0 {
		for _, key := range []string{"worker_resource_review", "worker_resource_scores", "worker_resource_format"} {
			if strings.TrimSpace(c.Prompts[key]) == "" {
				return fmt.Errorf("missing prompt %s", key)
			}
		}
	}
	for _, key := range []string{"queen_role", "worker_queen_monitor", "worker_queen_context", "worker_queen_scores", "worker_queen_format", "worker_team_report", "worker_team_assess", "worker_queen_return", "worker_contract_authority", "worker_initial_focus"} {
		if c.Prompts[key] == "" {
			return fmt.Errorf("missing prompt %s", key)
		}
	}
	return nil
}

func RunWorkerExperiment(ctx context.Context, c Config, dir, handoff string, client TextClient) (InquiryExperimentResult, error) {
	if err := validateWorkers(c); err != nil {
		return InquiryExperimentResult{}, err
	}
	if handoff == "" {
		return InquiryExperimentResult{}, errors.New("saved Queen handoff is required")
	}
	if _, err := os.Stat(filepath.Join(dir, c.Files.State)); err == nil {
		return InquiryExperimentResult{}, errors.New("preserve existing state; use work to continue it")
	} else if !errors.Is(err, os.ErrNotExist) {
		return InquiryExperimentResult{}, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return InquiryExperimentResult{}, err
	}
	h, err := OpenHive(c, dir, client)
	if err != nil {
		return InquiryExperimentResult{}, err
	}
	defer h.Close()
	run, err := h.exec(ctx, "create", "hive_runs", "INSERT INTO hive_runs(started,status,version,config) VALUES(?,?,?,?)", now(), "working", Version, jsonText(c))
	if err != nil {
		return InquiryExperimentResult{}, err
	}
	h.runID = run
	h.Log.Fields["run_id"] = h.runID
	request, err := h.exec(ctx, "create", "hive_requests", "INSERT INTO hive_requests(run_id,contract,status,created) VALUES(?,?,?,?)", h.runID, handoff, "working", now())
	if err != nil {
		return InquiryExperimentResult{}, err
	}
	h.requestID = request
	h.Log.Fields["request_id"] = h.requestID
	if _, err = h.exec(ctx, "bind_request", "hive_runs", "UPDATE hive_runs SET request_id=? WHERE id=?", h.requestID, h.runID); err != nil {
		return InquiryExperimentResult{}, err
	}
	// Startup inserts one record at a time; no population-sized object graph.
	rng := rand.New(rand.NewSource(c.Hive.Seed))
	for n := 0; n < c.Hive.DroneCount; n++ {
		pair := c.Chromosomes[rng.Intn(len(c.Chromosomes))]
		if _, err = h.exec(ctx, "create", "hive_drones", "INSERT INTO hive_drones(name,contribute,expand,signal,created) VALUES(?,?,?,?,?)", fmt.Sprintf("drone-%d", n+1), pair.Contribute, pair.Expand, pair.Contribute+" "+pair.Expand, now()); err != nil {
			return InquiryExperimentResult{}, err
		}
	}
	if _, err = h.exec(ctx, "create_root", "hive_inquiries", "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", h.requestID, c.Prompts["inquiry_root"], 0, now()); err != nil {
		return InquiryExperimentResult{}, err
	}
	if err = h.InitWorkerQueue(ctx, h.requestID); err != nil {
		return InquiryExperimentResult{}, err
	}
	if _, err = h.exec(ctx, "create", "hive_worker_queen", "INSERT INTO hive_worker_queen(request_id) VALUES(?)", h.requestID); err != nil {
		return InquiryExperimentResult{}, err
	}
	return runWorkerPool(ctx, h, client)
}

func ResumeWorkerExperiment(ctx context.Context, c Config, dir string, client TextClient) (InquiryExperimentResult, error) {
	if err := validateWorkers(c); err != nil {
		return InquiryExperimentResult{}, err
	}
	h, err := OpenHive(c, dir, client)
	if err != nil {
		return InquiryExperimentResult{}, err
	}
	defer h.Close()
	rows, err := h.query(ctx, "resume", "hive_requests", "SELECT id,run_id,status FROM hive_requests ORDER BY id DESC LIMIT 1")
	if err != nil {
		return InquiryExperimentResult{}, err
	}
	if len(rows) != 1 {
		return InquiryExperimentResult{}, errors.New("no saved worker request")
	}
	h.requestID = id(rows[0]["id"])
	h.runID = id(rows[0]["run_id"])
	h.Log.Fields["request_id"] = h.requestID
	h.Log.Fields["run_id"] = h.runID
	if str(rows[0]["status"]) == "provider_unavailable" {
		paused, e := h.savedWorkerProviderPause(ctx)
		if e != nil {
			return InquiryExperimentResult{}, e
		}
		if paused.ResumePhase != "working" && paused.ResumePhase != "converging" {
			return InquiryExperimentResult{}, errors.New("saved provider pause has no valid continuation phase")
		}
		changed, e := h.Store.Exec(ctx, "resume_provider", "hive_requests", h.Log.Fields,
			`UPDATE hive_requests SET status=?,finish_reason=NULL,result=NULL WHERE id=? AND status='provider_unavailable'
 AND json_extract(result,'$.ready_for_resume')=1
 AND NOT EXISTS(SELECT 1 FROM hive_worker_queue WHERE request_id=? AND owner<>'')
 AND NOT EXISTS(SELECT 1 FROM hive_worker_queen WHERE request_id=? AND owner<>'')`, paused.ResumePhase, h.requestID, h.requestID, h.requestID)
		if e != nil {
			return InquiryExperimentResult{}, e
		}
		n, e := changed.RowsAffected()
		if e != nil || n != 1 {
			return InquiryExperimentResult{}, errors.Join(e, errors.New("provider pause still has owned claims or its state changed; no claims were released by resume"))
		}
	}
	// This explicit resume is the operator's opportunity to supply a missing
	// capability/input after BLOCKED. Its original decision remains guidance.
	// Guard the observed stopped state: another controller may finish while
	// this caller opens the run. Already-working and terminal states are read
	// by the controller without clearing a newer result or phase.
	if state := str(rows[0]["status"]); state == "paused" || state == "failed" || state == "blocked" {
		if _, err = h.exec(ctx, "continue", "hive_requests", "UPDATE hive_requests SET status='working',finish_reason=NULL,result=NULL,criteria_met=NULL WHERE id=? AND status=?", h.requestID, state); err != nil {
			return InquiryExperimentResult{}, err
		}
	}
	return runWorkerPool(ctx, h, client)
}

func runWorkerPool(ctx context.Context, base *Hive, client TextClient) (result InquiryExperimentResult, returnedErr error) {
	result.RequestID = base.requestID
	stopView, viewErr := startDashboard(ctx, base)
	if viewErr != nil {
		result.Status = "failed"
		return result, viewErr
	}
	defer stopView()
	defer func() {
		if workerRetryExhausted(returnedErr) {
			paused, e := base.pauseWorkerProvider(context.Background(), returnedErr, true)
			if e != nil {
				returnedErr = errors.Join(returnedErr, e)
				result.Status, result.ReadyForResume = "failed", false
			} else {
				paused.apply(&result)
			}
		} else if returnedErr != nil {
			result.Status = "failed"
			// A different fatal error during drain invalidates clean continuation.
			_, e := base.exec(context.Background(), "provider_pause_failed", "hive_requests", "UPDATE hive_requests SET status='paused',finish_reason='failed' WHERE id=? AND status='provider_unavailable'", base.requestID)
			returnedErr = errors.Join(returnedErr, e)
		} else if result.Status == "provider_unavailable" {
			paused, e := base.savedWorkerProviderPause(context.Background())
			if e != nil {
				returnedErr = e
				result.Status = "failed"
			} else {
				paused.apply(&result)
			}
		}
		if returnedErr != nil {
			result.Error = returnedErr.Error()
		}
		_, err := base.exec(context.Background(), "finish_workers", "hive_runs", "UPDATE hive_runs SET status=?,finished=?,calls=calls+? WHERE id=?", result.Status, now(), result.Calls, base.runID)
		if err != nil {
			returnedErr = errors.Join(returnedErr, err)
			result.Status, result.ReadyForResume, result.Error = "failed", false, returnedErr.Error()
		}
		base.Log.Log("hive", "worker_pool_finished", map[string]any{"result": result, "outcome": result.Status})
	}()
	// The saved request chooses the next phase. A continued review returns here
	// after releasing the Queen, so every exploration pass has fresh workers
	// without recursive calls, discarded history or an iteration budget.
	for {
		rows, err := base.query(ctx, "worker_phase", "hive_requests", "SELECT status FROM hive_requests WHERE id=?", base.requestID)
		if err != nil {
			return result, err
		}
		if len(rows) != 1 {
			return result, errors.New("worker request missing")
		}
		switch state := str(rows[0]["status"]); state {
		case "converging", "returned", "blocked":
			result.ActiveTeams, err = base.observedInquiryTeams(ctx)
			if err != nil {
				return result, err
			}
			if err = completeWorkerResult(ctx, base, client, &result); err != nil {
				return result, err
			}
			if result.Status != "working" {
				return result, nil
			}
		case "working":
			pass, err := runWorkerExploration(ctx, base, client)
			result.Calls += pass.Calls
			result.Status, result.ActiveTeams = pass.Status, pass.ActiveTeams
			result.Answer, result.CriteriaMet = "", nil
			if err != nil {
				return result, err
			}
			switch pass.Status {
			case "convergence_requested", "working", "returned", "blocked":
				// Another controller may have completed the phase while this
				// pool drained. Route its saved state; do not overwrite it.
			default:
				return result, nil
			}
		default:
			result.Status = state
			return result, nil
		}
	}
}

func runWorkerExploration(ctx context.Context, base *Hive, client TextClient) (result InquiryExperimentResult, returnedErr error) {
	result.RequestID = base.requestID
	var calls atomic.Int64
	var stopping atomic.Bool
	var mu sync.Mutex
	stop := func(status string, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			returnedErr = errors.Join(returnedErr, err)
			if workerRetryExhausted(returnedErr) {
				result.Status = "provider_unavailable"
			} else {
				result.Status = "failed"
			}
		} else if result.Status == "" {
			result.Status = status
		}
		if !stopping.Swap(true) {
			if result.Status == "provider_unavailable" {
				if _, e := base.pauseWorkerProvider(context.Background(), returnedErr, false); e != nil {
					returnedErr = errors.Join(returnedErr, e)
					result.Status = "failed"
				}
				return
			}
			state := "paused"
			if status == "convergence_requested" {
				state = "converging"
			}
			_, e := base.exec(context.Background(), "stop_dispatch", "hive_requests", "UPDATE hive_requests SET status=?,finish_reason=? WHERE id=? AND status='working'", state, result.Status, base.requestID)
			if e != nil {
				returnedErr = errors.Join(returnedErr, e)
				result.Status = "failed"
			}
		}
	}
	wake := make(chan struct{}, 1)
	notify := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < base.Config.Workers.Count; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			h, err := workerView(base, client, &calls)
			if err != nil {
				stop("failed", err)
				return
			}
			owner := fmt.Sprintf("%d-%d-%d", os.Getpid(), time.Now().UnixNano(), slot)
			h.Log.Fields["worker"] = owner
			for !stopping.Load() {
				if err = ctx.Err(); err != nil {
					stop("paused", err)
					return
				}
				claim, e := h.ClaimDrone(ctx, owner, base.Config.Workers.Count)
				if e != nil {
					stop("failed", e)
					return
				}
				if claim == nil {
					delete(h.Log.Fields, "drone_id")
					h.Log.Fields["phase"] = "queen_monitor"
					q, qe := h.MonitorWorkerQueen(ctx, owner)
					if qe != nil {
						stop("failed", qe)
						return
					}
					if q.BeginCollapse {
						stop("convergence_requested", nil)
						return
					}
					if q.Called {
						notify()
						continue
					}
					// Idle workers do not call an LLM. SQLite remains the source of scheduling state.
					idle, e := h.workerPoolIdle(ctx)
					if e != nil {
						stop("failed", e)
						return
					}
					if idle {
						stop("idle", nil)
						return
					}
					select {
					case <-ctx.Done():
						stop("paused", ctx.Err())
						return
					case <-wake:
					case <-time.After(time.Duration(base.Config.Workers.IdlePollMS) * time.Millisecond):
					}
					continue
				}
				for claim != nil {
					states, se := h.query(ctx, "request_state", "hive_requests", "SELECT status,finish_reason FROM hive_requests WHERE id=?", h.requestID)
					if se != nil {
						stop("failed", se)
						return
					}
					if len(states) != 1 {
						stop("failed", errors.New("worker request missing"))
						return
					}
					if str(states[0]["status"]) != "working" {
						reason := str(states[0]["finish_reason"])
						if reason == "" {
							reason = "paused"
						}
						stop(reason, nil)
					}
					if stopping.Load() {
						if e = h.ReleaseWorker(context.Background(), *claim); e != nil {
							stop("failed", e)
						}
						break
					}
					h.Log.Fields["drone_id"] = claim.DroneID
					h.Log.Fields["phase"] = claim.Phase
					input, e := h.WorkerInput(ctx, *claim)
					if e != nil {
						stop("failed", e)
						return
					}
					drone, e := h.drone(ctx, claim.DroneID)
					if e != nil {
						stop("failed", e)
						return
					}
					prompt := h.Config.Prompts["worker_contract_authority"] + "\n\n" + h.Config.Prompts["inquiry_"+claim.Phase] + "\n\n" + h.Config.Prompts["inquiry_actions"] + "\n\n" + h.Config.Prompts["worker_initial_focus"] + "\n\n" + h.Config.Prompts["inquiry_scores"] + "\n\n" + h.Config.Prompts["inquiry_format"]
					reply, e := h.generatePrompt(ctx, "drone", h.Config.Models.Drone, "inquiry_"+claim.Phase, prompt, input, str(drone[claim.Phase]))
					if e != nil {
						stop("failed", e)
						if workerRetryExhausted(e) {
							if releaseErr := h.ReleaseWorker(context.Background(), *claim); releaseErr != nil {
								stop("failed", releaseErr)
							}
						}
						return
					}
					h.Log.Log("hive", "inquiry_reply", map[string]any{"body": reply, "outcome": "received"})
					action, e := ParseInquiryAction(reply)
					if e != nil {
						h.Log.Log("hive", "inquiry_action_rejected", map[string]any{"error": e.Error(), "body": reply})
						stop("failed", e)
						return
					}
					if action.Kind == "RESEARCH" {
						action, e = h.ResearchWorker(ctx, *claim, action)
						if e != nil {
							stop("failed", e)
							if workerRetryExhausted(e) {
								if releaseErr := h.ReleaseWorker(context.Background(), *claim); releaseErr != nil {
									stop("failed", releaseErr)
								}
							}
							return
						}
					}
					if e = h.AdvanceWorker(ctx, *claim, action); e != nil {
						stop("failed", e)
						return
					}
					notify()
					if h.Config.Workers.Observe == "growth" {
						grown, e := h.observedWorkerGrowth(ctx)
						if e != nil {
							stop("failed", e)
							return
						}
						if grown {
							stop("observed_growth", nil)
						}
					}
					if stopping.Load() {
						if e = h.ReleaseWorker(context.Background(), *claim); e != nil {
							stop("failed", e)
						}
						claim = nil
						break
					}
					if claim.Phase == "expand" {
						claim = nil
						break
					}
					priorClaim := *claim
					claim, e = h.ClaimDrone(ctx, owner, h.Config.Workers.Count)
					if e == nil && claim == nil {
						e = h.ReleaseWorker(context.Background(), priorClaim)
					}
					if e != nil {
						stop("failed", e)
						return
					}
				}
				if stopping.Load() {
					return
				}
				delete(h.Log.Fields, "drone_id")
				h.Log.Fields["phase"] = "queen_monitor"
				q, e := h.MonitorWorkerQueen(ctx, owner)
				if e != nil {
					stop("failed", e)
					return
				}
				if q.BeginCollapse {
					stop("convergence_requested", nil)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	result.Calls = int(calls.Load())
	states, stateErr := base.query(context.Background(), "final_worker_state", "hive_requests", "SELECT status,finish_reason FROM hive_requests WHERE id=?", base.requestID)
	if stateErr != nil {
		returnedErr = errors.Join(returnedErr, stateErr)
		result.Status = "failed"
	} else if len(states) == 1 && returnedErr == nil {
		state := str(states[0]["status"])
		if state == "converging" {
			result.Status = "convergence_requested"
		} else if state == "working" || state == "returned" || state == "blocked" {
			result.Status = state
		} else if saved := str(states[0]["finish_reason"]); saved != "" {
			result.Status = saved
			if saved == "failed" {
				returnedErr = errors.New("another worker stopped this request after a failure; inspect saved diagnostics")
			}
		}
	}
	if result.Status == "" {
		result.Status = "paused"
	}
	teams, err := base.observedInquiryTeams(context.Background())
	if err != nil {
		returnedErr = errors.Join(returnedErr, err)
		result.Status = "failed"
	}
	result.ActiveTeams = teams
	// stop_dispatch or the Queen already saved the stopped phase. A delayed
	// pool finalizer must not overwrite a subsequent continuation or return.
	base.Log.Log("hive", "worker_exploration_finished", map[string]any{"result": result, "outcome": result.Status})
	return result, returnedErr
}

func (h *Hive) observedWorkerGrowth(ctx context.Context) (bool, error) {
	rows, err := h.query(ctx, "observe_growth", "hive_worker_queue", `WITH contributors AS (SELECT m.inquiry_id,m.drone_id FROM hive_inquiry_members m JOIN hive_inquiries i ON i.id=m.inquiry_id WHERE i.request_id=? AND EXISTS(SELECT 1 FROM hive_inquiry_work w WHERE w.inquiry_id=m.inquiry_id AND w.drone_id=m.drone_id AND w.kind IN ('PROPOSE','JOIN','CONTRIBUTE'))), teams AS (SELECT inquiry_id FROM contributors GROUP BY inquiry_id HAVING COUNT(*)>1) SELECT (SELECT COUNT(*) FROM teams)>1 AND EXISTS(SELECT 1 FROM contributors c JOIN teams t ON t.inquiry_id=c.inquiry_id WHERE c.drone_id>?) AS observed`, h.requestID, h.Config.Hive.DroneCount)
	if err != nil {
		return false, err
	}
	return len(rows) == 1 && id(rows[0]["observed"]) != 0, nil
}

func executeWorkers(args []string) error {
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	config := f.String("config", "config.json", "configuration")
	dir := f.String("dir", ".", "runtime folder")
	handoff := f.String("handoff", "", "saved Queen handoff")
	feedback := f.String("feedback", "", "exact user feedback file for a confirmed contract revision")
	confirm := f.Bool("confirm", false, "confirm saved handoff")
	owner := f.String("owner", "", "exact stopped worker owner to release")
	stopped := f.Bool("owner-stopped", false, "confirm selected owner is stopped and its uncertain call was reviewed")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if args[0] == "worker-revise" && (*handoff == "" || *feedback == "" || !*confirm) {
		return errors.New("worker-revise requires -handoff REVISED_FILE -feedback USER_FEEDBACK_FILE -confirm; pause and drain workers first")
	}
	if *feedback != "" && args[0] != "worker-revise" {
		return errors.New("-feedback applies only to worker-revise")
	}
	if args[0] != "workers" {
		*config = filepath.Join(*dir, "config.json")
	}
	c, err := LoadConfig(*config)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if args[0] == "worker-revise" {
		body, e := os.ReadFile(*handoff)
		if e != nil {
			return e
		}
		feedbackBody, e := os.ReadFile(*feedback)
		if e != nil {
			return e
		}
		result, e := ReviseWorkerContract(ctx, c, *dir, string(body), string(feedbackBody))
		if e != nil {
			return e
		}
		data, e := json.MarshalIndent(result, "", "  ")
		if e != nil {
			return e
		}
		name := fmt.Sprintf("worker-revision-%s.json", time.Now().UTC().Format("20060102T150405.000000000"))
		return errors.Join(writeNew(filepath.Join(*dir, name), append(data, '\n')), json.NewEncoder(os.Stdout).Encode(result))
	}
	if args[0] == "worker-pause" {
		h, e := OpenHive(c, *dir, nil)
		if e != nil {
			return e
		}
		defer h.Close()
		_, e = h.exec(ctx, "operator_pause", "hive_requests", "UPDATE hive_requests SET status='paused',finish_reason='observation_paused' WHERE status='working'")
		if e == nil {
			fmt.Println("{\"status\":\"pause_requested\",\"inflight\":\"draining\"}")
		}
		return e
	}
	if args[0] == "worker-recover" {
		if *owner == "" || !*stopped {
			return errors.New("worker-recover requires exact -owner and -owner-stopped after reviewing the saved call")
		}
		return recoverWorkerOwner(ctx, c, *dir, *owner)
	}
	var result InquiryExperimentResult
	if args[0] == "workers" {
		if *handoff == "" || !*confirm {
			return errors.New("workers requires -handoff and -confirm")
		}
		body, e := os.ReadFile(*handoff)
		if e != nil {
			return e
		}
		if err = os.MkdirAll(*dir, 0700); err != nil {
			return err
		}
		source, _ := filepath.Abs(*config)
		dest, _ := filepath.Abs(filepath.Join(*dir, "config.json"))
		if source != dest {
			data, e := os.ReadFile(*config)
			if e != nil {
				return e
			}
			if e = writeNew(dest, data); e != nil {
				return e
			}
		}
		result, err = RunWorkerExperiment(ctx, c, *dir, string(body), nil)
	} else {
		result, err = ResumeWorkerExperiment(ctx, c, *dir, nil)
	}
	out, e := json.MarshalIndent(result, "", "  ")
	if e != nil {
		return errors.Join(err, e)
	}
	name := fmt.Sprintf("worker-result-%s.json", time.Now().UTC().Format("20060102T150405.000000000"))
	return errors.Join(err, writeNew(filepath.Join(*dir, name), append(out, '\n')), json.NewEncoder(os.Stdout).Encode(result))
}

// Completion begins only after exploration workers have drained.
func completeWorkerResult(ctx context.Context, h *Hive, client TextClient, result *InquiryExperimentResult) error {
	completion, err := h.CompleteWorkerRequest(ctx, client)
	result.Calls += completion.Calls
	if err != nil {
		return err
	}
	result.Status, result.Answer, result.CriteriaMet = completion.Status, completion.Answer, completion.CriteriaMet
	return nil
}
