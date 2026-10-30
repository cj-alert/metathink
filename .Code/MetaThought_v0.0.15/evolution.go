package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type EvolutionResult struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	Phase  string `json:"phase"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// This is continuation state, not a budget or an agent performance ledger.
type evolutionState struct {
	EvolutionResult
	Contract     string `json:"contract"`
	MetaContract string `json:"meta_contract"`
	ReferenceID  int64  `json:"reference_id"`
	MethodID     int64  `json:"method_id"`
	TrialID      int64  `json:"trial_id"`
	ParentID     int64  `json:"parent_id"`
	DonorID      int64  `json:"donor_id"`
	Action       string `json:"action"`
	Target       string `json:"target"`
	Instruction  string `json:"instruction"`
}

type evolutionMethod struct {
	ID, ParentID, DonorID int64
	Operator, Target      string
	Config                Config
}

type evolutionRunner struct {
	config  Config
	dir     string
	store   *Store
	logger  *Logger
	log     *metaQueenLog
	client  TextClient
	state   evolutionState
	inquiry *metaInquiry
}

func validateEvolution(c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(c.MetaQueen.ProblemType) == "" {
		return errors.New("evolution requires meta-queen problem_type")
	}
	for _, key := range []string{"mq_action", "mq_parent", "mq_donor", "mq_target", "mq_mutate", "mq_return"} {
		if strings.TrimSpace(c.Prompts[key]) == "" {
			return fmt.Errorf("missing prompt %s", key)
		}
	}
	return nil
}

func openEvolution(c Config, dir string) (*evolutionRunner, error) {
	logger, err := OpenLogger(filepath.Join(dir, c.Files.Debug), c.Schema.Logs)
	if err != nil {
		return nil, err
	}
	log := &metaQueenLog{Sink: logger, Fields: map[string]any{}}
	store, err := OpenStore(filepath.Join(dir, c.Files.State), c.Schema.State, log)
	if err != nil {
		logger.Close()
		return nil, err
	}
	return &evolutionRunner{config: c, dir: dir, store: store, logger: logger, log: log}, nil
}

func (e *evolutionRunner) close() {
	e.store.Close()
	e.logger.Close()
}

// RunEvolution starts one campaign in a fresh flat folder. Its population stays
// available until the meta-queen returns or the caller cancels. No live trial is
// launched by initialization, status, or loading a completed campaign.
func RunEvolution(ctx context.Context, c Config, dir, baselineDir string, baselineRequest int64, metaContract string, client TextClient) (EvolutionResult, error) {
	if err := validateEvolution(c); err != nil {
		return EvolutionResult{}, err
	}
	if _, err := ParseContract(metaContract); err != nil {
		return EvolutionResult{}, fmt.Errorf("confirmed meta contract: %w", err)
	}
	if baselineDir == "" || baselineRequest <= 0 {
		return EvolutionResult{}, errors.New("evolution requires a completed baseline request")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return EvolutionResult{}, err
	}
	for _, name := range []string{"config.json", "mq_config.json", "comparison.md", c.Files.State, c.Files.Contract, c.Files.Result} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return EvolutionResult{}, fmt.Errorf("evolution output already exists: %s; use evolve-resume or a fresh folder", name)
		} else if !os.IsNotExist(err) {
			return EvolutionResult{}, err
		}
	}
	body, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return EvolutionResult{}, err
	}
	if err = writeNew(filepath.Join(dir, "config.json"), body); err != nil {
		return EvolutionResult{}, err
	}
	e, err := openEvolution(c, dir)
	if err != nil {
		return EvolutionResult{}, err
	}
	defer e.close()
	e.state = evolutionState{EvolutionResult: EvolutionResult{Status: "running", Phase: "seed"}, MetaContract: metaContract}
	row, err := e.store.Exec(ctx, "start_evolution", "mq_evolutions", nil,
		"INSERT INTO mq_evolutions(problem_type,status,state,created,updated) VALUES(?,?,?,?,?)",
		c.MetaQueen.ProblemType, "running", jsonText(e.state), now(), now())
	if err != nil {
		return e.state.EvolutionResult, err
	}
	e.state.ID, err = row.LastInsertId()
	if err != nil {
		return e.state.EvolutionResult, err
	}
	e.log.Fields["evolution_id"], e.log.Fields["run_id"] = e.state.ID, e.state.ID
	if err = e.seed(ctx, baselineDir, baselineRequest); err != nil {
		return e.failed(ctx, err)
	}
	return e.run(ctx, client)
}

// Resume uses the campaign's saved controller. A supplied current config cannot
// silently change its evaluator, prompts, models, or active experimental intent.
func ResumeEvolution(ctx context.Context, _ Config, dir string, client TextClient) (EvolutionResult, error) {
	c, err := LoadConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		return EvolutionResult{}, err
	}
	if err = validateEvolution(c); err != nil {
		return EvolutionResult{}, err
	}
	if _, err = os.Stat(filepath.Join(dir, c.Files.State)); err != nil {
		return EvolutionResult{}, err
	}
	e, err := openEvolution(c, dir)
	if err != nil {
		return EvolutionResult{}, err
	}
	defer e.close()
	rows, err := e.store.Query(ctx, "resume_evolution", "mq_evolutions", nil, "SELECT id,state FROM mq_evolutions ORDER BY id")
	if err != nil {
		return EvolutionResult{}, err
	}
	if len(rows) != 1 {
		return EvolutionResult{}, errors.New("evolve-resume requires one saved campaign")
	}
	if err = json.Unmarshal([]byte(str(rows[0]["state"])), &e.state); err != nil {
		return EvolutionResult{}, err
	}
	e.state.ID = id(rows[0]["id"])
	e.log.Fields["evolution_id"], e.log.Fields["run_id"] = e.state.ID, e.state.ID
	if e.state.Status == "completed" {
		return e.state.EvolutionResult, nil
	}
	if e.state.Phase == "seed" {
		return e.state.EvolutionResult, errors.New("baseline import did not complete; preserve this campaign and start from a valid baseline in a fresh folder")
	}
	e.state.Status, e.state.Error = "running", ""
	if err = e.save(ctx); err != nil {
		return e.state.EvolutionResult, err
	}
	return e.run(ctx, client)
}

func (e *evolutionRunner) seed(ctx context.Context, dir string, requestID int64) error {
	path, err := filepath.Abs(filepath.Join(dir, e.config.Files.State))
	if err != nil {
		return err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro"}
	if !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path
	}
	store, err := OpenStore(u.String(), nil, e.log)
	if err != nil {
		return err
	}
	defer store.Close()
	rows, err := store.Query(ctx, "import_reference_config", "hive_runs", nil,
		"SELECT r.config FROM hive_runs r JOIN hive_requests q ON q.id=r.request_id WHERE q.id=? AND q.status='returned' AND r.status='completed' ORDER BY r.id DESC LIMIT 1", requestID)
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return errors.New("reference must have a returned request and completed run")
	}
	var c Config
	if err = json.Unmarshal([]byte(str(rows[0]["config"])), &c); err != nil {
		return err
	}
	c.Version, c.Schema, c.MetaQueen = Version, e.config.Schema, e.config.MetaQueen
	c.Meta.APIKeyEnv, c.Meta.APIKeyFile, c.Meta.APIKeyFileFormat = e.config.Meta.APIKeyEnv, e.config.Meta.APIKeyFile, e.config.Meta.APIKeyFileFormat
	metaFixedProtocols(&c, e.config)
	if err = c.Validate(); err != nil {
		return err
	}
	for _, target := range e.targets() {
		if _, err = metaTargetInstruction(c, e.config.MetaQueen.Chromosome, target); err != nil {
			return err
		}
	}
	evidence, err := loadMetaEvidence(ctx, store, requestID)
	if err != nil {
		return err
	}
	e.state.Contract = str(evidence.Request["contract"])
	if _, err = ParseContract(e.state.Contract); err != nil {
		return fmt.Errorf("reference requires a reviewed task contract; no configuration fallback: %w", err)
	}
	return e.atomic(ctx, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
		r, err := exec("import_reference", "mq_methods",
			"INSERT INTO mq_methods(evolution_id,operator,target,chromosome,config,created) VALUES(?,?,?,?,?,?)",
			e.state.ID, "reference", "", e.config.MetaQueen.Chromosome, jsonText(c), now())
		if err != nil {
			return err
		}
		e.state.ReferenceID, err = r.LastInsertId()
		if err != nil {
			return err
		}
		e.state.MethodID = e.state.ReferenceID
		r, err = exec("import_reference_evidence", "mq_trials",
			"INSERT INTO mq_trials(evolution_id,method_id,status,evidence,assessment,created,updated) VALUES(?,?,?,?,?,?,?)",
			e.state.ID, e.state.ReferenceID, "returned", jsonText(evidence), "", now(), now())
		if err != nil {
			return err
		}
		e.state.TrialID, err = r.LastInsertId()
		e.state.Phase = "assess"
		return err
	})
}

func (e *evolutionRunner) targets() []string {
	if len(e.config.MetaQueen.Targets) != 0 {
		return e.config.MetaQueen.Targets
	}
	return []string{e.config.MetaQueen.Behavior}
}

func (e *evolutionRunner) save(ctx context.Context) error {
	e.log.Log("meta_queen", "evolution_stage", map[string]any{"phase": e.state.Phase, "method_id": e.state.MethodID, "trial_id": e.state.TrialID, "outcome": e.state.Status})
	_, err := e.store.Exec(ctx, "save_evolution", "mq_evolutions", e.log.Fields,
		"UPDATE mq_evolutions SET status=?,state=?,updated=? WHERE id=?", e.state.Status, jsonText(e.state), now(), e.state.ID)
	return err
}

// Save related method/trial/checkpoint changes in one ordinary transaction, so a
// restart cannot orphan a completed selection and silently breed it again.
func (e *evolutionRunner) atomic(ctx context.Context, fn func(func(string, string, string, ...any) (sql.Result, error)) error) (err error) {
	before := e.state
	err = e.store.Transaction(ctx, e.log.Fields, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
		if err := fn(exec); err != nil {
			return err
		}
		_, err := exec("save_evolution", "mq_evolutions", "UPDATE mq_evolutions SET status=?,state=?,updated=? WHERE id=?",
			e.state.Status, jsonText(e.state), now(), e.state.ID)
		return err
	})
	if err != nil {
		e.state = before
	}
	return err
}

func (e *evolutionRunner) failed(ctx context.Context, err error) (EvolutionResult, error) {
	// A commit error does not prove rollback. Recover the durable checkpoint
	// only on this failure path before recording an interruption. If it cannot
	// be read, do not overwrite an uncertain method, trial or completed return.
	finishCtx := context.WithoutCancel(ctx)
	rows, readErr := e.store.Query(finishCtx, "recover_evolution_checkpoint", "mq_evolutions", e.log.Fields,
		"SELECT state FROM mq_evolutions WHERE id=?", e.state.ID)
	var saved evolutionState
	if readErr == nil {
		if len(rows) != 1 {
			readErr = errors.New("failed evolution has no saved checkpoint")
		} else {
			readErr = json.Unmarshal([]byte(str(rows[0]["state"])), &saved)
		}
	}
	if readErr != nil {
		e.state.Status, e.state.Error = "failed", err.Error()
		return e.state.EvolutionResult, errors.Join(err, readErr)
	}
	saved.ID = e.state.ID
	e.state = saved
	if e.state.Status == "completed" {
		return e.state.EvolutionResult, err
	}
	e.state.Status, e.state.Error = "failed", err.Error()
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		e.state.Status = "paused"
	}
	_, saveErr := e.store.Exec(finishCtx, "fail_evolution", "mq_evolutions", e.log.Fields,
		"UPDATE mq_evolutions SET status=?,state=?,updated=? WHERE id=? AND status!='completed'",
		e.state.Status, jsonText(e.state), now(), e.state.ID)
	return e.state.EvolutionResult, errors.Join(err, saveErr)
}

func (e *evolutionRunner) methods(ctx context.Context) ([]evolutionMethod, error) {
	rows, err := e.store.Query(ctx, "read_population", "mq_methods", e.log.Fields,
		"SELECT id,parent_id,donor_id,operator,target,config FROM mq_methods WHERE evolution_id=? ORDER BY id", e.state.ID)
	if err != nil {
		return nil, err
	}
	methods := make([]evolutionMethod, 0, len(rows))
	for _, row := range rows {
		m := evolutionMethod{ID: id(row["id"]), ParentID: id(row["parent_id"]), DonorID: id(row["donor_id"]), Operator: str(row["operator"]), Target: str(row["target"])}
		if err = json.Unmarshal([]byte(str(row["config"])), &m.Config); err != nil {
			return nil, err
		}
		methods = append(methods, m)
	}
	return methods, nil
}

func methodByID(methods []evolutionMethod, wanted int64) (evolutionMethod, error) {
	for _, m := range methods {
		if m.ID == wanted {
			return m, nil
		}
	}
	return evolutionMethod{}, fmt.Errorf("method %d not in this campaign", wanted)
}

func (e *evolutionRunner) input(ctx context.Context, methods []evolutionMethod) (map[string]any, error) {
	population := make([]map[string]any, 0, len(methods))
	for _, m := range methods {
		instructions := map[string]string{}
		for _, target := range e.targets() {
			value, err := metaTargetInstruction(m.Config, e.config.MetaQueen.Chromosome, target)
			if err != nil {
				return nil, err
			}
			instructions[target] = value
		}
		population = append(population, map[string]any{"id": m.ID, "parent_id": m.ParentID, "donor_id": m.DonorID, "operator": m.Operator, "target": m.Target, "instructions": instructions, "chromosomes": m.Config.Chromosomes})
	}
	rows, err := e.store.Query(ctx, "read_trial_evidence", "mq_trials", e.log.Fields,
		"SELECT id,method_id,status,evidence FROM mq_trials WHERE evolution_id=? ORDER BY id", e.state.ID)
	if err != nil {
		return nil, err
	}
	trials := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		var evidence metaEvidence
		if err = json.Unmarshal([]byte(str(row["evidence"])), &evidence); err != nil {
			return nil, err
		}
		trial := map[string]any{"id": id(row["id"]), "method_id": id(row["method_id"]), "status": str(row["status"]), "evidence": evidence}
		trials = append(trials, trial)
	}
	input := map[string]any{"meta_queen": metaRuntimeOptions(e.config), "task_contract": e.state.Contract,
		"reference_method_id": e.state.ReferenceID, "methods": population, "trials": trials,
		"parent_id": e.state.ParentID, "donor_id": e.state.DonorID, "method_id": e.state.MethodID,
		"trial_id": e.state.TrialID, "action": e.state.Action, "target": e.state.Target}
	if err := e.inquiry.attach(ctx, input); err != nil {
		return nil, err
	}
	return input, nil
}

func (e *evolutionRunner) choose(ctx context.Context, key string, input map[string]any, options []option) (string, error) {
	if len(options) == 0 {
		return "", fmt.Errorf("%s has no available choices", key)
	}
	if len(options) == 1 {
		e.log.Log("meta_queen", "sole_option", map[string]any{"prompt_key": key, "choice": options[0].value, "outcome": "selected"})
		return options[0].value, nil
	}
	prompt, err := renderPrompt(e.config.Prompts[key], map[string]any{"MetaQueen": metaRuntimeOptions(e.config), "Contract": e.inquiry.Contract, "Context": e.inquiry.Contract.Context})
	if err != nil {
		return "", err
	}
	choices := make([]DecisionChoice, 0, len(options))
	for _, o := range options {
		choices = append(choices, DecisionChoice{Value: o.value, Description: ptr(o.description)})
	}
	decisions := NewMetaDecisions(e.client, e.config.Decisions, e.log)
	decisions.Scope = "meta_queen"
	reply, err := decisions.Decide(ctx, DecisionRequest{Model: e.config.Models.Queen, Input: raw(jsonText(input)),
		Questions: []DecisionQuestion{{Type: "choice", Name: ptr(key), Instructions: raw(prompt), Choices: choices}}})
	if err != nil {
		return "", err
	}
	if len(reply.Answers) != 1 || reply.Answers[0].Type != "choice" {
		return "", fmt.Errorf("%s did not return a choice", key)
	}
	value, ok := reply.Answers[0].Choice.(string)
	if ok {
		for _, o := range options {
			if value == o.value {
				return value, nil
			}
		}
	}
	return "", fmt.Errorf("%s returned an unavailable choice", key)
}

func (e *evolutionRunner) run(ctx context.Context, client TextClient) (EvolutionResult, error) {
	e.client = client
	if concrete, ok := client.(*MetaClient); ok {
		copy := *concrete
		copy.Config, copy.Log = e.config.Meta, e.log
		e.client = &copy
	} else if client == nil {
		var err error
		e.client, err = NewMetaClient(e.config.Meta, e.log)
		if err != nil {
			return e.failed(ctx, err)
		}
	}
	var err error
	e.inquiry, err = openMetaInquiry(ctx, e.config, e.store, e.client, e.log, "evolution", e.state.ID, e.state.MetaContract)
	if err != nil {
		return e.failed(ctx, err)
	}
	e.log.Fields["inquiry_id"] = e.inquiry.ID
	for {
		if err := ctx.Err(); err != nil {
			return e.failed(ctx, err)
		}
		e.log.Fields["method_id"], e.log.Fields["trial_id"] = e.state.MethodID, e.state.TrialID
		e.log.Log("meta_queen", "evolution_step", map[string]any{"phase": e.state.Phase, "outcome": "started"})
		if err := e.step(ctx, client); err != nil {
			return e.failed(ctx, err)
		}
		if e.state.Status == "completed" {
			return e.state.EvolutionResult, nil
		}
	}
}

func (e *evolutionRunner) step(ctx context.Context, hiveClient TextClient) error {
	methods, err := e.methods(ctx)
	if err != nil {
		return err
	}
	input, err := e.input(ctx, methods)
	if err != nil {
		return err
	}
	switch e.state.Phase {
	case "action":
		options := []option{{"MUTATE", "Replace one whole instruction"}, {"RETEST", "Run an existing method again"}, {"RETURN", "Return supported findings, including no demonstrated improvement"}}
		if len(methods) > 1 {
			options = append(options, option{"CROSSOVER", "Transfer one compatible whole instruction from another parent"})
		}
		e.state.Action, err = e.choose(ctx, "mq_action", input, options)
		if err != nil {
			return err
		}
		e.state.ParentID, e.state.DonorID, e.state.MethodID, e.state.TrialID = 0, 0, 0, 0
		e.state.Target, e.state.Instruction = "", ""
		e.state.Phase = "parent"
		if e.state.Action == "RETURN" {
			e.state.Phase = "return"
		}
	case "parent", "donor":
		options := []option{}
		for _, m := range methods {
			if e.state.Phase == "donor" && m.ID == e.state.ParentID {
				continue
			}
			options = append(options, option{strconv.FormatInt(m.ID, 10), fmt.Sprintf("%s method; parent %d, donor %d", m.Operator, m.ParentID, m.DonorID)})
		}
		key := "mq_" + e.state.Phase
		chosen, x := e.choose(ctx, key, input, options)
		if x != nil {
			return x
		}
		selected, x := strconv.ParseInt(chosen, 10, 64)
		if x != nil {
			return x
		}
		if e.state.Phase == "donor" {
			e.state.DonorID, e.state.Phase = selected, "target"
		} else {
			e.state.ParentID, e.state.Phase = selected, "target"
			if e.state.Action == "CROSSOVER" {
				e.state.Phase = "donor"
			} else if e.state.Action == "RETEST" {
				e.state.Phase = "breed"
			}
		}
	case "target":
		options := []option{}
		for _, target := range e.targets() {
			options = append(options, option{target, "The complete " + target + " instruction"})
		}
		e.state.Target, err = e.choose(ctx, "mq_target", input, options)
		if err != nil {
			return err
		}
		e.state.Phase = "mutate"
		if e.state.Action == "CROSSOVER" {
			e.state.Phase = "breed"
		}
	case "mutate":
		parent, x := methodByID(methods, e.state.ParentID)
		if x != nil {
			return x
		}
		old, x := metaTargetInstruction(parent.Config, e.config.MetaQueen.Chromosome, e.state.Target)
		if x != nil {
			return x
		}
		input["old_instruction"] = old
		e.state.Instruction, err = metaGenerate(ctx, e.config, e.client, e.log, "mq_mutate", input, old)
		if err != nil {
			return err
		}
		e.state.Phase = "breed"
	case "breed":
		return e.breed(ctx, methods)
	case "trial":
		m, x := methodByID(methods, e.state.MethodID)
		if x != nil {
			return x
		}
		evidence, _, x := runEvolutionTrial(ctx, m.Config, e.dir, e.state.TrialID, e.state.Contract, hiveClient, e.log)
		if x != nil {
			return x
		}
		return e.atomic(ctx, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
			_, x := exec("trial_returned", "mq_trials", "UPDATE mq_trials SET status='returned',evidence=?,updated=? WHERE id=? AND evolution_id=?",
				jsonText(evidence), now(), e.state.TrialID, e.state.ID)
			e.state.Phase = "assess"
			return x
		})
	case "assess":
		if err := e.inquiry.assess(ctx, fmt.Sprintf("trial:%d", e.state.TrialID), input); err != nil {
			return err
		}
		return e.atomic(ctx, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
			_, x := exec("assess_trial", "mq_trials", "UPDATE mq_trials SET status='completed',updated=? WHERE id=? AND evolution_id=?",
				now(), e.state.TrialID, e.state.ID)
			e.state.Phase = "action"
			return x
		})
	case "return":
		e.state.Result, err = metaGenerate(ctx, e.config, e.client, e.log, "mq_return", input, "")
		if err != nil {
			return err
		}
		return e.atomic(ctx, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
			e.state.Status, e.state.Phase = "completed", "completed"
			_, err := exec("return_meta_inquiry", "mq_inquiries", "UPDATE mq_inquiries SET status='returned' WHERE id=?", e.inquiry.ID)
			return err
		})
	default:
		return fmt.Errorf("unknown evolution phase %q", e.state.Phase)
	}
	return e.save(ctx)
}

func (e *evolutionRunner) breed(ctx context.Context, methods []evolutionMethod) error {
	parent, err := methodByID(methods, e.state.ParentID)
	if err != nil {
		return err
	}
	variant := parent.Config // methods() decoded each configuration independently.
	if e.state.Action == "CROSSOVER" {
		donor, err := methodByID(methods, e.state.DonorID)
		if err != nil {
			return err
		}
		e.state.Instruction, err = metaTargetInstruction(donor.Config, e.config.MetaQueen.Chromosome, e.state.Target)
		if err != nil {
			return err
		}
	}
	if e.state.Action != "RETEST" {
		if strings.TrimSpace(e.state.Instruction) == "" {
			return errors.New("offspring instruction is empty")
		}
		switch e.state.Target {
		case "contribute":
			variant.Chromosomes[e.config.MetaQueen.Chromosome].Contribute = e.state.Instruction
		case "expand":
			variant.Chromosomes[e.config.MetaQueen.Chromosome].Expand = e.state.Instruction
		default:
			variant.Prompts[e.state.Target] = e.state.Instruction
		}
		if err = variant.Validate(); err != nil {
			return err
		}
	}
	return e.atomic(ctx, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
		e.state.MethodID = parent.ID
		if e.state.Action != "RETEST" {
			var donor any
			if e.state.DonorID != 0 {
				donor = e.state.DonorID
			}
			r, err := exec("save_offspring", "mq_methods",
				"INSERT INTO mq_methods(evolution_id,parent_id,donor_id,operator,target,chromosome,config,created) VALUES(?,?,?,?,?,?,?,?)",
				e.state.ID, parent.ID, donor, e.state.Action, e.state.Target, e.config.MetaQueen.Chromosome, jsonText(variant), now())
			if err != nil {
				return err
			}
			e.state.MethodID, err = r.LastInsertId()
			if err != nil {
				return err
			}
		}
		r, err := exec("start_trial", "mq_trials",
			"INSERT INTO mq_trials(evolution_id,method_id,status,created,updated) VALUES(?,?,?,?,?)",
			e.state.ID, e.state.MethodID, "running", now(), now())
		if err != nil {
			return err
		}
		e.state.TrialID, err = r.LastInsertId()
		e.state.Phase = "trial"
		return err
	})
}
