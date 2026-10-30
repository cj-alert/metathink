package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const metaQueenDiagnosticCanary = "MQ_TEST_DIAGNOSTICS_MUST_NEVER_ENTER_MODEL_CONTEXT"
const metaQueenTestMutation = "Develop a counterexample to the current explanation, then state what observation distinguishes it."
const metaQueenTestAssessment = "# Assessment\nThe candidate changed the inquiry through a counterexample; this small comparison does not establish general improvement.\n"

// This fixture goes through the real MetaDecisions plaintext parser and real
// hive runner. Only the remote provider is substituted.
type metaQueenTextFixture struct {
	config          Config
	calls           int
	proposals       int
	comparisons     int
	sawMutation     bool
	comparisonInput string
	proposalInput   string
	allInput        []string
	failComparison  bool
	contributions   int
}

func metaQueenMessageText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, raw := range v {
			if part, ok := raw.(map[string]any); ok {
				if text, ok := part["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return fmt.Sprint(v)
	}
}

func (f *metaQueenTextFixture) Complete(_ context.Context, request TextRequest) (TextResponse, error) {
	f.calls++
	var system, input string
	for _, message := range request.Messages {
		text := metaQueenMessageText(message["content"])
		if message["role"] == "developer" || message["role"] == "system" {
			system += text + "\n"
		} else {
			input += text + "\n"
		}
	}
	f.allInput = append(f.allInput, system+input)
	reply := "NONE"
	switch {
	case request.Scope == "meta_queen" && strings.Contains(system, "MQ_PROPOSE_FIXTURE"):
		f.proposals++
		f.proposalInput = input
		reply = metaQueenTestMutation
	case request.Scope == "meta_queen" && strings.Contains(system, "MQ_COMPARE_FIXTURE"):
		f.comparisons++
		f.comparisonInput = input
		if f.failComparison {
			return TextResponse{}, &ProviderError{Kind: "api_rejection", StatusCode: 503, ProviderCode: "fixture_unavailable", Message: "fixture comparison failed"}
		}
		reply = metaQueenTestAssessment
	case strings.Contains(system, "MQ_DECISION_FIXTURE"):
		reply = f.decisionReply(input)
	case strings.Contains(system, "Return one concise contribution"):
		f.contributions++
		if strings.Contains(system, metaQueenTestMutation) {
			f.sawMutation = true
		}
		reply = fmt.Sprintf("candidate-work-%d: challenge the offset model using a counterexample and measure the predicted difference.", f.contributions)
	case strings.Contains(system, "current best answer"):
		reply = "candidate-answer: the offset model and counterexample predict different readings at input 5; test that input."
	case strings.Contains(system, "Give one concise question"):
		reply = "Which observation would distinguish the counterexample?"
	default:
		// A real Expand contribution creates a directed exchange for trajectory evidence.
		if strings.Contains(system, "Offer one useful change in thinking") {
			reply = "candidate-expansion: reinterpret the offset as a measurement artifact and test it."
		}
	}
	return TextResponse{Text: reply, Model: request.Model}, nil
}

func (f *metaQueenTextFixture) decisionReply(input string) string {
	lines := regexp.MustCompile(`(?m)^([1-9][0-9]*): `).FindAllStringSubmatch(input, -1)
	count := 0
	for _, line := range lines {
		n, _ := strconv.Atoi(line[1])
		if n > count {
			count = n
		}
	}
	selected := 1
	score := strings.Contains(input, "MQ_SCORE_FIXTURE")
	if score {
		selected = count
	} else if strings.Contains(input, f.config.Prompts["invite"]) || strings.Contains(input, f.config.Prompts["recipient"]) || strings.Contains(input, f.config.Prompts["fit"]) {
		selected = 2
	}
	var reply strings.Builder
	if !score {
		fmt.Fprintf(&reply, "# CHOICE\n%d\n", selected)
	}
	reply.WriteString("# PROBABILITIES\n")
	for i := 1; i <= count; i++ {
		p := 0
		if i == selected {
			p = 1
		}
		fmt.Fprintf(&reply, "%d: %d\n", i, p)
	}
	reply.WriteString("# CONFIDENCE\n0.8")
	return reply.String()
}

func metaQueenFixtureConfig(t *testing.T) Config {
	t.Helper()
	c := testHiveConfig(t)
	c.MetaQueen = MetaQueenConfig{
		Objective:   "Improve intellectual influence while preserving the base task and honest uncertainty.",
		ProblemType: "sensor-model-comparison",
		Chromosome:  0, Behavior: "contribute",
		Criteria: []Criterion{{Name: "influence", Description: "Assess whether attributed contributions changed later inquiry.", Levels: []DecisionLevel{{Label: "No evidence"}, {Label: "Supported"}}, Weight: 1}},
	}
	c.Prompts["mq_propose"] = "MQ_PROPOSE_FIXTURE: Return one replacement instruction."
	c.Prompts["mq_compare"] = "MQ_COMPARE_FIXTURE: Compare the operational trajectories."
	c.Decisions = DecisionPrompts{
		System:      "MQ_DECISION_FIXTURE: use the requested plain-text sections.",
		InputPrefix: "Shared evidence:",
		Predicate:   "{{.Question}}\nMQ_PREDICATE_FIXTURE",
		Choice:      "{{.Question}}\nOPTIONS\n{{.Options}}\nMQ_CHOICE_FIXTURE",
		Score:       "{{.Question}}\nOPTIONS\n{{.Options}}\nMQ_SCORE_FIXTURE",
	}
	return c
}

func metaQueenBaseline(t *testing.T, c Config) (string, HiveResult, map[string][]byte) {
	t.Helper()
	dir := t.TempDir()
	const contract = "Explain the supplied readings and propose a discriminating observation, preserving uncertainty."
	config, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "config.json"), config, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, c.Files.Contract), []byte(contract), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := OpenHive(c, dir, &hiveFixture{})
	if err != nil {
		t.Fatal(err)
	}
	h.Decisions = &hiveDecisionsFixture{}
	h.Logger.Log("developer", "test_canary", map[string]any{"diagnostic": metaQueenDiagnosticCanary})
	result, err := h.Run(context.Background(), contract, 0)
	h.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, c.Files.Result), []byte(result.Answer), 0600); err != nil {
		t.Fatal(err)
	}
	return dir, result, metaQueenSnapshot(t, dir)
}

func metaQueenSnapshot(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("unexpected nested baseline artifact %s", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name()] = data
	}
	return files
}

func metaQueenReadDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMetaQueenChangesOneInstructionAndEvaluatesOperationalInfluence(t *testing.T) {
	c := metaQueenFixtureConfig(t)
	baselineDir, baseline, before := metaQueenBaseline(t, c)
	baselineConfig := c
	// Controller settings must not become additional experimental mutations.
	c.Models.Drone = "controller-drone"
	c.Models.Queen = "controller-queen"
	c.Meta.BaseURL = "https://controller.example.invalid/v1"
	c.Meta.ReasoningEffort = "xhigh"
	c.Meta.ModelReasoningEffort = map[string]string{"controller-queen": "minimal"}
	dir := t.TempDir()
	client := &metaQueenTextFixture{config: c}
	const feedback = "PRIOR_ASSESSMENT_FEEDBACK: prefer a discriminating counterexample."
	result, err := RunMetaExperiment(context.Background(), c, dir, baselineDir, baseline.RequestID, feedback, client)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" || result.ExperimentID == 0 || result.Candidate.Status != "returned" || result.Comparison != metaQueenTestAssessment {
		t.Fatalf("comparison did not complete honestly: %+v", result)
	}
	if !reflect.DeepEqual(before, metaQueenSnapshot(t, baselineDir)) {
		t.Fatal("archived baseline files changed or new files appeared")
	}
	var variant Config
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil || json.Unmarshal(data, &variant) != nil {
		t.Fatalf("candidate config not retained: %v", err)
	}
	expected := baselineConfig
	expected.Chromosomes = append([]Chromosome(nil), baselineConfig.Chromosomes...)
	expected.Chromosomes[0].Contribute = metaQueenTestMutation
	if !reflect.DeepEqual(expected, variant) {
		t.Fatal("variant changed conditions beyond the selected Contribute instruction")
	}
	if result.OldInstruction != c.Chromosomes[0].Contribute || result.NewInstruction != metaQueenTestMutation || !client.sawMutation {
		t.Fatal("replacement was not attributed or did not reach an actual drone call")
	}
	if client.proposals != 1 || client.comparisons != 1 || !strings.Contains(client.proposalInput, feedback) {
		t.Fatal("expected one cycle with previous assessment supplied to proposal")
	}
	for _, evidence := range []string{"peer-work-", "candidate-work-", "drone_id", "kind", "tick"} {
		if !strings.Contains(client.comparisonInput, evidence) {
			t.Errorf("comparison lacks attributed operational evidence %q", evidence)
		}
	}
	for _, input := range client.allInput {
		if strings.Contains(input, metaQueenDiagnosticCanary) {
			t.Fatal("diagnostics entered meta-queen or hive model context")
		}
		if strings.Contains(input, `"runs":`) {
			t.Fatal("run diagnostic records entered model evidence")
		}
	}
	assessment, err := os.ReadFile(filepath.Join(dir, "comparison.md"))
	if err != nil || string(assessment) != metaQueenTestAssessment {
		t.Fatal("assessment was not retained verbatim")
	}
	logs := metaQueenReadDB(t, filepath.Join(dir, c.Files.Debug))
	var mq, leaked int
	if err := logs.QueryRow("SELECT COUNT(*) FROM mq_log WHERE scope='meta_queen'").Scan(&mq); err != nil {
		t.Fatal(err)
	}
	if err := logs.QueryRow("SELECT COUNT(*) FROM hive_log WHERE scope='meta_queen'").Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if mq == 0 || leaked != 0 {
		t.Fatalf("meta-queen logs are not separated: mq=%d leaked=%d", mq, leaked)
	}
}

func TestMetaQueenProviderFailureRetainsCandidateAndDoesNotReplay(t *testing.T) {
	c := metaQueenFixtureConfig(t)
	baselineDir, baseline, before := metaQueenBaseline(t, c)
	dir := t.TempDir()
	client := &metaQueenTextFixture{config: c, failComparison: true}
	result, err := RunMetaExperiment(context.Background(), c, dir, baselineDir, baseline.RequestID, "", client)
	if err == nil || result.Status != "failed" || result.Candidate.Status != "returned" || result.Comparison != "" || !strings.Contains(result.Error, "fixture comparison failed") {
		t.Fatalf("failure was lost or falsely completed: %+v %v", result, err)
	}
	db := metaQueenReadDB(t, filepath.Join(dir, c.Files.State))
	var status, retained string
	if err := db.QueryRow("SELECT status,result FROM mq_experiments WHERE id=?", result.ExperimentID).Scan(&status, &retained); err != nil {
		t.Fatal(err)
	}
	var saved MetaExperimentResult
	if err := json.Unmarshal([]byte(retained), &saved); err != nil || status != "failed" || saved.Candidate.Status != "returned" || !strings.Contains(saved.Error, "fixture comparison failed") {
		t.Fatalf("durable failure/result missing: %s %v", status, err)
	}
	calls := client.calls
	if _, retryErr := RunMetaExperiment(context.Background(), c, dir, baselineDir, baseline.RequestID, "", client); retryErr == nil {
		t.Fatal("same experiment directory was replayed")
	}
	if client.calls != calls || client.proposals != 1 || client.comparisons != 1 {
		t.Fatal("failed comparison triggered a retry or replayed completed hive work")
	}
	if !reflect.DeepEqual(before, metaQueenSnapshot(t, baselineDir)) {
		t.Fatal("failure handling changed the archived baseline")
	}
}
