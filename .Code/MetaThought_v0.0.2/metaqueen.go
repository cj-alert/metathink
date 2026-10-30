package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// MetaQueenConfig describes this comparison, independently of the base-hive
// objective and its criteria. Chromosome is a zero-based pair index.
type MetaQueenConfig struct {
	Objective   string      `json:"objective"`
	ProblemType string      `json:"problem_type"`
	Chromosome  int         `json:"chromosome"`
	Behavior    string      `json:"behavior"`
	Criteria    []Criterion `json:"criteria"`
}

type MetaExperimentResult struct {
	ExperimentID       int64      `json:"experiment_id"`
	Status             string     `json:"status"`
	BaselineDirectory  string     `json:"baseline_directory"`
	BaselineRequestID  int64      `json:"baseline_request_id"`
	CandidateDirectory string     `json:"candidate_directory"`
	Chromosome         int        `json:"chromosome"`
	Behavior           string     `json:"behavior"`
	OldInstruction     string     `json:"old_instruction"`
	NewInstruction     string     `json:"new_instruction"`
	VariantConfig      string     `json:"variant_config"`
	Baseline           HiveResult `json:"baseline"`
	Candidate          HiveResult `json:"candidate"`
	Comparison         string     `json:"comparison"`
	Error              string     `json:"error,omitempty"`
}

// Only deliberate operational records enter the meta-queen's evidence. Neither
// diagnostic tables nor diagnostic files are opened by this reader.
type metaEvidence struct {
	Request       map[string]any   `json:"request"`
	Runs          []map[string]any `json:"-"`
	Drones        []map[string]any `json:"drones"`
	Commitments   []map[string]any `json:"commitments"`
	Contributions []map[string]any `json:"contributions"`
	Links         []map[string]any `json:"links"`
	Scores        []map[string]any `json:"scores"`
	Guidance      []map[string]any `json:"guidance"`
}

type metaQueenLog struct {
	Sink   EventSink
	Fields map[string]any
}

func (m *metaQueenLog) Log(scope, event string, fields map[string]any) {
	data := cloneLogFields(m.Fields)
	for key, value := range fields {
		data[key] = value
	}
	data["source_scope"] = scope
	m.Sink.Log("meta_queen", event, data)
}

// RunMetaExperiment performs one proposal, one fresh candidate hive, and one
// comparison. A previous assessment is supplied as text in prior. Each call
// needs fresh output artifacts; failed or completed work is never replayed.
func RunMetaExperiment(ctx context.Context, c Config, dir, baselineDir string, baselineRequest int64, prior string, client TextClient) (result MetaExperimentResult, err error) {
	result = MetaExperimentResult{
		Status: "running", BaselineDirectory: baselineDir, BaselineRequestID: baselineRequest,
		CandidateDirectory: dir, Chromosome: c.MetaQueen.Chromosome, Behavior: c.MetaQueen.Behavior,
		VariantConfig: filepath.Join(dir, "config.json"),
	}
	if baselineRequest <= 0 {
		return result, errors.New("experiment requires a baseline request ID")
	}
	if strings.TrimSpace(c.MetaQueen.Objective) == "" || strings.TrimSpace(c.MetaQueen.ProblemType) == "" || len(c.MetaQueen.Criteria) == 0 {
		return result, errors.New("meta-queen objective, problem_type, and comparative criteria are required")
	}
	if c.MetaQueen.Behavior != "contribute" && c.MetaQueen.Behavior != "expand" {
		return result, errors.New("meta-queen behavior must be contribute or expand")
	}
	for _, key := range []string{"mq_propose", "mq_compare"} {
		if strings.TrimSpace(c.Prompts[key]) == "" {
			return result, fmt.Errorf("missing prompt %s", key)
		}
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return result, err
	}
	// Exclusive artifacts protect existing work and prevent accidentally using
	// an old hive as the candidate. The launcher may already have copied the exe.
	for _, name := range []string{"mq_config.json", "config.json", "comparison.md", c.Files.State, c.Files.Debug, c.Files.Contract, c.Files.Result} {
		if _, statErr := os.Stat(filepath.Join(dir, name)); statErr == nil {
			return result, fmt.Errorf("experiment output already exists: %s; select a fresh directory", name)
		} else if !os.IsNotExist(statErr) {
			return result, statErr
		}
	}
	controller, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return result, err
	}
	if err = writeNew(filepath.Join(dir, "mq_config.json"), controller); err != nil {
		return result, err
	}
	logger, err := OpenLogger(filepath.Join(dir, c.Files.Debug), c.Schema.Logs)
	if err != nil {
		return result, err
	}
	defer logger.Close()
	log := &metaQueenLog{Sink: logger, Fields: map[string]any{"baseline_directory": baselineDir, "baseline_request_id": baselineRequest}}
	store, err := OpenStore(filepath.Join(dir, c.Files.State), c.Schema.State, log)
	if err != nil {
		return result, err
	}
	defer store.Close()
	descriptor := map[string]any{"chromosome": c.MetaQueen.Chromosome, "behavior": c.MetaQueen.Behavior, "baseline_directory": baselineDir, "baseline_request_id": baselineRequest}
	row, err := store.Exec(ctx, "start_experiment", "mq_experiments", nil,
		"INSERT INTO mq_experiments(problem_type,candidate,status,result,created) VALUES(?,?,?,?,?)",
		c.MetaQueen.ProblemType, jsonText(descriptor), "running", jsonText(result), now())
	if err != nil {
		return result, err
	}
	result.ExperimentID, err = row.LastInsertId()
	if err != nil {
		return result, err
	}
	log.Fields["experiment_id"], log.Fields["run_id"] = result.ExperimentID, result.ExperimentID
	persist := func(persistCtx context.Context, stage string) error {
		log.Log("meta_queen", "experiment_stage", map[string]any{"stage": stage, "outcome": result.Status})
		_, saveErr := store.Exec(persistCtx, "save_experiment", "mq_experiments", log.Fields,
			"UPDATE mq_experiments SET candidate=?,status=?,result=? WHERE id=?",
			jsonText(descriptor), result.Status, jsonText(result), result.ExperimentID)
		return saveErr
	}
	defer func() {
		if err != nil {
			result.Status, result.Error = "failed", err.Error()
		} else {
			result.Status = "completed"
		}
		if saveErr := persist(context.WithoutCancel(ctx), "finished"); saveErr != nil {
			err = errors.Join(err, saveErr)
			result.Status, result.Error = "failed", err.Error()
		}
	}()
	baselinePath, err := filepath.Abs(filepath.Join(baselineDir, c.Files.State))
	if err != nil {
		return result, err
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(baselinePath)}
	if !strings.HasPrefix(uri.Path, "/") {
		uri.Path = "/" + uri.Path
	}
	uri.RawQuery = "mode=ro"
	baseline, err := OpenStore(uri.String(), nil, log)
	if err != nil {
		return result, err
	}
	defer baseline.Close()
	baseRows, err := baseline.Query(ctx, "load_baseline_config", "hive_runs", nil,
		"SELECT r.config FROM hive_runs r JOIN hive_requests q ON q.id=r.request_id WHERE q.id=? AND q.status='returned' AND r.status='completed' ORDER BY r.id DESC LIMIT 1", baselineRequest)
	if err != nil {
		return result, err
	}
	if len(baseRows) == 0 {
		return result, errors.New("baseline request must have a completed run and returned result")
	}
	// Decode the saved configuration directly: its historical version is valid
	// baseline data, not an instruction to rerun the old executable.
	var variant Config
	if err = json.Unmarshal([]byte(str(baseRows[0]["config"])), &variant); err != nil {
		return result, fmt.Errorf("saved baseline configuration: %w", err)
	}
	if c.MetaQueen.Chromosome < 0 || c.MetaQueen.Chromosome >= len(variant.Chromosomes) {
		return result, errors.New("selected chromosome does not exist in the baseline")
	}
	variant.Version, variant.Schema = Version, c.Schema
	// Resolve current credentials without changing the candidate's saved
	// provider, models, reasoning settings, or other behavioral controls.
	variant.Meta.APIKeyEnv = c.Meta.APIKeyEnv
	variant.Meta.APIKeyFile = c.Meta.APIKeyFile
	variant.Meta.APIKeyFileFormat = c.Meta.APIKeyFileFormat
	variant.MetaQueen = c.MetaQueen
	// These newly added controller prompts do not alter any base-hive prompt.
	if variant.Prompts == nil {
		variant.Prompts = map[string]string{}
	}
	variant.Prompts["mq_propose"], variant.Prompts["mq_compare"] = c.Prompts["mq_propose"], c.Prompts["mq_compare"]
	selected := &variant.Chromosomes[c.MetaQueen.Chromosome]
	result.OldInstruction = selected.Contribute
	if c.MetaQueen.Behavior == "expand" {
		result.OldInstruction = selected.Expand
	}
	baseEvidence, err := loadMetaEvidence(ctx, baseline, baselineRequest)
	if err != nil {
		return result, err
	}
	result.Baseline = metaHiveResult(baseEvidence)
	contract := str(baseEvidence.Request["contract"])
	metaClient := client
	if concrete, ok := client.(*MetaClient); ok {
		copy := *concrete
		copy.Config = c.Meta
		copy.Log = log
		metaClient = &copy
	} else if client == nil {
		metaClient, err = NewMetaClient(c.Meta, log)
		if err != nil {
			return result, err
		}
	}
	inputs := map[string]any{
		"meta_queen": c.MetaQueen, "baseline": baseEvidence, "old_instruction": result.OldInstruction,
		"chromosome": c.MetaQueen.Chromosome, "behavior": c.MetaQueen.Behavior, "prior_assessment": prior,
	}
	proposal, err := metaGenerate(ctx, c, metaClient, log, "mq_propose", inputs, result.OldInstruction)
	result.NewInstruction = proposal
	if err != nil {
		return result, err
	}
	if c.MetaQueen.Behavior == "contribute" {
		selected.Contribute = proposal
	} else {
		selected.Expand = proposal
	}
	descriptor["old_instruction"], descriptor["new_instruction"] = result.OldInstruction, result.NewInstruction
	if err = persist(ctx, "proposed"); err != nil {
		return result, err
	}
	if err = variant.Validate(); err != nil {
		return result, fmt.Errorf("variant configuration: %w", err)
	}
	encodedVariant, err := json.MarshalIndent(variant, "", "  ")
	if err != nil {
		return result, err
	}
	if err = writeNew(result.VariantConfig, encodedVariant); err != nil {
		return result, err
	}
	if err = writeNew(filepath.Join(dir, variant.Files.Contract), []byte(contract)); err != nil {
		return result, err
	}
	hive, err := OpenHive(variant, dir, client)
	if err != nil {
		return result, err
	}
	if concrete, ok := client.(*MetaClient); ok {
		copy := *concrete
		copy.Config = variant.Meta
		copy.Log = hive.Log
		hive.Text.Inner = &copy
	}
	result.Candidate, err = hive.Run(ctx, contract, 0)
	var candidateEvidence metaEvidence
	if err == nil {
		// Reuse the candidate's connection with meta-queen SQL logging. This
		// remains correct if its saved runtime filenames differ from c.Files.
		reader := &Store{db: hive.Store.db, log: log}
		candidateEvidence, err = loadMetaEvidence(ctx, reader, result.Candidate.RequestID)
	}
	hive.Close()
	// Preserve even a paused candidate, before deciding whether comparison can
	// proceed. A later command does not replay this experiment directory.
	encodedCandidate, encodeErr := json.MarshalIndent(result.Candidate, "", "  ")
	if encodeErr != nil {
		err = errors.Join(err, encodeErr)
	} else {
		err = errors.Join(err, writeNew(filepath.Join(dir, variant.Files.Result), encodedCandidate))
	}
	if err != nil {
		return result, err
	}
	if err = persist(ctx, "candidate_returned"); err != nil {
		return result, err
	}
	inputs["candidate"], inputs["new_instruction"] = candidateEvidence, result.NewInstruction
	result.Comparison, err = metaGenerate(ctx, c, metaClient, log, "mq_compare", inputs, result.OldInstruction)
	if err != nil {
		return result, err
	}
	if err = writeNew(filepath.Join(dir, "comparison.md"), []byte(result.Comparison)); err != nil {
		return result, err
	}
	return result, nil
}

func metaGenerate(ctx context.Context, c Config, client TextClient, log EventSink, key string, input map[string]any, oldInstruction string) (string, error) {
	prompt, err := renderPrompt(c.Prompts[key], map[string]any{
		"MetaQueen": c.MetaQueen, "Chromosome": c.MetaQueen.Chromosome,
		"Behavior": c.MetaQueen.Behavior, "OldInstruction": oldInstruction,
	})
	if err != nil {
		return "", err
	}
	log.Log("meta_queen", "meta_call_start", map[string]any{"prompt_key": key, "model": c.Models.Queen, "outcome": "started"})
	reply, err := client.Complete(ctx, TextRequest{Model: c.Models.Queen, Scope: "meta_queen", Messages: []Message{
		{"role": "developer", "content": prompt}, {"role": "user", "content": jsonText(input)},
	}})
	if err == nil && reply.Refusal != "" {
		err = fmt.Errorf("%s refused: %s", key, reply.Refusal)
	}
	if err == nil && strings.TrimSpace(reply.Text) == "" {
		err = fmt.Errorf("%s returned no text", key)
	}
	fields := map[string]any{"prompt_key": key, "model": reply.Model, "outcome": "answered", "reply": reply.Text, "usage": reply.Usage}
	if err != nil {
		fields["outcome"], fields["error"] = "error", err.Error()
	}
	log.Log("meta_queen", "meta_call_done", fields)
	return reply.Text, err
}

func loadMetaEvidence(ctx context.Context, store *Store, requestID int64) (evidence metaEvidence, err error) {
	fields := map[string]any{"request_id": requestID, "scope": "meta_queen"}
	rows, err := store.Query(ctx, "read_operational_request", "hive_requests", fields, "SELECT * FROM hive_requests WHERE id=?", requestID)
	if err != nil {
		return evidence, err
	}
	if len(rows) != 1 {
		return evidence, fmt.Errorf("request %d not found", requestID)
	}
	evidence.Request = rows[0]
	queries := []struct {
		table string
		sql   string
		dest  *[]map[string]any
	}{
		{"hive_runs", "SELECT id,started,finished,status,version,calls,request_id FROM hive_runs WHERE request_id=? ORDER BY id", &evidence.Runs},
		{"hive_drones", "SELECT d.* FROM hive_drones d WHERE d.id IN (SELECT drone_id FROM hive_commitments WHERE request_id=? UNION SELECT drone_id FROM hive_contributions WHERE request_id=? UNION SELECT from_drone FROM hive_links WHERE request_id=? UNION SELECT to_drone FROM hive_links WHERE request_id=?) ORDER BY d.id", &evidence.Drones},
		{"hive_commitments", "SELECT * FROM hive_commitments WHERE request_id=? ORDER BY drone_id", &evidence.Commitments},
		{"hive_contributions", "SELECT * FROM hive_contributions WHERE request_id=? ORDER BY id", &evidence.Contributions},
		{"hive_links", "SELECT * FROM hive_links WHERE request_id=? ORDER BY id", &evidence.Links},
		{"hive_scores", "SELECT * FROM hive_scores WHERE request_id=? ORDER BY id", &evidence.Scores},
		{"hive_guidance", "SELECT * FROM hive_guidance WHERE request_id=? ORDER BY id", &evidence.Guidance},
	}
	for _, query := range queries {
		args := []any{requestID}
		if query.table == "hive_drones" {
			args = []any{requestID, requestID, requestID, requestID}
		}
		*query.dest, err = store.Query(ctx, "read_operational_evidence", query.table, fields, query.sql, args...)
		if err != nil {
			return evidence, err
		}
	}
	return evidence, nil
}

func metaHiveResult(e metaEvidence) HiveResult {
	r := HiveResult{Status: str(e.Request["status"]), RequestID: id(e.Request["id"]), TeamID: id(e.Request["team_id"]),
		CriteriaMet: id(e.Request["criteria_met"]) != 0, Reason: str(e.Request["finish_reason"]), Answer: str(e.Request["result"])}
	for _, row := range e.Runs {
		r.Calls += int(id(row["calls"]))
	}
	for _, row := range e.Contributions {
		if tick := int(id(row["tick"])); tick > r.Ticks {
			r.Ticks = tick
		}
	}
	return r
}
