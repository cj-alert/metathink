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
	"sync"
	"time"
	"unicode"
)

type DashboardSnapshot struct {
	LatestAnswer      *WorkerAnswer       `json:"latest_answer,omitempty"`
	GeneratedUTC      string              `json:"generated_utc"`
	InstanceName      string              `json:"instance_name"`
	DisplayTitle      string              `json:"display_title"`
	RunFolder         string              `json:"run_folder"`
	RequestID         int64               `json:"request_id"`
	RootInquiryID     int64               `json:"root_inquiry_id"`
	Status            string              `json:"status"`
	Goal              string              `json:"goal"`
	Objective         string              `json:"objective"`
	Criteria          []ContractCriterion `json:"criteria"`
	Result            string              `json:"result"`
	FinishReason      string              `json:"finish_reason"`
	WorkCount         int64               `json:"work_count"`
	TeamCount         int                 `json:"team_count"`
	WorkerLimit       int                 `json:"worker_limit"`
	Queen             DashboardQueen      `json:"queen"`
	Teams             []DashboardTeam     `json:"teams"`
	Drones            []DashboardDrone    `json:"drones"`
	Workers           []DashboardWorker   `json:"workers"`
	Links             []DashboardLink     `json:"links"`
	Assessments       []map[string]any    `json:"assessments"`
	AssessmentHistory []map[string]any    `json:"assessment_history"`
	Reports           []map[string]any    `json:"reports"`
}

type DashboardQueen struct {
	Owner           string                   `json:"owner"`
	FocusID         int64                    `json:"focus_id"`
	ThroughID       int64                    `json:"through_id"`
	Note            string                   `json:"note"`
	Reviewed        []map[string]any         `json:"reviewed"`
	ScoredDecisions []DashboardQueenDecision `json:"scored_decisions"`
}

// A saved Queen decision has its own scores and attribution. It is not a drone
// work item, and its scores must not be attached to an unrelated latest note.
type DashboardQueenDecision struct {
	Source         string  `json:"source"`
	RecordID       int64   `json:"record_id,omitempty"`
	InquiryID      int64   `json:"inquiry_id,omitempty"`
	BirthsReviewed int64   `json:"births_reviewed,omitempty"`
	Body           string  `json:"body"`
	Relevance      float64 `json:"relevance"`
	InquiryValue   float64 `json:"inquiry_value"`
	Created        string  `json:"created"`
}

type DashboardTeam struct {
	ID                     int64          `json:"id"`
	Question               string         `json:"question"`
	FriendlyName           string         `json:"friendly_name"`
	ProposedBy             int64          `json:"proposed_by"`
	Members                []int64        `json:"members"`
	ContributorCount       int64          `json:"contributor_count"`
	ActualContributorCount *int64         `json:"actual_contributor_count,omitempty"`
	WorkCount              int64          `json:"work_count"`
	LatestWork             *DashboardWork `json:"latest_work"`
}

type DashboardDrone struct {
	ID          int64          `json:"id"`
	Name        string         `json:"name"`
	Contribute  string         `json:"contribute"`
	Expand      string         `json:"expand"`
	Memberships []int64        `json:"memberships"`
	LatestWork  *DashboardWork `json:"latest_work"`
}

type DashboardWorker struct {
	Owner   string `json:"owner"`
	Phase   string `json:"phase"`
	DroneID int64  `json:"drone_id"`
	FocusID int64  `json:"focus_id"`
	State   string `json:"state"`
}

type DashboardLink struct {
	WorkID          int64  `json:"work_id"`
	FromID          int64  `json:"from_id"`
	ToID            int64  `json:"to_id"`
	DroneID         int64  `json:"drone_id"`
	Body            string `json:"body"`
	ConnectionCount int64  `json:"connection_count"`
}

// These are presentation labels only. Original questions and contracts remain
// unchanged in the snapshot and in operational storage.
func dashboardFriendlyName(question string) string {
	line := strings.TrimSpace(strings.SplitN(question, "\n", 2)[0])
	line = strings.Trim(line, "#*_` \t\r")
	words := strings.Fields(line)
	for len(words) > 0 {
		switch strings.ToLower(strings.Trim(words[0], ":?")) {
		case "does", "do", "can", "could", "would", "should", "is", "are", "how", "what", "why", "when", "whether", "will", "we", "you", "to":
			words = words[1:]
		default:
			goto content
		}
	}
content:
	line = strings.Join(words, " ")
	lower := strings.ToLower(line)
	end := len(lower)
	for _, boundary := range []string{" — ", " – ", ": ", " (", " due to ", " during ", " explain ", " and ", "?"} {
		if index := strings.Index(lower, boundary); index >= 0 && index < end {
			end = index
		}
	}
	// Unicode lowercasing can change byte width. Keep original text by mapping
	// the boundary's rune count back to the original line.
	short := strings.Fields(string([]rune(line)[:len([]rune(lower[:end]))]))
	if len(short) >= 3 {
		words = short
	}
	cleaned := []string{}
	for _, word := range words {
		word = strings.Trim(word, "#*_`.,:;!?()[]{}\"“”")
		if word != "" && word != "+" && word != "—" && word != "–" {
			cleaned = append(cleaned, word)
		}
		if len(cleaned) == 5 {
			break
		}
	}
	if len(cleaned) == 0 {
		return "Shared Inquiry Exploration"
	}
	if len(cleaned) < 3 {
		cleaned = append([]string{"Inquiry", "Into"}, cleaned...)
	}
	for index, word := range cleaned {
		if index != 0 {
			switch strings.ToLower(word) {
			case "of", "and", "or", "in", "to", "for", "with", "the", "a", "an", "at", "on", "by":
				cleaned[index] = strings.ToLower(word)
				continue
			}
		}
		runes := []rune(word)
		runes[0] = unicode.ToUpper(runes[0])
		cleaned[index] = string(runes)
	}
	return strings.Join(cleaned, " ")
}

func dashboardObjective(handoff string) string {
	lines := strings.Split(handoff, "\n")
	found := false
	var objective []string
	for _, original := range lines {
		line := strings.TrimSpace(original)
		heading := strings.Trim(line, "#*_` \t\r")
		name, value, colon := strings.Cut(heading, ":")
		if !found && strings.EqualFold(strings.TrimSpace(name), "objective") {
			found = true
			if colon && strings.TrimSpace(value) != "" {
				objective = append(objective, strings.TrimSpace(value))
			}
			continue
		}
		if !found {
			continue
		}
		if line == "" {
			if len(objective) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "#") || strings.HasSuffix(line, ":") {
			break
		}
		objective = append(objective, line)
	}
	if len(objective) > 0 {
		return strings.Join(objective, "\n")
	}
	for _, original := range lines {
		line := strings.TrimSpace(original)
		if line != "" && !strings.HasPrefix(line, "#") && !strings.HasSuffix(line, ":") {
			return line
		}
	}
	return "Governing objective not yet recorded."
}

func dashboardDisplayTitle(instance, objective string) string {
	prefix := strings.TrimSpace(strings.SplitN(instance, " - ", 2)[0])
	if prefix == "" {
		prefix = "MetaThought"
	}
	if strings.TrimSpace(objective) == "" {
		return prefix + " - Governing Objective Pending"
	}
	// Compress an explicitly supplied acronym only when its initials match the
	// immediately preceding words. This is domain-independent display text.
	words := []string{}
	for _, token := range strings.Fields(strings.ReplaceAll(objective, "(", " (")) {
		word := strings.Trim(token, "#*_`.,:;!?()[]{}\"“”")
		if word == "" {
			continue
		}
		letters := []rune(word)
		acronym := strings.HasPrefix(token, "(") && len(letters) >= 2
		for _, letter := range letters {
			acronym = acronym && (unicode.IsUpper(letter) || unicode.IsDigit(letter))
		}
		if acronym && len(words) >= len(letters) {
			start := len(words) - len(letters)
			matches := true
			for i, letter := range letters {
				previous := []rune(words[start+i])
				matches = matches && len(previous) > 0 && unicode.ToUpper(previous[0]) == letter
			}
			if matches {
				words = words[:start]
			}
		}
		words = append(words, word)
	}
	stop := map[string]bool{}
	for _, word := range strings.Fields("find identify develop create build provide determine explore investigate examine evaluate assess understand compare propose seek discover help one or more a an the to of for and on how whether is are with which that can could would should will we our solve cure solve/cure") {
		stop[word] = true
	}
	content := []string{}
	for _, word := range words {
		if !stop[strings.ToLower(word)] {
			content = append(content, word)
		}
		if len(content) == 5 {
			break
		}
	}
	if len(content) == 4 {
		switch strings.ToLower(content[2]) {
		case "hypotheses", "solutions", "approaches", "methods", "models", "predictions", "tests", "designs", "strategies", "plans", "ideas", "answers", "explanations", "proposals", "recommendations":
			content = append(content[:3], "for", content[3])
		}
	}
	if len(content) == 0 {
		return prefix + " - " + dashboardFriendlyName(objective)
	}
	return prefix + " - " + dashboardFriendlyName(strings.Join(content, " "))
}

type DashboardWork struct {
	ID           int64   `json:"id"`
	InquiryID    int64   `json:"inquiry_id"`
	DroneID      int64   `json:"drone_id"`
	Phase        string  `json:"phase"`
	Kind         string  `json:"kind"`
	Body         string  `json:"body"`
	Created      string  `json:"created"`
	Relevance    float64 `json:"relevance"`
	InquiryValue float64 `json:"inquiry_value"`
}

func dashboardWork(row map[string]any) *DashboardWork {
	if row == nil {
		return nil
	}
	// SQLite REAL columns are returned as float64; all action scores are REAL.
	relevance, _ := row["relevance"].(float64)
	value, _ := row["inquiry_value"].(float64)
	return &DashboardWork{ID: id(row["id"]), InquiryID: id(row["inquiry_id"]), DroneID: id(row["drone_id"]),
		Phase: str(row["phase"]), Kind: str(row["kind"]), Body: str(row["body"]), Created: str(row["created"]), Relevance: relevance, InquiryValue: value}
}

// Dashboard data is for human inspection only. It never becomes actor context
// and does not read diagnostic prompts, credentials, or prior model replies.
func (h *Hive) dashboardSnapshot(ctx context.Context) (DashboardSnapshot, error) {
	s := DashboardSnapshot{RunFolder: h.Dir, WorkerLimit: h.Config.Workers.Count,
		Teams: []DashboardTeam{}, Drones: []DashboardDrone{}, Workers: []DashboardWorker{}, Links: []DashboardLink{},
		Reports: []map[string]any{}, Assessments: []map[string]any{}, AssessmentHistory: []map[string]any{}}
	s.InstanceName = h.Config.Dashboard.InstanceName
	if s.InstanceName == "" {
		s.InstanceName = filepath.Base(filepath.Clean(h.Dir))
	}
	rows, err := h.query(ctx, "dashboard_request", "hive_requests", "SELECT id,contract,status,result,finish_reason FROM hive_requests WHERE (?=0 OR id=?) ORDER BY id DESC LIMIT 1", h.requestID, h.requestID)
	if err != nil {
		return s, err
	}
	if len(rows) == 0 {
		s.Status = "not_started"
		s.GeneratedUTC = now()
		return s, nil
	}
	r := rows[0]
	s.RequestID, s.Goal, s.Status, s.Result, s.FinishReason = id(r["id"]), str(r["contract"]), str(r["status"]), str(r["result"]), str(r["finish_reason"])
	s.Objective = dashboardObjective(s.Goal)
	s.DisplayTitle = dashboardDisplayTitle(s.InstanceName, s.Objective)
	rows, err = h.query(ctx, "dashboard_queen", "hive_worker_queen", "SELECT owner,focus_id,through_id,note FROM hive_worker_queen WHERE request_id=?", s.RequestID)
	if err != nil {
		return s, err
	}
	if len(rows) == 1 {
		r = rows[0]
		s.Queen = DashboardQueen{Owner: str(r["owner"]), FocusID: id(r["focus_id"]), ThroughID: id(r["through_id"]), Note: str(r["note"])}
	}
	s.Queen.ScoredDecisions = []DashboardQueenDecision{}
	guidance, err := h.query(ctx, "dashboard_queen_scored_guidance", "hive_worker_guidance", "SELECT id,inquiry_id,body,relevance,inquiry_value,created FROM hive_worker_guidance WHERE request_id=? ORDER BY id DESC LIMIT 1", s.RequestID)
	if err != nil {
		return s, err
	}
	if len(guidance) == 1 {
		g := guidance[0]
		relevance, _ := g["relevance"].(float64)
		value, _ := g["inquiry_value"].(float64)
		s.Queen.ScoredDecisions = append(s.Queen.ScoredDecisions, DashboardQueenDecision{Source: "guidance", RecordID: id(g["id"]), InquiryID: id(g["inquiry_id"]), Body: str(g["body"]), Relevance: relevance, InquiryValue: value, Created: str(g["created"])})
	}
	resourceTable, err := h.query(ctx, "dashboard_resource_table", "sqlite_master", "SELECT name FROM sqlite_master WHERE type='table' AND name='hive_worker_resources'")
	if err != nil {
		return s, err
	}
	if len(resourceTable) == 1 {
		resources, e := h.query(ctx, "dashboard_queen_scored_resources", "hive_worker_resources", "SELECT births_reviewed,note,relevance,inquiry_value,created FROM hive_worker_resources WHERE request_id=? AND created<>''", s.RequestID)
		if e != nil {
			return s, e
		}
		if len(resources) == 1 {
			r := resources[0]
			relevance, _ := r["relevance"].(float64)
			value, _ := r["inquiry_value"].(float64)
			s.Queen.ScoredDecisions = append(s.Queen.ScoredDecisions, DashboardQueenDecision{Source: "resource_review", BirthsReviewed: id(r["births_reviewed"]), Body: str(r["note"]), Relevance: relevance, InquiryValue: value, Created: str(r["created"])})
		}
	}
	if len(s.Queen.ScoredDecisions) == 2 {
		first, _ := time.Parse(time.RFC3339Nano, s.Queen.ScoredDecisions[0].Created)
		second, _ := time.Parse(time.RFC3339Nano, s.Queen.ScoredDecisions[1].Created)
		if second.After(first) {
			s.Queen.ScoredDecisions[0], s.Queen.ScoredDecisions[1] = s.Queen.ScoredDecisions[1], s.Queen.ScoredDecisions[0]
		}
	}
	s.Queen.Reviewed, err = h.query(ctx, "dashboard_reviewed", "hive_worker_queen_seen", "SELECT inquiry_id,last_work_id FROM hive_worker_queen_seen WHERE request_id=? ORDER BY inquiry_id", s.RequestID)
	if err != nil {
		return s, err
	}
	rows, err = h.query(ctx, "dashboard_inquiries", "hive_inquiries", `SELECT i.id,i.question,i.proposed_by,
 (SELECT COUNT(*) FROM hive_inquiry_work w WHERE w.inquiry_id=i.id) AS work_count,
 (SELECT COUNT(*) FROM hive_inquiry_members m WHERE m.inquiry_id=i.id AND EXISTS(
 SELECT 1 FROM hive_inquiry_work w WHERE w.inquiry_id=i.id AND w.drone_id=m.drone_id AND w.kind IN ('PROPOSE','JOIN','CONTRIBUTE'))) AS contributor_count,
 (SELECT COUNT(*) FROM hive_inquiry_members m WHERE m.inquiry_id=i.id AND EXISTS(
 SELECT 1 FROM hive_inquiry_work w WHERE w.inquiry_id=i.id AND w.drone_id=m.drone_id AND w.kind IN ('PROPOSE','CONTRIBUTE'))) AS actual_contributor_count
 FROM hive_inquiries i WHERE request_id=? ORDER BY i.id`, s.RequestID)
	if err != nil {
		return s, err
	}
	teamIndex := map[int64]int{}
	for _, r = range rows {
		if s.RootInquiryID == 0 {
			// The entry inquiry is created first; the query orders IDs ascending.
			s.RootInquiryID = id(r["id"])
		}
		teamIndex[id(r["id"])] = len(s.Teams)
		count := id(r["work_count"])
		s.WorkCount += count
		actualContributors := id(r["actual_contributor_count"])
		s.Teams = append(s.Teams, DashboardTeam{ID: id(r["id"]), Question: str(r["question"]), FriendlyName: dashboardFriendlyName(str(r["question"])), ProposedBy: id(r["proposed_by"]), Members: []int64{}, ContributorCount: id(r["contributor_count"]), ActualContributorCount: &actualContributors, WorkCount: count})
	}
	rows, err = h.query(ctx, "dashboard_drones", "hive_drones", "SELECT d.id,d.name,d.contribute,d.expand FROM hive_drones d JOIN hive_worker_queue q ON q.drone_id=d.id WHERE q.request_id=? ORDER BY d.id", s.RequestID)
	if err != nil {
		return s, err
	}
	droneIndex := map[int64]int{}
	for _, r = range rows {
		droneIndex[id(r["id"])] = len(s.Drones)
		s.Drones = append(s.Drones, DashboardDrone{ID: id(r["id"]), Name: str(r["name"]), Contribute: str(r["contribute"]), Expand: str(r["expand"]), Memberships: []int64{}})
	}
	rows, err = h.query(ctx, "dashboard_memberships", "hive_inquiry_members", "SELECT m.inquiry_id,m.drone_id FROM hive_inquiry_members m JOIN hive_inquiries i ON i.id=m.inquiry_id WHERE i.request_id=? ORDER BY m.inquiry_id,m.drone_id", s.RequestID)
	if err != nil {
		return s, err
	}
	for _, r = range rows {
		inquiry, drone := id(r["inquiry_id"]), id(r["drone_id"])
		if n, ok := teamIndex[inquiry]; ok {
			s.Teams[n].Members = append(s.Teams[n].Members, drone)
		}
		if n, ok := droneIndex[drone]; ok {
			s.Drones[n].Memberships = append(s.Drones[n].Memberships, inquiry)
		}
	}
	for _, team := range s.Teams {
		if len(team.Members) > 1 && team.ContributorCount > 1 {
			s.TeamCount++
		}
	}
	// Only one latest action per team and actor, even when their saved history is large.
	rows, err = h.query(ctx, "dashboard_latest_work", "hive_inquiry_work", `SELECT w.* FROM hive_inquiry_work w WHERE w.id IN (
 SELECT MAX(w.id) FROM hive_inquiry_work w JOIN hive_inquiries i ON i.id=w.inquiry_id WHERE i.request_id=? GROUP BY w.inquiry_id
 UNION SELECT MAX(w.id) FROM hive_inquiry_work w JOIN hive_inquiries i ON i.id=w.inquiry_id WHERE i.request_id=? GROUP BY w.drone_id) ORDER BY w.id`, s.RequestID, s.RequestID)
	if err != nil {
		return s, err
	}
	for _, r = range rows {
		w := dashboardWork(r)
		if n, ok := teamIndex[w.InquiryID]; ok {
			s.Teams[n].LatestWork = w
		}
		if n, ok := droneIndex[w.DroneID]; ok {
			s.Drones[n].LatestWork = w
		}
	}
	rows, err = h.query(ctx, "dashboard_workers", "hive_worker_queue", "SELECT drone_id,owner,phase,focus_id,asleep_work_id FROM hive_worker_queue WHERE request_id=? ORDER BY queue_order,drone_id", s.RequestID)
	if err != nil {
		return s, err
	}
	for _, r = range rows {
		state := "ready"
		if str(r["owner"]) != "" {
			state = "active"
		} else if r["asleep_work_id"] != nil {
			state = "waiting"
		}
		s.Workers = append(s.Workers, DashboardWorker{Owner: str(r["owner"]), Phase: str(r["phase"]), DroneID: id(r["drone_id"]), FocusID: id(r["focus_id"]), State: state})
	}
	rows, err = h.query(ctx, "dashboard_connections", "hive_inquiry_work", `WITH pairs AS (
 SELECT MAX(w.id) AS latest_id,COUNT(*) AS connection_count FROM hive_inquiry_work w
 JOIN hive_inquiries i ON i.id=w.inquiry_id WHERE i.request_id=? AND w.kind='CONNECT'
 GROUP BY w.inquiry_id,w.target_inquiry_id
 ) SELECT w.*,p.connection_count FROM pairs p JOIN hive_inquiry_work w ON w.id=p.latest_id ORDER BY w.id`, s.RequestID)
	if err != nil {
		return s, err
	}
	for _, r = range rows {
		s.Links = append(s.Links, DashboardLink{WorkID: id(r["id"]), FromID: id(r["inquiry_id"]), ToID: id(r["target_inquiry_id"]), DroneID: id(r["drone_id"]), Body: str(r["body"]), ConnectionCount: id(r["connection_count"])})
	}
	// The first later revision owns an older record's original scoring contract.
	// Do not parse prior grades against renamed criteria or attach newer targets.
	revisionRows, err := h.query(ctx, "dashboard_contract_revisions", "hive_guidance", `SELECT id,body FROM hive_guidance WHERE request_id=?
 AND CASE WHEN json_valid(body) THEN json_extract(body,'$.kind') END='contract_revision' ORDER BY id`, s.RequestID)
	if err != nil {
		return s, err
	}
	type revisionEvidence struct {
		id, through int64
		contract    string
		criteria    []ContractCriterion
		parseErr    error
	}
	var revisions []revisionEvidence
	for _, row := range revisionRows {
		var saved struct {
			OldContract   string `json:"old_contract"`
			ThroughWorkID *int64 `json:"through_work_id"`
		}
		if e := json.Unmarshal([]byte(str(row["body"])), &saved); e != nil {
			return s, fmt.Errorf("dashboard contract revision: %w", e)
		}
		if saved.ThroughWorkID == nil || *saved.ThroughWorkID < 0 {
			return s, errors.New("dashboard contract revision has no valid work boundary")
		}
		oldCriteria, e := workerHandoffCriteria(saved.OldContract)
		revisions = append(revisions, revisionEvidence{id: id(row["id"]), through: *saved.ThroughWorkID, contract: saved.OldContract, criteria: oldCriteria, parseErr: e})
	}
	rows, err = h.query(ctx, "dashboard_reports", "hive_inquiry_work", `SELECT w.* FROM hive_inquiry_work w JOIN hive_inquiries i ON i.id=w.inquiry_id WHERE i.request_id=? AND w.kind IN ('TEAM_REPORT','TEAM_ASSESSMENT') ORDER BY w.id`, s.RequestID)
	if err != nil {
		return s, err
	}
	criteria, criteriaErr := workerHandoffCriteria(s.Goal)
	if criteriaErr == nil {
		s.Criteria = criteria
	}
	for _, r = range rows {
		if str(r["kind"]) == "TEAM_REPORT" {
			s.Reports = append(s.Reports, r)
			continue
		}
		var profile []CriterionAssessment
		rowCriteria := criteria
		parseErr := criteriaErr
		var previous *revisionEvidence
		for index := range revisions {
			if id(r["id"]) <= revisions[index].through {
				previous = &revisions[index]
				rowCriteria, parseErr = previous.criteria, previous.parseErr
				break
			}
		}
		destination := &s.Assessments
		if previous != nil {
			destination = &s.AssessmentHistory
		}
		if parseErr == nil {
			profile, _, parseErr = parseWorkerAssessment(str(r["body"]), rowCriteria)
		}
		if parseErr != nil {
			entry := map[string]any{"work_id": r["id"], "inquiry_id": r["inquiry_id"], "drone_id": r["drone_id"], "body": r["body"], "parse_error": parseErr.Error()}
			if previous != nil {
				entry["contract_revision_id"], entry["assessed_contract"] = previous.id, previous.contract
			}
			*destination = append(*destination, entry)
			continue
		}
		for index, a := range profile {
			entry := map[string]any{"work_id": r["id"], "inquiry_id": r["inquiry_id"], "drone_id": r["drone_id"], "criterion": a.Name,
				"score": a.ScoreValue(), "incomplete": a.Incomplete, "reason": a.Reason, "evidence": a.Evidence, "uncertainty": a.Uncertainty}
			if previous != nil {
				entry["contract_revision_id"] = previous.id
				if index == 0 {
					entry["assessed_contract"] = previous.contract
				}
			}
			for _, criterion := range rowCriteria {
				if criterion.Name == a.Name && criterion.Target != nil {
					entry["target"] = criterion.Target
				}
			}
			*destination = append(*destination, entry)
		}
	}
	available, err := h.query(ctx, "dashboard_answers_available", "sqlite_master", "SELECT name FROM sqlite_master WHERE type='table' AND name='hive_request_answers'")
	if err != nil {
		return s, err
	}
	if len(available) != 0 {
		answers, e := h.query(ctx, "dashboard_latest_answer", "hive_request_answers", "SELECT id FROM hive_request_answers WHERE request_id=? ORDER BY id DESC LIMIT 1", s.RequestID)
		if e != nil {
			return s, e
		}
		if len(answers) != 0 {
			// The dashboard can inspect a saved request without an active worker identity.
			view := *h
			view.requestID = s.RequestID
			view.Log = &ContextLog{Sink: h.Log.Sink, Fields: cloneLogFields(h.Log.Fields)}
			view.Log.Fields["request_id"] = s.RequestID
			answer, e := view.readWorkerAnswer(ctx, id(answers[0]["id"]))
			if e != nil {
				return s, e
			}
			s.LatestAnswer = &answer
		}
	}
	s.GeneratedUTC = now()
	return s, nil
}

func dashboardFinal(status string) bool {
	return status == "returned" || status == "finished" || status == "paused" || status == "failed" || status == "blocked" || status == "provider_unavailable"
}

func dashboardFlatHTML(name string) bool {
	return name != "" && filepath.Base(name) == name && !strings.ContainsAny(name, "/\\:\x00<>\"|?*{}") && strings.EqualFold(filepath.Ext(name), ".html")
}

func (d DashboardSettings) validate() error {
	instance := d.InstanceName
	if instance == "" {
		instance = "instance"
	}
	if strings.TrimSpace(instance) == "" || instance == "." || instance == ".." || strings.ContainsAny(instance, "/\\:\x00<>\"|?*{}") {
		return errors.New("dashboard instance_name must be one valid filename component")
	}
	name := strings.ReplaceAll(d.File, "{instance}", instance)
	if d.RefreshSeconds <= 0 || !dashboardFlatHTML(name) {
		return errors.New("dashboard requires positive refresh_seconds and a flat .html filename; {instance} is the only filename placeholder")
	}
	if d.PublishDirectory != "" {
		if !strings.Contains(d.File, "{instance}") {
			return errors.New("published dashboard filename must contain {instance}")
		}
	}
	return nil
}

func dashboardPaths(c Config, runDir string) (instance, local, published string, err error) {
	if err = c.Dashboard.validate(); err != nil {
		return
	}
	instance = c.Dashboard.InstanceName
	if instance == "" {
		var absolute string
		absolute, err = filepath.Abs(runDir)
		if err != nil {
			return
		}
		instance = filepath.Base(filepath.Clean(absolute))
	}
	name := strings.ReplaceAll(c.Dashboard.File, "{instance}", instance)
	if !dashboardFlatHTML(name) {
		err = errors.New("resolved dashboard filename must be a flat .html filename")
		return
	}
	for _, reserved := range []string{c.Files.State, c.Files.Debug, c.Files.Contract, c.Files.Result} {
		if strings.EqualFold(name, reserved) {
			err = errors.New("resolved dashboard filename collides with an operational runtime file")
			return
		}
	}
	local, err = filepath.Abs(filepath.Join(runDir, name))
	if err != nil {
		return
	}
	if c.Dashboard.PublishDirectory != "" {
		published, err = filepath.Abs(filepath.Join(c.Dashboard.PublishDirectory, name))
	}
	return
}

func replaceDashboardFile(path string, body []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".dashboard-*.tmp")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	_, writeErr := file.Write(body)
	closeErr := file.Close()
	if err = errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func writeDashboard(ctx context.Context, h *Hive) (DashboardSnapshot, error) {
	instance, local, published, err := dashboardPaths(h.Config, h.Dir)
	if err != nil {
		return DashboardSnapshot{}, err
	}
	s, err := h.dashboardSnapshot(ctx)
	if err != nil {
		return s, err
	}
	s.InstanceName = instance
	body, err := renderDashboard(s, h.Config.Dashboard.RefreshSeconds)
	if err != nil {
		return s, err
	}
	if err = replaceDashboardFile(local, body); err != nil {
		return s, err
	}
	if published != "" && !strings.EqualFold(local, published) {
		if err = replaceDashboardFile(published, body); err != nil {
			h.Log.Log("developer", "dashboard_publish_failed", map[string]any{"instance": instance, "local_path": local, "published_path": published, "error": err.Error(), "outcome": "error"})
			return s, err
		}
	}
	h.Log.Log("developer", "dashboard_updated", map[string]any{"request_id": s.RequestID, "status": s.Status, "instance": instance, "file": local, "published_path": published, "teams": s.TeamCount, "drones": len(s.Drones), "outcome": "saved"})
	return s, nil
}

// Stop waits for the ticker to drain, then writes a final snapshot before the
// owning Hive closes. Dashboard failures are diagnostics, never actor evidence.
func startDashboard(ctx context.Context, h *Hive) (func(), error) {
	if !h.Config.Dashboard.Enabled {
		return func() {}, nil
	}
	if err := h.Config.Validate(); err != nil {
		return nil, err
	}
	if _, err := writeDashboard(ctx, h); err != nil {
		h.Log.Log("developer", "dashboard_update_failed", map[string]any{"error": err.Error(), "outcome": "error", "initial": true})
	}
	watch, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Duration(h.Config.Dashboard.RefreshSeconds) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-watch.Done():
				return
			case <-ticker.C:
				s, err := writeDashboard(watch, h)
				if err != nil {
					h.Log.Log("developer", "dashboard_update_failed", map[string]any{"error": err.Error(), "outcome": "error"})
				} else if dashboardFinal(s.Status) {
					return
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
			if _, err := writeDashboard(context.Background(), h); err != nil {
				h.Log.Log("developer", "dashboard_update_failed", map[string]any{"error": err.Error(), "outcome": "error", "final": true})
			}
		})
	}, nil
}

// The standalone viewer can observe an older running executable. It initializes
// no operational schema, opens no Meta client, and never changes request state.
func executeDashboard(args []string) error {
	f := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	dir := f.String("dir", ".", "saved flat run folder")
	configPath := f.String("config", "", "configuration; defaults to RUN_FOLDER/config.json")
	watch := f.Bool("watch", false, "refresh until the request stops or Ctrl+C")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected dashboard arguments")
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	path := *configPath
	if path == "" {
		path = filepath.Join(abs, "config.json")
	}
	c, err := LoadConfig(path)
	if err != nil {
		return err
	}
	// Existing v10 run snapshots predate the optional display settings.
	if c.Dashboard.File == "" {
		c.Dashboard.File = "dashboard.html"
	}
	if c.Dashboard.RefreshSeconds == 0 {
		c.Dashboard.RefreshSeconds = 5
	}
	c.Dashboard.Enabled = true
	if err = c.Validate(); err != nil {
		return err
	}
	statePath := filepath.Join(abs, c.Files.State)
	info, err := os.Stat(statePath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("dashboard requires an existing regular state database")
	}
	logger, err := OpenLogger(filepath.Join(abs, c.Files.Debug), c.Schema.Logs)
	if err != nil {
		return err
	}
	defer logger.Close()
	log := &ContextLog{Sink: logger, Fields: map[string]any{"source": "dashboard"}}
	uriPath := filepath.ToSlash(statePath)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	stateURI := (&url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=ro"}).String()
	store, err := OpenStore(stateURI, nil, log)
	if err != nil {
		return err
	}
	defer store.Close()
	h := &Hive{Config: c, Dir: abs, Store: store, Logger: logger, Log: log}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	s, err := writeDashboard(ctx, h)
	if err != nil {
		return err
	}
	instance, local, published, pathErr := dashboardPaths(c, abs)
	if pathErr != nil {
		return pathErr
	}
	fmt.Println(jsonText(map[string]any{"dashboard": local, "instance": instance, "published_path": published, "status": s.Status, "watch": *watch, "state_mode": "ro"}))
	if !*watch || dashboardFinal(s.Status) {
		return nil
	}
	ticker := time.NewTicker(time.Duration(c.Dashboard.RefreshSeconds) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_, err = writeDashboard(context.Background(), h)
			return err
		case <-ticker.C:
			s, err = writeDashboard(ctx, h)
			if err != nil {
				return err
			}
			if dashboardFinal(s.Status) {
				return nil
			}
		}
	}
}
