package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ContextLog struct {
	Sink   EventSink
	Fields map[string]any
}

func (l *ContextLog) Log(scope, event string, fields map[string]any) {
	m := map[string]any{}
	for k, v := range l.Fields {
		m[k] = v
	}
	for k, v := range fields {
		m[k] = v
	}
	l.Sink.Log(scope, event, m)
}

type LoggingClient struct {
	Inner TextClient
	Calls int
	mu    sync.Mutex
	Log   EventSink
}

func (b *LoggingClient) Complete(ctx context.Context, r TextRequest) (TextResponse, error) {
	b.mu.Lock()
	b.Calls++
	n := b.Calls
	b.mu.Unlock()
	b.Log.Log(r.Scope, "call_observed", map[string]any{"calls": n})
	return b.Inner.Complete(ctx, r)
}

type Hive struct {
	Config           Config
	Contract         Contract
	Dir              string
	Store            *Store
	Logger           *Logger
	Log              *ContextLog
	Text             *LoggingClient
	Decisions        DecisionClient
	requestID, runID int64
}
type HiveResult struct {
	Status               string  `json:"status"`
	RequestID            int64   `json:"request_id"`
	TeamID               int64   `json:"team_id"`
	Ticks                int     `json:"ticks"`
	Calls                int     `json:"calls"`
	CriteriaMet          bool    `json:"criteria_met"`
	CriteriaStatus       string  `json:"criteria_status,omitempty"`
	Rationale            string  `json:"rationale,omitempty"`
	SelectedCandidateIDs []int64 `json:"selected_candidate_ids,omitempty"`
	Reason               string  `json:"reason"`
	Answer               string  `json:"answer"`
}
type option struct{ value, description string }

func OpenHive(c Config, dir string, client TextClient) (*Hive, error) {
	l, err := OpenLogger(filepath.Join(dir, c.Files.Debug), c.Schema.Logs)
	if err != nil {
		return nil, err
	}
	h := &Hive{Config: c, Dir: dir, Logger: l}
	h.Log = &ContextLog{Sink: l, Fields: map[string]any{}}
	h.Store, err = OpenStore(filepath.Join(dir, c.Files.State), c.Schema.State, h.Log)
	if err != nil {
		l.Close()
		return nil, err
	}
	if client == nil {
		client, err = NewMetaClient(c.Meta, h.Log)
		if err != nil {
			h.Close()
			return nil, err
		}
	}
	h.Text = &LoggingClient{Inner: client, Log: h.Log}
	h.Decisions = NewMetaDecisions(h.Text, c.Decisions, h.Log)
	h.Log.Log("hive", "initialized", map[string]any{"version": Version, "outcome": "ok"})
	return h, nil
}
func (h *Hive) Close() {
	if h.Store != nil {
		h.Store.Close()
	}
	if h.Logger != nil {
		h.Logger.Close()
	}
}
func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func id(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}
func str(v any) string {
	if v == nil {
		return ""
	}
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return fmt.Sprint(v)
}
func ptr[T any](v T) *T         { return &v }
func raw(v any) json.RawMessage { return json.RawMessage(jsonText(v)) }
func (h *Hive) query(ctx context.Context, op, table, sql string, args ...any) ([]map[string]any, error) {
	return h.Store.Query(ctx, op, table, h.Log.Fields, sql, args...)
}
func (h *Hive) exec(ctx context.Context, op, table, sql string, args ...any) (int64, error) {
	r, err := h.Store.Exec(ctx, op, table, h.Log.Fields, sql, args...)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (h *Hive) generate(ctx context.Context, scope, model, key, input, chromosome string) (string, error) {
	return h.generatePrompt(ctx, scope, model, key, h.Config.Prompts[key], input, chromosome)
}

func (h *Hive) generatePrompt(ctx context.Context, scope, model, key, source, input, chromosome string) (string, error) {
	p, err := renderPrompt(source, map[string]any{"Chromosome": chromosome})
	if err != nil {
		return "", err
	}
	if h.Config.Workers.Continuous && (key == "inquiry_contribute" || key == "inquiry_expand" || key == "worker_queen_monitor") {
		p += "\n\n" + h.Config.Prompts["worker_continuous"]
	}
	r, err := h.Text.Complete(ctx, TextRequest{Scope: scope, ContextKind: key, Model: model, Messages: []Message{{"role": "developer", "content": p}, {"role": "user", "content": input}}})
	if err != nil {
		return "", err
	}
	if r.Refusal != "" {
		return "", fmt.Errorf("%s refused", key)
	}
	if strings.TrimSpace(r.Text) == "" {
		return "", fmt.Errorf("%s returned no text", key)
	}
	return r.Text, nil
}

func (h *Hive) choose(ctx context.Context, model, key, input string, options []option) (string, error) {
	if len(options) < 2 {
		return "", fmt.Errorf("choice %s needs at least two options", key)
	}
	choices := make([]DecisionChoice, len(options))
	for i, o := range options {
		choices[i] = DecisionChoice{Value: o.value, Description: ptr(o.description)}
	}
	r, err := h.Decisions.Decide(ctx, DecisionRequest{Model: model, ContextKind: key, Input: raw(input), Questions: []DecisionQuestion{{Type: "choice", Name: ptr(key), Instructions: raw(h.Config.Prompts[key]), Choices: choices}}})
	if err != nil {
		return "", err
	}
	if len(r.Answers) != 1 || r.Answers[0].Type != "choice" {
		return "", fmt.Errorf("%s did not return a choice", key)
	}
	return str(r.Answers[0].Choice), nil
}

func (h *Hive) Plan(ctx context.Context, question, priorDraft, feedback string) (string, error) {
	return DraftQuestionContract(ctx, h.Config, h.Text, h.Log, question, priorDraft, feedback)
}

func (h *Hive) seed(ctx context.Context) error {
	rows, err := h.query(ctx, "list", "hive_drones", "SELECT id FROM hive_drones ORDER BY id")
	if err != nil {
		return err
	}
	assign := make([]int, h.Config.Hive.DroneCount)
	for i := range assign {
		assign[i] = i % len(h.Config.Chromosomes)
	}
	rand.New(rand.NewSource(h.Config.Hive.Seed)).Shuffle(len(assign), func(i, j int) { assign[i], assign[j] = assign[j], assign[i] })
	for i := len(rows); i < h.Config.Hive.DroneCount; i++ {
		pair := h.Config.Chromosomes[assign[i]]
		_, err = h.exec(ctx, "create", "hive_drones", "INSERT INTO hive_drones(name,contribute,expand,signal,created) VALUES(?,?,?,?,?)", fmt.Sprintf("drone-%d", i+1), pair.Contribute, pair.Expand, pair.Contribute+" "+pair.Expand, now())
		if err != nil {
			return err
		}
	}
	return nil
}

func (h *Hive) drone(ctx context.Context, droneID int64) (map[string]any, error) {
	r, e := h.query(ctx, "get", "hive_drones", "SELECT * FROM hive_drones WHERE id=?", droneID)
	if e != nil {
		return nil, e
	}
	if len(r) != 1 {
		return nil, fmt.Errorf("drone %d missing", droneID)
	}
	return r[0], nil
}
func (h *Hive) members(ctx context.Context, teamID int64) ([]map[string]any, error) {
	return h.query(ctx, "members", "hive_members", "SELECT d.* FROM hive_members m JOIN hive_drones d ON d.id=m.drone_id WHERE m.team_id=? ORDER BY d.id", teamID)
}
func (h *Hive) accept(ctx context.Context, droneID int64, contract string) (bool, error) {
	d, err := h.drone(ctx, droneID)
	if err != nil {
		return false, err
	}
	h.Log.Fields["drone_id"] = droneID
	defer delete(h.Log.Fields, "drone_id")
	v, err := h.choose(ctx, h.Config.Models.Drone, "commit", jsonText(map[string]any{"contract": contract, "drone": d}), []option{{"ACCEPT", "Commit to the shared request"}, {"DECLINE", "Do not commit"}})
	return v == "ACCEPT", err
}
func (h *Hive) commit(ctx context.Context, teamID, droneID int64) error {
	_, err := h.exec(ctx, "commit", "hive_commitments", "INSERT OR IGNORE INTO hive_commitments(request_id,team_id,drone_id,created) VALUES(?,?,?,?)", h.requestID, teamID, droneID, now())
	return err
}

func (h *Hive) route(ctx context.Context, contract string) (int64, error) {
	teams, err := h.query(ctx, "board", "hive_teams", "SELECT t.id,t.summary,COUNT(m.drone_id) AS members FROM hive_teams t LEFT JOIN hive_members m ON m.team_id=t.id GROUP BY t.id ORDER BY t.id")
	if err != nil {
		return 0, err
	}
	var selected string
	if len(teams) > 0 {
		opts := []option{{"NEW", "No existing team fits"}}
		for _, t := range teams {
			if id(t["members"]) >= 2 {
				opts = append(opts, option{str(t["id"]), str(t["summary"])})
			}
		}
		if len(opts) > 1 {
			selected, err = h.choose(ctx, h.Config.Models.Drone, "fit", contract, opts)
			if err != nil {
				return 0, err
			}
		}
	}
	if selected != "" && selected != "NEW" {
		teamID := id(selected)
		members, e := h.members(ctx, teamID)
		if e != nil {
			return 0, e
		}
		accepted := []int64{}
		for _, d := range members {
			ok, e := h.accept(ctx, id(d["id"]), contract)
			if e != nil {
				return 0, e
			}
			if ok {
				accepted = append(accepted, id(d["id"]))
			}
		}
		if len(accepted) >= 2 {
			for _, d := range accepted {
				if e = h.commit(ctx, teamID, d); e != nil {
					return 0, e
				}
			}
			return teamID, nil
		}
	}
	drones, err := h.query(ctx, "board", "hive_drones", "SELECT * FROM hive_drones ORDER BY id")
	if err != nil {
		return 0, err
	}
	if len(drones) < 2 {
		return 0, errors.New("fewer than two drones")
	}
	// The receiving drone has no ongoing authority. Membership remains peer-based.
	receiver := id(drones[(h.runID-1)%int64(len(drones))]["id"])
	ok, err := h.accept(ctx, receiver, contract)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, errors.New("receiving drone declined; obligation remains open")
	}
	options := []option{{"NONE", "No suitable collaborator"}}
	for _, d := range drones {
		if id(d["id"]) != receiver {
			options = append(options, option{str(d["id"]), str(d["signal"])})
		}
	}
	chosen, err := h.choose(ctx, h.Config.Models.Drone, "invite", contract, options)
	if err != nil {
		return 0, err
	}
	if chosen == "NONE" {
		return 0, errors.New("no collaborator selected; obligation remains open")
	}
	other := id(chosen)
	ok, err = h.accept(ctx, other, contract)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, errors.New("invited drone declined; obligation remains open")
	}
	teamID, err := h.exec(ctx, "form", "hive_teams", "INSERT INTO hive_teams(summary,created) VALUES(?,?)", contract, now())
	if err != nil {
		return 0, err
	}
	for _, d := range []int64{receiver, other} {
		if _, err = h.exec(ctx, "join", "hive_members", "INSERT INTO hive_members(team_id,drone_id) VALUES(?,?)", teamID, d); err != nil {
			return 0, err
		}
		if err = h.commit(ctx, teamID, d); err != nil {
			return 0, err
		}
	}
	h.Log.Log("hive", "team_formed", map[string]any{"team_id": teamID, "members": []int64{receiver, other}})
	return teamID, nil
}

func (h *Hive) context(ctx context.Context, contract string, droneID int64) (string, error) {
	work, err := h.query(ctx, "context", "hive_contributions", "SELECT id,drone_id,kind,body,tick FROM hive_contributions WHERE request_id=? ORDER BY id DESC", h.requestID)
	if err != nil {
		return "", err
	}
	inbox, err := h.query(ctx, "inbox", "hive_links", "SELECT c.id,c.request_id,c.drone_id,c.kind,c.body FROM hive_links l JOIN hive_contributions c ON c.id=l.contribution_id WHERE l.to_drone=? ORDER BY l.id DESC", droneID)
	if err != nil {
		return "", err
	}
	guidance, err := h.query(ctx, "guidance", "hive_guidance", "SELECT body FROM hive_guidance WHERE request_id=? ORDER BY id DESC LIMIT 1", h.requestID)
	if err != nil {
		return "", err
	}
	return jsonText(map[string]any{"contract": contract, "drone_id": droneID, "recent_contributions": work, "received_offers": inbox, "queen_guidance": guidance}), nil
}

func (h *Hive) tick(ctx context.Context, contract string, teamID int64, tick int, d map[string]any) error {
	droneID := id(d["id"])
	h.Log.Fields["drone_id"] = droneID
	defer delete(h.Log.Fields, "drone_id")
	h.Log.Log("hive", "tick_start", map[string]any{"tick": tick})
	for _, kind := range []string{"contribute", "expand"} {
		input, err := h.context(ctx, contract, droneID)
		if err != nil {
			return err
		}
		body, err := h.generate(ctx, "drone", h.Config.Models.Drone, kind, input, str(d[kind]))
		if err != nil {
			return err
		}
		if kind == "contribute" && strings.HasPrefix(strings.TrimSpace(body), "# JAVASCRIPT\n") {
			source := strings.TrimPrefix(strings.TrimSpace(body), "# JAVASCRIPT\n")
			value, e := EvalJS(ctx, source)
			fields := map[string]any{"source": source, "result": value, "outcome": "ok"}
			if e != nil {
				fields["outcome"] = "error"
				fields["error"] = e.Error()
				body = "Computation failed: " + e.Error()
			} else {
				body = "Computation: " + source + "\nResult: " + value
			}
			h.Log.Log("drone", "javascript", fields)
		}
		contributionID, err := h.exec(ctx, "publish", "hive_contributions", "INSERT INTO hive_contributions(request_id,team_id,drone_id,tick,kind,body,created) VALUES(?,?,?,?,?,?,?)", h.requestID, teamID, droneID, tick, kind, body, now())
		if err != nil {
			return err
		}
		if kind == "expand" {
			if _, err = h.exec(ctx, "signal", "hive_drones", "UPDATE hive_drones SET signal=? WHERE id=?", body, droneID); err != nil {
				return err
			}
			if strings.TrimSpace(body) != "NONE" {
				board, e := h.query(ctx, "board", "hive_drones", "SELECT id,signal FROM hive_drones WHERE id<>? ORDER BY id", droneID)
				if e != nil {
					return e
				}
				opts := []option{{"NONE", "Publish without a specific recipient"}}
				for _, peer := range board {
					opts = append(opts, option{str(peer["id"]), str(peer["signal"])})
				}
				if len(opts) > 1 {
					target, e := h.choose(ctx, h.Config.Models.Drone, "recipient", body, opts)
					if e != nil {
						return e
					}
					if target != "NONE" {
						if _, e = h.exec(ctx, "exchange", "hive_links", "INSERT INTO hive_links(request_id,contribution_id,from_drone,to_drone,created) VALUES(?,?,?,?,?)", h.requestID, contributionID, droneID, id(target), now()); e != nil {
							return e
						}
					}
				}
			}
		}
	}
	h.Log.Log("hive", "tick_complete", map[string]any{"tick": tick, "outcome": "ok"})
	return nil
}

func (h *Hive) grow(ctx context.Context, contract string, teamID int64, tick int) error {
	peers, err := h.query(ctx, "candidates", "hive_drones", "SELECT id,signal FROM hive_drones WHERE id NOT IN (SELECT drone_id FROM hive_members WHERE team_id=?) ORDER BY id", teamID)
	if err != nil {
		return err
	}
	if len(peers) == 0 {
		return nil
	}
	members, err := h.query(ctx, "participants", "hive_commitments", "SELECT d.* FROM hive_commitments c JOIN hive_drones d ON d.id=c.drone_id WHERE c.request_id=? ORDER BY d.id", h.requestID)
	if err != nil {
		return err
	}
	if len(members) < 2 {
		return errors.New("request lacks two committed drones")
	}
	actor := id(members[(tick-1)%len(members)]["id"])
	input, err := h.context(ctx, contract, actor)
	if err != nil {
		return err
	}
	opts := []option{{"NONE", "Continue with the current team"}}
	for _, d := range peers {
		opts = append(opts, option{str(d["id"]), str(d["signal"])})
	}
	h.Log.Fields["drone_id"] = actor
	chosen, err := h.choose(ctx, h.Config.Models.Drone, "invite", input, opts)
	delete(h.Log.Fields, "drone_id")
	if err != nil {
		return err
	}
	if chosen == "NONE" {
		return nil
	}
	newID := id(chosen)
	ok, err := h.accept(ctx, newID, contract)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if _, err = h.exec(ctx, "join", "hive_members", "INSERT OR IGNORE INTO hive_members(team_id,drone_id) VALUES(?,?)", teamID, newID); err != nil {
		return err
	}
	return h.commit(ctx, teamID, newID)
}

func (h *Hive) Run(ctx context.Context, contract string, parentID int64) (HiveResult, error) {
	return h.run(ctx, contract, parentID, 0, 0, 1)
}

// Resume continues the same obligation with saved findings.
// It starts new atomic work after the last recorded tick; it never replays completed contributions.
func (h *Hive) Resume(ctx context.Context, requestID int64) (HiveResult, error) {
	rows, err := h.query(ctx, "resume", "hive_requests", "SELECT contract,status,team_id FROM hive_requests WHERE id=?", requestID)
	if err != nil {
		return HiveResult{}, err
	}
	if len(rows) != 1 {
		return HiveResult{}, errors.New("request not found")
	}
	h.requestID = requestID
	h.Log.Fields["request_id"] = requestID
	if returned, found, err := h.recoverReturn(ctx, requestID); err != nil {
		return HiveResult{}, err
	} else if found {
		return returned, nil
	}
	if str(rows[0]["status"]) == "returned" {
		return HiveResult{}, errors.New("request already returned but has no recoverable explicit candidate selection; preserve the historical result")
	}
	ticks, err := h.query(ctx, "last_tick", "hive_contributions", "SELECT COALESCE(MAX(tick),0) AS last_tick FROM hive_contributions WHERE request_id=?", requestID)
	if err != nil {
		return HiveResult{}, err
	}
	return h.run(ctx, str(rows[0]["contract"]), 0, requestID, id(rows[0]["team_id"]), int(id(ticks[0]["last_tick"]))+1)
}

func (h *Hive) run(ctx context.Context, contract string, parentID, existingRequest, existingTeam int64, startTick int) (result HiveResult, err error) {
	// The reviewed contract owns execution. Draft settings never supply fallback criteria.
	h.Contract, err = ParseContract(contract)
	if err != nil {
		return result, fmt.Errorf("confirmed contract: %w", err)
	}
	h.Log.Log("hive", "contract_selected", map[string]any{"criteria": h.Contract.Criteria, "source": "confirmed_request", "resuming": existingRequest != 0})
	h.requestID = existingRequest
	result.RequestID, result.TeamID = existingRequest, existingTeam
	h.runID, err = h.exec(ctx, "start", "hive_runs", "INSERT INTO hive_runs(started,status,version,config,request_id) VALUES(?,?,?,?,?)", now(), "running", Version, jsonText(h.Config), existingRequest)
	if err != nil {
		return result, err
	}
	h.Log.Fields["run_id"] = h.runID
	defer func() {
		result.Calls = h.Text.Calls
		status := "completed"
		cleanup := context.WithoutCancel(ctx)
		if err != nil {
			status = "failed"
			h.Log.Log("hive", "run_failed", map[string]any{"outcome": "error", "error": err.Error()})
			if h.requestID != 0 {
				// An uncertain commit outcome must never downgrade a durable
				// RETURN. Resolve saved state only on this exceptional path.
				returned, found, recoveryErr := h.recoverReturn(cleanup, h.requestID)
				if found && recoveryErr == nil {
					result, status = returned, "completed"
					result.Calls = h.Text.Calls
				} else {
					err = errors.Join(err, recoveryErr)
					result.Status = "paused"
					_, pauseErr := h.exec(cleanup, "pause", "hive_requests", "UPDATE hive_requests SET status='paused',finish_reason=? WHERE id=? AND status<>'returned'", err.Error(), h.requestID)
					if pauseErr != nil {
						err = errors.Join(err, pauseErr)
					}
					err = fmt.Errorf("request %d paused: %w", h.requestID, err)
				}
			}
		}
		_, closeErr := h.exec(cleanup, "finish", "hive_runs", "UPDATE hive_runs SET status=?,finished=?,calls=? WHERE id=?", status, now(), h.Text.Calls, h.runID)
		if closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	if existingRequest == 0 {
		var parent any
		if parentID > 0 {
			parent = parentID
		}
		h.requestID, err = h.exec(ctx, "request", "hive_requests", "INSERT INTO hive_requests(run_id,parent_id,contract,status,created) VALUES(?,?,?,?,?)", h.runID, parent, contract, "pending", now())
		if err != nil {
			return result, err
		}
		result.RequestID = h.requestID
	}
	h.Log.Fields["request_id"] = h.requestID
	if _, err = h.exec(ctx, "link", "hive_runs", "UPDATE hive_runs SET request_id=? WHERE id=?", h.requestID, h.runID); err != nil {
		return result, err
	}
	if err = h.seed(ctx); err != nil {
		return result, err
	}
	if result.TeamID == 0 {
		result.TeamID, err = h.route(ctx, contract)
		if err != nil {
			return result, err
		}
	}
	h.Log.Fields["team_id"] = result.TeamID
	if _, err = h.exec(ctx, "assign", "hive_requests", "UPDATE hive_requests SET team_id=?,status='active' WHERE id=?", result.TeamID, h.requestID); err != nil {
		return result, err
	}
	for tick := startTick; ; tick++ {
		members, e := h.query(ctx, "participants", "hive_commitments", "SELECT d.* FROM hive_commitments c JOIN hive_drones d ON d.id=c.drone_id WHERE c.request_id=? ORDER BY d.id", h.requestID)
		if e != nil {
			return result, e
		}
		if len(members) < 2 {
			return result, errors.New("request lacks two committed drones")
		}
		for _, d := range members {
			if err = h.tick(ctx, contract, result.TeamID, tick, d); err != nil {
				return result, err
			}
		}
		result.Ticks = tick - startTick + 1
		{
			input, e := h.context(ctx, contract, id(members[(tick-1)%len(members)]["id"]))
			if e != nil {
				return result, e
			}
			result.Answer, err = h.generate(ctx, "team", h.Config.Models.Drone, "candidate", input, "")
			if err != nil {
				return result, err
			}
			candidateID, e := h.exec(ctx, "candidate", "hive_contributions", "INSERT INTO hive_contributions(request_id,team_id,drone_id,tick,kind,body,created) VALUES(?,?,?,?,?,?,?)", h.requestID, result.TeamID, id(members[(tick-1)%len(members)]["id"]), tick, "candidate", result.Answer, now())
			if e != nil {
				return result, e
			}
			if e = h.assessCandidate(ctx, contract, candidateID, result.TeamID, tick, members); e != nil {
				return result, e
			}
			review, e := h.reviewCandidate(ctx, contract, candidateID, tick)
			if e != nil {
				return result, e
			}
			result.CriteriaStatus, result.Rationale = review.Criteria, review.Reason
			// Compatibility field: true only when the Queen reports the criteria supported.
			// The status preserves unmet versus uncertain; this is not a numerical grade.
			result.CriteriaMet = review.Criteria == "MET"
			if review.Decision == "RETURN" {
				result.Reason = "queen_return"
				result.Status = "returned"
				result.SelectedCandidateIDs = review.SelectedCandidateIDs
				result.Answer = review.answer
				h.Log.Log("queen", "result_received", map[string]any{"outcome": "returned", "selected_candidate_ids": result.SelectedCandidateIDs, "criteria_status": result.CriteriaStatus, "reason": result.Rationale, "answer": result.Answer})
				return result, nil
			}
		}
		input, e := h.queenEvidence(ctx, contract, 0)
		if e != nil {
			return result, e
		}
		guidance, e := h.generate(ctx, "queen", h.Config.Models.Queen, "guide", input, "")
		if e != nil {
			return result, e
		}
		if _, e = h.exec(ctx, "guide", "hive_guidance", "INSERT INTO hive_guidance(request_id,tick,body,created) VALUES(?,?,?,?)", h.requestID, tick, guidance, now()); e != nil {
			return result, e
		}
		if err = h.grow(ctx, contract, result.TeamID, tick); err != nil {
			return result, err
		}
	}
}
