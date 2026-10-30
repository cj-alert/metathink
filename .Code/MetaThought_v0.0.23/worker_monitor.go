package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type WorkerQueenResult struct {
	Called        bool
	BeginCollapse bool
}

// workerQueenInput reads current structural facts in one SQLite snapshot. The
// selected work remains an incremental item; these counts neither assess its
// quality nor mark unpresented work as reviewed.
func (h *Hive) workerQueenInput(ctx context.Context, focus int64, work []map[string]any, prior string) (string, error) {
	rows, err := h.query(ctx, "queen_current_context", "hive_inquiries", `WITH request_inquiries AS (
 SELECT id,question FROM hive_inquiries WHERE request_id=?
 ), member_facts AS (
 SELECT m.inquiry_id,COUNT(*) AS member_count,
 SUM(EXISTS(SELECT 1 FROM hive_inquiry_work w WHERE w.inquiry_id=m.inquiry_id AND w.drone_id=m.drone_id
 AND w.kind IN ('PROPOSE','JOIN','CONTRIBUTE'))) AS contributor_count
 FROM hive_inquiry_members m JOIN request_inquiries i ON i.id=m.inquiry_id GROUP BY m.inquiry_id
 ), work_facts AS (
 SELECT w.inquiry_id,COUNT(*) AS work_count,MAX(w.id) AS latest_work_id,
 SUM(CASE WHEN w.id>COALESCE(s.last_work_id,0) THEN 1 ELSE 0 END) AS unreviewed_work_count
 FROM hive_inquiry_work w JOIN request_inquiries i ON i.id=w.inquiry_id
 LEFT JOIN hive_worker_queen_seen s ON s.request_id=? AND s.inquiry_id=i.id GROUP BY w.inquiry_id
 ) SELECT r.contract,i.id AS inquiry_id,i.question,
 (SELECT COUNT(*) FROM request_inquiries) AS request_inquiry_count,
 (SELECT COUNT(*) FROM member_facts WHERE member_count>1 AND contributor_count>1) AS request_team_count,
 COALESCE((SELECT SUM(work_count) FROM work_facts),0) AS request_work_count,
 COALESCE((SELECT MAX(latest_work_id) FROM work_facts),0) AS request_latest_work_id,
 COALESCE(m.member_count,0) AS focused_member_count,COALESCE(m.contributor_count,0) AS focused_contributor_count,
 COALESCE(w.work_count,0) AS focused_work_count,COALESCE(w.latest_work_id,0) AS focused_latest_work_id,
 COALESCE(s.last_work_id,0) AS reviewed_through_work_id,COALESCE(w.unreviewed_work_count,0) AS unreviewed_work_count
 FROM hive_requests r JOIN request_inquiries i ON i.id=?
 LEFT JOIN member_facts m ON m.inquiry_id=i.id LEFT JOIN work_facts w ON w.inquiry_id=i.id
 LEFT JOIN hive_worker_queen_seen s ON s.request_id=r.id AND s.inquiry_id=i.id WHERE r.id=?`,
		h.requestID, h.requestID, focus, h.requestID)
	if err != nil {
		return "", err
	}
	if len(rows) != 1 {
		return "", errors.New("Queen request or focused inquiry is missing")
	}
	revision, err := h.currentWorkerRevision(ctx, h.requestID)
	if err != nil {
		return "", err
	}
	row := rows[0]
	// Read at most one recent original within the captured facts boundary. It
	// supplements the FIFO item; it does not advance that item's checkpoint.
	latest, err := h.query(ctx, "queen_latest_work", "hive_inquiry_work",
		`SELECT * FROM hive_inquiry_work WHERE inquiry_id=? AND id<=?
		 AND kind IN ('PROPOSE','JOIN','CONTRIBUTE','CONNECT') ORDER BY id DESC LIMIT 1`,
		focus, id(row["focused_latest_work_id"]))
	if err != nil {
		return "", err
	}
	if len(latest) == 1 {
		for _, selected := range work {
			if id(selected["id"]) == id(latest[0]["id"]) {
				latest = nil
				break
			}
		}
	}
	facts := map[string]any{
		"request": map[string]any{
			"inquiry_count": row["request_inquiry_count"], "observed_team_count": row["request_team_count"],
			"work_record_count": row["request_work_count"], "latest_work_id": row["request_latest_work_id"],
		},
		"focus": map[string]any{
			"inquiry_id": row["inquiry_id"], "committed_member_count": row["focused_member_count"],
			"contributing_member_count": row["focused_contributor_count"], "work_record_count": row["focused_work_count"],
			"latest_work_id": row["focused_latest_work_id"], "reviewed_through_work_id": row["reviewed_through_work_id"],
			"unreviewed_work_count": row["unreviewed_work_count"],
		},
	}
	report, assessment, err := h.latestWorkerTeamRecords(ctx, focus)
	if err != nil {
		return "", err
	}
	research, err := h.latestWorkerResearch(ctx, focus)
	if err != nil {
		return "", err
	}
	return jsonText(map[string]any{"handoff": row["contract"], "contract_revision": revision,
		"inquiry": []map[string]any{{"id": row["inquiry_id"], "question": row["question"]}},
		"work":    work, "latest_work": workerOne(latest), "prior_queen_note": prior, "current_facts": facts,
		"team_report": report, "team_assessment": assessment, "research_work": research}), nil
}

func (h *Hive) workerQueenPrompt() string {
	return h.Config.Prompts["queen_role"] + "\n\n" + h.Config.Prompts["worker_queen_monitor"] + "\n\n" +
		h.Config.Prompts["worker_queen_context"] + "\n\n" + h.Config.Prompts["worker_queen_scores"] + "\n\n" + h.Config.Prompts["worker_queen_format"] + "\n\n" + h.Config.Prompts["worker_contract_authority"]
}

// Monitoring consumes one original operational contribution at a time. Its
// checkpoint is the exact captured item, never a later MAX observed on commit.
func (h *Hive) MonitorWorkerQueen(ctx context.Context, owner string) (result WorkerQueenResult, err error) {
	if answer, handled, e := h.processWorkerInterim(ctx, owner); handled || e != nil {
		return answer, e
	}
	rows, err := h.query(ctx, "claim_queen", "hive_worker_queen", `UPDATE hive_worker_queen SET owner=? WHERE request_id=? AND owner='' AND EXISTS(SELECT 1 FROM hive_requests r WHERE r.id=hive_worker_queen.request_id AND r.status='working') AND
 (SELECT COUNT(*) FROM hive_worker_queue WHERE owner<>'')+(SELECT COUNT(*) FROM hive_worker_queen WHERE owner<>'') < ?
 AND (EXISTS(SELECT 1 FROM hive_inquiry_work w JOIN hive_inquiries i ON i.id=w.inquiry_id LEFT JOIN hive_worker_queen_seen s ON s.request_id=i.request_id AND s.inquiry_id=i.id WHERE i.request_id=? AND w.id>COALESCE(s.last_work_id,0))
 OR `+workerResourceDueSQL("hive_worker_queen.request_id")+`) RETURNING request_id,note`, owner, h.requestID, h.Config.Workers.Count, h.requestID, h.Config.Workers.ResourceReviewEvery, h.Config.Workers.ResourceReviewEvery)
	if err != nil || len(rows) == 0 {
		return result, err
	}
	if handled, review, resourceErr := h.reviewWorkerResources(ctx, owner); handled || resourceErr != nil {
		return review, resourceErr
	}
	prior := str(rows[0]["note"])
	work, err := h.query(ctx, "queen_next", "hive_inquiry_work", `SELECT w.* FROM hive_inquiry_work w JOIN hive_inquiries i ON i.id=w.inquiry_id LEFT JOIN hive_worker_queen_seen s ON s.request_id=i.request_id AND s.inquiry_id=i.id WHERE i.request_id=? AND w.id>COALESCE(s.last_work_id,0) ORDER BY w.id LIMIT 1`, h.requestID)
	if err != nil {
		return result, err
	}
	if len(work) != 1 {
		return result, errors.New("claimed Queen has no unreviewed work")
	}
	through, focus := id(work[0]["id"]), id(work[0]["inquiry_id"])
	if _, err = h.exec(ctx, "queen_snapshot", "hive_worker_queen", "UPDATE hive_worker_queen SET focus_id=?,through_id=? WHERE request_id=? AND owner=?", focus, through, h.requestID, owner); err != nil {
		return result, err
	}
	input, err := h.workerQueenInput(ctx, focus, work, prior)
	if err != nil {
		return result, err
	}
	prompt := h.workerQueenPrompt()
	result.Called = true
	reply, err := h.generatePrompt(ctx, "queen", h.Config.Models.Queen, "worker_queen_monitor", prompt, input, "")
	if err != nil {
		if workerRetryExhausted(err) {
			_, pauseErr := h.pauseWorkerProvider(context.Background(), err, false)
			_, releaseErr := h.exec(context.Background(), "release_exhausted_queen", "hive_worker_queen", "UPDATE hive_worker_queen SET owner='' WHERE request_id=? AND owner=?", h.requestID, owner)
			err = errors.Join(err, pauseErr, releaseErr)
		}
		return result, err
	}
	h.Log.Log("hive", "worker_queen_reply", map[string]any{"body": reply, "reviewed_work_id": through, "inquiry_id": focus})
	action, err := h.routeWorkerQueen(ctx, input, reply)
	if err != nil {
		h.Log.Log("queen", "worker_queen_routing_rejected", map[string]any{"error": err.Error(), "reviewed_work_id": through, "outcome": "not_applied"})
		_, releaseErr := h.exec(context.Background(), "release_rejected_queen_route", "hive_worker_queen", "UPDATE hive_worker_queen SET owner='' WHERE request_id=? AND owner=?", h.requestID, owner)
		return result, errors.Join(err, releaseErr)
	}
	err = h.Store.Transaction(ctx, h.Log.Fields, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
		changed, e := exec("finish_queen", "hive_worker_queen", "UPDATE hive_worker_queen SET owner='',note=? WHERE request_id=? AND owner=? AND focus_id=? AND through_id=?", action.Body, h.requestID, owner, focus, through)
		if e != nil {
			return e
		}
		n, e := changed.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return errors.New("Queen ownership changed; preserve reply without applying it")
		}
		if _, e = exec("reviewed", "hive_worker_queen_seen", "INSERT INTO hive_worker_queen_seen(request_id,inquiry_id,last_work_id) VALUES(?,?,?) ON CONFLICT(request_id,inquiry_id) DO UPDATE SET last_work_id=MAX(last_work_id,excluded.last_work_id)", h.requestID, focus, through); e != nil {
			return e
		}
		if action.Kind == "GUIDE" {
			if _, e = exec("guide", "hive_worker_guidance", "INSERT INTO hive_worker_guidance(request_id,inquiry_id,body,relevance,inquiry_value,created) VALUES(?,?,?,?,?,?)", h.requestID, focus, action.Body, action.Relevance, action.InquiryValue, now()); e != nil {
				return e
			}
		}
		if action.Kind == "BEGIN_COLLAPSE" {
			if _, e = exec("begin_collapse", "hive_requests", "UPDATE hive_requests SET status='converging',finish_reason='convergence_requested' WHERE id=? AND status='working'", h.requestID); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	result.BeginCollapse = action.Kind == "BEGIN_COLLAPSE"
	h.Log.Log("hive", "worker_queen_decision", map[string]any{"action": action, "inquiry_id": focus, "reviewed_work_id": through, "outcome": "saved"})
	return result, nil
}

func (h *Hive) workerPoolIdle(ctx context.Context) (bool, error) {
	// A single read snapshot must include readiness as well as ownership. A
	// worker can release a ready actor after another worker's empty claim result.
	// Keep the awake/unread predicates identical to ClaimDrone and the Queen
	// unseen-work predicate identical to MonitorWorkerQueen.
	rows, err := h.query(ctx, "worker_idle", "hive_worker_queue", `WITH changes AS (
  SELECT COALESCE((SELECT MAX(w.id) FROM hive_inquiry_work w JOIN hive_inquiries i ON i.id=w.inquiry_id
  WHERE i.request_id=? AND w.kind IN ('PROPOSE','JOIN','CONTRIBUTE','CONNECT','SPAWN','RESEARCH')),0) AS work_id,
  COALESCE((SELECT MAX(id) FROM hive_worker_guidance WHERE request_id=?),0) AS guidance_id
 ) SELECT CASE WHEN r.status<>'working' THEN 1 ELSE (
  NOT EXISTS(SELECT 1 FROM hive_worker_queue WHERE owner<>'')
  AND NOT EXISTS(SELECT 1 FROM hive_worker_queen WHERE owner<>'')
  AND NOT EXISTS(SELECT 1 FROM hive_worker_queue q WHERE q.request_id=r.id AND q.owner='' AND (
   q.asleep_work_id IS NULL OR c.work_id>q.asleep_work_id OR c.guidance_id>COALESCE(q.asleep_guidance_id,0) OR q.read_work_id IS NOT NULL
   OR EXISTS(SELECT 1 FROM hive_inquiry_work w WHERE w.inquiry_id=q.focus_id AND w.drone_id<>q.drone_id AND w.id>q.peer_after AND w.kind IN ('PROPOSE','JOIN','CONTRIBUTE'))
   OR EXISTS(SELECT 1 FROM hive_inquiry_work w WHERE w.target_inquiry_id=q.focus_id AND w.id>q.connection_after AND w.kind='CONNECT')))
  AND NOT EXISTS(SELECT 1 FROM hive_inquiry_work w JOIN hive_inquiries i ON i.id=w.inquiry_id
   LEFT JOIN hive_worker_queen_seen s ON s.request_id=i.request_id AND s.inquiry_id=i.id
   WHERE i.request_id=r.id AND w.id>COALESCE(s.last_work_id,0))
  `+h.pendingInterimSQL("r.id")+` AND NOT `+workerResourceDueSQL("r.id")+`
 ) END AS idle FROM hive_requests r CROSS JOIN changes c WHERE r.id=?`, h.requestID, h.requestID, h.Config.Workers.ResourceReviewEvery, h.Config.Workers.ResourceReviewEvery, h.requestID)
	if err != nil {
		return false, err
	}
	if len(rows) != 1 {
		return false, errors.New("worker request missing during idle check")
	}
	return id(rows[0]["idle"]) != 0, nil
}

func recoverWorkerOwner(ctx context.Context, c Config, dir, owner string) error {
	h, err := OpenHive(c, dir, nil)
	if err != nil {
		return err
	}
	defer h.Close()
	// Explicit human/process confirmation of a stopped owner is required by CLI.
	// No timer expires a live model call or automatically replays an unknown one.
	err = h.Store.Transaction(ctx, map[string]any{"scope": "developer", "worker": owner}, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
		if _, e := exec("recover_stopped_owner", "hive_worker_queue", "UPDATE hive_worker_queue SET owner='' WHERE owner=?", owner); e != nil {
			return e
		}
		_, e := exec("recover_stopped_owner", "hive_worker_queen", "UPDATE hive_worker_queen SET owner='' WHERE owner=?", owner)
		return e
	})
	if err == nil {
		h.Log.Log("developer", "worker_owner_released", map[string]any{"worker": owner, "outcome": "released", "next_phase_preserved": true})
		fmt.Println(jsonText(map[string]any{"owner": owner, "outcome": "released", "next_phase_preserved": true}))
	}
	return err
}
