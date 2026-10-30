package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type WorkerCompletion struct {
	Status      string `json:"status"`
	Answer      string `json:"answer"`
	Calls       int    `json:"calls"`
	CriteriaMet *bool  `json:"criteria_met"`
}

type workerTeamReport struct {
	ID             int64                 `json:"id"`
	InquiryID      int64                 `json:"inquiry_id"`
	DroneID        int64                 `json:"drone_id"`
	Body           string                `json:"body"`
	AssessmentID   int64                 `json:"assessment_id"`
	AssessorID     int64                 `json:"assessor_id"`
	Assessments    []CriterionAssessment `json:"assessments"`
	TargetStatuses []map[string]any      `json:"target_statuses"`
}

type workerReturn struct {
	Action   InquiryAction
	Reports  []int64
	Criteria string
}

var handoffTargetLine = regexp.MustCompile(`^\s*(?:(?:[0-9]+|[A-Za-z])\.\s+)?([A-Za-z][A-Za-z0-9_ -]*?)\s*(>=|<=|>|<|=)\s*([^\s]+)\s*$`)

// The accepted legacy handoff has standalone target lines, repeated in its
// definitions. Extract only their declared names and exact comparisons. The
// complete saved handoff remains the authority for definitions and uncertainty.
func workerHandoffCriteria(handoff string) ([]ContractCriterion, error) {
	contract, err := ParseContract(handoff)
	if err == nil {
		return contract.Criteria, nil
	}
	if strings.Contains(handoff, "# CRITERIA") {
		return nil, err
	}
	var criteria []ContractCriterion
	seen := map[string]ContractCriterion{}
	for _, line := range strings.Split(handoff, "\n") {
		parts := handoffTargetLine.FindStringSubmatch(line)
		if parts == nil {
			continue
		}
		name := strings.TrimSpace(parts[1])
		target, err := parseContractTarget(parts[2] + " " + parts[3])
		if err != nil {
			return nil, fmt.Errorf("handoff criterion %q: %w", name, err)
		}
		key := strings.ToLower(name)
		if previous, exists := seen[key]; exists {
			if previous.Name != name || *previous.Target != *target {
				return nil, fmt.Errorf("handoff criterion %q has conflicting declarations", name)
			}
			continue
		}
		criterion := ContractCriterion{Name: name, Target: target}
		seen[key] = criterion
		criteria = append(criteria, criterion)
	}
	if len(criteria) == 0 {
		return nil, errors.New("saved handoff has no unambiguous declared scoring criteria")
	}
	return criteria, nil
}

// Additional envelope fields are separated for parsing only; the original
// response is what gets persisted. Ordinary action parsing remains unchanged.
func workerExtraSections(text string, names ...string) (string, map[string]string, error) {
	allowed, values := map[string]bool{}, map[string]string{}
	for _, name := range names {
		allowed[name] = true
	}
	var base strings.Builder
	current := ""
	for _, original := range strings.Split(text, "\n") {
		line := strings.TrimSpace(original)
		if strings.HasPrefix(line, "# ") {
			name := strings.TrimPrefix(line, "# ")
			if allowed[name] {
				if _, exists := values[name]; exists {
					return "", nil, fmt.Errorf("duplicate heading %q", name)
				}
				values[name], current = "", name
				continue
			}
			current = ""
		}
		if current != "" {
			values[current] += original + "\n"
		} else {
			base.WriteString(original + "\n")
		}
	}
	for _, name := range names {
		values[name] = strings.TrimSpace(values[name])
		if values[name] == "" {
			return "", nil, fmt.Errorf("missing or empty heading %q", name)
		}
	}
	return base.String(), values, nil
}

func parseWorkerAssessment(text string, criteria []ContractCriterion) ([]CriterionAssessment, InquiryAction, error) {
	return parseWorkerAssessmentReply(text, criteria, false)
}

// Saved numeric profiles predate Reason. New live profiles require it, while
// an explicit incomplete profile always needs its reason and available evidence.
func parseWorkerAssessmentReply(text string, criteria []ContractCriterion, requireReason bool) ([]CriterionAssessment, InquiryAction, error) {
	body, extra, err := workerExtraSections(text, "YOUR SCORE")
	if err != nil {
		return nil, InquiryAction{}, err
	}
	assessments, err := parseCriterionAssessments(body, criteria, true, requireReason)
	if err != nil {
		return nil, InquiryAction{}, err
	}
	// Reuse the existing strict local-score parser, with no model-facing prompt
	// or generated-content substitution. Only the score section is parsed here.
	local, err := parseScoredWorkerAction("# ACTION\nPASS\n# YOUR ANSWER\nassessment\n# YOUR SCORE\n"+extra["YOUR SCORE"], map[string]int{"PASS": 0})
	return assessments, local, err
}

func parseWorkerReturn(text string) (workerReturn, error) {
	body, extra, err := workerExtraSections(text, "REPORTS", "CRITERIA")
	if err != nil {
		return workerReturn{}, err
	}
	action, err := parseScoredWorkerAction(body, map[string]int{"RETURN": 0, "CONTINUE": 0, "BLOCKED": 0})
	if err != nil {
		return workerReturn{}, err
	}
	result := workerReturn{Action: action, Criteria: extra["CRITERIA"]}
	if result.Criteria != "MET" && result.Criteria != "UNMET" && result.Criteria != "UNCERTAIN" {
		return result, errors.New("CRITERIA must be MET, UNMET or UNCERTAIN")
	}
	seen := map[int64]bool{}
	for _, value := range strings.Split(extra["REPORTS"], ",") {
		value = strings.TrimSpace(value)
		for _, digit := range value {
			if digit < '0' || digit > '9' {
				return result, errors.New("REPORTS requires positive integer report IDs separated by commas")
			}
		}
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 || seen[id] {
			return result, errors.New("REPORTS requires distinct positive integer report IDs")
		}
		seen[id] = true
		result.Reports = append(result.Reports, id)
	}
	return result, nil
}

// A blocked inquiry must identify an external dependency, rather than treating
// a low score as exhaustion. The Queen supplies the substantive explanation;
// this parser only checks that each required explanation was actually supplied.
func validateWorkerBlocker(body string) error {
	for _, field := range []string{"Blocker:", "Why alternatives cannot progress:", "Needed:"} {
		found := false
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, field) && strings.TrimSpace(strings.TrimPrefix(line, field)) != "" {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("BLOCKED requires a nonempty %s line in YOUR ANSWER", field)
		}
	}
	return nil
}

func workerCriteriaMet(value string) *bool {
	if value == "UNCERTAIN" {
		return nil
	}
	met := value == "MET"
	return &met
}

// CompleteWorkerRequest is called only after the worker pool has drained.
// Reports are delegated within real committed teams; a different contributor
// assesses each report. They gain no standing authority from this delegation.
func (base *Hive) CompleteWorkerRequest(ctx context.Context, client TextClient) (result WorkerCompletion, err error) {
	var calls atomic.Int64
	h, err := workerView(base, client, &calls)
	if err != nil {
		return result, err
	}
	defer func() { result.Calls = int(calls.Load()) }()
	rows, err := h.query(ctx, "completion_request", "hive_requests", "SELECT contract,status,result FROM hive_requests WHERE id=?", h.requestID)
	if err != nil {
		return result, err
	}
	if len(rows) != 1 {
		return result, errors.New("completion request is missing")
	}
	if state := str(rows[0]["status"]); state == "returned" || state == "blocked" {
		saved, err := parseWorkerReturn(str(rows[0]["result"]))
		if err != nil {
			return result, fmt.Errorf("saved worker return: %w", err)
		}
		// Older returned/UNMET outcomes stay readable; this does not authorize a
		// new unmet return or automatically reactivate a blocked inquiry.
		return WorkerCompletion{Status: state, Answer: saved.Action.Body, CriteriaMet: workerCriteriaMet(saved.Criteria)}, nil
	}
	if str(rows[0]["status"]) != "converging" {
		return result, errors.New("worker completion requires the Queen's saved BEGIN_COLLAPSE decision")
	}
	handoff := str(rows[0]["contract"])
	criteria, err := workerHandoffCriteria(handoff)
	if err != nil {
		return result, err
	}
	for _, key := range []string{"worker_team_report", "worker_team_assess", "worker_queen_return", "inquiry_scores", "queen_role", "worker_queen_scores", "worker_contract_authority"} {
		if strings.TrimSpace(h.Config.Prompts[key]) == "" {
			return result, fmt.Errorf("missing prompt %s", key)
		}
	}
	owner := fmt.Sprintf("convergence-%d", time.Now().UnixNano())
	h.Log.Fields["worker"], h.Log.Fields["phase"] = owner, "convergence"
	claimed, err := h.query(ctx, "claim_convergence", "hive_worker_queen", `UPDATE hive_worker_queen SET owner=? WHERE request_id=? AND owner=''
 AND EXISTS(SELECT 1 FROM hive_requests WHERE id=? AND status='converging')
 AND NOT EXISTS(SELECT 1 FROM hive_worker_queue WHERE request_id=? AND owner<>'')
 AND (SELECT COUNT(*) FROM hive_worker_queue WHERE owner<>'')+(SELECT COUNT(*) FROM hive_worker_queen WHERE owner<>'') < ? RETURNING request_id`, owner, h.requestID, h.requestID, h.requestID, h.Config.Workers.Count)
	if err != nil {
		return result, err
	}
	if len(claimed) != 1 {
		return result, errors.New("convergence requires drained workers and an unclaimed Queen")
	}
	defer func() {
		if workerRetryExhausted(err) {
			_, pauseErr := h.pauseWorkerProvider(context.Background(), err, false)
			err = errors.Join(err, pauseErr)
		}
		_, releaseErr := h.exec(context.Background(), "release_convergence", "hive_worker_queen", "UPDATE hive_worker_queen SET owner='' WHERE request_id=? AND owner=?", h.requestID, owner)
		if releaseErr != nil {
			err = errors.Join(err, releaseErr)
		}
	}()
	teams, err := h.observedInquiryTeams(ctx)
	if err != nil {
		return result, err
	}
	if len(teams) == 0 {
		return result, errors.New("convergence requires a team with at least two committed contributors")
	}
	reports := make([]workerTeamReport, 0, len(teams))
	for _, team := range teams {
		report, err := h.completeWorkerTeam(ctx, handoff, criteria, team)
		if err != nil {
			return result, err
		}
		reports = append(reports, report)
	}
	delete(h.Log.Fields, "drone_id")
	h.Log.Fields["phase"] = "queen_return"
	input := jsonText(map[string]any{"purpose": "queen_return", "handoff": handoff, "scoring_elements": criteria, "reports": reports})
	prompt := h.Config.Prompts["worker_contract_authority"] + "\n\n" + h.Config.Prompts["queen_role"] + "\n\n" + h.Config.Prompts["worker_queen_return"] + "\n\n" + h.Config.Prompts["worker_queen_scores"]
	reply, err := h.generatePrompt(ctx, "queen", h.Config.Models.Queen, "worker_queen_return", prompt, input, "")
	if err != nil {
		return result, err
	}
	h.Log.Log("hive", "worker_return_reply", map[string]any{"body": reply})
	returned, err := parseWorkerReturn(reply)
	if err != nil {
		return result, err
	}
	if returned.Action.Kind == "RETURN" && returned.Criteria != "MET" {
		return result, errors.New("new RETURN requires MET; continue useful exploration or identify a concrete external blocker")
	}
	if returned.Action.Kind == "BLOCKED" {
		if err := validateWorkerBlocker(returned.Action.Body); err != nil {
			return result, err
		}
	}
	for _, selected := range returned.Reports {
		var found *workerTeamReport
		for index := range reports {
			if reports[index].ID == selected {
				found = &reports[index]
				break
			}
		}
		if found == nil {
			return result, fmt.Errorf("Queen selected unknown report %d", selected)
		}
		if returned.Criteria == "MET" {
			for index, criterion := range criteria {
				if found.Assessments[index].Incomplete {
					return result, fmt.Errorf("Queen marked MET but report %d has an incomplete assessment for %q", selected, criterion.Name)
				}
				if criterion.Target != nil && !criterion.Target.Met(found.Assessments[index].Score) {
					return result, fmt.Errorf("Queen marked MET but report %d misses target %q", selected, criterion.Name)
				}
			}
		}
	}
	met := workerCriteriaMet(returned.Criteria)
	var savedMet any
	if met != nil {
		savedMet = *met
	}
	status, finishReason := "returned", "convergence_returned"
	var savedResult any = reply
	switch returned.Action.Kind {
	case "CONTINUE":
		status, finishReason, savedResult = "working", "convergence_continued", nil
	case "BLOCKED":
		status, finishReason = "blocked", "convergence_blocked"
	}
	err = h.Store.Transaction(ctx, h.Log.Fields, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
		changed, e := exec("complete_request", "hive_requests", `UPDATE hive_requests SET status=?,result=?,finish_reason=?,criteria_met=? WHERE id=? AND status='converging' AND EXISTS(SELECT 1 FROM hive_worker_queen WHERE request_id=? AND owner=?)`, status, savedResult, finishReason, savedMet, h.requestID, h.requestID, owner)
		if e != nil {
			return e
		}
		n, e := changed.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return errors.New("convergence ownership or request state changed before return")
		}
		if returned.Action.Kind == "CONTINUE" || returned.Action.Kind == "BLOCKED" {
			// The original review is ordinary global guidance, with its score
			// and report references intact. Its ID wakes existing sleeping actors.
			if _, e = exec("continue_guidance", "hive_worker_guidance", "INSERT INTO hive_worker_guidance(request_id,inquiry_id,body,relevance,inquiry_value,created) VALUES(?,0,?,?,?,?)", h.requestID, reply, returned.Action.Relevance, returned.Action.InquiryValue, now()); e != nil {
				return e
			}
		}
		if _, e = exec("finish_convergence", "hive_worker_queen", "UPDATE hive_worker_queen SET owner='',note=? WHERE request_id=? AND owner=?", returned.Action.Body, h.requestID, owner); e != nil {
			return e
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	h.Log.Log("hive", "worker_return_saved", map[string]any{"action": returned.Action.Kind, "report_ids": returned.Reports, "criteria": returned.Criteria, "relevance": returned.Action.Relevance, "inquiry_value": returned.Action.InquiryValue, "outcome": status})
	return WorkerCompletion{Status: status, Answer: returned.Action.Body, CriteriaMet: met}, nil
}

func (h *Hive) completeWorkerTeam(ctx context.Context, handoff string, criteria []ContractCriterion, team InquiryTeam) (workerTeamReport, error) {
	result := workerTeamReport{InquiryID: team.InquiryID}
	cutoff, err := h.workerContractCutoff(ctx)
	if err != nil {
		return result, err
	}
	input, err := h.workerTeamReviewInput(ctx, handoff, criteria, team.InquiryID)
	if err != nil {
		return result, err
	}

	saved, err := h.query(ctx, "saved_team_report", "hive_inquiry_work", "SELECT id,drone_id,body FROM hive_inquiry_work WHERE inquiry_id=? AND phase='convergence' AND kind='TEAM_REPORT' AND id>? ORDER BY id DESC LIMIT 1", team.InquiryID, cutoff)
	if err != nil {
		return result, err
	}
	if len(saved) == 1 {
		input["prior_report"] = saved[0]
		prior, e := h.query(ctx, "prior_team_assessment", "hive_inquiry_work", "SELECT id,drone_id,body FROM hive_inquiry_work WHERE inquiry_id=? AND phase='convergence' AND kind='TEAM_ASSESSMENT' AND id>? AND id>? ORDER BY id LIMIT 1", team.InquiryID, id(saved[0]["id"]), cutoff)
		if e != nil {
			return result, e
		}
		input["prior_assessment"] = workerOne(prior)
		changed, e := h.query(ctx, "team_report_new_work", "hive_inquiry_work", `SELECT id FROM hive_inquiry_work WHERE id>? AND
 ((inquiry_id=? AND kind IN ('PROPOSE','JOIN','CONTRIBUTE','CONNECT','RESEARCH')) OR (target_inquiry_id=? AND kind='CONNECT')) ORDER BY id LIMIT 1`, id(saved[0]["id"]), team.InquiryID, team.InquiryID)
		if e != nil {
			return result, e
		}
		if len(changed) != 0 {
			saved = nil
		}
	}
	if len(saved) == 1 {
		result.ID, result.DroneID, result.Body = id(saved[0]["id"]), id(saved[0]["drone_id"]), str(saved[0]["body"])
	} else {
		result.DroneID = team.Contributors[0]
		h.Log.Fields["drone_id"], h.Log.Fields["phase"] = result.DroneID, "team_report"
		input["drone_id"] = result.DroneID
		prompt := h.Config.Prompts["worker_contract_authority"] + "\n\n" + h.Config.Prompts["worker_team_report"] + "\n\n" + h.Config.Prompts["inquiry_scores"]
		reply, err := h.generatePrompt(ctx, "drone", h.Config.Models.Drone, "worker_team_report", prompt, jsonText(input), "")
		if err != nil {
			return result, err
		}
		h.Log.Log("hive", "worker_team_report_reply", map[string]any{"body": reply, "inquiry_id": team.InquiryID})
		action, err := ParseInquiryAction(reply)
		if err != nil {
			return result, err
		}
		if action.Kind != "CONTRIBUTE" || action.InquiryID != team.InquiryID {
			return result, errors.New("team report must CONTRIBUTE to its own inquiry")
		}
		result.ID, err = h.exec(ctx, "save_team_report", "hive_inquiry_work", "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,'convergence','TEAM_REPORT',?,?,?,?)", team.InquiryID, result.DroneID, reply, action.Relevance, action.InquiryValue, now())
		if err != nil {
			return result, err
		}
		result.Body = reply
	}
	for _, contributor := range team.Contributors {
		if contributor != result.DroneID {
			result.AssessorID = contributor
			break
		}
	}
	if result.AssessorID == 0 {
		return result, errors.New("team report needs a different committed contributor to assess it")
	}
	// Reports and their assessments are appended while workers are drained.
	// The first assessment after the selected latest report belongs to it;
	// older report/assessment pairs remain original evidence.
	saved, err = h.query(ctx, "saved_team_assessment", "hive_inquiry_work", "SELECT id,drone_id,body FROM hive_inquiry_work WHERE inquiry_id=? AND phase='convergence' AND kind='TEAM_ASSESSMENT' AND id>? AND id>? ORDER BY id LIMIT 1", team.InquiryID, result.ID, cutoff)
	if err != nil {
		return result, err
	}
	var assessmentReply string
	if len(saved) == 1 {
		result.AssessmentID, result.AssessorID, assessmentReply = id(saved[0]["id"]), id(saved[0]["drone_id"]), str(saved[0]["body"])
		if result.AssessorID == result.DroneID {
			return result, errors.New("saved report was assessed by its own reporter")
		}
	} else {
		h.Log.Fields["drone_id"], h.Log.Fields["phase"] = result.AssessorID, "team_assessment"
		input["purpose"], input["drone_id"] = "team_assessment", result.AssessorID
		input["report"] = map[string]any{"id": result.ID, "drone_id": result.DroneID, "body": result.Body}
		prompt := h.Config.Prompts["worker_contract_authority"] + "\n\n" + h.Config.Prompts["worker_team_assess"]
		assessmentReply, err = h.generatePrompt(ctx, "drone", h.Config.Models.Drone, "worker_team_assess", prompt, jsonText(input), "")
		if err != nil {
			return result, err
		}
		h.Log.Log("hive", "worker_team_assessment_reply", map[string]any{"body": assessmentReply, "report_id": result.ID, "inquiry_id": team.InquiryID})
	}
	assessments, local, err := parseWorkerAssessmentReply(assessmentReply, criteria, result.AssessmentID == 0)
	if err != nil {
		return result, err
	}
	result.Assessments = assessments
	if result.AssessmentID == 0 {
		result.AssessmentID, err = h.exec(ctx, "save_team_assessment", "hive_inquiry_work", "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,'convergence','TEAM_ASSESSMENT',?,?,?,?)", team.InquiryID, result.AssessorID, assessmentReply, local.Relevance, local.InquiryValue, now())
		if err != nil {
			return result, err
		}
	}
	for index, criterion := range criteria {
		status := map[string]any{"name": criterion.Name, "score": assessments[index].ScoreValue(), "incomplete": assessments[index].Incomplete, "reason": assessments[index].Reason, "target": criterion.Target, "met": nil}
		if criterion.Target != nil && !assessments[index].Incomplete {
			status["met"] = criterion.Target.Met(assessments[index].Score)
		}
		result.TargetStatuses = append(result.TargetStatuses, status)
	}
	h.Log.Log("hive", "worker_team_complete", map[string]any{"inquiry_id": team.InquiryID, "report_id": result.ID, "assessment_id": result.AssessmentID, "outcome": "saved"})
	return result, nil
}

// A confirmed contract revision leaves old reports intact but excludes them
// from the current rubric. Ordinary non-JSON guidance is not a revision.
func (h *Hive) workerContractCutoff(ctx context.Context) (int64, error) {
	rows, err := h.query(ctx, "contract_revision_cutoff", "hive_guidance", `SELECT body FROM hive_guidance WHERE request_id=?
 AND CASE WHEN json_valid(body) THEN json_extract(body,'$.kind') END='contract_revision' ORDER BY id DESC LIMIT 1`, h.requestID)
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	var revision struct {
		ThroughWorkID *int64 `json:"through_work_id"`
	}
	if err := json.Unmarshal([]byte(str(rows[0]["body"])), &revision); err != nil {
		return 0, fmt.Errorf("saved contract revision: %w", err)
	}
	if revision.ThroughWorkID == nil || *revision.ThroughWorkID < 0 {
		return 0, errors.New("saved contract revision has no valid work boundary")
	}
	return *revision.ThroughWorkID, nil
}

// Latest originals under the current rubric are shared as evidence, not rewritten into Queen grades.
// Report sequence provides the assessment association without a second ledger.
func (h *Hive) latestWorkerTeamRecords(ctx context.Context, inquiryID int64) (map[string]any, map[string]any, error) {
	cutoff, err := h.workerContractCutoff(ctx)
	if err != nil {
		return nil, nil, err
	}
	reports, err := h.query(ctx, "latest_team_report", "hive_inquiry_work", "SELECT id,inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created FROM hive_inquiry_work WHERE inquiry_id=? AND phase='convergence' AND kind='TEAM_REPORT' AND id>? ORDER BY id DESC LIMIT 1", inquiryID, cutoff)
	if err != nil || len(reports) == 0 {
		return nil, nil, err
	}
	assessments, err := h.query(ctx, "latest_team_assessment", "hive_inquiry_work", "SELECT id,inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created FROM hive_inquiry_work WHERE inquiry_id=? AND phase='convergence' AND kind='TEAM_ASSESSMENT' AND id>? AND id>? ORDER BY id LIMIT 1", inquiryID, id(reports[0]["id"]), cutoff)
	if err != nil {
		return nil, nil, err
	}
	return reports[0], workerOne(assessments), nil
}
