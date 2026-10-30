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
	"strings"
)

// MetaQueenConfig describes this comparison, independently of the base-hive
// objective and its criteria. Chromosome is a zero-based pair index.
type MetaQueenConfig struct {
	Objective   string      `json:"objective"`
	Intent      string      `json:"intent,omitempty"`
	ProblemType string      `json:"problem_type"`
	Context     string      `json:"context,omitempty"`
	Chromosome  int         `json:"chromosome"`
	Behavior    string      `json:"behavior"`
	Targets     []string    `json:"targets,omitempty"`
	Criteria    []Criterion `json:"criteria"`
}

type MetaExperimentResult struct {
	ExperimentID       int64      `json:"experiment_id"`
	MetaInquiryID      int64      `json:"meta_inquiry_id"`
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
	Request        map[string]any   `json:"request"`
	Runs           []map[string]any `json:"-"`
	Drones         []map[string]any `json:"drones"`
	Commitments    []map[string]any `json:"commitments"`
	Contributions  []map[string]any `json:"contributions"`
	Links          []map[string]any `json:"links"`
	ReceivedOffers []map[string]any `json:"received_offers"`
	Scores         []map[string]any `json:"scores"`
	Assessments    []map[string]any `json:"drone_assessments,omitempty"`
	Reviews        []map[string]any `json:"queen_reviews,omitempty"`
	Guidance       []map[string]any `json:"guidance"`
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

// RunMetaExperiment optionally selects one configured target, then performs one
// proposal, one fresh candidate hive, and one comparison. A previous assessment
// is supplied as text in prior. Failed/completed artifacts are never replayed.
func RunMetaExperiment(ctx context.Context, c Config, dir, baselineDir string, baselineRequest int64, metaContract, prior string, client TextClient) (result MetaExperimentResult, err error) {
	result = MetaExperimentResult{
		Status: "running", BaselineDirectory: baselineDir, BaselineRequestID: baselineRequest,
		CandidateDirectory: dir, Chromosome: c.MetaQueen.Chromosome, Behavior: c.MetaQueen.Behavior,
		VariantConfig: filepath.Join(dir, "config.json"),
	}
	if baselineRequest <= 0 {
		return result, errors.New("experiment requires a baseline request ID")
	}
	if _, err = ParseContract(metaContract); err != nil {
		return result, fmt.Errorf("confirmed meta contract: %w", err)
	}
	if strings.TrimSpace(c.MetaQueen.ProblemType) == "" {
		return result, errors.New("meta-queen problem_type is required")
	}
	if len(c.MetaQueen.Targets) > 1 && strings.TrimSpace(c.Prompts["mq_select"]) == "" {
		return result, errors.New("missing prompt mq_select")
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
	for _, name := range []string{"mq_config.json", "config.json", "comparison.md", c.Files.State, c.Files.Contract, c.Files.Result} {
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
		return store.Transaction(persistCtx, log.Fields, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
			if _, saveErr := exec("save_experiment", "mq_experiments", "UPDATE mq_experiments SET candidate=?,status=?,result=? WHERE id=?",
				jsonText(descriptor), result.Status, jsonText(result), result.ExperimentID); saveErr != nil {
				return saveErr
			}
			if result.Status == "completed" && result.MetaInquiryID != 0 {
				_, saveErr := exec("return_meta_inquiry", "mq_inquiries", "UPDATE mq_inquiries SET status='returned' WHERE id=?", result.MetaInquiryID)
				return saveErr
			}
			return nil
		})
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
	targets := c.MetaQueen.Targets
	if len(targets) == 0 {
		targets = []string{c.MetaQueen.Behavior}
	}
	if len(targets) == 1 {
		result.Behavior = targets[0]
	}
	oldInstructions := make(map[string]string, len(targets))
	targetEvidence := make([]map[string]string, 0, len(targets))
	for _, target := range targets {
		if _, exists := oldInstructions[target]; exists {
			return result, fmt.Errorf("duplicate meta-queen target %q", target)
		}
		instruction, targetErr := metaTargetInstruction(variant, c.MetaQueen.Chromosome, target)
		if targetErr != nil {
			return result, targetErr
		}
		oldInstructions[target] = instruction
		targetEvidence = append(targetEvidence, map[string]string{"target": target, "old_instruction": instruction})
	}
	variant.Version, variant.Schema = Version, c.Schema
	// Resolve current credentials without changing the candidate's saved
	// provider, models, reasoning settings, or other behavioral controls.
	variant.Meta.APIKeyEnv = c.Meta.APIKeyEnv
	variant.Meta.APIKeyFile = c.Meta.APIKeyFile
	variant.Meta.APIKeyFileFormat = c.Meta.APIKeyFileFormat
	variant.MetaQueen = c.MetaQueen
	metaFixedProtocols(&variant, c)
	// These newly added controller prompts do not alter any base-hive prompt.
	if variant.Prompts == nil {
		variant.Prompts = map[string]string{}
	}
	for _, key := range []string{"mq_select", "mq_propose", "mq_compare"} {
		if prompt, present := c.Prompts[key]; present {
			variant.Prompts[key] = prompt
		}
	}
	baseEvidence, err := loadMetaEvidence(ctx, baseline, baselineRequest)
	if err != nil {
		return result, err
	}
	result.Baseline = metaHiveResult(baseEvidence)
	contract := str(baseEvidence.Request["contract"])
	if _, err = ParseContract(contract); err != nil {
		return result, fmt.Errorf("baseline requires a reviewed task contract; no configuration fallback: %w", err)
	}
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
	inquiry, err := openMetaInquiry(ctx, c, store, metaClient, log, "experiment", result.ExperimentID, metaContract)
	if err != nil {
		return result, err
	}
	result.MetaInquiryID = inquiry.ID
	log.Fields["inquiry_id"] = inquiry.ID
	inputs := map[string]any{
		"meta_queen": metaRuntimeOptions(c), "task_contract": contract, "baseline": baseEvidence,
		"chromosome": c.MetaQueen.Chromosome, "behavior": c.MetaQueen.Behavior, "prior_assessment": prior,
	}
	if err = inquiry.assess(ctx, "reference", inputs); err != nil {
		return result, err
	}
	if err = inquiry.attach(ctx, inputs); err != nil {
		return result, err
	}
	if len(c.MetaQueen.Targets) > 1 {
		selectionInput := cloneLogFields(inputs)
		selectionInput["targets"] = targetEvidence
		selectionPrompt, selectErr := renderPrompt(c.Prompts["mq_select"], map[string]any{
			"MetaQueen": metaRuntimeOptions(c), "Contract": inquiry.Contract, "Context": inquiry.Contract.Context,
			"Chromosome": c.MetaQueen.Chromosome, "Behavior": c.MetaQueen.Behavior,
			"Targets": targetEvidence, "OldInstruction": "",
		})
		if selectErr != nil {
			return result, selectErr
		}
		choices := make([]DecisionChoice, 0, len(targets))
		for _, target := range targets {
			choices = append(choices, DecisionChoice{Value: target, Description: ptr(oldInstructions[target])})
		}
		decisions := NewMetaDecisions(metaClient, c.Decisions, log)
		decisions.Scope = "meta_queen"
		selection, selectErr := decisions.Decide(ctx, DecisionRequest{
			Model: c.Models.Queen, ContextKind: "mq_target", Input: raw(jsonText(selectionInput)),
			Questions: []DecisionQuestion{{Type: "choice", Name: ptr("mq_target"), Instructions: raw(selectionPrompt), Choices: choices}},
		})
		if selectErr != nil {
			return result, selectErr
		}
		if len(selection.Answers) != 1 || selection.Answers[0].Type != "choice" {
			return result, errors.New("mq_target did not return a choice")
		}
		selected, valid := selection.Answers[0].Choice.(string)
		if _, exists := oldInstructions[selected]; !valid || !exists {
			return result, errors.New("mq_target returned an unconfigured target")
		}
		result.Behavior = selected
	}
	// c is a local value copy; mq_config.json retains the original controller.
	c.MetaQueen.Behavior = result.Behavior
	variant.MetaQueen.Behavior = result.Behavior
	result.OldInstruction = oldInstructions[result.Behavior]
	descriptor["behavior"] = result.Behavior
	if len(c.MetaQueen.Targets) > 0 {
		if err = persist(ctx, "target_selected"); err != nil {
			return result, err
		}
	}
	inputs["meta_queen"], inputs["behavior"], inputs["old_instruction"] = metaRuntimeOptions(c), c.MetaQueen.Behavior, result.OldInstruction
	proposal, err := metaGenerate(ctx, c, metaClient, log, "mq_propose", inputs, result.OldInstruction)
	result.NewInstruction = proposal
	if err != nil {
		return result, err
	}
	switch result.Behavior {
	case "contribute":
		variant.Chromosomes[c.MetaQueen.Chromosome].Contribute = proposal
	case "expand":
		variant.Chromosomes[c.MetaQueen.Chromosome].Expand = proposal
	default:
		variant.Prompts[result.Behavior] = proposal
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
	if err = inquiry.assess(ctx, "candidate", inputs); err != nil {
		return result, err
	}
	if err = inquiry.attach(ctx, inputs); err != nil {
		return result, err
	}
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
	contract, ok := input["meta_contract"].(Contract)
	if !ok {
		return "", errors.New("meta generation requires the confirmed meta contract")
	}
	if target, ok := input["target"].(string); ok && target != "" {
		c.MetaQueen.Behavior = target
	}
	prompt, err := renderPrompt(c.Prompts[key], map[string]any{
		"MetaQueen": metaRuntimeOptions(c), "Contract": contract, "Context": contract.Context, "Chromosome": c.MetaQueen.Chromosome,
		"Behavior": c.MetaQueen.Behavior, "OldInstruction": oldInstruction,
	})
	if err != nil {
		return "", err
	}
	return metaText(ctx, client, log, c.Models.Queen, key, prompt, input)
}

func metaTargetInstruction(baseline Config, chromosome int, target string) (string, error) {
	if target == "contribute" || target == "expand" {
		if chromosome < 0 || chromosome >= len(baseline.Chromosomes) {
			return "", errors.New("selected chromosome does not exist in the baseline")
		}
		if target == "contribute" {
			return baseline.Chromosomes[chromosome].Contribute, nil
		}
		return baseline.Chromosomes[chromosome].Expand, nil
	}
	if target != "candidate" && target != "collapse" {
		return "", fmt.Errorf("meta-queen target %q is not a supported behavioral instruction; use contribute, expand, candidate or collapse", target)
	}
	instruction, exists := baseline.Prompts[target]
	if !exists || strings.TrimSpace(instruction) == "" {
		return "", fmt.Errorf("meta-queen target %q is not a baseline hive instruction", target)
	}
	return instruction, nil
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
		{"hive_links", "SELECT l.to_drone,l.from_drone,c.id AS contribution_id,c.request_id,c.kind,c.body FROM hive_links l JOIN hive_contributions c ON c.id=l.contribution_id WHERE l.to_drone IN (SELECT drone_id FROM hive_commitments WHERE request_id=?) ORDER BY l.id", &evidence.ReceivedOffers},
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
	// Historical evidence has no drone assessment/review tables. Read them only
	// when present; never manufacture grades from legacy Queen scores.
	available, err := store.Query(ctx, "assessment_tables", "sqlite_master", fields, "SELECT name FROM sqlite_master WHERE type='table' AND name IN ('hive_assessments','hive_reviews')")
	if err != nil {
		return evidence, err
	}
	for _, table := range available {
		name := str(table["name"])
		switch name {
		case "hive_assessments":
			evidence.Assessments, err = store.Query(ctx, "read_drone_assessments", name, fields, "SELECT * FROM hive_assessments WHERE request_id=? ORDER BY id", requestID)
		case "hive_reviews":
			evidence.Reviews, err = store.Query(ctx, "read_queen_reviews", name, fields, "SELECT * FROM hive_reviews WHERE request_id=? ORDER BY id", requestID)
		}
		if err != nil {
			return evidence, err
		}
	}
	return evidence, nil
}

func metaHiveResult(e metaEvidence) HiveResult {
	r := HiveResult{Status: str(e.Request["status"]), RequestID: id(e.Request["id"]), TeamID: id(e.Request["team_id"]),
		CriteriaMet: id(e.Request["criteria_met"]) != 0, Reason: str(e.Request["finish_reason"]), Answer: str(e.Request["result"])}
	for n := len(e.Reviews) - 1; n >= 0; n-- {
		review := e.Reviews[n]
		if r.Status == "returned" && str(review["decision"]) != "RETURN" {
			continue
		}
		r.CriteriaStatus, r.Rationale = str(review["criteria_status"]), str(review["reason"])
		if selected := str(review["selected_candidate_ids"]); selected != "" {
			_ = json.Unmarshal([]byte(selected), &r.SelectedCandidateIDs)
		}
		break
	}
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
