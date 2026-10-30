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
		return errors.New("usage: MetaThought <version|init|plan|run|resume|experiment|evolve|evolve-resume|decide|eval|status|analyze> -config config.json -dir RUN_FOLDER")
	}
	command := args[0]
	if command == "version" {
		fmt.Println("MetaThought_v" + Version)
		return nil
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	configPath := f.String("config", "config.json", "initialization configuration")
	dir := f.String("dir", ".", "flat runtime folder")
	contractPath := f.String("contract", "", "confirmed contract file to run")
	requestPath := f.String("request", "", "Decisions request JSON file")
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
	valid := map[string]bool{"init": true, "plan": true, "run": true, "resume": true, "experiment": true, "evolve": true, "evolve-resume": true, "decide": true, "eval": true, "status": true, "analyze": true}
	if !valid[command] {
		return fmt.Errorf("unknown command %q", command)
	}
	selectedConfig := *configPath
	if command == "evolve-resume" {
		selectedConfig = filepath.Join(*dir, "config.json")
	}
	c, err := LoadConfig(selectedConfig)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(*dir, 0700); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if command == "evolve" || command == "evolve-resume" {
		var result EvolutionResult
		if command == "evolve" {
			if *baselineDir == "" || *baselineRequest < 1 {
				return errors.New("evolve requires -baseline and a positive -baseline-request")
			}
			result, err = RunEvolution(ctx, c, *dir, *baselineDir, *baselineRequest, nil)
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
		if *baselineDir == "" || *baselineRequest < 1 {
			return errors.New("experiment requires -baseline and a positive -baseline-request")
		}
		var prior []byte
		if *priorPath != "" {
			prior, err = os.ReadFile(*priorPath)
			if err != nil {
				return err
			}
		}
		result, err := RunMetaExperiment(ctx, c, *dir, *baselineDir, *baselineRequest, string(prior), nil)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
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
	case "plan":
		path := filepath.Join(*dir, c.Files.Contract)
		if _, e := os.Stat(path); e == nil {
			return fmt.Errorf("contract already exists; preserve it and select another run folder")
		}
		contract, e := h.Plan(ctx)
		if e != nil {
			return e
		}
		if e = writeNew(path, []byte(contract+"\n")); e != nil {
			return e
		}
		fmt.Println(contract)
		return nil
	case "run", "resume":
		if command == "run" && *contractPath == "" {
			return errors.New("run requires -contract: selecting a contract confirms it for this run")
		}
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
		body := fmt.Sprintf("# MetaThought v%s result\n\nRequest: %d\nStatus: %s\nTeam: %d\nTicks this attempt: %d\nCalls: %d\nReturn reason: %s\nCriteria met: %t\n\n%s\n", Version, result.RequestID, result.Status, result.TeamID, result.Ticks, result.Calls, result.Reason, result.CriteriaMet, result.Answer)
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
		"requests":        "SELECT id,parent_id,team_id,status,finish_reason,criteria_met FROM hive_requests ORDER BY id",
		"teams":           "SELECT t.id,t.summary,COUNT(m.drone_id) AS members FROM hive_teams t LEFT JOIN hive_members m ON t.id=m.team_id GROUP BY t.id",
		"runs":            "SELECT id,status,calls,started,finished FROM hive_runs ORDER BY id",
		"meta_experiments": "SELECT id,problem_type,status,created FROM mq_experiments ORDER BY id",
		"evolutions":      "SELECT id,problem_type,status,created,updated FROM mq_evolutions ORDER BY id",
		"methods":         "SELECT id,evolution_id,parent_id,donor_id,operator,target,chromosome,created FROM mq_methods ORDER BY id",
		"trials":          "SELECT id,evolution_id,method_id,status,created,updated FROM mq_trials ORDER BY id",
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
