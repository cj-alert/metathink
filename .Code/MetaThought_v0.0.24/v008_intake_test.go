package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func v8IntakeConfig(t *testing.T) Config {
	t.Helper()
	c := v7Config(t)
	c.Intake.Instructions = "Exact intake: intention → criteria.\r\nKeep uncertainty.  \n"
	c.Experiment.Objective, c.Experiment.Intent, c.Experiment.Context = "PRESET_LEAK objective", "PRESET_LEAK intent", "PRESET_LEAK context"
	c.Experiment.Criteria = []Criterion{{Name: "PRESET_LEAK criterion", Description: "PRESET_LEAK target"}}
	c.MetaQueen.Objective, c.MetaQueen.Context, c.MetaQueen.ProblemType = "PRESET_LEAK meta objective", "PRESET_LEAK meta context", "PRESET_LEAK problem"
	c.MetaQueen.Criteria = []Criterion{{Name: "PRESET_LEAK meta criterion"}}
	c.MetaQueen.Targets = []string{"PRESET_LEAK target"}
	return c
}

func v8AssertInput(t *testing.T, c Config, request TextRequest, question, prior, feedback string, meta bool) {
	t.Helper()
	wantScope := "queen"
	if meta {
		wantScope = "meta_queen"
	}
	if request.Model != c.Models.Queen || request.Scope != wantScope || len(request.Messages) != 2 {
		t.Fatalf("wrong planning route: %+v", request)
	}
	if request.Messages[0]["role"] != "developer" || request.Messages[0]["content"] != c.Prompts["contract"] || request.Messages[1]["role"] != "user" {
		t.Fatal("planning changed the contract prompt or message roles")
	}
	input := str(request.Messages[1]["content"])
	if strings.Contains(input, "PRESET_LEAK") {
		t.Fatal("a preset objective, criterion or target leaked into raw-question intake")
	}
	var received map[string]string
	if err := json.Unmarshal([]byte(input), &received); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"instructions": c.Intake.Instructions, "question": question}
	if prior != "" {
		want["prior_draft"] = prior
	}
	if feedback != "" {
		want["feedback"] = feedback
	}
	if !reflect.DeepEqual(received, want) {
		t.Fatalf("intake was changed or seeded: got %#v want %#v", received, want)
	}
}

func v8AssertFile(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != expected {
		t.Fatalf("saved output changed: %s error=%v got=%q want=%q", path, err, data, expected)
	}
}

func v8AssertPlanningOnly(t *testing.T, dir string, c Config, meta bool, terminal string, count int) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, c.Files.State)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("drafting created state or an obligation: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, c.Files.Debug))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var hiveTables int
	if err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('hive_requests','hive_drones','hive_teams','mq_inquiries')").Scan(&hiveTables); err != nil || hiveTables != 0 {
		t.Fatalf("planning initialized operational tables: count=%d error=%v", hiveTables, err)
	}
	table, other := "hive_log", "mq_log"
	if meta {
		table, other = other, table
	}
	var actual, wrongScope int
	if err = db.QueryRow("SELECT count(*) FROM "+table+" WHERE event=?", terminal).Scan(&actual); err != nil || actual != count {
		t.Fatalf("saved terminal diagnostics: count=%d want=%d error=%v", actual, count, err)
	}
	if err = db.QueryRow("SELECT count(*) FROM " + other + " WHERE event LIKE 'contract_%'").Scan(&wrongScope); err != nil || wrongScope != 0 {
		t.Fatalf("planning diagnostics crossed scope: count=%d error=%v", wrongScope, err)
	}
}

func TestV008ExactQuestionIntakeAndRevisionPreserved(t *testing.T) {
	for _, meta := range []bool{false, true} {
		name := "base"
		if meta {
			name = "meta"
		}
		t.Run(name, func(t *testing.T) {
			c, dir := v8IntakeConfig(t), t.TempDir()
			question := "What could explain α?\r\nInclude uncertainty — do not assume a solution.  \n"
			first := strings.TrimSuffix(v7ContractText("first proposal", "novelty", "testability"), "\n")
			second := v7ContractText("revised proposal", "support", "falsifiability") + "  \r\n\t"
			feedback := "Revise the criteria, not my question.\r\nPreserve α and uncertainty.  "
			calls := 0
			client := v7TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
				calls++
				if calls == 1 {
					v8AssertInput(t, c, request, question, "", "", meta)
					return TextResponse{Text: first, Model: request.Model}, nil
				}
				v8AssertInput(t, c, request, question, first, feedback, meta)
				return TextResponse{Text: second, Model: request.Model}, nil
			})
			firstPath, secondPath := filepath.Join(dir, "proposal.md"), filepath.Join(dir, "revision.md")
			text, err := PlanQuestion(context.Background(), c, dir, firstPath, question, "", "", client, meta)
			if err != nil || text != first {
				t.Fatalf("first proposal: %v %q", err, text)
			}
			v8AssertPlanningOnly(t, dir, c, meta, "contract_drafted", 1)
			text, err = PlanQuestion(context.Background(), c, dir, secondPath, question, first, feedback, client, meta)
			if err != nil || text != second || calls != 2 {
				t.Fatalf("revision: calls=%d error=%v reply=%q", calls, err, text)
			}
			v8AssertFile(t, firstPath, first)
			v8AssertFile(t, secondPath, second)
			v8AssertPlanningOnly(t, dir, c, meta, "contract_drafted", 2)
			t.Logf("%s: exact question/intake/prior/feedback; two unchanged drafts; no hive; injected calls=%d", name, calls)
		})
	}
}

func TestV008RejectedDraftSavedBeforeValidation(t *testing.T) {
	cases := []struct {
		name  string
		reply TextResponse
		err   error
		want  string
		meta  bool
	}{
		{"malformed", TextResponse{Text: "Not a contract.  \r\n\t"}, nil, "Not a contract.  \r\n\t", false},
		{"refused_with_text", TextResponse{Text: "Original refusal reply.  ", Refusal: "refused"}, nil, "Original refusal reply.  ", true},
		{"refusal_only", TextResponse{Refusal: "Exact refusal only.\r\n"}, nil, "Exact refusal only.\r\n", false},
		{"partial_provider_error", TextResponse{Text: "Partial reply\n "}, errors.New("fixture provider error"), "Partial reply\n ", true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			c, dir := v8IntakeConfig(t), t.TempDir()
			path := filepath.Join(dir, "rejected.md")
			calls := 0
			text, err := PlanQuestion(context.Background(), c, dir, path, "Exact question", "", "", v7TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
				calls++
				v8AssertInput(t, c, request, "Exact question", "", "", test.meta)
				return test.reply, test.err
			}), test.meta)
			if err == nil || text != test.want || calls != 1 {
				t.Fatalf("rejected reply changed, accepted or retried: calls=%d text=%q err=%v", calls, text, err)
			}
			v8AssertFile(t, path, test.want)
			v8AssertPlanningOnly(t, dir, c, test.meta, "contract_rejected", 1)
			t.Logf("%s preserved exact rejected reply; one injected call; no hive", test.name)
		})
	}
}

func TestV008ExistingProposalPreventsProviderReplay(t *testing.T) {
	c, dir := v8IntakeConfig(t), t.TempDir()
	path := filepath.Join(dir, "proposal.md")
	saved := "Preserve even a rejected prior proposal.  \n"
	if err := os.WriteFile(path, []byte(saved), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	_, err := PlanQuestion(context.Background(), c, dir, path, "Question", "", "", v7TextFunc(func(context.Context, TextRequest) (TextResponse, error) {
		calls++
		return TextResponse{}, errors.New("must not call")
	}), false)
	if err == nil || !strings.Contains(err.Error(), "preserve draft output") || calls != 0 {
		t.Fatalf("existing proposal replayed: calls=%d error=%v", calls, err)
	}
	v8AssertFile(t, path, saved)
	v8AssertPlanningOnly(t, dir, c, false, "contract_drafted", 0)
}

func TestV008CLIGuardsBeforeConfigurationOrProvider(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"run", "-contract", "reviewed.md"}, "run requires -contract and -confirm"},
		{[]string{"experiment", "-meta-contract", "reviewed.md"}, "experiment requires -meta-contract and -confirm"},
		{[]string{"evolve", "-meta-contract", "reviewed.md"}, "evolve requires -meta-contract and -confirm"},
		{[]string{"plan", "-out", "proposal.md"}, "plan requires -question and -out"},
		{[]string{"meta-plan", "-question", "question.txt"}, "meta-plan requires -question and -out"},
		{[]string{"plan", "-question", "question.txt", "-out", "proposal.md", "-confirm"}, "-confirm applies only"},
		{[]string{"meta-plan", "-question", "question.txt", "-out", "proposal.md", "-confirm"}, "-confirm applies only"},
	}
	for _, test := range cases {
		t.Run(strings.Join(test.args, "_"), func(t *testing.T) {
			base := t.TempDir()
			dir := filepath.Join(base, "must-not-exist")
			args := append(append([]string{}, test.args...), "-config", filepath.Join(base, "missing.json"), "-dir", dir)
			before := v7NetworkAttempts.Load()
			err := execute(args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("wrong preflight rejection: %v", err)
			}
			if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) || v7NetworkAttempts.Load() != before {
				t.Fatalf("guard created a runtime folder or called HTTP: %v", err)
			}
		})
	}
}

func TestV008MetaCLIAllowsSavedConfigurationAndRevision(t *testing.T) {
	c, dir := v8IntakeConfig(t), t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	question, prior, feedback, out := filepath.Join(dir, "question.txt"), filepath.Join(dir, c.Files.Contract), filepath.Join(dir, "feedback.txt"), filepath.Join(dir, "revision.md")
	for path, body := range map[string]string{question: "Original question\n", prior: v7ContractText("prior"), feedback: "Exact feedback  ", out: "Already saved proposal"} {
		if err = os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	before := v7NetworkAttempts.Load()
	err = execute([]string{"meta-plan", "-config", configPath, "-dir", dir, "-question", question, "-draft", prior, "-feedback", feedback, "-out", out})
	if err == nil || !strings.Contains(err.Error(), "preserve draft output") {
		t.Fatalf("saved config/prior draft blocked meta planning before exclusive-output boundary: %v", err)
	}
	if v7NetworkAttempts.Load() != before {
		t.Fatal("exclusive-output rejection called HTTP")
	}
	v8AssertFile(t, out, "Already saved proposal")
	v8AssertPlanningOnly(t, dir, c, true, "contract_drafted", 0)
}

func TestV008GeneratedProposalSuppliesExecutedCriteria(t *testing.T) {
	c, dir := v8IntakeConfig(t), t.TempDir()
	proposal := v7ContractText("generated then reviewed", "generated_evidence", "generated_testability")
	path := filepath.Join(dir, "proposal.md")
	_, err := PlanQuestion(context.Background(), c, dir, path, "Question with no preset rubric", "", "", v7TextFunc(func(_ context.Context, request TextRequest) (TextResponse, error) {
		v8AssertInput(t, c, request, "Question with no preset rubric", "", "", false)
		return TextResponse{Text: proposal, Model: request.Model}, nil
	}), false)
	if err != nil {
		t.Fatal(err)
	}
	v8AssertPlanningOnly(t, dir, c, false, "contract_drafted", 1)
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture := v007NewBaseTextFixture(t, string(saved))
	h := v7OpenHive(t, fixture)
	v007PrepareBaseFixture(h, t)
	result, err := h.Run(context.Background(), string(saved), 0)
	if err != nil {
		t.Fatal(err)
	}
	v007AssertBaseReturn(t, h, fixture, result)
	t.Logf("saved proposal became the explicit runtime contract; generated criteria reached both drone scorers and Queen; request=%d", result.RequestID)
}
