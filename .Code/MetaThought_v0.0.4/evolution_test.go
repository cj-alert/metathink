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

const evolutionFixtureReturn = "# Findings\nThe retained trials support further study; general improvement is not established.\n"

// Only remote responses are substituted: control decisions still pass through
// the plaintext Decisions parser, and each candidate uses the real hive/store.
type evolutionTextFixture struct {
	hive           metaQueenTextFixture
	actions        []string
	parents        []string
	actionIndex    int
	parentIndex    int
	donorCalls     int
	mutations      int
	assessments    int
	returns        int
	hiveCalls      int
	failAssessment bool
	refuseAction   bool
	requests       []TextRequest
	actionRequests []TextRequest
	parentRequests []TextRequest
	assessRequests []TextRequest
	returnRequest  TextRequest
	assessmentText []string
}

func evolutionFixtureConfig(t *testing.T) Config {
	t.Helper()
	c := metaQueenFixtureConfig(t)
	c.MetaQueen.Targets = []string{"contribute", "expand"}
	c.MetaQueen.Context = "Compare how a proposed counterexample changes subsequent inquiry."
	for _, key := range []string{"action", "parent", "donor", "target", "mutate", "assess", "return"} {
		c.Prompts["mq_"+key] = "EVO_" + strings.ToUpper(key) + "_FIXTURE: perform this one operation."
	}
	return c
}

func evolutionRequestParts(request TextRequest, userOnly bool) []string {
	var parts []string
	for _, message := range request.Messages {
		if userOnly && message["role"] != "user" {
			continue
		}
		if content, ok := message["content"].([]any); ok {
			for _, part := range content {
				if text, ok := part.(string); ok {
					parts = append(parts, text)
				} else if object, ok := part.(map[string]any); ok {
					if text, ok := object["text"].(string); ok {
						parts = append(parts, text)
					}
				}
			}
		} else if text, ok := message["content"].(string); ok {
			parts = append(parts, text)
		}
	}
	return parts
}

func (f *evolutionTextFixture) Complete(ctx context.Context, request TextRequest) (TextResponse, error) {
	f.requests = append(f.requests, request)
	if request.Scope != "meta_queen" {
		f.hiveCalls++
		return f.hive.Complete(ctx, request)
	}
	var operation string
	// Match a direct instruction part, not markers inside serialized method
	// configurations or historical evidence.
	for _, part := range evolutionRequestParts(request, false) {
		for _, key := range []string{"action", "parent", "donor", "target", "mutate", "assess", "return"} {
			if strings.HasPrefix(part, "EVO_"+strings.ToUpper(key)+"_FIXTURE:") {
				operation = key
			}
		}
	}
	var selected, reply string
	switch operation {
	case "action":
		f.actionRequests = append(f.actionRequests, request)
		if f.refuseAction {
			return TextResponse{Refusal: "fixture cannot choose the next evolutionary action", Model: request.Model}, nil
		}
		if f.actionIndex >= len(f.actions) {
			return TextResponse{}, fmt.Errorf("unexpected additional evolutionary action")
		}
		selected = f.actions[f.actionIndex]
		f.actionIndex++
	case "parent":
		f.parentRequests = append(f.parentRequests, request)
		if f.parentIndex >= len(f.parents) {
			return TextResponse{}, fmt.Errorf("unexpected additional parent selection")
		}
		selected = f.parents[f.parentIndex]
		f.parentIndex++
	case "donor":
		f.donorCalls++
		selected = "2"
	case "target":
		selected = "contribute"
	case "mutate":
		f.mutations++
		reply = metaQueenTestMutation
	case "assess":
		f.assessments++
		f.assessRequests = append(f.assessRequests, request)
		if f.failAssessment {
			return TextResponse{}, &ProviderError{Kind: "api_rejection", StatusCode: 503, ProviderCode: "fixture_unavailable", Message: "evolution assessment fixture unavailable"}
		}
		reply = fmt.Sprintf("EVOLUTION_ASSESSMENT_%d:\nThe attributed counterexample changed the inquiry; this evidence does not establish general improvement.\n", f.assessments)
		f.assessmentText = append(f.assessmentText, reply)
	case "return":
		f.returns++
		f.returnRequest = request
		reply = evolutionFixtureReturn
	default:
		return TextResponse{}, fmt.Errorf("unrecognized evolution fixture operation")
	}
	if selected != "" {
		var err error
		reply, err = evolutionChoiceReply(request, selected)
		if err != nil {
			return TextResponse{}, err
		}
	}
	return TextResponse{Text: reply, Model: request.Model}, nil
}

func evolutionChoiceReply(request TextRequest, selected string) (string, error) {
	input := strings.Join(evolutionRequestParts(request, true), "\n")
	options := regexp.MustCompile(`(?m)^([1-9][0-9]*): ("(?:[^"\\]|\\.)*")`).FindAllStringSubmatch(input, -1)
	position := 0
	for _, option := range options {
		value, err := strconv.Unquote(option[2])
		if err == nil && value == selected {
			position, _ = strconv.Atoi(option[1])
		}
	}
	if position == 0 {
		return "", fmt.Errorf("fixture choice %q not offered", selected)
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
	return out.String(), nil
}

func evolutionContainsExact(value any, wanted string) bool {
	switch item := value.(type) {
	case string:
		if item == wanted {
			return true
		}
		trimmed := strings.TrimSpace(item)
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			var decoded any
			if json.Unmarshal([]byte(item), &decoded) == nil {
				return evolutionContainsExact(decoded, wanted)
			}
		}
	case []any:
		for _, child := range item {
			if evolutionContainsExact(child, wanted) {
				return true
			}
		}
	case map[string]any:
		for _, child := range item {
			if evolutionContainsExact(child, wanted) {
				return true
			}
		}
	}
	return false
}

func evolutionRequestContainsExact(request TextRequest, wanted string) bool {
	for _, part := range evolutionRequestParts(request, true) {
		if evolutionContainsExact(part, wanted) {
			return true
		}
	}
	return false
}

type evolutionSavedMethod struct {
	id, parent, donor int64
	operator, target  string
	chromosome        int
	config            Config
}

func evolutionMethods(t *testing.T, db *sql.DB) []evolutionSavedMethod {
	t.Helper()
	rows, err := db.Query("SELECT id,COALESCE(parent_id,0),COALESCE(donor_id,0),operator,target,chromosome,config FROM mq_methods ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var methods []evolutionSavedMethod
	for rows.Next() {
		var method evolutionSavedMethod
		var config string
		var target sql.NullString
		var chromosome sql.NullInt64
		if err := rows.Scan(&method.id, &method.parent, &method.donor, &method.operator, &target, &chromosome, &config); err != nil {
			t.Fatal(err)
		}
		method.target, method.chromosome = target.String, int(chromosome.Int64)
		if err := json.Unmarshal([]byte(config), &method.config); err != nil {
			t.Fatal(err)
		}
		methods = append(methods, method)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return methods
}

func evolutionAssertFixedConditions(t *testing.T, baseline, actual Config, mutated bool) {
	t.Helper()
	if !reflect.DeepEqual(baseline.Models, actual.Models) || !reflect.DeepEqual(baseline.Meta, actual.Meta) ||
		!reflect.DeepEqual(baseline.Experiment, actual.Experiment) || !reflect.DeepEqual(baseline.Hive, actual.Hive) ||
		!reflect.DeepEqual(baseline.Decisions, actual.Decisions) || !reflect.DeepEqual(baseline.MetaQueen.Criteria, actual.MetaQueen.Criteria) {
		t.Fatal("method changed fixed models, evaluation criteria, or hive conditions")
	}
	for key, prompt := range baseline.Prompts {
		if !strings.HasPrefix(key, "mq_") && actual.Prompts[key] != prompt {
			t.Fatalf("method changed unselected hive prompt %q", key)
		}
	}
	expected := append([]Chromosome(nil), baseline.Chromosomes...)
	if mutated {
		expected[0].Contribute = metaQueenTestMutation
	}
	if !reflect.DeepEqual(expected, actual.Chromosomes) {
		t.Fatal("method changed an unselected chromosome or lost the selected instruction")
	}
}

func evolutionAssertNoDiagnostics(t *testing.T, requests []TextRequest) {
	t.Helper()
	for _, request := range requests {
		for _, part := range evolutionRequestParts(request, false) {
			if strings.Contains(part, metaQueenDiagnosticCanary) || strings.Contains(part, `"runs":`) {
				t.Fatal("diagnostic content entered a controller or hive model request")
			}
		}
	}
}

func TestEvolutionMutatesCrossesRetestsAndReturnsFromRetainedEvidence(t *testing.T) {
	c := evolutionFixtureConfig(t)
	baselineDir, baseline, before := metaQueenBaseline(t, c)
	contract, err := os.ReadFile(filepath.Join(baselineDir, c.Files.Contract))
	if err != nil {
		t.Fatal(err)
	}
	client := &evolutionTextFixture{
		hive:    metaQueenTextFixture{config: c},
		actions: []string{"MUTATE", "CROSSOVER", "RETEST", "RETURN"},
		parents: []string{"1", "2"},
	}
	dir := t.TempDir()
	result, err := RunEvolution(context.Background(), c, dir, baselineDir, baseline.RequestID, client)
	if err != nil || result.Status != "completed" || result.Phase != "completed" || result.Result != evolutionFixtureReturn || result.Error != "" {
		t.Fatalf("evolution did not complete honestly: %+v %v", result, err)
	}
	if client.mutations != 1 || client.assessments != 3 || client.returns != 1 || client.hiveCalls == 0 || !client.hive.sawMutation {
		t.Fatalf("mutation, crossover, or retest did not reach actual hive work: %+v", result)
	}
	if client.parentIndex != 2 || client.donorCalls != 0 {
		t.Fatal("single available parent/donor caused an unnecessary model choice")
	}
	db := metaQueenReadDB(t, filepath.Join(dir, c.Files.State))
	methods := evolutionMethods(t, db)
	if len(methods) != 3 || methods[0].id != 1 {
		t.Fatalf("reference or offspring population lost: %+v", methods)
	}
	for index, method := range methods {
		evolutionAssertFixedConditions(t, c, method.config, index > 0)
	}
	if methods[1].parent != 1 || methods[1].donor != 0 || strings.ToUpper(methods[1].operator) != "MUTATE" ||
		methods[2].parent != 1 || methods[2].donor != 2 || strings.ToUpper(methods[2].operator) != "CROSSOVER" ||
		methods[1].target != "contribute" || methods[2].target != "contribute" ||
		methods[1].chromosome != 0 || methods[2].chromosome != 0 {
		t.Fatal("atomic mutation or whole-instruction crossover lineage was not retained")
	}
	rows, err := db.Query("SELECT method_id,status,evidence,assessment FROM mq_trials ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	var trialMethods []int64
	for rows.Next() {
		var method int64
		var status, evidence, assessment string
		if err := rows.Scan(&method, &status, &evidence, &assessment); err != nil {
			t.Fatal(err)
		}
		if status != "completed" || !evolutionContainsExact(evidence, string(contract)) {
			t.Fatalf("trial lost completed status or fixed contract: method=%d status=%s", method, status)
		}
		if len(trialMethods) > 0 && assessment != client.assessmentText[len(trialMethods)-1] {
			t.Fatal("generated assessment was not preserved verbatim")
		}
		trialMethods = append(trialMethods, method)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || !reflect.DeepEqual(trialMethods, []int64{1, 2, 3, 2}) {
		t.Fatalf("retest did not create fresh evidence for the same method: %v %v", trialMethods, err)
	}
	if len(client.actionRequests) != 4 || !evolutionRequestContainsExact(client.actionRequests[1], client.assessmentText[0]) ||
		len(client.parentRequests) != 2 || !evolutionRequestContainsExact(client.parentRequests[0], client.assessmentText[0]) {
		t.Fatal("saved assessment did not inform subsequent action and parent choices")
	}
	for _, assessment := range client.assessmentText {
		if !evolutionRequestContainsExact(client.returnRequest, assessment) {
			t.Fatal("return omitted a retained assessment")
		}
	}
	for _, request := range client.assessRequests {
		input := strings.Join(evolutionRequestParts(request, true), "\n")
		for _, marker := range []string{"peer-work-", "candidate-work-", "drone_id", "tick"} {
			if !strings.Contains(input, marker) {
				t.Fatalf("assessment omitted attributed reference/candidate evidence %q", marker)
			}
		}
	}
	evolutionAssertNoDiagnostics(t, client.requests)
	if !reflect.DeepEqual(before, metaQueenSnapshot(t, baselineDir)) {
		t.Fatal("evolution changed the archived baseline")
	}
	calls := len(client.requests)
	resumed, err := ResumeEvolution(context.Background(), c, dir, client)
	if err != nil || !reflect.DeepEqual(result, resumed) || len(client.requests) != calls {
		t.Fatalf("completed resume replayed work or lost its return: %+v %v", resumed, err)
	}
}

func TestEvolutionAssessmentResumePreservesReturnedHive(t *testing.T) {
	c := evolutionFixtureConfig(t)
	baselineDir, baseline, before := metaQueenBaseline(t, c)
	dir := t.TempDir()
	client := &evolutionTextFixture{
		hive:    metaQueenTextFixture{config: c},
		actions: []string{"MUTATE", "RETURN"}, failAssessment: true,
	}
	failed, err := RunEvolution(context.Background(), c, dir, baselineDir, baseline.RequestID, client)
	if err == nil || failed.Status != "failed" || failed.Phase != "assess" || !strings.Contains(failed.Error, "evolution assessment fixture unavailable") {
		t.Fatalf("assessment failure was hidden or continuation phase lost: %+v %v", failed, err)
	}
	if client.assessments != 1 || client.mutations != 1 || client.hiveCalls == 0 || client.returns != 0 {
		t.Fatal("failed invocation retried or returned despite missing assessment")
	}
	db := metaQueenReadDB(t, filepath.Join(dir, c.Files.State))
	var status, evidence, state string
	if err := db.QueryRow("SELECT status,evidence FROM mq_trials ORDER BY id DESC LIMIT 1").Scan(&status, &evidence); err != nil {
		t.Fatal(err)
	}
	if status != "returned" || !evolutionContainsExact(evidence, "returned") || !strings.Contains(evidence, "candidate-work-") {
		t.Fatal("failed assessment discarded the completed hive trajectory")
	}
	if err := db.QueryRow("SELECT status,state FROM mq_evolutions WHERE id=?", failed.ID).Scan(&status, &state); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || !strings.Contains(state, "evolution assessment fixture unavailable") {
		t.Fatal("failure was not retained in evolution state")
	}
	hiveCalls := client.hiveCalls
	client.failAssessment = false
	resumed, err := ResumeEvolution(context.Background(), c, dir, client)
	if err != nil || resumed.Status != "completed" || resumed.Result != evolutionFixtureReturn {
		t.Fatalf("explicit resume did not finish the retained trial: %+v %v", resumed, err)
	}
	if client.hiveCalls != hiveCalls || client.mutations != 1 || client.assessments != 2 || client.returns != 1 {
		t.Fatal("assessment resume regenerated a method or replayed completed hive calls")
	}
	var methods, trials int
	if err := db.QueryRow("SELECT COUNT(*) FROM mq_methods").Scan(&methods); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM mq_trials").Scan(&trials); err != nil {
		t.Fatal(err)
	}
	if methods != 2 || trials != 2 {
		t.Fatalf("resume duplicated methods or trials: methods=%d trials=%d", methods, trials)
	}
	evolutionAssertNoDiagnostics(t, client.requests)
	if !reflect.DeepEqual(before, metaQueenSnapshot(t, baselineDir)) {
		t.Fatal("failure/resume changed the reference baseline")
	}
}

func TestEvolutionRefusalIsPersistedWithoutOffspring(t *testing.T) {
	c := evolutionFixtureConfig(t)
	baselineDir, baseline, _ := metaQueenBaseline(t, c)
	dir := t.TempDir()
	client := &evolutionTextFixture{hive: metaQueenTextFixture{config: c}, refuseAction: true}
	result, err := RunEvolution(context.Background(), c, dir, baselineDir, baseline.RequestID, client)
	if err == nil || result.Status != "failed" || result.Phase != "action" || result.Error == "" || result.Result != "" {
		t.Fatalf("refusal was treated as an action or successful return: %+v %v", result, err)
	}
	db := metaQueenReadDB(t, filepath.Join(dir, c.Files.State))
	var status, state string
	if err := db.QueryRow("SELECT status,state FROM mq_evolutions WHERE id=?", result.ID).Scan(&status, &state); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || !evolutionContainsExact(state, result.Error) {
		t.Fatal("refusal failure was not retained")
	}
	var methods, trials int
	if err := db.QueryRow("SELECT COUNT(*) FROM mq_methods").Scan(&methods); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM mq_trials").Scan(&trials); err != nil {
		t.Fatal(err)
	}
	if methods != 1 || trials != 1 || client.hiveCalls != 0 || client.mutations != 0 {
		t.Fatal("refusal created offspring, replayed reference, or lost reference evidence")
	}
	logs := metaQueenReadDB(t, filepath.Join(dir, c.Files.Debug))
	var refusals, leaked int
	if err := logs.QueryRow("SELECT COUNT(*) FROM mq_log WHERE event='decision.question.finish' AND outcome='refusal' AND json_extract(details,'$.name')='mq_action'").Scan(&refusals); err != nil {
		t.Fatal(err)
	}
	if err := logs.QueryRow("SELECT COUNT(*) FROM hive_log WHERE scope='meta_queen'").Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if refusals != 1 || leaked != 0 {
		t.Fatal("refusal diagnostics were lost or not isolated")
	}
	evolutionAssertNoDiagnostics(t, client.requests)
}
