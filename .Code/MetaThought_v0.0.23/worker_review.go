package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
)

// Formal review keeps original research even when its author has contributed again.
func (h *Hive) workerTeamReviewInput(ctx context.Context, handoff string, criteria []ContractCriterion, inquiryID int64) (map[string]any, error) {
	inquiry, err := h.query(ctx, "convergence_inquiry", "hive_inquiries", "SELECT id,question FROM hive_inquiries WHERE id=? AND request_id=?", inquiryID, h.requestID)
	if err != nil {
		return nil, err
	}
	if len(inquiry) != 1 {
		return nil, errors.New("team inquiry is missing")
	}
	members, err := h.query(ctx, "convergence_members", "hive_inquiry_members", "SELECT inquiry_id,drone_id FROM hive_inquiry_members WHERE inquiry_id=? ORDER BY drone_id", inquiryID)
	if err != nil {
		return nil, err
	}
	work, err := h.query(ctx, "convergence_member_work", "hive_inquiry_work", `SELECT w.* FROM hive_inquiry_work w JOIN hive_inquiry_members m ON m.inquiry_id=w.inquiry_id AND m.drone_id=w.drone_id WHERE w.inquiry_id=? AND w.kind IN ('PROPOSE','JOIN','CONTRIBUTE','RESEARCH') AND NOT EXISTS(SELECT 1 FROM hive_inquiry_work later WHERE later.inquiry_id=w.inquiry_id AND later.drone_id=w.drone_id AND later.kind IN ('PROPOSE','JOIN','CONTRIBUTE','RESEARCH') AND later.id>w.id) ORDER BY w.id`, inquiryID)
	if err != nil {
		return nil, err
	}
	connections, err := h.query(ctx, "convergence_connections", "hive_inquiry_work", "SELECT * FROM hive_inquiry_work WHERE kind='CONNECT' AND (inquiry_id=? OR target_inquiry_id=?) ORDER BY id", inquiryID, inquiryID)
	if err != nil {
		return nil, err
	}
	research, err := h.latestWorkerResearch(ctx, inquiryID)
	if err != nil {
		return nil, err
	}
	history, err := h.query(ctx, "convergence_research_history", "hive_inquiry_work", "SELECT * FROM hive_inquiry_work WHERE inquiry_id=? AND kind='RESEARCH' ORDER BY id", inquiryID)
	if err != nil {
		return nil, err
	}
	input := map[string]any{"purpose": "team_report", "handoff": handoff, "inquiry": inquiry[0], "committed_members": members,
		"work": work, "work_selection": "latest substantive saved contribution per committed member; earlier original records remain saved", "connections": connections, "research_work": research, "research_history": history, "scoring_elements": criteria}
	return input, nil
}

// ReviewSavedTeam assesses one existing report without writing to its source hive.
// The same input builder and scoring parser are used by ordinary convergence.
func ReviewSavedTeam(ctx context.Context, c Config, baseline, dir string, reportID int64, client TextClient) (result map[string]any, err error) {
	if reportID < 1 {
		return nil, errors.New("a saved report ID is required")
	}
	if err = os.Mkdir(dir, 0700); err != nil {
		return nil, err
	}
	result = map[string]any{"report_id": reportID, "source": baseline, "source_state_mode": "ro", "hive_started": false, "model": c.Models.Drone, "calls": 0}
	defer func() {
		if err != nil {
			result["outcome"], result["error"] = "error", err.Error()
		}
		data, encodeErr := json.MarshalIndent(result, "", "  ")
		if encodeErr == nil {
			encodeErr = writeNew(filepath.Join(dir, "review-result.json"), append(data, '\n'))
		}
		err = errors.Join(err, encodeErr)
	}()
	logger, err := OpenLogger(filepath.Join(dir, c.Files.Debug), c.Schema.Logs)
	if err != nil {
		return result, err
	}
	defer logger.Close()
	log := &ContextLog{Sink: logger, Fields: map[string]any{"worker": "saved-team-review", "phase": "team_assessment", "source": baseline}}
	statePath, err := filepath.Abs(filepath.Join(baseline, c.Files.State))
	if err != nil {
		return result, err
	}
	uriPath := filepath.ToSlash(statePath)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	stateURI := (&url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=ro"}).String()
	store, err := OpenStore(stateURI, nil, log)
	if err != nil {
		return result, err
	}
	defer store.Close()
	h := &Hive{Config: c, Dir: dir, Store: store, Logger: logger, Log: log}
	reports, err := h.query(ctx, "saved_review_report", "hive_inquiry_work", "SELECT w.id,w.inquiry_id,w.drone_id,w.body,i.request_id,r.run_id,r.contract FROM hive_inquiry_work w JOIN hive_inquiries i ON i.id=w.inquiry_id JOIN hive_requests r ON r.id=i.request_id WHERE w.id=? AND w.kind='TEAM_REPORT'", reportID)
	if err != nil {
		return result, err
	}
	if len(reports) != 1 {
		return result, fmt.Errorf("saved team report %d not found", reportID)
	}
	report := reports[0]
	h.requestID, h.runID = id(report["request_id"]), id(report["run_id"])
	log.Fields["request_id"], log.Fields["run_id"] = h.requestID, h.runID
	criteria, err := workerHandoffCriteria(str(report["contract"]))
	if err != nil {
		return result, err
	}
	original, err := h.query(ctx, "saved_review_assessor", "hive_inquiry_work", "SELECT drone_id FROM hive_inquiry_work WHERE inquiry_id=? AND kind='TEAM_ASSESSMENT' AND id>? ORDER BY id LIMIT 1", report["inquiry_id"], reportID)
	if err != nil {
		return result, err
	}
	if len(original) != 1 || id(original[0]["drone_id"]) == id(report["drone_id"]) {
		return result, errors.New("saved report needs its different original assessor")
	}
	assessor := id(original[0]["drone_id"])
	input, err := h.workerTeamReviewInput(ctx, str(report["contract"]), criteria, id(report["inquiry_id"]))
	if err != nil {
		return result, err
	}
	input["purpose"], input["drone_id"] = "team_assessment", assessor
	input["report"] = map[string]any{"id": reportID, "drone_id": report["drone_id"], "body": report["body"]}
	prompt := c.Prompts["worker_contract_authority"] + "\n\n" + c.Prompts["worker_team_assess"]
	if err = writeNew(filepath.Join(dir, "review-input.json"), []byte(jsonText(input))); err != nil {
		return result, err
	}
	if err = writeNew(filepath.Join(dir, "review-prompt.txt"), []byte(prompt)); err != nil {
		return result, err
	}
	result["inquiry_id"], result["assessor_id"] = report["inquiry_id"], assessor
	log.Fields["drone_id"] = assessor
	if client == nil {
		client, err = NewMetaClient(c.Meta, log)
		if err != nil {
			return result, err
		}
	}
	h.Text = &LoggingClient{Inner: client, Log: log}
	reply, callErr := h.generatePrompt(ctx, "drone", c.Models.Drone, "worker_team_assess", prompt, jsonText(input), "")
	result["calls"] = h.Text.Calls
	if callErr != nil {
		return result, callErr
	}
	if err = writeNew(filepath.Join(dir, "review-reply.txt"), []byte(reply)); err != nil {
		return result, err
	}
	assessments, local, err := parseWorkerAssessmentReply(reply, criteria, true)
	if err != nil {
		return result, err
	}
	result["outcome"], result["assessments"] = "assessed", assessments
	result["relevance"], result["inquiry_value"] = local.Relevance, local.InquiryValue
	log.Log("hive", "saved_team_review_finished", map[string]any{"report_id": reportID, "outcome": "assessed", "source_state_mode": "ro"})
	return result, nil
}

func executeSavedTeamReview(args []string) error {
	f := flag.NewFlagSet("review-team", flag.ContinueOnError)
	configPath := f.String("config", "config.json", "configuration with current review prompts")
	baseline := f.String("baseline", "", "saved hive folder to read only")
	dir := f.String("dir", "", "fresh output folder for this assessment")
	report := f.Int64("report", 0, "saved team report ID")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 || *baseline == "" || *dir == "" || *report < 1 {
		return errors.New("review-team requires -baseline, -report and a fresh -dir")
	}
	c, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	result, runErr := ReviewSavedTeam(ctx, c, *baseline, *dir, *report, nil)
	return errors.Join(runErr, json.NewEncoder(os.Stdout).Encode(result))
}
