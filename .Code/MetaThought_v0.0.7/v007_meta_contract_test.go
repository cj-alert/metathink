package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const v7MetaDiagnosticCanary = "V007_DIAGNOSTICS_MUST_NOT_BECOME_EVIDENCE"

type v7MetaFixture struct {
	t               *testing.T
	criteria        []string
	commitChoices   []string
	commitCalls     int
	assessmentCalls map[int64]int
	failSecondOnce  bool
	invalidScore    bool
	requests        []TextRequest
	queenInputs     []map[string]any
}

func v7MetaConfig(t *testing.T) Config {
	t.Helper()
	c := v7Config(t)
	c.MetaQueen.Objective = "FORBIDDEN_DRAFT_OBJECTIVE"
	c.MetaQueen.Intent = "FORBIDDEN_DRAFT_INTENT"
	c.MetaQueen.Context = "FORBIDDEN_DRAFT_CONTEXT"
	c.MetaQueen.Criteria = []Criterion{{Name: "FORBIDDEN_DRAFT_CRITERION"}}
	c.MetaQueen.ProblemType = "local-method-evaluation"
	c.MetaQueen.Targets = []string{"contribute", "expand"}
	c.Prompts["commit"] = "V7_META_COMMIT: choose whether to commit."
	c.Prompts["assess"] = "V7_META_ASSESS: assess each confirmed criterion using the documented plain-text format."
	c.Prompts["mq_action"] = "V7_META_ACTION: choose the next experimental action."
	c.Prompts["mq_return"] = "V7_META_RETURN: explain the supported methodological finding."
	return c
}

func v7MetaParts(request TextRequest) []string {
	var parts []string
	for _, message := range request.Messages {
		switch content := message["content"].(type) {
		case string:
			parts = append(parts, content)
		case []any:
			for _, item := range content {
				if object, ok := item.(map[string]any); ok {
					if text, ok := object["text"].(string); ok {
						parts = append(parts, text)
					}
				}
			}
		}
	}
	return parts
}

func v7MetaInput(t *testing.T, request TextRequest) map[string]any {
	t.Helper()
	for _, part := range v7MetaParts(request) {
		var input map[string]any
		if json.Unmarshal([]byte(part), &input) == nil && input != nil {
			return input
		}
	}
	t.Fatal("fixture did not receive the operational JSON input")
	return nil
}

// This produces the full plaintext Decisions reply, so commitments and Queen
// actions still traverse the production adapter and its distribution parser.
func v7MetaChoice(t *testing.T, request TextRequest, selected string) string {
	t.Helper()
	text := strings.Join(v7MetaParts(request), "\n")
	options := regexp.MustCompile(`(?m)^([1-9][0-9]*): ("(?:[^"\\]|\\.)*")`).FindAllStringSubmatch(text, -1)
	position := 0
	for _, option := range options {
		value, err := strconv.Unquote(option[2])
		if err == nil && value == selected {
			position, _ = strconv.Atoi(option[1])
		}
	}
	if position == 0 {
		t.Fatalf("fixture choice %q was not offered", selected)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "# CHOICE\n%d\n# PROBABILITIES\n", position)
	for _, option := range options {
		n, _ := strconv.Atoi(option[1])
		probability := 0
		if n == position {
			probability = 1
		}
		fmt.Fprintf(&out, "%d: %d\n", n, probability)
	}
	out.WriteString("# CONFIDENCE\n0.8")
	return out.String()
}

func (f *v7MetaFixture) Complete(_ context.Context, request TextRequest) (TextResponse, error) {
	f.requests = append(f.requests, request)
	if request.Scope != "meta_queen" {
		return TextResponse{}, fmt.Errorf("unexpected non-meta call %q", request.Scope)
	}
	parts := v7MetaParts(request)
	operation := ""
	for _, part := range parts {
		for _, name := range []string{"COMMIT", "ASSESS", "ACTION", "RETURN"} {
			if strings.HasPrefix(part, "V7_META_"+name+":") {
				operation = name
			}
		}
	}
	input := v7MetaInput(f.t, request)
	var reply string
	switch operation {
	case "COMMIT":
		if request.Model != "fixture-drone" {
			f.t.Errorf("commit model = %q", request.Model)
		}
		choice := "ACCEPT"
		if f.commitCalls < len(f.commitChoices) {
			choice = f.commitChoices[f.commitCalls]
		}
		f.commitCalls++
		reply = v7MetaChoice(f.t, request, choice)
	case "ASSESS":
		if request.Model != "fixture-drone" {
			f.t.Errorf("numeric assessment model = %q", request.Model)
		}
		if f.commitCalls != 2 {
			f.t.Errorf("assessment began without two explicit commitment replies: %d", f.commitCalls)
		}
		drone := input["drone"].(map[string]any)
		droneID := int64(drone["drone_id"].(float64))
		if f.assessmentCalls == nil {
			f.assessmentCalls = map[int64]int{}
		}
		f.assessmentCalls[droneID]++
		if droneID == 2 && f.failSecondOnce {
			f.failSecondOnce = false
			return TextResponse{}, &ProviderError{Kind: "api_rejection", StatusCode: 503, Message: "fixture assessment unavailable"}
		}
		scores := []float64{0.5, 0.8}
		if droneID == 2 {
			scores = []float64{0.9, 0.2}
		}
		if f.invalidScore {
			scores[0] = 1.1
		}
		var out strings.Builder
		for n, criterion := range f.criteria {
			fmt.Fprintf(&out, "# %s\nScore: %g\nEvidence: evidence-drone-%d-%s\nUncertainty: uncertainty-drone-%d-%s\n",
				criterion, scores[n], droneID, criterion, droneID, criterion)
		}
		reply = out.String()
	case "ACTION":
		f.queenInputs = append(f.queenInputs, input)
		if request.Model != "fixture-queen" {
			f.t.Errorf("action model = %q", request.Model)
		}
		reply = v7MetaChoice(f.t, request, "RETURN")
	case "RETURN":
		f.queenInputs = append(f.queenInputs, input)
		if request.Model != "fixture-queen" {
			f.t.Errorf("return model = %q", request.Model)
		}
		reply = "The two drones disagree about the method. The reference evidence does not demonstrate an improvement."
	default:
		return TextResponse{}, fmt.Errorf("unrecognized local meta fixture instruction")
	}
	return TextResponse{Text: reply, Model: request.Model}, nil
}

func v7MetaOpenStore(t *testing.T, c Config, dir string) (*Store, *Logger) {
	t.Helper()
	logger, err := OpenLogger(filepath.Join(dir, c.Files.Debug), c.Schema.Logs)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(filepath.Join(dir, c.Files.State), c.Schema.State, &metaQueenLog{Sink: logger})
	if err != nil {
		logger.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close(); logger.Close() })
	return store, logger
}

func v7MetaExec(t *testing.T, store *Store, query string, args ...any) {
	t.Helper()
	if _, err := store.Exec(context.Background(), "fixture", schemaTarget(query), nil, query, args...); err != nil {
		t.Fatal(err)
	}
}

func v7MetaQuery(t *testing.T, store *Store, query string, args ...any) []map[string]any {
	t.Helper()
	rows, err := store.Query(context.Background(), "fixture", "operational_state", nil, query, args...)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func v7MetaAssertContract(t *testing.T, input map[string]any, wanted Contract) {
	t.Helper()
	value, exists := input["meta_contract"]
	if !exists {
		value = input["contract"]
	}
	var actual Contract
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(encoded, &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, wanted) {
		t.Fatalf("model contract differs from confirmed contract: got %+v want %+v", actual, wanted)
	}
}

func v7MetaAssertBoundary(t *testing.T, fixture *v7MetaFixture, contract Contract) {
	t.Helper()
	for _, request := range fixture.requests {
		text := strings.Join(v7MetaParts(request), "\n")
		for _, forbidden := range []string{"FORBIDDEN_DRAFT_", "REPLACEMENT_DRAFT_", v7MetaDiagnosticCanary} {
			if strings.Contains(text, forbidden) {
				t.Errorf("model input contains forbidden source %q", forbidden)
			}
		}
		v7MetaAssertContract(t, v7MetaInput(t, request), contract)
	}
}

func TestV007MetaContractAtomicVectorsAndReopen(t *testing.T) {
	ctx := context.Background()
	c := v7MetaConfig(t)
	text := v7ContractText("confirmed-meta", "influence", "testability")
	contract, err := ParseContract(text)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &v7MetaFixture{t: t, criteria: []string{"influence", "testability"}}
	dir := t.TempDir()
	store, logger := v7MetaOpenStore(t, c, dir)
	logger.Log("meta_queen", "fixture_diagnostic", map[string]any{"debug_only": v7MetaDiagnosticCanary})
	logger.Log("hive", "fixture_diagnostic", map[string]any{"debug_only": v7MetaDiagnosticCanary})
	inquiry, err := openMetaInquiry(ctx, c, store, fixture, logger, "experiment", 19, text)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range v7MetaQuery(t, store, "SELECT status FROM mq_commitments WHERE inquiry_id=?", inquiry.ID) {
		if str(row["status"]) != "pending" {
			t.Fatalf("drone was committed before its decision: %v", row)
		}
	}
	v7MetaExec(t, store, "CREATE TRIGGER v7_interrupt_meta BEFORE INSERT ON mq_assessments WHEN NEW.drone_id=2 AND NEW.criterion='testability' BEGIN SELECT RAISE(ABORT,'v7_vector_interrupted'); END")
	input := map[string]any{"meta_queen": metaRuntimeOptions(c), "task_contract": v7ContractText("fixed-task"), "observed_exchange": "one drone challenged an assumption and another changed its proposal"}
	err = inquiry.assess(ctx, "method-reference", input)
	if err == nil || !strings.Contains(err.Error(), "v7_vector_interrupted") {
		t.Fatalf("expected injected vector write failure, got %v", err)
	}
	rows := v7MetaQuery(t, store, "SELECT drone_id,criterion,score FROM mq_assessments ORDER BY id")
	if len(rows) != 2 || id(rows[0]["drone_id"]) != 1 || id(rows[1]["drone_id"]) != 1 {
		t.Fatalf("partial second vector survived rollback: %v", rows)
	}
	if fixture.commitCalls != 2 || fixture.assessmentCalls[1] != 1 || fixture.assessmentCalls[2] != 1 {
		t.Fatalf("unexpected calls before interruption: commits=%d assessments=%v", fixture.commitCalls, fixture.assessmentCalls)
	}
	store.Close()
	logger.Close()

	// Reopen using changed draft seeds and different supplied text. The saved
	// inquiry remains the authority, and its completed vector is not replayed.
	c.MetaQueen.Objective, c.MetaQueen.Context = "REPLACEMENT_DRAFT_OBJECTIVE", "REPLACEMENT_DRAFT_CONTEXT"
	c.MetaQueen.Criteria = []Criterion{{Name: "REPLACEMENT_DRAFT_CRITERION"}}
	store, logger = v7MetaOpenStore(t, c, dir)
	reopened, err := openMetaInquiry(ctx, c, store, fixture, logger, "experiment", 19, v7ContractText("replacement", "wrong-criterion"))
	if err != nil {
		t.Fatal(err)
	}
	if reopened.ID != inquiry.ID || !reflect.DeepEqual(reopened.Contract, contract) {
		t.Fatalf("reopen changed saved meta contract: %+v", reopened.Contract)
	}
	v7MetaExec(t, store, "DROP TRIGGER v7_interrupt_meta")
	if err = reopened.assess(ctx, "method-reference", input); err != nil {
		t.Fatal(err)
	}
	rows = v7MetaQuery(t, store, "SELECT drone_id,criterion,score,evidence,uncertainty FROM mq_assessments ORDER BY id")
	if len(rows) != 4 || fixture.commitCalls != 2 || fixture.assessmentCalls[1] != 1 || fixture.assessmentCalls[2] != 2 {
		t.Fatalf("completed work replayed or score vector missing: rows=%v commits=%d assessments=%v", rows, fixture.commitCalls, fixture.assessmentCalls)
	}
	want := map[int64]map[string]float64{1: {"influence": 0.5, "testability": 0.8}, 2: {"influence": 0.9, "testability": 0.2}}
	for _, row := range rows {
		droneID, criterion := id(row["drone_id"]), str(row["criterion"])
		if row["score"] != want[droneID][criterion] || !strings.Contains(str(row["evidence"]), fmt.Sprintf("drone-%d", droneID)) || str(row["uncertainty"]) == "" {
			t.Errorf("incorrect attributed score: %v", row)
		}
	}
	attached := map[string]any{}
	if err = reopened.attach(ctx, attached); err != nil {
		t.Fatal(err)
	}
	for _, row := range attached["meta_drone_assessments"].([]map[string]any) {
		actual := row["score"].(float64)
		if row["target_met"] != (actual > 0.5) {
			t.Errorf("strict target comparison changed actual score meaning: %v", row)
		}
	}
	saved := v7MetaQuery(t, store, "SELECT contract FROM mq_inquiries WHERE id=?", inquiry.ID)
	if len(saved) != 1 || str(saved[0]["contract"]) != text {
		t.Fatal("confirmed source text was replaced")
	}
	v7MetaAssertBoundary(t, fixture, contract)
	t.Logf("meta_vectors=2 criteria_per_vector=2 accepted_commitments=%d completed_vector_replayed=false strict_target_preserved=true", fixture.commitCalls)
}

func TestV007MetaRequiresTwoExplicitCommitments(t *testing.T) {
	c := v7MetaConfig(t)
	store, logger := v7MetaOpenStore(t, c, t.TempDir())
	fixture := &v7MetaFixture{t: t, criteria: []string{"influence", "testability"}, commitChoices: []string{"ACCEPT", "DECLINE"}}
	inquiry, err := openMetaInquiry(context.Background(), c, store, fixture, logger, "experiment", 20, v7ContractText("confirmed-meta", fixture.criteria...))
	if err != nil {
		t.Fatal(err)
	}
	err = inquiry.assess(context.Background(), "reference", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "lacks two committed drones") {
		t.Fatalf("one accepting drone should not become a team: %v", err)
	}
	rows := v7MetaQuery(t, store, "SELECT drone_id,status FROM mq_commitments ORDER BY drone_id")
	if len(rows) != 2 || str(rows[0]["status"]) != "accepted" || str(rows[1]["status"]) != "declined" || fixture.commitCalls != 2 || len(fixture.assessmentCalls) != 0 {
		t.Fatalf("commitment decision not respected: rows=%v calls=%d assessments=%v", rows, fixture.commitCalls, fixture.assessmentCalls)
	}
	if len(v7MetaQuery(t, store, "SELECT id FROM mq_assessments")) != 0 {
		t.Fatal("a non-team persisted method scores")
	}
}

func TestV007MetaRejectsOutOfRangeActualScore(t *testing.T) {
	c := v7MetaConfig(t)
	store, logger := v7MetaOpenStore(t, c, t.TempDir())
	fixture := &v7MetaFixture{t: t, criteria: []string{"influence", "testability"}, invalidScore: true}
	inquiry, err := openMetaInquiry(context.Background(), c, store, fixture, logger, "experiment", 21, v7ContractText("confirmed-meta", fixture.criteria...))
	if err != nil {
		t.Fatal(err)
	}
	err = inquiry.assess(context.Background(), "reference", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "[0,1]") {
		t.Fatalf("expected actual score rejection, got %v", err)
	}
	if len(v7MetaQuery(t, store, "SELECT id FROM mq_assessments")) != 0 {
		t.Fatal("invalid actual score entered operational evidence")
	}
}

func v7MetaSavedReturn(t *testing.T, h *Hive, teamID int64, body string) int64 {
	t.Helper()
	candidate := v7AddCandidate(t, h, teamID, body, true)
	v7Exec(t, h, "INSERT INTO hive_reviews(request_id,candidate_id,selected_candidate_ids,tick,decision,criteria_status,reason,created) VALUES(?,?,?,?,?,?,?,?)", h.requestID, candidate, jsonText([]int64{candidate}), 1, "RETURN", "MET", "supported local fixture", now())
	v7Exec(t, h, "UPDATE hive_requests SET status='returned',result=?,finish_reason='queen_return',criteria_met=1 WHERE id=?", body, h.requestID)
	return candidate
}

func TestV007EvolutionConfirmedContractAssessmentResume(t *testing.T) {
	ctx := context.Background()
	noHiveCalls := v7TextFunc(func(context.Context, TextRequest) (TextResponse, error) {
		return TextResponse{}, errors.New("completed baseline must not execute another model call")
	})
	h := v7OpenHive(t, noHiveCalls)
	taskContract := v7ContractText("fixed-task")
	requestID, teamID := v7SeedRequest(t, h, taskContract)
	v7MetaSavedReturn(t, h, teamID, "Reference answer with limited evidence.")
	v7Exec(t, h, "UPDATE hive_runs SET status='completed',finished=? WHERE id=?", now(), h.runID)
	h.Logger.Log("hive", "fixture_diagnostic", map[string]any{"debug_only": v7MetaDiagnosticCanary})
	baselineDir := h.Dir
	h.Close()
	c := v7MetaConfig(t)
	metaText := v7ContractText("reviewed-meta", "influence", "testability")
	contract, err := ParseContract(metaText)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &v7MetaFixture{t: t, criteria: []string{"influence", "testability"}, failSecondOnce: true}
	dir := t.TempDir()
	first, err := RunEvolution(ctx, c, dir, baselineDir, requestID, metaText, fixture)
	if err == nil || !strings.Contains(err.Error(), "fixture assessment unavailable") || first.Status != "failed" || first.Phase != "assess" {
		t.Fatalf("assessment failure lost its resumable phase: result=%+v error=%v", first, err)
	}
	if fixture.commitCalls != 2 || fixture.assessmentCalls[1] != 1 || fixture.assessmentCalls[2] != 1 || len(fixture.queenInputs) != 0 {
		t.Fatalf("unexpected first attempt calls: commits=%d assessments=%v queen=%d", fixture.commitCalls, fixture.assessmentCalls, len(fixture.queenInputs))
	}
	changed := c
	changed.MetaQueen.Objective, changed.MetaQueen.Context = "REPLACEMENT_DRAFT_OBJECTIVE", "REPLACEMENT_DRAFT_CONTEXT"
	changed.MetaQueen.Criteria = nil
	completed, err := ResumeEvolution(ctx, changed, dir, fixture)
	if err != nil || completed.Status != "completed" || completed.Phase != "completed" {
		t.Fatalf("resume failed: result=%+v error=%v", completed, err)
	}
	if fixture.commitCalls != 2 || fixture.assessmentCalls[1] != 1 || fixture.assessmentCalls[2] != 2 || len(fixture.queenInputs) != 2 {
		t.Fatalf("resume replayed completed work: commits=%d assessments=%v queen=%d", fixture.commitCalls, fixture.assessmentCalls, len(fixture.queenInputs))
	}
	for _, input := range fixture.queenInputs {
		scores, ok := input["meta_drone_assessments"].([]any)
		if !ok || len(scores) != 4 || input["task_contract"] != taskContract {
			t.Fatalf("Queen lacks attributed scores or fixed task: %v", input)
		}
		for _, value := range scores {
			score := value.(map[string]any)
			droneID := int64(score["drone_id"].(float64))
			criterion := score["criterion"].(string)
			want := map[int64]map[string]float64{1: {"influence": 0.5, "testability": 0.8}, 2: {"influence": 0.9, "testability": 0.2}}
			if score["score"] != want[droneID][criterion] || score["subject_key"] != "trial:1" || score["target_met"] != (want[droneID][criterion] > 0.5) {
				t.Fatalf("Queen input replaced attributed disagreement: %v", score)
			}
		}
		v7MetaAssertContract(t, input, contract)
	}
	v7MetaAssertBoundary(t, fixture, contract)
	store, logger := v7MetaOpenStore(t, c, dir)
	inquiries := v7MetaQuery(t, store, "SELECT contract,status FROM mq_inquiries")
	trials := v7MetaQuery(t, store, "SELECT status,assessment FROM mq_trials")
	if len(inquiries) != 1 || str(inquiries[0]["contract"]) != metaText || str(inquiries[0]["status"]) != "returned" || len(trials) != 1 || str(trials[0]["status"]) != "completed" || str(trials[0]["assessment"]) != "" {
		t.Fatalf("completed method inquiry not persisted correctly: inquiries=%v trials=%v", inquiries, trials)
	}
	store.Close()
	logger.Close()
	calls := 0
	again, err := ResumeEvolution(ctx, changed, dir, v7TextFunc(func(context.Context, TextRequest) (TextResponse, error) {
		calls++
		return TextResponse{}, errors.New("completed campaign must not call a model")
	}))
	if err != nil || calls != 0 || !reflect.DeepEqual(again, completed) {
		t.Fatalf("completed campaign replayed: calls=%d result=%+v error=%v", calls, again, err)
	}
	t.Log("real_campaign=completed confirmed_meta_contract_retained=true prior_drone_vector_replayed=false completed_resume_model_calls=0")
}

func TestV007EvolutionFailureKeepsDurableCheckpoint(t *testing.T) {
	for _, status := range []string{"running", "completed"} {
		t.Run(status, func(t *testing.T) {
			c := v7MetaConfig(t)
			e, err := openEvolution(c, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer e.close()
			phase := "assess"
			if status == "completed" {
				phase = "completed"
			}
			saved := evolutionState{EvolutionResult: EvolutionResult{ID: 1, Status: status, Phase: phase, Result: "durable result"}, MetaContract: v7ContractText("saved-meta"), Contract: v7ContractText("saved-task"), ReferenceID: 4, MethodID: 7, TrialID: 9}
			v7MetaExec(t, e.store, "INSERT INTO mq_evolutions(id,problem_type,status,state,created,updated) VALUES(?,?,?,?,?,?)", 1, "fixture", status, jsonText(saved), now(), now())
			e.state = evolutionState{EvolutionResult: EvolutionResult{ID: 1, Status: "running", Phase: "breed"}, MethodID: 2, TrialID: 3}
			_, failure := e.failed(context.Background(), errors.New("simulated uncertain commit"))
			if failure == nil || e.state.MethodID != 7 || e.state.TrialID != 9 || e.state.Phase != phase || e.state.MetaContract != saved.MetaContract {
				t.Fatalf("failure rewound durable checkpoint: state=%+v error=%v", e.state, failure)
			}
			rows := v7MetaQuery(t, e.store, "SELECT status,state FROM mq_evolutions WHERE id=1")
			var persisted evolutionState
			if err := json.Unmarshal([]byte(str(rows[0]["state"])), &persisted); err != nil {
				t.Fatal(err)
			}
			if status == "completed" {
				if !reflect.DeepEqual(persisted, saved) || str(rows[0]["status"]) != "completed" {
					t.Fatalf("completed campaign overwritten: %+v", persisted)
				}
			} else if persisted.Status != "failed" || persisted.MethodID != 7 || persisted.TrialID != 9 || persisted.Phase != "assess" {
				t.Fatalf("failure did not retain committed continuation: %+v", persisted)
			}
		})
	}
}

func TestV007ReturnedEvolutionTrialFinalizesWithoutModelReplay(t *testing.T) {
	ctx := context.Background()
	c := v7Config(t)
	dir, trialID := t.TempDir(), int64(7)
	trial := evolutionTrialConfig(c, trialID)
	calls := 0
	client := v7TextFunc(func(context.Context, TextRequest) (TextResponse, error) {
		calls++
		return TextResponse{}, errors.New("returned trial must not call a model")
	})
	h, err := OpenHive(trial, dir, client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	contract := v7ContractText("trial-task")
	requestID, teamID := v7SeedRequest(t, h, contract)
	answer := "The already selected answer must survive."
	candidateID := v7MetaSavedReturn(t, h, teamID, answer)
	// The RETURN and request finalization committed, but the owning run still
	// says running: this models interruption before the outer run cleanup.
	h.Close()
	evidence, result, err := runEvolutionTrial(ctx, c, dir, trialID, contract, client, nil)
	if err != nil || calls != 0 || result.RequestID != requestID || result.Status != "returned" || result.Answer != answer || !reflect.DeepEqual(result.SelectedCandidateIDs, []int64{candidateID}) {
		t.Fatalf("returned trial recovery failed: calls=%d result=%+v error=%v", calls, result, err)
	}
	if len(evidence.Runs) != 1 || str(evidence.Runs[0]["status"]) != "completed" {
		t.Fatalf("latest owning trial run remained unfinished: %v", evidence.Runs)
	}
	artifact, err := os.ReadFile(filepath.Join(dir, trial.Files.Result))
	if err != nil || string(artifact) != answer {
		t.Fatalf("selected answer artifact changed: body=%q error=%v", artifact, err)
	}
	t.Log("returned_trial_model_calls=0 latest_run_finalized=true selected_answer_preserved=true")
}
