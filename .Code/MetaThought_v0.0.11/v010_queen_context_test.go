package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type v10QueenRequestFacts struct {
	InquiryCount      int64 `json:"inquiry_count"`
	ObservedTeamCount int64 `json:"observed_team_count"`
	WorkRecordCount   int64 `json:"work_record_count"`
	LatestWorkID      int64 `json:"latest_work_id"`
}

type v10QueenFocusFacts struct {
	InquiryID               int64 `json:"inquiry_id"`
	CommittedMemberCount    int64 `json:"committed_member_count"`
	ContributingMemberCount int64 `json:"contributing_member_count"`
	WorkRecordCount         int64 `json:"work_record_count"`
	LatestWorkID            int64 `json:"latest_work_id"`
	ReviewedThroughWorkID   int64 `json:"reviewed_through_work_id"`
	UnreviewedWorkCount     int64 `json:"unreviewed_work_count"`
}

type v10QueenFacts struct {
	Request v10QueenRequestFacts `json:"request"`
	Focus   v10QueenFocusFacts   `json:"focus"`
}

type v10QueenContext struct {
	Handoff        string           `json:"handoff"`
	Inquiry        []map[string]any `json:"inquiry"`
	Work           []map[string]any `json:"work"`
	PriorQueenNote string           `json:"prior_queen_note"`
	CurrentFacts   v10QueenFacts    `json:"current_facts"`
}

func v10DecodeQueenContext(t *testing.T, input string) v10QueenContext {
	t.Helper()
	var got v10QueenContext
	if err := json.Unmarshal([]byte(input), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestV010QueenContextReportsActualTeamsAlongsideOneWorkItem(t *testing.T) {
	h, _, _ := v10Fixture(t, 4, nil)
	ctx := v10Context(t)
	addInquiry := func(request int64, question string) int64 {
		return v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", request, question, 0, now())
	}
	member := func(inquiry, drone int64) {
		v10Exec(t, h, "INSERT INTO hive_inquiry_members(inquiry_id,drone_id,commitment,created) VALUES(?,?,?,?)", inquiry, drone, "fixture commitment", now())
	}
	work := func(inquiry, drone int64, kind, body string) int64 {
		return v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,?,?,?,?,?,?)", inquiry, drone, "contribute", kind, body, 0.8, 0.7, now())
	}
	member(1, 1)
	member(1, 2)
	selected := work(1, 1, "JOIN", "Selected original contribution.")
	work(1, 2, "JOIN", "UNSELECTED_FOCUS_WORK")
	work(1, 1, "CONTRIBUTE", "UNSELECTED_FOCUS_WORK")
	focusLatest := work(1, 4, "CONTRIBUTE", "An uncommitted author must not count as a contributing member.")
	second := addInquiry(h.requestID, "Second active inquiry")
	member(second, 2)
	member(second, 3)
	work(second, 2, "JOIN", "OTHER_INQUIRY_WORK_NOT_HISTORY")
	work(second, 3, "JOIN", "OTHER_INQUIRY_WORK_NOT_HISTORY")
	// Two authors do not establish a team if only one has committed.
	singleton := addInquiry(h.requestID, "One commitment despite two authors")
	member(singleton, 4)
	work(singleton, 4, "JOIN", "Singleton commitment")
	requestLatest := work(singleton, 3, "CONTRIBUTE", "Uncommitted extra author")
	otherRequest := v10Exec(t, h, "INSERT INTO hive_requests(run_id,contract,status,created) VALUES(?,?,?,?)", h.runID, "OTHER_REQUEST_CANARY", "working", now())
	otherInquiry := addInquiry(otherRequest, "OTHER_REQUEST_CANARY")
	member(otherInquiry, 1)
	member(otherInquiry, 4)
	work(otherInquiry, 1, "JOIN", "OTHER_REQUEST_CANARY")
	work(otherInquiry, 4, "JOIN", "OTHER_REQUEST_CANARY")
	v10Exec(t, h, "INSERT INTO hive_worker_queen_seen(request_id,inquiry_id,last_work_id) VALUES(?,?,?)", h.requestID, 1, selected)
	prior := "There is only one author and no team.\r\nPrior unsupported claim; preserve exactly: α.  "
	rows, err := h.query(ctx, "fixture_selected_work", "hive_inquiry_work", "SELECT * FROM hive_inquiry_work WHERE id=?", selected)
	if err != nil {
		t.Fatal(err)
	}
	input, err := h.workerQueenInput(ctx, 1, rows, prior)
	if err != nil {
		t.Fatal(err)
	}
	got := v10DecodeQueenContext(t, input)
	want := v10QueenFacts{
		Request: v10QueenRequestFacts{InquiryCount: 3, ObservedTeamCount: 2, WorkRecordCount: 8, LatestWorkID: requestLatest},
		Focus:   v10QueenFocusFacts{InquiryID: 1, CommittedMemberCount: 2, ContributingMemberCount: 2, WorkRecordCount: 4, LatestWorkID: focusLatest, ReviewedThroughWorkID: selected, UnreviewedWorkCount: 3},
	}
	if got.CurrentFacts != want {
		t.Fatalf("facts count the sample, uncommitted authors, or another request: got %+v want %+v", got.CurrentFacts, want)
	}
	if got.Handoff != v9Handoff || got.PriorQueenNote != prior || len(got.Inquiry) != 1 || id(got.Inquiry[0]["id"]) != 1 || len(got.Work) != 1 || id(got.Work[0]["id"]) != selected {
		t.Fatalf("context changed the governing text, prior note, or selected sample: %+v", got)
	}
	if str(got.Work[0]["body"]) != str(rows[0]["body"]) {
		t.Fatal("selected work was rewritten")
	}
	for _, absent := range []string{v9DiagnosticCanary, v9OldSeedCanary, "OTHER_REQUEST_CANARY", "UNSELECTED_FOCUS_WORK", "OTHER_INQUIRY_WORK_NOT_HISTORY"} {
		if strings.Contains(input, absent) {
			t.Fatalf("context includes unwanted history or diagnostic data: %s", absent)
		}
	}
	wantPrompt := h.Config.Prompts["queen_role"] + "\n\n" + h.Config.Prompts["worker_queen_monitor"] + "\n\n" + h.Config.Prompts["worker_queen_context"] + "\n\n" + h.Config.Prompts["worker_queen_scores"] + "\n\n" + h.Config.Prompts["worker_queen_format"] + "\n\n" + h.Config.Prompts["worker_contract_authority"]
	if gotPrompt := h.workerQueenPrompt(); gotPrompt != wantPrompt {
		t.Fatal("exported Queen role differs from the configured production role")
	}
}

// This independent check deliberately uses ordinary scalar/grouped SQL rather
// than the production context query. It never reads diagnostic source data.
func v10IndependentQueenFacts(t *testing.T, h *Hive, requestID, inquiryID int64) v10QueenFacts {
	t.Helper()
	count := func(query string, args ...any) int64 {
		t.Helper()
		rows, err := h.query(context.Background(), "independent_context_count", "operational_facts", query, args...)
		if err != nil || len(rows) != 1 || len(rows[0]) != 1 {
			t.Fatalf("independent scalar count: rows=%v error=%v", rows, err)
		}
		for _, value := range rows[0] {
			return id(value)
		}
		panic("checked scalar row is empty")
	}
	facts := v10QueenFacts{}
	facts.Request.InquiryCount = count("SELECT COUNT(*) FROM hive_inquiries WHERE request_id=?", requestID)
	facts.Request.WorkRecordCount = count("SELECT COUNT(*) FROM hive_inquiry_work WHERE inquiry_id IN (SELECT id FROM hive_inquiries WHERE request_id=?)", requestID)
	facts.Request.LatestWorkID = count("SELECT COALESCE(MAX(id),0) FROM hive_inquiry_work WHERE inquiry_id IN (SELECT id FROM hive_inquiries WHERE request_id=?)", requestID)
	rows, err := h.query(context.Background(), "independent_context_teams", "hive_inquiries", "SELECT i.id, COUNT(DISTINCT m.drone_id) AS members, COUNT(DISTINCT w.drone_id) AS contributors FROM hive_inquiries i LEFT JOIN hive_inquiry_members m ON m.inquiry_id=i.id LEFT JOIN hive_inquiry_work w ON w.inquiry_id=m.inquiry_id AND w.drone_id=m.drone_id AND w.kind IN ('PROPOSE','JOIN','CONTRIBUTE') WHERE i.request_id=? GROUP BY i.id", requestID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if id(row["members"]) > 1 && id(row["contributors"]) > 1 {
			facts.Request.ObservedTeamCount++
		}
	}
	facts.Focus.InquiryID = inquiryID
	facts.Focus.CommittedMemberCount = count("SELECT COUNT(*) FROM hive_inquiry_members WHERE inquiry_id=?", inquiryID)
	facts.Focus.ContributingMemberCount = count("SELECT COUNT(DISTINCT w.drone_id) FROM hive_inquiry_work w JOIN hive_inquiry_members m ON m.inquiry_id=w.inquiry_id AND m.drone_id=w.drone_id WHERE w.inquiry_id=? AND w.kind IN ('PROPOSE','JOIN','CONTRIBUTE')", inquiryID)
	facts.Focus.WorkRecordCount = count("SELECT COUNT(*) FROM hive_inquiry_work WHERE inquiry_id=?", inquiryID)
	facts.Focus.LatestWorkID = count("SELECT COALESCE(MAX(id),0) FROM hive_inquiry_work WHERE inquiry_id=?", inquiryID)
	facts.Focus.ReviewedThroughWorkID = count("SELECT COALESCE((SELECT last_work_id FROM hive_worker_queen_seen WHERE request_id=? AND inquiry_id=?),0)", requestID, inquiryID)
	facts.Focus.UnreviewedWorkCount = count("SELECT COUNT(*) FROM hive_inquiry_work WHERE inquiry_id=? AND id>?", inquiryID, facts.Focus.ReviewedThroughWorkID)
	return facts
}

func TestV010QueenContextSaved(t *testing.T) {
	source, export, configPath := os.Getenv("METATHOUGHT_QUEEN_SOURCE"), os.Getenv("METATHOUGHT_QUEEN_EXPORT"), os.Getenv("METATHOUGHT_QUEEN_CONFIG")
	if source == "" || export == "" || configPath == "" {
		t.Skip("saved context export requires explicit source, fresh export folder, and config")
	}
	workID, err := strconv.ParseInt(os.Getenv("METATHOUGHT_QUEEN_WORK"), 10, 64)
	if err != nil || workID <= 0 {
		t.Fatal("METATHOUGHT_QUEEN_WORK must name one positive saved work ID")
	}
	c, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	statePath, err := filepath.Abs(filepath.Join(source, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(statePath); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("saved state database is required: %v", err)
	}
	// mode=ro prevents schema initialization and source writes. OpenHive is not
	// used: only the NEW export folder receives diagnostic SQL-read summaries.
	uriPath := filepath.ToSlash(statePath)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	stateURI := (&url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite", stateURI)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(export, 0700); err != nil {
		t.Fatalf("export must be a new folder: %v", err)
	}
	logger, err := OpenLogger(filepath.Join(export, "debug.db"), c.Schema.Logs)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	log := &ContextLog{Sink: logger, Fields: map[string]any{"scope": "developer", "purpose": "saved_queen_context_check"}}
	h := &Hive{Config: c, Store: &Store{db: db, log: log}, Log: log}
	ctx := context.Background()
	work, err := h.query(ctx, "saved_selected_work", "hive_inquiry_work", "SELECT * FROM hive_inquiry_work WHERE id=?", workID)
	if err != nil || len(work) != 1 {
		t.Fatalf("selected work missing: rows=%d error=%v", len(work), err)
	}
	focus := id(work[0]["inquiry_id"])
	inquiries, err := h.query(ctx, "saved_work_request", "hive_inquiries", "SELECT request_id FROM hive_inquiries WHERE id=?", focus)
	if err != nil || len(inquiries) != 1 {
		t.Fatalf("selected inquiry missing: rows=%d error=%v", len(inquiries), err)
	}
	h.requestID = id(inquiries[0]["request_id"])
	h.Log.Fields["request_id"] = h.requestID
	queen, err := h.query(ctx, "saved_queen_note", "hive_worker_queen", "SELECT note FROM hive_worker_queen WHERE request_id=?", h.requestID)
	if err != nil || len(queen) != 1 {
		t.Fatalf("persisted Queen note missing: rows=%d error=%v", len(queen), err)
	}
	prior := str(queen[0]["note"])
	input, err := h.workerQueenInput(ctx, focus, work, prior)
	if err != nil {
		t.Fatal(err)
	}
	got := v10DecodeQueenContext(t, input)
	independent := v10IndependentQueenFacts(t, h, h.requestID, focus)
	requests, err := h.query(ctx, "independent_saved_handoff", "hive_requests", "SELECT contract FROM hive_requests WHERE id=?", h.requestID)
	if err != nil || len(requests) != 1 {
		t.Fatalf("saved request missing: rows=%d error=%v", len(requests), err)
	}
	savedHandoff := str(requests[0]["contract"])
	if got.CurrentFacts != independent {
		t.Fatalf("production facts differ from independent saved-state counts: got %+v want %+v", got.CurrentFacts, independent)
	}
	if got.Handoff != savedHandoff || got.PriorQueenNote != prior || len(got.Inquiry) != 1 || id(got.Inquiry[0]["id"]) != focus || len(got.Work) != 1 || id(got.Work[0]["id"]) != workID {
		t.Fatal("saved request, prior note, focus, or one-item review sample changed")
	}
	var originalWork []map[string]any
	if err := json.Unmarshal([]byte(jsonText(work)), &originalWork); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Work, originalWork) {
		t.Fatal("original saved work fields changed during context export")
	}
	write := func(name, body string) {
		t.Helper()
		if err := writeNew(filepath.Join(export, name), []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	write("input.json", input)
	write("role.txt", h.workerQueenPrompt())
	write("context-proof.json", jsonText(map[string]any{
		"source_state": statePath, "source_mode": "ro", "request_id": h.requestID,
		"selected_work_id": workID, "selected_work_count": len(got.Work),
		"facts": got.CurrentFacts, "independent_counts": independent,
		"handoff_preserved": true, "prior_queen_note_preserved": true,
		"source_diagnostics_read": false, "provider_calls": 0,
	}))
	fmt.Printf("saved_queen_context_export=%s request=%d work=%d facts=%s\n", export, h.requestID, workID, jsonText(got.CurrentFacts))
}

func TestV010QueenContextReply(t *testing.T) {
	folder := os.Getenv("METATHOUGHT_QUEEN_REPLY_FOLDER")
	if folder == "" {
		t.Skip("saved reply parsing requires an explicit reply folder")
	}
	reply, err := os.ReadFile(filepath.Join(folder, "reply.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Use the exact persisted reply; parsing does not judge whether the Queen's
	// conclusion is supported and does not apply her action to the saved hive.
	action, err := ParseQueenWorkerAction(string(reply))
	if err != nil {
		t.Fatalf("saved Queen reply rejected by production parser: %v", err)
	}
	if err := writeNew(filepath.Join(folder, "reply-parsed.json"), []byte(jsonText(action))); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("saved_queen_reply_parsed=%s action=%s relevance=%g inquiry_value=%g\n", folder, action.Kind, action.Relevance, action.InquiryValue)
}
