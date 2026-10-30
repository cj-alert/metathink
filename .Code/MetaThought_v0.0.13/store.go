package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Logger writes diagnostics to a database never exposed through Store. Its own
// inserts deliberately bypass Store so logging cannot recursively log itself.
type Logger struct {
	db       *sql.DB
	mu       sync.Mutex
	fallback io.Writer
}

func OpenLogger(path string, schema []string) (*Logger, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open diagnostics: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for i, statement := range schema {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, fmt.Errorf("diagnostic schema statement %d: %w", i, err)
		}
	}
	return &Logger{db: db, fallback: os.Stderr}, nil
}

func (l *Logger) Log(scope, event string, fields map[string]any) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	data := cloneLogFields(fields)
	details, err := json.Marshal(data)
	if err != nil {
		l.failed(event, fmt.Errorf("encode diagnostics: %w", err))
		return
	}
	table := "hive_log"
	if scope == "meta_queen" {
		table = "mq_log"
	}
	if l.db == nil {
		l.failed(event, fmt.Errorf("diagnostic database closed"))
		return
	}
	// table is selected solely from these two literals, never from event data.
	_, err = l.db.Exec("INSERT INTO "+table+" (ts,scope,event,run_id,request_id,team_id,drone_id,model,outcome,duration_ms,sql_op,table_name,rows_count,decision_type,details) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		time.Now().UTC().Format(time.RFC3339Nano), scope, event,
		data["run_id"], data["request_id"], data["team_id"], data["drone_id"],
		data["model"], data["outcome"], data["duration_ms"], data["sql_op"],
		data["table_name"], data["rows_count"], data["decision_type"], string(details))
	if err != nil {
		l.failed(event, err)
	}
}

func (l *Logger) failed(event string, err error) {
	if l.fallback == nil {
		return
	}
	// Do not retry through Log: a broken sink must not cause recursion. Do not
	// copy the rejected payload to stderr, which may contain private research.
	_ = json.NewEncoder(l.fallback).Encode(map[string]any{
		"ts":    time.Now().UTC().Format(time.RFC3339Nano),
		"event": "diagnostic_write_failed", "source_event": event, "error": err.Error(),
	})
}

func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.db == nil {
		return nil
	}
	err := l.db.Close()
	l.db = nil
	return err
}

// Store contains only operational state. All application access goes through
// Exec or Query, which emit SQL summaries without copying SQL text or arguments.
type Store struct {
	db  *sql.DB
	log EventSink
}

func OpenStore(path string, schema []string, log EventSink) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open working state: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db, log: log}
	for i, statement := range schema {
		_, err := s.Exec(context.Background(), "initialize_schema", schemaTarget(statement), map[string]any{"statement_index": i}, statement)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("working schema statement %d: %w", i, err)
		}
	}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Exec(ctx context.Context, op, table string, fields map[string]any, query string, args ...any) (sql.Result, error) {
	started := time.Now()
	s.record("sql_start", op, table, fields, "started", 0, nil, 0)
	result, err := s.db.ExecContext(ctx, query, args...)
	var affected int64
	if err == nil {
		affected, err = result.RowsAffected()
	}
	s.record("sql_done", op, table, fields, storeOutcome(err), affected, err, time.Since(started))
	return result, err
}

// Transaction atomically persists one completed unit of operational work.
// The callback uses its supplied exec, not Store methods on another connection.
// Each statement and the transaction outcome remain visible in diagnostics.
func (s *Store) Transaction(ctx context.Context, fields map[string]any, work func(exec func(op, table, query string, args ...any) (sql.Result, error)) error) (err error) {
	started := time.Now()
	fields = cloneLogFields(fields)
	s.record("transaction_start", "begin", "transaction", fields, "started", 0, nil, 0)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.record("transaction_done", "begin", "transaction", fields, "error", 0, err, time.Since(started))
		return err
	}
	committed := false
	commitAttempted := false
	defer func() {
		if !committed {
			rollbackErr := tx.Rollback()
			if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				err = errors.Join(err, rollbackErr)
			}
		}
		outcome := "committed"
		if !committed {
			outcome = "rolled_back"
			if commitAttempted {
				outcome = "commit_error"
			}
		}
		s.record("transaction_done", "transaction", "transaction", fields, outcome, 0, err, time.Since(started))
	}()
	exec := func(op, table, query string, args ...any) (sql.Result, error) {
		start := time.Now()
		s.record("sql_start", op, table, fields, "started", 0, nil, 0)
		result, err := tx.ExecContext(ctx, query, args...)
		var affected int64
		if err == nil {
			affected, err = result.RowsAffected()
		}
		s.record("sql_done", op, table, fields, storeOutcome(err), affected, err, time.Since(start))
		return result, err
	}
	if err = work(exec); err != nil {
		return err
	}
	commitAttempted = true
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) Query(ctx context.Context, op, table string, fields map[string]any, query string, args ...any) (result []map[string]any, err error) {
	started := time.Now()
	s.record("sql_start", op, table, fields, "started", 0, nil, 0)
	defer func() {
		s.record("sql_done", op, table, fields, storeOutcome(err), int64(len(result)), err, time.Since(started))
	}()
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result = make([]map[string]any, 0)
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err = rows.Scan(targets...); err != nil {
			return result, err
		}
		row := make(map[string]any, len(columns))
		for i, name := range columns {
			if blob, ok := values[i].([]byte); ok {
				row[name] = append([]byte(nil), blob...)
			} else {
				row[name] = values[i]
			}
		}
		result = append(result, row)
	}
	err = rows.Err()
	return result, err
}

func (s *Store) record(event, op, table string, fields map[string]any, status string, count int64, err error, elapsed time.Duration) {
	if s.log == nil {
		return
	}
	data := cloneLogFields(fields)
	data["sql_op"], data["table_name"], data["outcome"] = op, table, status
	if event == "sql_done" {
		data["rows_count"] = count
		data["duration_ms"] = float64(elapsed) / float64(time.Millisecond)
	}
	if err != nil {
		data["error"] = err.Error()
	}
	scope := "hive"
	if strings.HasPrefix(table, "mq_") {
		scope = "meta_queen"
	}
	if named, ok := data["scope"].(string); ok && named != "" {
		scope = named
	}
	s.log.Log(scope, event, data)
}

func cloneLogFields(fields map[string]any) map[string]any {
	copy := make(map[string]any, len(fields)+6)
	for key, value := range fields {
		copy[key] = value
	}
	return copy
}

func storeOutcome(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}

// This labels schema diagnostics, not SQL execution. Schema statements remain
// the configured original text and are never rewritten by this helper.
func schemaTarget(statement string) string {
	words := strings.Fields(statement)
	for i, word := range words {
		if strings.EqualFold(word, "TABLE") {
			j := i + 1
			if j+2 < len(words) && strings.EqualFold(words[j], "IF") && strings.EqualFold(words[j+1], "NOT") && strings.EqualFold(words[j+2], "EXISTS") {
				j += 3
			}
			if j < len(words) {
				return strings.Trim(strings.SplitN(words[j], "(", 2)[0], "\"`[];")
			}
		}
		if strings.EqualFold(word, "ON") && i+1 < len(words) {
			return strings.Trim(strings.SplitN(words[i+1], "(", 2)[0], "\"`[];")
		}
	}
	return "schema"
}
