package main

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"strings"
)

// DraftMetaContract reuses the base Queen's draft/parser path. Its output is
// only a proposal; callers must obtain user confirmation before starting work.
func DraftMetaContract(ctx context.Context, c Config, client TextClient, sink EventSink) (string, error) {
	log := &metaQueenLog{Sink: sink, Fields: map[string]any{"operation": "meta_contract_draft"}}
	if client == nil {
		var err error
		client, err = NewMetaClient(c.Meta, log)
		if err != nil {
			return "", err
		}
	} else if concrete, ok := client.(*MetaClient); ok {
		copy := *concrete
		copy.Config, copy.Log = c.Meta, log
		client = &copy
	}
	c.Experiment.Objective, c.Experiment.Intent = c.MetaQueen.Objective, c.MetaQueen.Intent
	c.Experiment.Context, c.Experiment.Criteria = c.MetaQueen.Context, c.MetaQueen.Criteria
	h := &Hive{Config: c, Log: &ContextLog{Sink: log, Fields: map[string]any{}}}
	h.Text = &LoggingClient{Inner: metaScopedClient{client}, Log: log}
	return h.Plan(ctx)
}

type metaScopedClient struct{ TextClient }

func (m metaScopedClient) Complete(ctx context.Context, request TextRequest) (TextResponse, error) {
	request.Scope = "meta_queen"
	return m.TextClient.Complete(ctx, request)
}

// Draft seeds are deliberately absent from runtime inputs. The reviewed
// contract is the only source of objectives, context and scoring elements.
func metaRuntimeOptions(c Config) map[string]any {
	return map[string]any{"problem_type": c.MetaQueen.ProblemType, "chromosome": c.MetaQueen.Chromosome,
		"behavior": c.MetaQueen.Behavior, "targets": c.MetaQueen.Targets}
}

type metaInquiry struct {
	ID       int64
	Contract Contract
	Config   Config
	Store    *Store
	Client   TextClient
	Log      EventSink
}

// The inquiry is also the identity of its committed team. Membership is scoped
// to this request; these ordinary drones do not acquire permanent judge roles.
func openMetaInquiry(ctx context.Context, c Config, store *Store, client TextClient, log EventSink, kind string, ownerID int64, reviewed string) (*metaInquiry, error) {
	i := &metaInquiry{Config: c, Store: store, Client: client, Log: log}
	rows, err := store.Query(ctx, "read_meta_inquiry", "mq_inquiries", nil,
		"SELECT id,contract FROM mq_inquiries WHERE kind=? AND owner_id=?", kind, ownerID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 1 {
		i.ID, reviewed = id(rows[0]["id"]), str(rows[0]["contract"])
	}
	i.Contract, err = ParseContract(reviewed)
	if err != nil {
		return nil, fmt.Errorf("confirmed meta contract: %w", err)
	}
	if i.ID != 0 {
		return i, nil
	}
	if c.Hive.DroneCount < 2 || len(c.Chromosomes) == 0 {
		return nil, fmt.Errorf("meta inquiry requires at least two available drones and their chromosomes")
	}
	assign := make([]int, c.Hive.DroneCount)
	for n := range assign {
		assign[n] = n % len(c.Chromosomes)
	}
	rand.New(rand.NewSource(c.Hive.Seed)).Shuffle(len(assign), func(a, b int) { assign[a], assign[b] = assign[b], assign[a] })
	err = store.Transaction(ctx, nil, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
		row, err := exec("create_meta_inquiry", "mq_inquiries",
			"INSERT INTO mq_inquiries(kind,owner_id,contract,status,created) VALUES(?,?,?,?,?)", kind, ownerID, reviewed, "active", now())
		if err != nil {
			return err
		}
		i.ID, err = row.LastInsertId()
		if err != nil {
			return err
		}
		for n, pair := range assign {
			if _, err = exec("seed_meta_drone", "mq_commitments",
				"INSERT INTO mq_commitments(inquiry_id,drone_id,pair,status,created) VALUES(?,?,?,?,?)",
				i.ID, n+1, jsonText(c.Chromosomes[pair]), "pending", now()); err != nil {
				return err
			}
		}
		return nil
	})
	return i, err
}

func (i *metaInquiry) members(ctx context.Context) ([]map[string]any, error) {
	return i.Store.Query(ctx, "read_meta_commitments", "mq_commitments", map[string]any{"inquiry_id": i.ID},
		"SELECT drone_id,pair,status FROM mq_commitments WHERE inquiry_id=? ORDER BY drone_id", i.ID)
}

func (i *metaInquiry) ensureTeam(ctx context.Context, evidence map[string]any) error {
	members, err := i.members(ctx)
	if err != nil {
		return err
	}
	for _, member := range members {
		if str(member["status"]) != "pending" {
			continue
		}
		droneID := id(member["drone_id"])
		log := &metaQueenLog{Sink: i.Log, Fields: map[string]any{"inquiry_id": i.ID, "drone_id": droneID, "actor_kind": "drone"}}
		prompt, err := renderPrompt(i.Config.Prompts["commit"], map[string]any{"Contract": i.Contract, "Chromosome": member["pair"]})
		if err != nil {
			return err
		}
		decisions := NewMetaDecisions(i.Client, i.Config.Decisions, log)
		decisions.Scope = "meta_queen"
		input := map[string]any{"contract": i.Contract, "drone": member, "participants": members, "operational_context": evidence}
		reply, err := decisions.Decide(ctx, DecisionRequest{Model: i.Config.Models.Drone, Input: raw(jsonText(input)),
			Questions: []DecisionQuestion{{Type: "choice", Name: ptr("mq_commit"), Instructions: raw(prompt), Choices: []DecisionChoice{
				{Value: "ACCEPT", Description: ptr("Commit to the shared request")}, {Value: "DECLINE", Description: ptr("Do not commit")},
			}}}})
		if err != nil {
			return err
		}
		if len(reply.Answers) != 1 || reply.Answers[0].Type != "choice" {
			return fmt.Errorf("meta drone %d did not return a commitment choice", droneID)
		}
		choice, _ := reply.Answers[0].Choice.(string)
		status := "accepted"
		if choice == "DECLINE" {
			status = "declined"
		} else if choice != "ACCEPT" {
			return fmt.Errorf("meta drone %d returned an unknown commitment", droneID)
		}
		if _, err = i.Store.Exec(ctx, "meta_commitment", "mq_commitments", log.Fields,
			"UPDATE mq_commitments SET status=? WHERE inquiry_id=? AND drone_id=?", status, i.ID, droneID); err != nil {
			return err
		}
		member["status"] = status
	}
	accepted := 0
	for _, member := range members {
		if str(member["status"]) == "accepted" {
			accepted++
		}
	}
	if accepted < 2 {
		return fmt.Errorf("meta inquiry %d lacks two committed drones", i.ID)
	}
	return nil
}

// Each drone supplies a complete vector using the same parser and prompt as
// base-hive assessment. Persisted vectors survive later failures and are not
// regenerated by an explicit campaign resume.
func (i *metaInquiry) assess(ctx context.Context, subject string, operational map[string]any) error {
	if err := i.ensureTeam(ctx, operational); err != nil {
		return err
	}
	members, err := i.members(ctx)
	if err != nil {
		return err
	}
	for _, member := range members {
		if str(member["status"]) != "accepted" {
			continue
		}
		droneID := id(member["drone_id"])
		fields := map[string]any{"inquiry_id": i.ID, "subject_key": subject, "drone_id": droneID, "actor_kind": "drone"}
		previous, err := i.Store.Query(ctx, "read_meta_vector", "mq_assessments", fields,
			"SELECT id FROM mq_assessments WHERE inquiry_id=? AND subject_key=? AND drone_id=?", i.ID, subject, droneID)
		if err != nil {
			return err
		}
		if len(previous) > 0 {
			continue
		}
		input := cloneLogFields(operational)
		if err = i.attach(ctx, input); err != nil {
			return err
		}
		input["subject"], input["drone"], input["scoring_elements"] = subject, member, i.Contract.Criteria
		prompt, err := renderPrompt(i.Config.Prompts["assess"], map[string]any{"Contract": i.Contract, "Chromosome": member["pair"]})
		if err != nil {
			return err
		}
		log := &metaQueenLog{Sink: i.Log, Fields: fields}
		reply, err := metaText(ctx, i.Client, log, i.Config.Models.Drone, "assess", prompt, input)
		if err != nil {
			return err
		}
		assessments, err := ParseAssessment(reply, i.Contract.Criteria)
		if err != nil {
			log.Log("meta_queen", "assessment_parse", map[string]any{"outcome": "error", "error": err.Error(), "reply": reply})
			return fmt.Errorf("meta drone %d subject %s: %w", droneID, subject, err)
		}
		log.Log("meta_queen", "assessment_parse", map[string]any{"outcome": "parsed", "criterion_count": len(assessments)})
		if err = i.Store.Transaction(ctx, fields, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
			for _, a := range assessments {
				if _, err := exec("meta_assessment", "mq_assessments",
					"INSERT INTO mq_assessments(inquiry_id,subject_key,drone_id,criterion,score,evidence,uncertainty,created) VALUES(?,?,?,?,?,?,?,?)",
					i.ID, subject, droneID, a.Name, a.Score, a.Evidence, a.Uncertainty, now()); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func (i *metaInquiry) attach(ctx context.Context, input map[string]any) error {
	members, err := i.members(ctx)
	if err != nil {
		return err
	}
	scores, err := i.Store.Query(ctx, "read_meta_assessments", "mq_assessments", map[string]any{"inquiry_id": i.ID},
		"SELECT id,subject_key,drone_id,criterion,score,evidence,uncertainty FROM mq_assessments WHERE inquiry_id=? ORDER BY id", i.ID)
	if err != nil {
		return err
	}
	for _, score := range scores {
		for _, criterion := range i.Contract.Criteria {
			if str(score["criterion"]) == criterion.Name && criterion.Target != nil {
				if actual, ok := score["score"].(float64); ok {
					score["target_met"] = criterion.Target.Met(actual)
				}
			}
		}
	}
	input["meta_contract"], input["meta_inquiry_id"] = i.Contract, i.ID
	input["meta_participants"], input["meta_drone_assessments"] = members, scores
	return nil
}

// The reply envelope and assessment protocol stay fixed while behavior varies.
// This explicit import upgrade never rewrites a saved behavior instruction.
func metaFixedProtocols(variant *Config, controller Config) {
	if variant.Prompts == nil {
		variant.Prompts = map[string]string{}
	}
	for _, key := range []string{"assess", "collapse_format"} {
		variant.Prompts[key] = controller.Prompts[key]
	}
}

func metaText(ctx context.Context, client TextClient, log EventSink, model, key, prompt string, input map[string]any) (string, error) {
	messages := []Message{{"role": "developer", "content": prompt}, {"role": "user", "content": jsonText(input)}}
	log.Log("meta_queen", "meta_call_start", map[string]any{"prompt_key": key, "model": model, "messages": messages, "outcome": "started"})
	reply, err := client.Complete(ctx, TextRequest{Model: model, Scope: "meta_queen", Messages: messages})
	if err == nil && reply.Refusal != "" {
		err = fmt.Errorf("%s refused: %s", key, reply.Refusal)
	}
	if err == nil && strings.TrimSpace(reply.Text) == "" {
		err = fmt.Errorf("%s returned no text", key)
	}
	fields := map[string]any{"prompt_key": key, "model": reply.Model, "outcome": "answered", "reply": reply.Text, "refusal": reply.Refusal, "usage": reply.Usage, "raw": string(reply.Raw)}
	if err != nil {
		fields["outcome"], fields["error"] = "error", err.Error()
	}
	log.Log("meta_queen", "meta_call_done", fields)
	return reply.Text, err
}
