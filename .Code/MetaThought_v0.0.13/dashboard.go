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
)

type DashboardSnapshot struct {
	GeneratedUTC      string            `json:"generated_utc"`
	InstanceName      string            `json:"instance_name"`
	RunFolder         string            `json:"run_folder"`
	RequestID         int64             `json:"request_id"`
	Status            string            `json:"status"`
	Goal              string            `json:"goal"`
	Result            string            `json:"result"`
	FinishReason      string            `json:"finish_reason"`
	WorkCount         int64             `json:"work_count"`
	TeamCount         int               `json:"team_count"`
	WorkerLimit       int               `json:"worker_limit"`
	Queen             DashboardQueen    `json:"queen"`
	Teams             []DashboardTeam   `json:"teams"`
	Drones            []DashboardDrone  `json:"drones"`
	Workers           []DashboardWorker `json:"workers"`
	Links             []DashboardLink   `json:"links"`
	Assessments       []map[string]any  `json:"assessments"`
	AssessmentHistory []map[string]any  `json:"assessment_history"`
	Reports           []map[string]any  `json:"reports"`
}

type DashboardQueen struct {
	Owner     string           `json:"owner"`
	FocusID   int64            `json:"focus_id"`
	ThroughID int64            `json:"through_id"`
	Note      string           `json:"note"`
	Reviewed  []map[string]any `json:"reviewed"`
}

type DashboardTeam struct {
	ID               int64          `json:"id"`
	Question         string         `json:"question"`
	ProposedBy       int64          `json:"proposed_by"`
	Members          []int64        `json:"members"`
	ContributorCount int64          `json:"contributor_count"`
	WorkCount        int64          `json:"work_count"`
	LatestWork       *DashboardWork `json:"latest_work"`
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
	WorkID  int64  `json:"work_id"`
	FromID  int64  `json:"from_id"`
	ToID    int64  `json:"to_id"`
	DroneID int64  `json:"drone_id"`
	Body    string `json:"body"`
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
	rows, err = h.query(ctx, "dashboard_queen", "hive_worker_queen", "SELECT owner,focus_id,through_id,note FROM hive_worker_queen WHERE request_id=?", s.RequestID)
	if err != nil {
		return s, err
	}
	if len(rows) == 1 {
		r = rows[0]
		s.Queen = DashboardQueen{Owner: str(r["owner"]), FocusID: id(r["focus_id"]), ThroughID: id(r["through_id"]), Note: str(r["note"])}
	}
	s.Queen.Reviewed, err = h.query(ctx, "dashboard_reviewed", "hive_worker_queen_seen", "SELECT inquiry_id,last_work_id FROM hive_worker_queen_seen WHERE request_id=? ORDER BY inquiry_id", s.RequestID)
	if err != nil {
		return s, err
	}
	rows, err = h.query(ctx, "dashboard_inquiries", "hive_inquiries", `SELECT i.id,i.question,i.proposed_by,
 (SELECT COUNT(*) FROM hive_inquiry_work w WHERE w.inquiry_id=i.id) AS work_count,
 (SELECT COUNT(*) FROM hive_inquiry_members m WHERE m.inquiry_id=i.id AND EXISTS(
 SELECT 1 FROM hive_inquiry_work w WHERE w.inquiry_id=i.id AND w.drone_id=m.drone_id AND w.kind IN ('PROPOSE','JOIN','CONTRIBUTE'))) AS contributor_count
 FROM hive_inquiries i WHERE request_id=? ORDER BY i.id`, s.RequestID)
	if err != nil {
		return s, err
	}
	teamIndex := map[int64]int{}
	for _, r = range rows {
		teamIndex[id(r["id"])] = len(s.Teams)
		count := id(r["work_count"])
		s.WorkCount += count
		s.Teams = append(s.Teams, DashboardTeam{ID: id(r["id"]), Question: str(r["question"]), ProposedBy: id(r["proposed_by"]), Members: []int64{}, ContributorCount: id(r["contributor_count"]), WorkCount: count})
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
	rows, err = h.query(ctx, "dashboard_connections", "hive_inquiry_work", `SELECT w.* FROM hive_inquiry_work w JOIN hive_inquiries i ON i.id=w.inquiry_id WHERE i.request_id=? AND w.kind='CONNECT' AND w.id IN (SELECT MAX(x.id) FROM hive_inquiry_work x JOIN hive_inquiries j ON j.id=x.inquiry_id WHERE j.request_id=? AND x.kind='CONNECT' GROUP BY x.inquiry_id,x.target_inquiry_id) ORDER BY w.id`, s.RequestID, s.RequestID)
	if err != nil {
		return s, err
	}
	for _, r = range rows {
		s.Links = append(s.Links, DashboardLink{WorkID: id(r["id"]), FromID: id(r["inquiry_id"]), ToID: id(r["target_inquiry_id"]), DroneID: id(r["drone_id"]), Body: str(r["body"])})
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
	s.GeneratedUTC = now()
	return s, nil
}

func dashboardFinal(status string) bool {
	return status == "returned" || status == "paused" || status == "failed" || status == "blocked" || status == "provider_unavailable"
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
