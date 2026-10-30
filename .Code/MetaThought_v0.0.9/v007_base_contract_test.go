package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type v007BaseChoices struct {
	t *testing.T
}

func (fixture v007BaseChoices) Decide(_ context.Context, request DecisionRequest) (Decision, error) {
	fixture.t.Helper()
	if strings.Contains(string(request.Input), "SEED_ONLY") {
		fixture.t.Fatal("routing received draft configuration instead of the reviewed contract")
	}
	result := Decision{Model: request.Model}
	for _, question := range request.Questions {
		if question.Type != "choice" || question.Name == nil {
			return Decision{}, fmt.Errorf("unexpected local decision: %+v", question)
		}
		var selected any
		switch *question.Name {
		case "commit":
			selected = "ACCEPT"
		case "fit", "invite":
			if len(question.Choices) < 2 {
				return Decision{}, errors.New("local team choice has no collaborator")
			}
			selected = question.Choices[1].Value
		case "recipient":
			selected = "NONE"
		default:
			return Decision{}, fmt.Errorf("unexpected local decision %s", *question.Name)
		}
		result.Answers = append(result.Answers, DecisionAnswer{Type: "choice", Name: question.Name, Choice: selected})
	}
	return result, nil
}

type v007BaseTextFixture struct {
	t              *testing.T
	contract       string
	parsed         Contract
	failContribute bool
	counts         map[string]int
	answer         string
}

func v007NewBaseTextFixture(t *testing.T, contract string) *v007BaseTextFixture {
	t.Helper()
	parsed, err := ParseContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	return &v007BaseTextFixture{
		t: t, contract: contract, parsed: parsed, counts: map[string]int{},
		answer: "Exact fixture candidate.\n\nPreserve this separate paragraph and its uncertainty.\n",
	}
}

func (fixture *v007BaseTextFixture) Complete(_ context.Context, request TextRequest) (TextResponse, error) {
	t := fixture.t
	t.Helper()
	if len(request.Messages) != 2 {
		t.Fatalf("unexpected messages: %+v", request.Messages)
	}
	prompt := str(request.Messages[0]["content"])
	input := str(request.Messages[1]["content"])
	if strings.Contains(input, "SEED_ONLY") || strings.Contains(input, "V007_PRIVATE_DIAGNOSTIC") {
		t.Fatalf("model input contains draft seed or diagnostic material: %s", input)
	}
	key := strings.TrimPrefix(strings.Fields(prompt)[0], "V007_")
	fixture.counts[key]++
	response := TextResponse{Model: request.Model}
	switch key {
	case "contribute", "expand", "candidate":
		var received struct {
			Contract string `json:"contract"`
		}
		if err := json.Unmarshal([]byte(input), &received); err != nil {
			t.Fatal(err)
		}
		if received.Contract != fixture.contract {
			t.Fatalf("%s did not receive the exact reviewed contract", key)
		}
		switch key {
		case "contribute":
			if fixture.failContribute {
				return TextResponse{}, errors.New("fixture interrupted before contribution")
			}
			response.Text = "Contribution grounded in the confirmed context."
		case "expand":
			response.Text = "NONE"
		case "candidate":
			response.Text = fixture.answer
		}
	case "assess":
		var received struct {
			Candidate       string              `json:"candidate"`
			ScoringElements []ContractCriterion `json:"scoring_elements"`
			Context         struct {
				Contract string `json:"contract"`
				DroneID  int64  `json:"drone_id"`
			} `json:"operational_context"`
		}
		if err := json.Unmarshal([]byte(input), &received); err != nil {
			t.Fatal(err)
		}
		if received.Context.Contract != fixture.contract || !reflect.DeepEqual(received.ScoringElements, fixture.parsed.Criteria) {
			t.Fatal("assessment used draft criteria or lost reviewed contract details")
		}
		if received.Candidate != fixture.answer {
			t.Fatal("assessment subject differs from the saved candidate")
		}
		scores := []float64{0, 0.5}
		if received.Context.DroneID == 2 {
			scores = []float64{1, 0.75}
		} else if received.Context.DroneID != 1 {
			t.Fatalf("unexpected assessor %d", received.Context.DroneID)
		}
		var body strings.Builder
		for index, criterion := range fixture.parsed.Criteria {
			fmt.Fprintf(&body, "# %s\nScore: %g\nEvidence: peer %d observed %s\nUncertainty: peer %d retains disagreement\n", criterion.Name, scores[index], received.Context.DroneID, criterion.Name, received.Context.DroneID)
		}
		response.Text = body.String()
	case "collapse":
		var received struct {
			Contract  Contract `json:"contract"`
			Candidate int64    `json:"current_candidate_id"`
			Profiles  []struct {
				DroneID     int64   `json:"drone_id"`
				Criterion   string  `json:"criterion"`
				Score       float64 `json:"score"`
				TargetMet   *bool   `json:"target_met"`
				Evidence    string  `json:"evidence"`
				Uncertainty string  `json:"uncertainty"`
			} `json:"drone_assessments"`
		}
		if err := json.Unmarshal([]byte(input), &received); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(received.Contract, fixture.parsed) {
			t.Fatal("Queen received a different governing contract")
		}
		if len(received.Profiles) != 4 {
			t.Fatalf("Queen needs separate peer profiles, got %+v", received.Profiles)
		}
		expected := map[string]float64{
			"1/" + fixture.parsed.Criteria[0].Name: 0,
			"1/" + fixture.parsed.Criteria[1].Name: 0.5,
			"2/" + fixture.parsed.Criteria[0].Name: 1,
			"2/" + fixture.parsed.Criteria[1].Name: 0.75,
		}
		for _, profile := range received.Profiles {
			name := fmt.Sprintf("%d/%s", profile.DroneID, profile.Criterion)
			score, exists := expected[name]
			if !exists || profile.Score != score {
				t.Fatalf("lost or combined peer score: %+v", profile)
			}
			delete(expected, name)
			if profile.TargetMet == nil || *profile.TargetMet != (score > 0.5) {
				t.Fatalf("strict >0.5 target changed at score %g: %+v", score, profile.TargetMet)
			}
			if profile.Evidence == "" || profile.Uncertainty == "" {
				t.Fatal("Queen lost the score's evidence or uncertainty")
			}
		}
		if len(expected) != 0 {
			t.Fatalf("missing attributed scores: %v", expected)
		}
		response.Text = fmt.Sprintf("# DECISION\nRETURN\n# CANDIDATES\n%d\n# CRITERIA\nUNMET\n# REASON\nReturn these findings while preserving the two peers' disagreement.\n", received.Candidate)
	default:
		return TextResponse{}, fmt.Errorf("unexpected fixture prompt %q", prompt)
	}
	return response, nil
}

func v007PrepareBaseFixture(h *Hive, t *testing.T) {
	t.Helper()
	h.Config.Experiment.Objective = "SEED_ONLY objective"
	h.Config.Experiment.Intent = "SEED_ONLY intent"
	h.Config.Experiment.Context = "SEED_ONLY context"
	h.Config.Experiment.Criteria = []Criterion{{Name: "SEED_ONLY_criterion"}}
	for _, key := range []string{"contribute", "expand", "candidate", "assess", "collapse", "guide"} {
		h.Config.Prompts[key] = "V007_" + key
	}
	h.Decisions = v007BaseChoices{t: t}
	h.Logger.Log("developer", "fixture_canary", map[string]any{"private": "V007_PRIVATE_DIAGNOSTIC"})
}

func v007AssertBaseReturn(t *testing.T, h *Hive, fixture *v007BaseTextFixture, result HiveResult) {
	t.Helper()
	if result.Status != "returned" || result.CriteriaStatus != "UNMET" || result.CriteriaMet {
		t.Fatalf("unmet findings changed into success or failure: %+v", result)
	}
	if result.Answer != fixture.answer || len(result.SelectedCandidateIDs) != 1 {
		t.Fatalf("returned answer is not the selected saved candidate: %+v", result)
	}
	for key, expected := range map[string]int{"contribute": 2, "expand": 2, "candidate": 1, "assess": 2, "collapse": 1} {
		if fixture.counts[key] != expected {
			t.Fatalf("%s calls: got %d want %d", key, fixture.counts[key], expected)
		}
	}
	rows, err := h.query(context.Background(), "fixture_contract", "hive_requests", "SELECT contract,status FROM hive_requests WHERE id=?", result.RequestID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("read request: %v %v", rows, err)
	}
	if str(rows[0]["contract"]) != fixture.contract || str(rows[0]["status"]) != "returned" {
		t.Fatalf("saved governing contract or return was changed: %v", rows)
	}
}

func TestV007BaseRunUsesConfirmedContractAndPreservesScores(t *testing.T) {
	contract := v7ContractText("reviewed base inquiry", "reviewed_evidence", "reviewed_usefulness")
	fixture := v007NewBaseTextFixture(t, contract)
	h := v7OpenHive(t, fixture)
	v007PrepareBaseFixture(h, t)
	result, err := h.Run(context.Background(), contract, 0)
	if err != nil {
		t.Fatal(err)
	}
	v007AssertBaseReturn(t, h, fixture, result)
}

func TestV007BaseResumeUsesSavedConfirmedContract(t *testing.T) {
	contract := v7ContractText("saved reviewed inquiry", "saved_evidence", "saved_usefulness")
	first := v007NewBaseTextFixture(t, contract)
	first.failContribute = true
	h := v7OpenHive(t, first)
	v007PrepareBaseFixture(h, t)
	interrupted, err := h.Run(context.Background(), contract, 0)
	if err == nil || !strings.Contains(err.Error(), "fixture interrupted") || interrupted.RequestID == 0 {
		t.Fatalf("expected a persisted interrupted obligation: %+v %v", interrupted, err)
	}
	dir, config := h.Dir, h.Config
	h.Close()
	config.Experiment.Objective = "SEED_ONLY replacement objective"
	config.Experiment.Context = "SEED_ONLY replacement context"
	resumedFixture := v007NewBaseTextFixture(t, contract)
	resumed, err := OpenHive(config, dir, resumedFixture)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resumed.Close)
	resumed.Decisions = v007BaseChoices{t: t}
	result, err := resumed.Resume(context.Background(), interrupted.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if result.RequestID != interrupted.RequestID || result.TeamID != interrupted.TeamID {
		t.Fatalf("resume replaced the original obligation or team: before=%+v after=%+v", interrupted, result)
	}
	v007AssertBaseReturn(t, resumed, resumedFixture, result)
}

func TestV007MalformedAssessmentsNeverPersistDefaultScores(t *testing.T) {
	contract := v7ContractText("score completeness", "alpha", "beta")
	valid := "# alpha\nScore: 0\nEvidence: alpha observation\nUncertainty: unresolved\n# beta\nScore: 1\nEvidence: beta observation\nUncertainty: unresolved\n"
	cases := map[string]string{
		"missing_score":       strings.Replace(valid, "Score: 0\n", "", 1),
		"missing_criterion":   strings.Split(valid, "# beta")[0],
		"non_number":          strings.Replace(valid, "Score: 0", "Score: unknown", 1),
		"nan":                 strings.Replace(valid, "Score: 0", "Score: NaN", 1),
		"infinity":            strings.Replace(valid, "Score: 0", "Score: +Inf", 1),
		"negative":            strings.Replace(valid, "Score: 0", "Score: -0.01", 1),
		"above_one":           strings.Replace(valid, "Score: 1", "Score: 1.01", 1),
		"duplicate_criterion": valid + strings.Split(valid, "# beta")[0],
		"missing_evidence":    strings.Replace(valid, "Evidence: alpha observation\n", "", 1),
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			calls := 0
			h := v7OpenHive(t, v7TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
				calls++
				return TextResponse{Text: reply, Model: request.Model}, nil
			}))
			_, teamID := v7SeedRequest(t, h, contract)
			candidateID := v7AddCandidate(t, h, teamID, "unassessed candidate", false)
			members, err := h.members(context.Background(), teamID)
			if err != nil {
				t.Fatal(err)
			}
			err = h.assessCandidate(context.Background(), contract, candidateID, teamID, 1, members)
			if err == nil || calls != 1 {
				t.Fatalf("malformed assessment accepted or retried: calls=%d error=%v", calls, err)
			}
			rows, queryErr := h.query(context.Background(), "fixture_scores", "hive_assessments", "SELECT id FROM hive_assessments WHERE candidate_id=?", candidateID)
			if queryErr != nil || len(rows) != 0 {
				t.Fatalf("incomplete assessment persisted scores: %v %v", rows, queryErr)
			}
		})
	}
}

func TestV007AssessmentVectorRollbackPreservesEarlierPeer(t *testing.T) {
	contract := v7ContractText("atomic vectors", "alpha", "beta")
	calls := 0
	h := v7OpenHive(t, v7TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
		calls++
		return TextResponse{Text: "# alpha\nScore: 0\nEvidence: observed alpha\nUncertainty: unresolved\n# beta\nScore: 1\nEvidence: observed beta\nUncertainty: unresolved\n", Model: request.Model}, nil
	}))
	_, teamID := v7SeedRequest(t, h, contract)
	candidateID := v7AddCandidate(t, h, teamID, "transaction subject", false)
	members, err := h.members(context.Background(), teamID)
	if err != nil || len(members) != 2 {
		t.Fatalf("members: %v %v", members, err)
	}
	firstPeer, secondPeer := id(members[0]["id"]), id(members[1]["id"])
	v7Exec(t, h, fmt.Sprintf("CREATE TRIGGER v007_fail_second_criterion BEFORE INSERT ON hive_assessments WHEN NEW.drone_id=%d AND NEW.criterion='beta' BEGIN SELECT RAISE(ABORT, 'v007 second criterion write rejected'); END", secondPeer))
	err = h.assessCandidate(context.Background(), contract, candidateID, teamID, 1, members)
	if err == nil || !strings.Contains(err.Error(), "v007 second criterion write rejected") || calls != 2 {
		t.Fatalf("expected the second peer's mid-vector write failure: calls=%d error=%v", calls, err)
	}
	rows, err := h.query(context.Background(), "fixture_scores", "hive_assessments", "SELECT drone_id,criterion,score FROM hive_assessments WHERE candidate_id=? ORDER BY id", candidateID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("first vector was lost or failed vector was partially kept: %v %v", rows, err)
	}
	for index, row := range rows {
		if id(row["drone_id"]) != firstPeer || str(row["criterion"]) != []string{"alpha", "beta"}[index] || row["score"] != []float64{0, 1}[index] {
			t.Fatalf("prior peer's complete vector was changed: %v", rows)
		}
	}
}

func TestV007CLIConfirmationGuardsPrecedeConfigDatabaseAndProvider(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"run_without_confirmation", []string{"run", "-contract", "reviewed.md"}, "run requires -contract and -confirm"},
		{"run_without_contract", []string{"run", "-confirm"}, "run requires -contract and -confirm"},
		{"experiment_without_confirmation", []string{"experiment", "-meta-contract", "reviewed-meta.md"}, "experiment requires -meta-contract and -confirm"},
		{"evolve_without_confirmation", []string{"evolve", "-meta-contract", "reviewed-meta.md"}, "evolve requires -meta-contract and -confirm"},
		{"experiment_without_contract", []string{"experiment", "-confirm"}, "experiment requires -meta-contract and -confirm"},
		{"evolve_without_contract", []string{"evolve", "-confirm"}, "evolve requires -meta-contract and -confirm"},
		{"base_resume_new_confirmation", []string{"resume", "-request-id", "1", "-confirm"}, "-confirm applies only"},
		{"base_resume_replacement_contract", []string{"resume", "-request-id", "1", "-contract", "replacement.md"}, "-contract applies only to run"},
		{"meta_resume_replacement_contract", []string{"evolve-resume", "-meta-contract", "replacement.md"}, "-meta-contract applies only"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			dir := filepath.Join(base, "must-not-be-created")
			args := append(append([]string{}, test.args...), "-config", filepath.Join(base, "missing-config.json"), "-dir", dir)
			before := v7NetworkAttempts.Load()
			err := execute(args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("confirmation did not fail before reading missing configuration: %v", err)
			}
			if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected CLI invocation created its runtime folder: %v", err)
			}
			if v7NetworkAttempts.Load() != before {
				t.Fatal("rejected CLI invocation attempted HTTP")
			}
		})
	}
}
