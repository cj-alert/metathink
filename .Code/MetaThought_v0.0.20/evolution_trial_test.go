package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEvolutionTrialConfigClonesMutableControls(t *testing.T) {
	c := metaQueenFixtureConfig(t)
	before := jsonText(c)
	trial := evolutionTrialConfig(c, 7)
	if trial.Files.State != "trial-7-state.db" || trial.Files.Debug != "trial-7-debug.db" || trial.Files.Contract != "trial-7-contract.md" || trial.Files.Result != "trial-7-result.md" {
		t.Fatalf("trial files not isolated: %+v", trial.Files)
	}
	trial.Prompts["candidate"] = "new candidate prompt"
	trial.Chromosomes[0].Contribute = "new chromosome"
	trial.Experiment.Criteria[0].Levels[0].Label = "new criterion label"
	if jsonText(c) != before {
		t.Fatal("trial mutation changed original configuration")
	}
}

type failedEvolutionTrialClient struct{ calls int }

func (f *failedEvolutionTrialClient) Complete(context.Context, TextRequest) (TextResponse, error) {
	f.calls++
	return TextResponse{}, errors.New("fixture provider stopped")
}

func TestEvolutionTrialResumesUnassignedRequestAndReusesCompletedEvidence(t *testing.T) {
	ctx := context.Background()
	c := metaQueenFixtureConfig(t)
	dir := t.TempDir()
	stopped := &failedEvolutionTrialClient{}
	_, partial, err := runEvolutionTrial(ctx, c, dir, 9, "Explain the sensor readings and propose a distinguishing measurement.", stopped, &recordingHiveLog{})
	if err == nil || partial.RequestID == 0 || partial.TeamID != 0 || partial.Status != "paused" {
		t.Fatalf("expected an unassigned paused obligation: %+v, %v", partial, err)
	}
	trial := evolutionTrialConfig(c, 9)
	if _, err := os.Stat(filepath.Join(dir, trial.Files.Result)); !os.IsNotExist(err) {
		t.Fatalf("paused trial has a final result artifact: %v", err)
	}
	working := &metaQueenTextFixture{config: c}
	evidence, returned, err := runEvolutionTrial(ctx, c, dir, 9, "This later text must not replace the saved contract.", working, &recordingHiveLog{})
	if err != nil || returned.Status != "returned" || returned.RequestID != partial.RequestID {
		t.Fatalf("resume did not finish the same obligation: %+v, %v", returned, err)
	}
	if str(evidence.Request["contract"]) != "Explain the sensor readings and propose a distinguishing measurement." {
		t.Fatal("resume replaced the stored contract")
	}
	artifacts := map[string][]byte{}
	for _, name := range []string{"trial-9-config.json", trial.Files.Contract, trial.Files.Result} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		artifacts[name] = body
	}
	neverCall := &failedEvolutionTrialClient{}
	reusedEvidence, reused, err := runEvolutionTrial(ctx, c, dir, 9, "ignored", neverCall, &recordingHiveLog{})
	if err != nil || neverCall.calls != 0 || !reflect.DeepEqual(reused, returned) {
		t.Fatalf("completed trial replayed or changed: calls=%d returned=%+v reused=%+v err=%v", neverCall.calls, returned, reused, err)
	}
	if len(reusedEvidence.Runs) != len(evidence.Runs) || len(reusedEvidence.Contributions) != len(evidence.Contributions) {
		t.Fatal("completed reuse added a run or repeated contributions")
	}
	for name, expected := range artifacts {
		actual, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(actual) != string(expected) {
			t.Fatalf("completed artifact changed: %s err=%v", name, err)
		}
	}
}
