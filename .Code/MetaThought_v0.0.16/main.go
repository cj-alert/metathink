package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

func main() {
	if err := execute(os.Args[1:]); err != nil {
		_ = json.NewEncoder(os.Stderr).Encode(map[string]any{"outcome": "error", "error": err.Error()})
		os.Exit(1)
	}
}

func writeNew(path string, data []byte) error {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(data)
	closeErr := f.Close()
	if e != nil {
		return e
	}
	return closeErr
}

func execute(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: MetaThought <version|init|plan|meta-plan|inquire|run|resume|experiment|evolve|evolve-resume|reassess|decide|eval|status|analyze> -config config.json -dir RUN_FOLDER")
	}
	command := args[0]
	if command == "dashboard" {
		return executeDashboard(args)
	}
	if command == "workers" || command == "work" || command == "worker-recover" || command == "worker-pause" || command == "worker-revise" {
		return executeWorkers(args)
	}
	if command == "version" {
		fmt.Println("MetaThought_v" + Version)
		return nil
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	configPath := f.String("config", "config.json", "initialization configuration")
	dir := f.String("dir", ".", "flat runtime folder")
	contractPath := f.String("contract", "", "reviewed contract file to run")
	handoffPath := f.String("handoff", "", "saved Queen handoff for an early inquiry observation")
	metaContractPath := f.String("meta-contract", "", "reviewed governing meta contract for experiment or evolve")
	questionPath := f.String("question", "", "exact user question file for plan or meta-plan")
	draftPath := f.String("draft", "", "previous Queen proposal file to revise")
	feedbackPath := f.String("feedback", "", "exact user feedback file for a Queen proposal")
	outputPath := f.String("out", "", "new output file for the exact Queen proposal")
	confirm := f.Bool("confirm", false, "confirm the objectives and scoring in the selected contract")
	requestPath := f.String("request", "", "Decisions request or saved reassessment transcript JSON file")
	subject := f.String("subject", "", "saved assessment subject for reassess")
	scriptPath := f.String("script", "", "JavaScript file")
	parentID := f.Int64("parent", 0, "earlier request ID; creates an additional obligation")
	resumeID := f.Int64("request-id", 0, "existing unfinished obligation to resume")
	resultName := f.String("result", "", "new flat result filename; preserves earlier outputs")
	baselineDir := f.String("baseline", "", "saved completed baseline experiment folder")
	baselineRequest := f.Int64("baseline-request", 1, "completed baseline request to compare")
	priorPath := f.String("prior", "", "previous meta-queen assessment to use as feedback")
	if e := f.Parse(args[1:]); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	valid := map[string]bool{"inquire": true, "init": true, "plan": true, "meta-plan": true, "run": true, "resume": true, "experiment": true, "evolve": true, "evolve-resume": true, "reassess": true, "decide": true, "eval": true, "status": true, "analyze": true}
	if !valid[command] {
		return fmt.Errorf("unknown command %q", command)
	}
	if command == "inquire" && (*handoffPath == "" || !*confirm) {
		return errors.New("inquire requires -handoff and -confirm")
	}
	if *handoffPath != "" && command != "inquire" {
		return errors.New("-handoff applies only to inquire")
	}
	planning := command == "plan" || command == "meta-plan"
	if planning && (*questionPath == "" || *outputPath == "") {
		return fmt.Errorf("%s requires -question and -out; drafting does not start drones", command)
	}
	if !planning && (*questionPath != "" || *draftPath != "" || *feedbackPath != "" || *outputPath != "") {
		return errors.New("-question, -draft, -feedback and -out apply only to plan or meta-plan")
	}
	if command == "reassess" && (*requestPath == "" || *subject == "") {
		return errors.New("reassess requires -request saved-transcript.json and -subject")
	}
	if command == "run" && (*contractPath == "" || !*confirm) {
		return errors.New("run requires -contract and -confirm after reviewing the Queen's objectives and scoring")
	}
	metaStart := command == "experiment" || command == "evolve"
	if metaStart && (*metaContractPath == "" || !*confirm) {
		return fmt.Errorf("%s requires -meta-contract and -confirm after reviewing the Meta-Queen's objectives and scoring", command)
	}
	if *confirm && command != "run" && command != "inquire" && !metaStart {
		return errors.New("-confirm applies only to run, inquire, experiment or evolve; drafting and resume do not confirm a new contract")
	}
	if *contractPath != "" && command != "run" {
		return errors.New("-contract applies only to run; resume and meta trials use the saved task contract")
	}
	if *metaContractPath != "" && !metaStart {
		return errors.New("-meta-contract applies only to experiment or evolve; meta-plan drafts separately and evolve-resume uses the saved contract")
	}
	if *baselineDir != "" && !metaStart {
		return errors.New("-baseline applies only to experiment or evolve; resume uses the saved inquiry")
	}
	if *priorPath != "" && command != "experiment" {
		return errors.New("-prior applies only to experiment")
	}
	if metaStart && (*baselineDir == "" || *baselineRequest < 1) {
		return fmt.Errorf("%s requires -baseline and a positive -baseline-request", command)
	}
	selectedConfig := *configPath
	if command == "evolve-resume" {
		selectedConfig = filepath.Join(*dir, "config.json")
	}
	c, err := LoadConfig(selectedConfig)
	if err != nil {
		return err
	}
	if command == "meta-plan" {
		for _, name := range []string{c.Files.State, c.Files.Result, "mq_config.json", "comparison.md"} {
			path := filepath.Join(*dir, name)
			if _, e := os.Stat(path); e == nil {
				return fmt.Errorf("active inquiry artifact already exists at %s; preserve it and choose a new meta draft folder", path)
			} else if !errors.Is(e, os.ErrNotExist) {
				return e
			}
		}
	}
	var reviewedMetaContract string
	if metaStart {
		text, e := os.ReadFile(*metaContractPath)
		if e != nil {
			return fmt.Errorf("read reviewed meta contract: %w", e)
		}
		if _, e = ParseContract(string(text)); e != nil {
			return fmt.Errorf("reviewed meta contract: %w", e)
		}
		reviewedMetaContract = string(text)
	}
	if err = os.MkdirAll(*dir, 0700); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if command == "inquire" {
		handoff, e := os.ReadFile(*handoffPath)
		if e != nil {
			return e
		}
		result, runErr := RunInquiryExperiment(ctx, c, *dir, string(handoff), nil)
		saveErr := writeInquiryResult(*dir, result)
		outputErr := json.NewEncoder(os.Stdout).Encode(result)
		return errors.Join(runErr, saveErr, outputErr)
	}
	if command == "reassess" {
		return ReassessSaved(ctx, c, *dir, *requestPath, *subject)
	}
	if planning {
		question, e := os.ReadFile(*questionPath)
		if e != nil {
			return fmt.Errorf("read question: %w", e)
		}
		var priorDraft, feedback []byte
		if *draftPath != "" {
			priorDraft, e = os.ReadFile(*draftPath)
			if e != nil {
				return fmt.Errorf("read previous draft: %w", e)
			}
		}
		if *feedbackPath != "" {
			feedback, e = os.ReadFile(*feedbackPath)
			if e != nil {
				return fmt.Errorf("read feedback: %w", e)
			}
		}
		text, e := PlanQuestion(ctx, c, *dir, *outputPath, string(question), string(priorDraft), string(feedback), nil, command == "meta-plan")
		fmt.Print(text)
		if e != nil {
			return e
		}
		if command == "meta-plan" {
			fmt.Fprintln(os.Stderr, "Meta draft only. Review the saved proposal before experiment or evolve -meta-contract <file> -confirm. The saved task contract remains separate.")
		} else {
			fmt.Fprintln(os.Stderr, "Draft only. Review the saved proposal before run -contract <file> -confirm.")
		}
		return nil
	}
	if command == "evolve" || command == "evolve-resume" {
		var result EvolutionResult
		if command == "evolve" {
			result, err = RunEvolution(ctx, c, *dir, *baselineDir, *baselineRequest, reviewedMetaContract, nil)
		} else {
			result, err = ResumeEvolution(ctx, c, *dir, nil)
		}
		outputErr := json.NewEncoder(os.Stdout).Encode(result)
		if err != nil {
			return err
		}
		return outputErr
	}
	if command == "experiment" {
		var prior []byte
		if *priorPath != "" {
			prior, err = os.ReadFile(*priorPath)
			if err != nil {
				return err
			}
		}
		result, err := RunMetaExperiment(ctx, c, *dir, *baselineDir, *baselineRequest, reviewedMetaContract, string(prior), nil)
		outputErr := json.NewEncoder(os.Stdout).Encode(result)
		if err != nil {
			return err
		}
		return outputErr
	}
	if command == "init" {
		savedPath := filepath.Join(*dir, "config.json")
		a, _ := filepath.Abs(*configPath)
		b, _ := filepath.Abs(savedPath)
		if a != b {
			data, e := os.ReadFile(*configPath)
			if e != nil {
				return e
			}
			if e = writeNew(savedPath, data); e != nil {
				return fmt.Errorf("preserving existing run config: %w", e)
			}
		}
		logger, e := OpenLogger(filepath.Join(*dir, c.Files.Debug), c.Schema.Logs)
		if e != nil {
			return e
		}
		defer logger.Close()
		store, e := OpenStore(filepath.Join(*dir, c.Files.State), c.Schema.State, logger)
		if e != nil {
			return e
		}
		defer store.Close()
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"version": Version, "outcome": "initialized", "directory": *dir})
	}
	if command == "eval" {
		if *scriptPath == "" {
			return errors.New("eval requires -script")
		}
		source, e := os.ReadFile(*scriptPath)
		if e != nil {
			return e
		}
		logger, e := OpenLogger(filepath.Join(*dir, c.Files.Debug), c.Schema.Logs)
		if e != nil {
			return e
		}
		defer logger.Close()
		value, e := EvalJS(ctx, string(source))
		fields := map[string]any{"source": string(source), "result": value, "outcome": "ok"}
		if e != nil {
			fields["outcome"] = "error"
			fields["error"] = e.Error()
		}
		logger.Log("developer", "javascript", fields)
		if e != nil {
			return e
		}
		fmt.Println(value)
		return nil
	}
	if command == "status" || command == "analyze" {
		return inspect(ctx, c, *dir, command)
	}
	h, err := OpenHive(c, *dir, nil)
	if err != nil {
		return err
	}
	defer h.Close()
	switch command {
	case "run", "resume":
		if command == "resume" && *resumeID < 1 {
			return errors.New("resume requires -request-id")
		}
		name := c.Files.Result
		if command == "resume" {
			name = fmt.Sprintf("resume-%d-%s-%s", *resumeID, time.Now().UTC().Format("20060102T150405.000000000"), c.Files.Result)
		}
		if *resultName != "" {
			name = *resultName
		}
		if filepath.Base(name) != name || name == "." {
			return errors.New("result must be a flat filename")
		}
		resultPath := filepath.Join(*dir, name)
		if _, e := os.Stat(resultPath); e == nil {
			return errors.New("result file exists; preserve it and configure a new result filename before another request")
		}
		var result HiveResult
		var e error
		if command == "resume" {
			result, e = h.Resume(ctx, *resumeID)
		} else {
			var contract []byte
			contract, e = os.ReadFile(*contractPath)
			if e == nil {
				result, e = h.Run(ctx, string(contract), *parentID)
			}
		}
		if e != nil {
			return e
		}
		body := fmt.Sprintf("# MetaThought v%s result\n\nRequest: %d\nStatus: %s\nTeam: %d\nSelected candidates: %v\nTicks this attempt: %d\nCalls: %d\nReturn reason: %s\nCriteria status: %s\nCriteria met: %t\n\n## Queen rationale\n\n%s\n\n## Answer\n\n%s\n", Version, result.RequestID, result.Status, result.TeamID, result.SelectedCandidateIDs, result.Ticks, result.Calls, result.Reason, result.CriteriaStatus, result.CriteriaMet, result.Rationale, result.Answer)
		if e = writeNew(resultPath, []byte(body)); e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	case "decide":
		if *requestPath == "" {
			return errors.New("decide requires -request")
		}
		data, e := os.ReadFile(*requestPath)
		if e != nil {
			return e
		}
		var request DecisionRequest
		if e = json.Unmarshal(data, &request); e != nil {
			return e
		}
		answer, e := h.Decisions.Decide(ctx, request)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(answer)
	}
	return nil
}

func inspect(ctx context.Context, c Config, dir, command string) error {
	logger, e := OpenLogger(filepath.Join(dir, c.Files.Debug), c.Schema.Logs)
	if e != nil {
		return e
	}
	defer logger.Close()
	path := filepath.Join(dir, c.Files.State)
	queries := map[string]string{
		"requests":         "SELECT id,parent_id,team_id,status,finish_reason,criteria_met FROM hive_requests ORDER BY id",
		"teams":            "SELECT t.id,t.summary,COUNT(m.drone_id) AS members FROM hive_teams t LEFT JOIN hive_members m ON t.id=m.team_id GROUP BY t.id",
		"runs":             "SELECT id,status,calls,started,finished FROM hive_runs ORDER BY id",
		"meta_experiments": "SELECT id,problem_type,status,created FROM mq_experiments ORDER BY id",
		"meta_inquiries":   "SELECT id,kind,owner_id,contract,status,created FROM mq_inquiries ORDER BY id",
		"meta_commitments": "SELECT inquiry_id,drone_id,pair,status,created FROM mq_commitments ORDER BY inquiry_id,drone_id",
		"meta_assessments": "SELECT id,inquiry_id,subject_key,drone_id,criterion,score,evidence,uncertainty,created FROM mq_assessments ORDER BY id",
		"evolutions":       "SELECT id,problem_type,status,created,updated FROM mq_evolutions ORDER BY id",
		"methods":          "SELECT id,evolution_id,parent_id,donor_id,operator,target,chromosome,created FROM mq_methods ORDER BY id",
		"trials":           "SELECT id,evolution_id,method_id,status,created,updated FROM mq_trials ORDER BY id",
	}
	if command == "analyze" {
		path = filepath.Join(dir, c.Files.Debug)
		queries = map[string]string{"events": "SELECT event,outcome,COUNT(*) AS count FROM hive_log GROUP BY event,outcome ORDER BY event,outcome", "decisions": "SELECT decision_type,model,outcome,COUNT(*) AS count FROM hive_log WHERE decision_type IS NOT NULL GROUP BY decision_type,model,outcome", "sql": "SELECT table_name,sql_op,outcome,COUNT(*) AS count,SUM(rows_count) AS rows,SUM(duration_ms) AS duration_ms FROM hive_log WHERE sql_op IS NOT NULL GROUP BY table_name,sql_op,outcome", "meta_queen_events": "SELECT event,outcome,COUNT(*) AS count FROM mq_log GROUP BY event,outcome", "latest_request": "SELECT ts,model,json_extract(details,'$.request') AS body FROM hive_log WHERE event='llm.start' ORDER BY id DESC LIMIT 1", "api_rejections": "SELECT ts,model,json_extract(details,'$.status_code') AS http_status,json_extract(details,'$.body') AS response FROM hive_log WHERE event='llm.response' AND json_extract(details,'$.status_code')>=400 ORDER BY id DESC LIMIT 100"}
	}
	store, e := OpenStore(path, nil, logger)
	if e != nil {
		return e
	}
	defer store.Close()
	out := map[string]any{}
	for name, q := range queries {
		rows, e := store.Query(ctx, "inspect", name, map[string]any{"scope": "developer"}, q)
		if e != nil {
			return e
		}
		out[name] = rows
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}
