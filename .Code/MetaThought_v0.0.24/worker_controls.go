package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"time"
)

var errWorkerCompletionBusy = errors.New("convergence requires drained workers and an unclaimed Queen")

type WorkerAnswer struct {
	ID          int64  `json:"id"`
	RequestID   int64  `json:"request_id"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	Body        string `json:"body"`
	CriteriaMet *bool  `json:"criteria_met"`
	Error       string `json:"error,omitempty"`
	Created     string `json:"created"`
}

func (h *Hive) workerControlsEnabled() bool {
	return h.Config.Prompts["worker_queen_interim"] != "" && h.Config.Prompts["worker_queen_finish"] != ""
}
func (h *Hive) pendingInterimSQL(request string) string {
	if !h.workerControlsEnabled() {
		return ""
	}
	return " AND NOT EXISTS(SELECT 1 FROM hive_request_answers a WHERE a.request_id=" + request + " AND a.kind='interim' AND a.status='pending') "
}
func (h *Hive) pendingWorkerAnswer(ctx context.Context, kind string) (int64, error) {
	if !h.workerControlsEnabled() {
		return 0, nil
	}
	rows, err := h.query(ctx, "pending_answer", "hive_request_answers", "SELECT id FROM hive_request_answers WHERE request_id=? AND kind=? AND status IN ('pending','working') ORDER BY id LIMIT 1", h.requestID, kind)
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	return id(rows[0]["id"]), nil
}
func (h *Hive) readWorkerAnswer(ctx context.Context, answerID int64) (WorkerAnswer, error) {
	rows, err := h.query(ctx, "read_answer", "hive_request_answers", "SELECT * FROM hive_request_answers WHERE request_id=? AND id=?", h.requestID, answerID)
	if err != nil {
		return WorkerAnswer{}, err
	}
	if len(rows) != 1 {
		return WorkerAnswer{}, errors.New("request answer missing")
	}
	r := rows[0]
	answer := WorkerAnswer{ID: id(r["id"]), RequestID: h.requestID, Kind: str(r["kind"]), Status: str(r["status"]), Body: str(r["body"]), Error: str(r["error"]), Created: str(r["created"])}
	if r["criteria_met"] != nil {
		v := id(r["criteria_met"]) != 0
		answer.CriteriaMet = &v
	}
	return answer, nil
}
func (h *Hive) exportWorkerAnswer(ctx context.Context, answerID int64) error {
	answer, err := h.readWorkerAnswer(ctx, answerID)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(answer, "", "  ")
	if err != nil {
		return err
	}
	err = writeNew(filepath.Join(h.Dir, fmt.Sprintf("request-answer-%d.json", answerID)), append(data, '\n'))
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	return err
}
func writeWorkerAnswer(exec func(string, string, string, ...any) (sql.Result, error), request, answerID int64, kind, reply string, met *bool, local ScoredText) (int64, error) {
	var value any
	if met != nil {
		value = *met
	}
	if answerID == 0 {
		result, err := exec("save_answer", "hive_request_answers", "INSERT INTO hive_request_answers(request_id,kind,status,body,criteria_met,relevance,inquiry_value,created) VALUES(?,?,'completed',?,?,?,?,?)", request, kind, reply, value, local.Relevance, local.InquiryValue, now())
		if err != nil {
			return 0, err
		}
		return result.LastInsertId()
	}
	result, err := exec("complete_answer", "hive_request_answers", "UPDATE hive_request_answers SET status='completed',body=?,criteria_met=?,relevance=?,inquiry_value=? WHERE id=? AND request_id=? AND status IN ('pending','working')", reply, value, local.Relevance, local.InquiryValue, answerID, request)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return 0, errors.Join(err, errors.New("answer request changed"))
	}
	return answerID, nil
}
func (h *Hive) currentWorkerAnswerInput(ctx context.Context, purpose string) (map[string]any, error) {
	rows, err := h.query(ctx, "answer_request", "hive_requests", "SELECT contract,status FROM hive_requests WHERE id=?", h.requestID)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, errors.New("request missing")
	}
	criteria, err := workerHandoffCriteria(str(rows[0]["contract"]))
	if err != nil {
		return nil, err
	}
	inquiries, err := h.query(ctx, "answer_inquiries", "hive_inquiries", "SELECT id,question FROM hive_inquiries WHERE request_id=? ORDER BY id", h.requestID)
	if err != nil {
		return nil, err
	}
	var reports, research []any
	for _, inquiry := range inquiries {
		report, assessment, err := h.latestWorkerTeamRecords(ctx, id(inquiry["id"]))
		if err != nil {
			return nil, err
		}
		if report != nil {
			reports = append(reports, map[string]any{"inquiry": inquiry, "report": report, "assessment": assessment})
		}
		found, err := h.latestWorkerResearch(ctx, id(inquiry["id"]))
		if err != nil {
			return nil, err
		}
		if found != nil {
			research = append(research, found)
		}
	}
	candidates, err := h.query(ctx, "answer_current_candidates", "hive_inquiry_work", `SELECT w.* FROM hive_inquiry_work w JOIN hive_inquiries i ON i.id=w.inquiry_id WHERE i.request_id=? AND w.kind IN ('PROPOSE','CONTRIBUTE') AND NOT EXISTS(SELECT 1 FROM hive_inquiry_work later WHERE later.inquiry_id=w.inquiry_id AND later.kind IN ('PROPOSE','CONTRIBUTE') AND later.id>w.id) ORDER BY w.inquiry_id`, h.requestID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"purpose": purpose, "handoff": rows[0]["contract"], "scoring_elements": criteria, "reports": reports, "current_candidates": candidates, "research": research, "captured_utc": now()}, nil
}
func (h *Hive) processWorkerInterim(ctx context.Context, owner string) (result WorkerQueenResult, handled bool, err error) {
	if !h.workerControlsEnabled() {
		return result, false, nil
	}
	rows, err := h.query(ctx, "claim_interim_queen", "hive_worker_queen", `UPDATE hive_worker_queen SET owner=? WHERE request_id=? AND owner='' AND EXISTS(SELECT 1 FROM hive_requests r WHERE r.id=hive_worker_queen.request_id AND r.status IN ('working','paused','blocked')) AND EXISTS(SELECT 1 FROM hive_request_answers a WHERE a.request_id=hive_worker_queen.request_id AND a.kind='interim' AND a.status='pending') AND (SELECT COUNT(*) FROM hive_worker_queue WHERE owner<>'')+(SELECT COUNT(*) FROM hive_worker_queen WHERE owner<>'') < ? RETURNING request_id`, owner, h.requestID, h.Config.Workers.Count)
	if err != nil || len(rows) == 0 {
		return result, false, err
	}
	handled = true
	var answerID int64
	defer func() {
		if err != nil && answerID != 0 {
			_, saved := h.exec(context.Background(), "fail_interim_answer", "hive_request_answers", "UPDATE hive_request_answers SET status='failed',error=? WHERE id=? AND status IN ('pending','working')", err.Error(), answerID)
			err = errors.Join(err, saved)
		}
		_, released := h.exec(context.Background(), "release_interim_queen", "hive_worker_queen", "UPDATE hive_worker_queen SET owner='' WHERE request_id=? AND owner=?", h.requestID, owner)
		err = errors.Join(err, released)
	}()
	answerID, err = h.pendingWorkerAnswer(ctx, "interim")
	if err != nil {
		return result, true, err
	}
	if answerID == 0 {
		return result, true, nil
	}
	if _, err = h.exec(ctx, "start_interim_answer", "hive_request_answers", "UPDATE hive_request_answers SET status='working' WHERE id=? AND status='pending'", answerID); err != nil {
		return result, true, err
	}
	input, err := h.currentWorkerAnswerInput(ctx, "interim_answer")
	if err != nil {
		return result, true, err
	}
	h.Log.Fields["worker"], h.Log.Fields["phase"] = owner, "queen_interim"
	prompt := h.Config.Prompts["worker_contract_authority"] + "\n\n" + h.Config.Prompts["queen_role"] + "\n\n" + h.Config.Prompts["worker_queen_interim"]
	result.Called = true
	reply, err := h.generatePrompt(ctx, "queen", h.Config.Models.Queen, "worker_queen_interim", prompt, jsonText(input), "")
	if err != nil {
		return result, true, err
	}
	h.Log.Log("hive", "worker_interim_reply", map[string]any{"body": reply, "answer_id": answerID})
	scored, err := ParseScoredText(reply)
	if err != nil {
		return result, true, err
	}
	err = h.Store.Transaction(ctx, h.Log.Fields, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
		_, e := writeWorkerAnswer(exec, h.requestID, answerID, "interim", reply, nil, scored)
		return e
	})
	if err == nil {
		err = h.exportWorkerAnswer(ctx, answerID)
	}
	return result, true, err
}
func (h *Hive) submitWorkerAnswer(ctx context.Context, kind string) (WorkerAnswer, error) {
	if kind != "interim" && kind != "final" {
		return WorkerAnswer{}, errors.New("unknown answer control")
	}
	var answerID int64
	err := h.Store.Transaction(ctx, h.Log.Fields, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
		row, e := exec("request_answer", "hive_request_answers", "INSERT INTO hive_request_answers(request_id,kind,status,created) VALUES(?,?,'pending',?)", h.requestID, kind, now())
		if e != nil {
			return e
		}
		answerID, e = row.LastInsertId()
		if e != nil {
			return e
		}
		if kind == "final" {
			_, e = exec("request_finish", "hive_requests", "UPDATE hive_requests SET status='converging',finish_reason='operator_finish_requested' WHERE id=? AND status NOT IN ('returned','finished')", h.requestID)
		}
		return e
	})
	if err != nil {
		return WorkerAnswer{}, err
	}
	return h.readWorkerAnswer(ctx, answerID)
}
func (h *Hive) finishWorkerControl(ctx context.Context, answerID int64, handoff string, criteria []ContractCriterion, reports []workerTeamReport) (completion WorkerCompletion, err error) {
	defer func() {
		if err != nil {
			_, saved := h.exec(context.Background(), "fail_final_answer", "hive_request_answers", "UPDATE hive_request_answers SET status='failed',error=? WHERE id=? AND status IN ('pending','working')", err.Error(), answerID)
			err = errors.Join(err, saved)
		}
	}()
	if _, err = h.exec(ctx, "start_final_answer", "hive_request_answers", "UPDATE hive_request_answers SET status='working' WHERE id=? AND status='pending'", answerID); err != nil {
		return completion, err
	}
	input := map[string]any{"purpose": "final_answer", "handoff": handoff, "scoring_elements": criteria, "reports": reports}
	if len(reports) == 0 {
		input, err = h.currentWorkerAnswerInput(ctx, "final_answer")
		if err != nil {
			return completion, err
		}
	}
	h.Log.Fields["phase"] = "queen_finish"
	prompt := h.Config.Prompts["worker_contract_authority"] + "\n\n" + h.Config.Prompts["queen_role"] + "\n\n" + h.Config.Prompts["worker_queen_finish"]
	reply, err := h.generatePrompt(ctx, "queen", h.Config.Models.Queen, "worker_queen_finish", prompt, jsonText(input), "")
	if err != nil {
		return completion, err
	}
	h.Log.Log("hive", "worker_final_reply", map[string]any{"body": reply, "answer_id": answerID})
	scored, err := ParseScoredText(reply)
	if err != nil {
		return completion, err
	}
	err = h.Store.Transaction(ctx, h.Log.Fields, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
		if _, e := writeWorkerAnswer(exec, h.requestID, answerID, "final", reply, nil, scored); e != nil {
			return e
		}
		changed, e := exec("finish_request_by_user", "hive_requests", "UPDATE hive_requests SET status='finished',finish_reason='operator_finished',result=?,criteria_met=NULL WHERE id=? AND status='converging'", reply, h.requestID)
		if e != nil {
			return e
		}
		n, e := changed.RowsAffected()
		if e != nil || n != 1 {
			return errors.Join(e, errors.New("request state changed before user finish"))
		}
		// Closing the parent also supplies its best final answer to queued interim asks.
		_, e = exec("finish_pending_answers", "hive_request_answers", "UPDATE hive_request_answers SET status='completed',body=?,criteria_met=NULL,relevance=?,inquiry_value=? WHERE request_id=? AND status='pending'", reply, scored.Relevance, scored.InquiryValue, h.requestID)
		return e
	})
	if err != nil {
		return completion, err
	}
	if err = h.exportWorkerAnswer(ctx, answerID); err != nil {
		return completion, err
	}
	return WorkerCompletion{Status: "finished", Answer: scored.Body, CriteriaMet: nil}, nil
}
func (h *Hive) RunWorkerControl(ctx context.Context, kind string, wait bool) (WorkerAnswer, error) {
	if !h.workerControlsEnabled() {
		return WorkerAnswer{}, errors.New("answer controls require the current prompts and schema; apply a full version update")
	}
	rows, err := h.query(ctx, "control_current_request", "hive_requests", "SELECT status,result,criteria_met FROM hive_requests WHERE id=?", h.requestID)
	if err != nil {
		return WorkerAnswer{}, err
	}
	if len(rows) != 1 {
		return WorkerAnswer{}, errors.New("request missing")
	}
	if state := str(rows[0]["status"]); state == "returned" || state == "finished" {
		answer := WorkerAnswer{RequestID: h.requestID, Kind: "final", Status: "completed", Body: str(rows[0]["result"])}
		if rows[0]["criteria_met"] != nil {
			value := id(rows[0]["criteria_met"]) != 0
			answer.CriteriaMet = &value
		}
		return answer, nil
	}
	answer, err := h.submitWorkerAnswer(ctx, kind)
	if err != nil || !wait {
		return answer, err
	}
	var count atomic.Int64
	view, err := workerView(h, h.Text.Inner, &count)
	if err != nil {
		return answer, err
	}
	defer func() {
		if count.Load() > 0 {
			_, _ = h.exec(context.Background(), "operator_call_count", "hive_runs", "UPDATE hive_runs SET calls=calls+? WHERE id=?", count.Load(), h.runID)
		}
	}()
	owner := fmt.Sprintf("operator-answer-%d", answer.ID)
	for {
		if kind == "interim" {
			_, _, err = view.processWorkerInterim(ctx, owner)
		} else {
			ready, e := view.query(ctx, "finish_ready", "hive_worker_queen", "SELECT NOT EXISTS(SELECT 1 FROM hive_worker_queue WHERE request_id=? AND owner<>'') AND NOT EXISTS(SELECT 1 FROM hive_worker_queen WHERE request_id=? AND owner<>'') AS ready", h.requestID, h.requestID)
			if e != nil {
				return answer, e
			}
			if len(ready) == 1 && id(ready[0]["ready"]) != 0 {
				var completed WorkerCompletion
				completed, err = h.CompleteWorkerRequest(ctx, h.Text.Inner)
				count.Add(int64(completed.Calls))
				if errors.Is(err, errWorkerCompletionBusy) {
					err = nil
				}
			}
		}
		if err != nil {
			return answer, err
		}
		answer, err = h.readWorkerAnswer(ctx, answer.ID)
		if err != nil {
			return answer, err
		}
		if answer.Status == "completed" {
			err = h.exportWorkerAnswer(ctx, answer.ID)
			return answer, err
		}
		if answer.Status == "failed" {
			return answer, errors.New(answer.Error)
		}
		select {
		case <-ctx.Done():
			return answer, ctx.Err()
		case <-time.After(time.Duration(max(1, h.Config.Workers.IdlePollMS)) * time.Millisecond):
		}
	}
}
func executeWorkerControl(args []string) error {
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	dir := f.String("dir", ".", "runtime folder")
	config := f.String("config", "", "current configuration, defaults to runtime config")
	request := f.Int64("request-id", 0, "request ID, defaults to latest")
	wait := f.Bool("wait", true, "wait for the answer without starting exploration")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if *config == "" {
		*config = filepath.Join(*dir, "config.json")
	}
	c, err := LoadConfig(*config)
	if err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(*dir, c.Files.State)); err != nil {
		return err
	}
	h, err := OpenHive(c, *dir, nil)
	if err != nil {
		return err
	}
	defer h.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	query := "SELECT id,run_id FROM hive_requests ORDER BY id DESC LIMIT 1"
	var params []any
	if *request > 0 {
		query = "SELECT id,run_id FROM hive_requests WHERE id=?"
		params = []any{*request}
	}
	rows, err := h.query(ctx, "control_request", "hive_requests", query, params...)
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return errors.New("request missing")
	}
	h.requestID, h.runID = id(rows[0]["id"]), id(rows[0]["run_id"])
	h.Log.Fields["request_id"], h.Log.Fields["run_id"] = h.requestID, h.runID
	kind := "interim"
	if args[0] == "finish" {
		kind = "final"
	}
	answer, runErr := h.RunWorkerControl(ctx, kind, *wait)
	return errors.Join(runErr, json.NewEncoder(os.Stdout).Encode(answer))
}
