package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func diagnosticTestSchema() []string {
	var statements []string
	for _, table := range []string{"hive_log", "mq_log"} {
		statements = append(statements, "CREATE TABLE "+table+" (id INTEGER PRIMARY KEY, ts TEXT NOT NULL, scope TEXT, event TEXT, run_id INTEGER, request_id INTEGER, team_id INTEGER, drone_id INTEGER, model TEXT, outcome TEXT, duration_ms REAL, sql_op TEXT, table_name TEXT, rows_count INTEGER, decision_type TEXT, details TEXT NOT NULL)")
	}
	return statements
}

func TestStorePersistenceAndSQLDiagnostics(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	log, err := OpenLogger(filepath.Join(dir, "debug.db"), diagnosticTestSchema())
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	path := filepath.Join(dir, "state.db")
	schema := []string{"PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000", "CREATE TABLE IF NOT EXISTS hive_items(id INTEGER PRIMARY KEY, body TEXT NOT NULL)"}
	s, err := OpenStore(path, schema, log)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]any{"request_id": 9}
	result, err := s.Exec(ctx, "save_item", "hive_items", fields, "INSERT INTO hive_items(body) VALUES (?)", "private source material")
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil || id <= 0 {
		t.Fatalf("missing inserted identity: id=%d err=%v", id, err)
	}
	if len(fields) != 1 {
		t.Fatalf("SQL logging modified caller metadata: %#v", fields)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path, schema, log)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.Query(ctx, "load_item", "hive_items", fields, "SELECT id,body FROM hive_items WHERE id=?", id)
	if err != nil || len(rows) != 1 || rows[0]["body"] != "private source material" {
		t.Fatalf("reopen lost state: %#v err=%v", rows, err)
	}
	empty, err := s.Query(ctx, "load_absent", "hive_items", fields, "SELECT id FROM hive_items WHERE id=?", id+1)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty read not represented distinctly: %#v err=%v", empty, err)
	}
	if _, err := s.Query(ctx, "invalid_read", "missing", fields, "SELECT id FROM missing"); err == nil {
		t.Fatal("invalid SQL accepted")
	}
	if _, err := s.Query(ctx, "diagnostic_boundary", "hive_log", fields, "SELECT * FROM hive_log"); err == nil {
		t.Fatal("diagnostics unexpectedly accessible through state database")
	}
	checks := []struct {
		op      string
		outcome string
		rows    int
	}{
		{"save_item", "ok", 1}, {"load_item", "ok", 1}, {"load_absent", "ok", 0}, {"invalid_read", "error", 0},
	}
	for _, check := range checks {
		var status, detail string
		var count int
		var duration float64
		err := log.db.QueryRow("SELECT outcome,rows_count,duration_ms,details FROM hive_log WHERE event='sql_done' AND sql_op=?", check.op).Scan(&status, &count, &duration, &detail)
		if err != nil || status != check.outcome || count != check.rows || duration < 0 {
			t.Errorf("wrong summary for %s: status=%s rows=%d duration=%f err=%v", check.op, status, count, duration, err)
		}
		if strings.Contains(detail, "private source material") || strings.Contains(detail, "INSERT INTO") {
			t.Errorf("SQL text/arguments leaked into summary: %s", detail)
		}
		var starts int
		if err := log.db.QueryRow("SELECT COUNT(*) FROM hive_log WHERE event='sql_start' AND sql_op=?", check.op).Scan(&starts); err != nil || starts != 1 {
			t.Errorf("missing operation start for %s: starts=%d err=%v", check.op, starts, err)
		}
	}
}

func TestLoggerSeparatesMetaQueenAndReportsSinkFailureOnce(t *testing.T) {
	log, err := OpenLogger(":memory:", diagnosticTestSchema())
	if err != nil {
		t.Fatal(err)
	}
	var fallback bytes.Buffer
	log.fallback = &fallback
	log.Log("meta_queen", "candidate_chosen", map[string]any{"run_id": 1, "model": "test-model"})
	log.Log("drone", "contribute", map[string]any{"drone_id": 2})
	for _, table := range []string{"mq_log", "hive_log"} {
		var count int
		if err := log.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("scope routing or recursive logging failure in %s: count=%d err=%v", table, count, err)
		}
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	log.Log("hive", "after_close", map[string]any{"body": "private diagnostic payload"})
	var record map[string]any
	if err := json.Unmarshal(fallback.Bytes(), &record); err != nil {
		t.Fatalf("expected exactly one JSON fallback record: %q: %v", fallback.String(), err)
	}
	if record["event"] != "diagnostic_write_failed" || record["source_event"] != "after_close" {
		t.Fatalf("unexpected fallback: %#v", record)
	}
	if strings.Contains(fallback.String(), "private diagnostic payload") {
		t.Fatal("failed log payload leaked to fallback")
	}
}

func TestMetaTableAccessRoutesSQLDiagnosticsToMetaQueen(t *testing.T) {
	log, err := OpenLogger(":memory:", diagnosticTestSchema())
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	s, err := OpenStore(":memory:", []string{"CREATE TABLE mq_experiments(id INTEGER PRIMARY KEY)"}, log)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Exec(context.Background(), "save_experiment", "mq_experiments", nil, "INSERT INTO mq_experiments DEFAULT VALUES"); err != nil {
		t.Fatal(err)
	}
	var meta, hive int
	if err := log.db.QueryRow("SELECT COUNT(*) FROM mq_log WHERE sql_op='save_experiment'").Scan(&meta); err != nil {
		t.Fatal(err)
	}
	if err := log.db.QueryRow("SELECT COUNT(*) FROM hive_log WHERE sql_op='save_experiment'").Scan(&hive); err != nil {
		t.Fatal(err)
	}
	if meta != 2 || hive != 0 {
		t.Fatalf("wrong SQL scope: meta=%d hive=%d", meta, hive)
	}
}
