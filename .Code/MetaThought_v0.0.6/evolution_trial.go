package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// evolutionTrialConfig gives each candidate independent flat runtime files and
// owned prompt/criterion slices. Config is a JSON document throughout the app.
func evolutionTrialConfig(c Config, trialID int64) Config {
	encoded, err := json.Marshal(c)
	if err != nil {
		panic(fmt.Errorf("clone evolution trial configuration: %w", err))
	}
	var trial Config
	if err := json.Unmarshal(encoded, &trial); err != nil {
		panic(fmt.Errorf("clone evolution trial configuration: %w", err))
	}
	prefix := fmt.Sprintf("trial-%d-", trialID)
	trial.Files.State = prefix + "state.db"
	trial.Files.Debug = prefix + "debug.db"
	trial.Files.Contract = prefix + "contract.md"
	trial.Files.Result = prefix + "result.md"
	return trial
}

// runEvolutionTrial is the candidate-hive continuation boundary. SQLite holds
// the actual obligation state; saved files are retained, never compared by
// fingerprints or rewritten to make a resumed request match fresh inputs.
func runEvolutionTrial(ctx context.Context, c Config, dir string, trialID int64, contract string, client TextClient, log EventSink) (metaEvidence, HiveResult, error) {
	var evidence metaEvidence
	var result HiveResult
	if err := os.MkdirAll(dir, 0700); err != nil {
		return evidence, result, err
	}
	trial := evolutionTrialConfig(c, trialID)
	configPath := filepath.Join(dir, fmt.Sprintf("trial-%d-config.json", trialID))
	saved, err := os.ReadFile(configPath)
	if err == nil {
		// Resume with the saved trial's behavioral controls, allowing only the
		// current credential source to follow a moved/rotated installation.
		if err := json.Unmarshal(saved, &trial); err != nil {
			return evidence, result, fmt.Errorf("saved trial configuration: %w", err)
		}
		trial = evolutionTrialConfig(trial, trialID)
		trial.Meta.APIKeyEnv = c.Meta.APIKeyEnv
		trial.Meta.APIKeyFile = c.Meta.APIKeyFile
		trial.Meta.APIKeyFileFormat = c.Meta.APIKeyFileFormat
	} else if os.IsNotExist(err) {
		encoded, err := json.MarshalIndent(trial, "", "  ")
		if err != nil {
			return evidence, result, err
		}
		if err := writeNew(configPath, encoded); err != nil {
			return evidence, result, err
		}
	} else {
		return evidence, result, err
	}
	if err := trial.Validate(); err != nil {
		return evidence, result, err
	}
	hive, err := OpenHive(trial, dir, client)
	if err != nil {
		return evidence, result, err
	}
	defer hive.Close()
	if concrete, ok := client.(*MetaClient); ok {
		copy := *concrete
		copy.Config = trial.Meta
		copy.Log = hive.Log
		hive.Text.Inner = &copy
	}
	if log == nil {
		log = hive.Log
	}
	reader := &Store{db: hive.Store.db, log: log}
	fields := map[string]any{"trial_id": trialID, "scope": "meta_queen"}
	requests, err := reader.Query(ctx, "read_trial_obligation", "hive_requests", fields,
		"SELECT id,status,contract FROM hive_requests ORDER BY id")
	if err != nil {
		return evidence, result, err
	}
	if len(requests) > 1 {
		return evidence, result, errors.New("trial database contains multiple obligations; select the owning trial")
	}
	var requestID int64
	if len(requests) == 1 {
		requestID = id(requests[0]["id"])
		contract = str(requests[0]["contract"])
	} else {
		// An interruption can occur after the contract was saved but before
		// SQLite received its first request. That saved intent still owns it.
		body, readErr := os.ReadFile(filepath.Join(dir, trial.Files.Contract))
		if readErr == nil {
			contract = string(body)
		} else if !os.IsNotExist(readErr) {
			return evidence, result, readErr
		}
	}
	if err := preserveTrialFile(filepath.Join(dir, trial.Files.Contract), []byte(contract)); err != nil {
		return evidence, result, err
	}
	var runErr error
	switch {
	case len(requests) == 0:
		log.Log("meta_queen", "evolution_trial_started", fields)
		result, runErr = hive.Run(ctx, contract, 0)
		requestID = result.RequestID
	case str(requests[0]["status"]) == "returned":
		fields["request_id"] = requestID
		log.Log("meta_queen", "evolution_trial_reused", fields)
	default:
		fields["request_id"] = requestID
		log.Log("meta_queen", "evolution_trial_resumed", fields)
		// Resume reuses this obligation even when no team was assigned before
		// interruption; the existing hive handles routing and commitments.
		result, runErr = hive.Resume(ctx, requestID)
	}
	if requestID == 0 {
		return evidence, result, runErr
	}
	readContext := ctx
	if runErr != nil {
		readContext = context.WithoutCancel(ctx)
	}
	evidence, err = loadMetaEvidence(readContext, reader, requestID)
	if err != nil {
		return evidence, result, errors.Join(runErr, err)
	}
	result = metaHiveResult(evidence)
	if result.Status == "returned" {
		// Do not create a misleading final-answer artifact for paused trials.
		// A returned database result can restore a file missed by interruption.
		runErr = errors.Join(runErr, preserveTrialFile(filepath.Join(dir, trial.Files.Result), []byte(result.Answer)))
	}
	return evidence, result, runErr
}

func preserveTrialFile(path string, body []byte) error {
	err := writeNew(path, body)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	return err
}
