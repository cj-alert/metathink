package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"
)

type WorkerClaim struct {
	RequestID   int64  `json:"request_id"`
	DroneID     int64  `json:"drone_id"`
	Owner       string `json:"owner"`
	Phase       string `json:"phase"`
	FocusID     int64  `json:"focus_id"`
	DiscoveryID int64  `json:"discovery_id"`
	ReadWorkID  int64  `json:"read_work_id"`
}

func workerFields(claim WorkerClaim) map[string]any {
	return map[string]any{"request_id": claim.RequestID, "drone_id": claim.DroneID, "worker_id": claim.Owner, "phase": claim.Phase}
}

func (h *Hive) workerQuery(ctx context.Context, claim WorkerClaim, op, table, query string, args ...any) ([]map[string]any, error) {
	return h.Store.Query(ctx, op, table, workerFields(claim), query, args...)
}

// InitWorkerQueue appends missing actors without loading the population into Go.
func (h *Hive) InitWorkerQueue(ctx context.Context, requestID int64) error {
	rows, err := h.Store.Query(ctx, "queue_root", "hive_inquiries", map[string]any{"request_id": requestID},
		"SELECT id FROM hive_inquiries WHERE request_id=? ORDER BY id LIMIT 1", requestID)
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return fmt.Errorf("request %d has no inquiry for the worker queue", requestID)
	}
	focusID := id(rows[0]["id"])
	_, err = h.Store.Exec(ctx, "initialize_queue", "hive_worker_queue", map[string]any{"request_id": requestID},
		`INSERT INTO hive_worker_queue(drone_id,request_id,focus_id,discovery_id,queue_order)
		 SELECT d.id,?,?,?,(SELECT COALESCE(MAX(queue_order),0) FROM hive_worker_queue)+ROW_NUMBER() OVER(ORDER BY d.id)
		 FROM hive_drones d WHERE NOT EXISTS(SELECT 1 FROM hive_worker_queue q WHERE q.drone_id=d.id)`,
		requestID, focusID, focusID)
	return err
}

// ClaimDrone is one atomic SQLite statement. Existing ownership is retained
// across Contribute/Expand; each newly claimed actor occupies one model slot.
func (h *Hive) ClaimDrone(ctx context.Context, owner string, workers int) (*WorkerClaim, error) {
	if strings.TrimSpace(owner) == "" || workers < 1 || h.requestID < 1 {
		return nil, fmt.Errorf("worker owner, positive concurrency and request ID are required")
	}
	rows, err := h.Store.Query(ctx, "claim_drone", "hive_worker_queue",
		map[string]any{"request_id": h.requestID, "worker_id": owner, "drone_id": nil, "phase": nil},
		`WITH changes AS (
		 SELECT COALESCE((SELECT MAX(w.id) FROM hive_inquiry_work w JOIN hive_inquiries i ON i.id=w.inquiry_id
		 WHERE i.request_id=? AND w.kind IN ('PROPOSE','JOIN','CONTRIBUTE','CONNECT','SPAWN','RESEARCH')),0) AS work_id,
		 COALESCE((SELECT MAX(id) FROM hive_worker_guidance WHERE request_id=?),0) AS guidance_id
		 ) UPDATE hive_worker_queue SET owner=? WHERE drone_id=(
		 SELECT drone_id FROM (
		 SELECT drone_id,0 AS priority FROM hive_worker_queue WHERE owner=? AND request_id=?
		 UNION ALL SELECT chosen.drone_id,1 AS priority FROM (
		 SELECT q.drone_id FROM hive_worker_queue q CROSS JOIN changes c
		 WHERE q.request_id=? AND q.owner='' AND (
		 q.asleep_work_id IS NULL OR c.work_id>q.asleep_work_id OR c.guidance_id>COALESCE(q.asleep_guidance_id,0) OR q.read_work_id IS NOT NULL
		 OR EXISTS(SELECT 1 FROM hive_inquiry_work w WHERE w.inquiry_id=q.focus_id AND w.drone_id<>q.drone_id AND w.id>q.peer_after AND w.kind IN ('PROPOSE','JOIN','CONTRIBUTE'))
		 OR EXISTS(SELECT 1 FROM hive_inquiry_work w WHERE w.target_inquiry_id=q.focus_id AND w.id>q.connection_after AND w.kind='CONNECT'))
		 AND ((SELECT COUNT(*) FROM hive_worker_queue WHERE owner<>'')+(SELECT COUNT(*) FROM hive_worker_queen WHERE owner<>''))<?
		 ORDER BY q.queue_order,q.drone_id LIMIT 1) chosen) ORDER BY priority LIMIT 1)
		 AND EXISTS(SELECT 1 FROM hive_requests WHERE id=? AND status='working')
		 RETURNING request_id,drone_id,owner,phase,focus_id,discovery_id,read_work_id`,
		h.requestID, h.requestID, owner, owner, h.requestID, h.requestID, workers, h.requestID)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	row := rows[0]
	claim := &WorkerClaim{RequestID: id(row["request_id"]), DroneID: id(row["drone_id"]), Owner: str(row["owner"]),
		Phase: str(row["phase"]), FocusID: id(row["focus_id"]), DiscoveryID: id(row["discovery_id"]), ReadWorkID: id(row["read_work_id"])}
	h.Log.Log("hive", "worker_claim", workerFields(*claim))
	return claim, nil
}

func workerOne(rows []map[string]any) map[string]any {
	if len(rows) == 0 {
		return nil
	}
	return rows[0]
}

// workerArtifact retains the original body and an ordinary predecessor ID so
// READ can follow older evidence without including the entire history.
func (h *Hive) workerArtifact(ctx context.Context, claim WorkerClaim, workID int64) (map[string]any, error) {
	rows, err := h.workerQuery(ctx, claim, "read_artifact", "hive_inquiry_work",
		`SELECT w.*,(SELECT MAX(p.id) FROM hive_inquiry_work p WHERE p.inquiry_id=w.inquiry_id AND p.id<w.id) AS previous_work_id
		 FROM hive_inquiry_work w JOIN hive_inquiries i ON i.id=w.inquiry_id WHERE w.id=? AND i.request_id=?`, workID, claim.RequestID)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("work %d does not belong to request %d", workID, claim.RequestID)
	}
	return rows[0], nil
}

// WorkerInput presents one atomic intellectual exchange. Uncommitted spawned
// actors receive compact inquiry cards, even when invited to the root inquiry.
// A NULL cohort identifies an original seed; exact originals remain available through READ.
func (h *Hive) WorkerInput(ctx context.Context, claim WorkerClaim) (string, error) {
	queue, err := h.workerQuery(ctx, claim, "worker_context_cursor", "hive_worker_queue",
		"SELECT peer_after,connection_after,discovery_id,omit_oversized FROM hive_worker_queue WHERE drone_id=? AND request_id=? AND owner=? AND phase=?",
		claim.DroneID, claim.RequestID, claim.Owner, claim.Phase)
	if err != nil {
		return "", err
	}
	if len(queue) != 1 {
		return "", fmt.Errorf("worker does not own this phase")
	}
	// Capture the wake-up boundary before reading context. Work arriving during
	// thinking cannot be silently included in a later PASS sleep boundary.
	revisions, err := h.workerQuery(ctx, claim, "worker_context_revision", "hive_inquiry_work",
		`SELECT COALESCE((SELECT MAX(w.id) FROM hive_inquiry_work w JOIN hive_inquiries i ON i.id=w.inquiry_id
		 WHERE i.request_id=? AND w.kind IN ('PROPOSE','JOIN','CONTRIBUTE','CONNECT','SPAWN','RESEARCH')),0) AS work_id,
		 COALESCE((SELECT MAX(id) FROM hive_worker_guidance WHERE request_id=?),0) AS guidance_id`, claim.RequestID, claim.RequestID)
	if err != nil {
		return "", err
	}
	request, err := h.workerQuery(ctx, claim, "worker_handoff", "hive_requests", "SELECT contract FROM hive_requests WHERE id=?", claim.RequestID)
	if err != nil {
		return "", err
	}
	drone, err := h.workerQuery(ctx, claim, "worker_actor", "hive_drones", "SELECT id,name,contribute,expand,signal FROM hive_drones WHERE id=?", claim.DroneID)
	if err != nil {
		return "", err
	}
	focus, err := h.workerQuery(ctx, claim, "worker_focus", "hive_inquiries", "SELECT id,request_id,question,proposed_by,created,(SELECT MIN(id) FROM hive_inquiries WHERE request_id=?) AS root_id FROM hive_inquiries WHERE id=? AND request_id=?", claim.RequestID, claim.FocusID, claim.RequestID)
	if err != nil {
		return "", err
	}
	if len(request) != 1 || len(drone) != 1 || len(focus) != 1 {
		return "", fmt.Errorf("worker request, actor or focused inquiry is missing")
	}
	rootID := id(focus[0]["root_id"])
	delete(focus[0], "root_id")
	revision, err := h.currentWorkerRevision(ctx, claim.RequestID)
	if err != nil {
		return "", err
	}
	memberships, err := h.workerQuery(ctx, claim, "worker_memberships", "hive_inquiry_members",
		"SELECT m.inquiry_id FROM hive_inquiry_members m JOIN hive_inquiries i ON i.id=m.inquiry_id WHERE m.drone_id=? AND i.request_id=? ORDER BY m.inquiry_id", claim.DroneID, claim.RequestID)
	if err != nil {
		return "", err
	}
	if len(memberships) == 0 && queue[0]["omit_oversized"] != nil {
		signals, err := h.workerQuery(ctx, claim, "worker_newborn_inquiries", "hive_inquiries", `SELECT i.id,i.question,
		 (SELECT MAX(w.id) FROM hive_inquiry_work w WHERE w.inquiry_id=i.id AND w.kind IN ('PROPOSE','JOIN','CONTRIBUTE')) AS latest_work_id
		 FROM hive_inquiries i WHERE i.request_id=? AND i.id<>? ORDER BY i.id`, claim.RequestID, rootID)
		if err != nil {
			return "", err
		}
		omitOversized := id(queue[0]["omit_oversized"]) != 0
		selected := map[int64]bool{}
		discovered := false
		if omitOversized {
			// DISCOVER is an explicit request to browse beyond the initial menu.
			// Its existing operational record survives READs and process restarts.
			rows, err := h.workerQuery(ctx, claim, "worker_newborn_discovered", "hive_inquiry_work",
				`SELECT EXISTS(SELECT 1 FROM hive_inquiry_work w JOIN hive_inquiries i ON i.id=w.inquiry_id
				 WHERE i.request_id=? AND w.drone_id=? AND w.kind='DISCOVER') AS discovered`, claim.RequestID, claim.DroneID)
			if err != nil {
				return "", err
			}
			discovered = id(rows[0]["discovered"]) != 0
			if !discovered {
				resources, err := h.workerQuery(ctx, claim, "worker_newborn_resources", "hive_worker_resources",
					"SELECT selected_ids FROM hive_worker_resources WHERE request_id=?", claim.RequestID)
				if err != nil {
					return "", err
				}
				if len(resources) != 0 {
					var ids []int64
					if err := json.Unmarshal([]byte(str(resources[0]["selected_ids"])), &ids); err != nil {
						return "", fmt.Errorf("saved resource inquiry selection: %w", err)
					}
					if ids == nil {
						return "", fmt.Errorf("saved resource inquiry selection must be an array")
					}
					for _, inquiryID := range ids {
						selected[inquiryID] = true
					}
				}
			}
		}
		shown, omitted := []int64{}, []int64{}
		visible := make([]map[string]any, 0, len(signals))
		for _, signal := range signals {
			inquiryID := id(signal["id"])
			if selected[inquiryID] {
				omitted = append(omitted, inquiryID)
				continue
			}
			signal["question_first_line"] = strings.SplitN(str(signal["question"]), "\n", 2)[0]
			delete(signal, "question")
			shown = append(shown, inquiryID)
			visible = append(visible, signal)
		}
		var artifact map[string]any
		if claim.ReadWorkID != 0 {
			artifact, err = h.workerArtifact(ctx, claim, claim.ReadWorkID)
			if err != nil {
				return "", err
			}
		}
		result, err := h.Store.Exec(ctx, "save_offered_context", "hive_worker_queue", workerFields(claim),
			`UPDATE hive_worker_queue SET offered_peer_id=0,offered_connection_id=0,seen_work_id=?,seen_guidance_id=?
			 WHERE drone_id=? AND request_id=? AND owner=? AND phase=?`, id(revisions[0]["work_id"]), id(revisions[0]["guidance_id"]), claim.DroneID, claim.RequestID, claim.Owner, claim.Phase)
		if err != nil {
			return "", err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return "", fmt.Errorf("worker lost ownership while preparing context: rows=%d error=%v", count, err)
		}
		actor := map[string]any{"id": drone[0]["id"], "name": drone[0]["name"], "contribute": drone[0]["contribute"], "expand": drone[0]["expand"]}
		fields := workerFields(claim)
		fields["omit_oversized"], fields["discovered"], fields["shown_inquiry_ids"], fields["omitted_inquiry_ids"] = omitOversized, discovered, shown, omitted
		h.Log.Log("hive", "newborn_menu", fields)
		return jsonText(map[string]any{"handoff": request[0]["contract"], "contract_revision": revision, "drone": actor, "phase": claim.Phase,
			"needs_fit_choice": true, "inquiry_signals": visible, "memberships": memberships, "read_work": artifact}), nil
	}
	own, err := h.workerQuery(ctx, claim, "worker_own_latest", "hive_inquiry_work",
		`SELECT w.*,(SELECT MAX(p.id) FROM hive_inquiry_work p WHERE p.inquiry_id=w.inquiry_id AND p.id<w.id) AS previous_work_id
		 FROM hive_inquiry_work w WHERE w.inquiry_id=? AND w.drone_id=? AND w.kind IN ('PROPOSE','JOIN','CONTRIBUTE') ORDER BY w.id DESC LIMIT 1`, claim.FocusID, claim.DroneID)
	if err != nil {
		return "", err
	}
	peer, err := h.workerQuery(ctx, claim, "worker_peer", "hive_inquiry_work",
		`SELECT w.*,(SELECT MAX(p.id) FROM hive_inquiry_work p WHERE p.inquiry_id=w.inquiry_id AND p.id<w.id) AS previous_work_id
		 FROM hive_inquiry_work w WHERE w.inquiry_id=? AND w.drone_id<>? AND w.id>?
		 AND w.kind IN ('PROPOSE','JOIN','CONTRIBUTE') ORDER BY w.id LIMIT 1`, claim.FocusID, claim.DroneID, id(queue[0]["peer_after"]))
	if err != nil {
		return "", err
	}
	offer, err := h.workerQuery(ctx, claim, "worker_connection", "hive_inquiry_work",
		`SELECT w.*,(SELECT MAX(p.id) FROM hive_inquiry_work p WHERE p.inquiry_id=w.inquiry_id AND p.id<w.id) AS previous_work_id
		 FROM hive_inquiry_work w WHERE w.target_inquiry_id=? AND w.id>? AND w.kind='CONNECT' ORDER BY w.id LIMIT 1`, claim.FocusID, id(queue[0]["connection_after"]))
	if err != nil {
		return "", err
	}
	directory, err := h.workerQuery(ctx, claim, "worker_directory_offer", "hive_inquiries",
		`SELECT i.id,i.question,i.proposed_by,
		 (SELECT COUNT(*) FROM hive_inquiry_members m WHERE m.inquiry_id=i.id) AS committed_member_count,
		 (SELECT MAX(w.id) FROM hive_inquiry_work w WHERE w.inquiry_id=i.id AND w.kind IN ('PROPOSE','JOIN','CONTRIBUTE')) AS latest_work_id
		 FROM hive_inquiries i WHERE i.id=COALESCE(
		 (SELECT MIN(id) FROM hive_inquiries WHERE request_id=? AND proposed_by<>0 AND id<>? AND id>?),
		 (SELECT MIN(id) FROM hive_inquiries WHERE request_id=? AND proposed_by<>0 AND id<>?))`,
		claim.RequestID, claim.FocusID, id(queue[0]["discovery_id"]), claim.RequestID, claim.FocusID)
	if err != nil {
		return "", err
	}
	if len(directory) == 1 {
		// Automatically offer one other inquiry. The complete original is
		// available through its work ID; membership is information, not ranking.
		// This is a labelled first-line signal, not a rewritten inquiry body.
		directory[0]["question_first_line"] = strings.SplitN(str(directory[0]["question"]), "\n", 2)[0]
		delete(directory[0], "question")
	}
	guidance, err := h.workerQuery(ctx, claim, "worker_guidance", "hive_worker_guidance",
		"SELECT id,request_id,inquiry_id,body,relevance,inquiry_value,created FROM hive_worker_guidance WHERE request_id=? AND (COALESCE(inquiry_id,0)=0 OR inquiry_id=?) ORDER BY id DESC LIMIT 1", claim.RequestID, claim.FocusID)
	if err != nil {
		return "", err
	}
	if revision != nil && len(guidance) == 1 {
		created, err := time.Parse(time.RFC3339Nano, str(guidance[0]["created"]))
		if err != nil {
			return "", fmt.Errorf("saved Queen guidance time: %w", err)
		}
		if !created.After(revision.Created) {
			guidance = nil
		}
	}
	report, assessment, err := h.latestWorkerTeamRecords(ctx, claim.FocusID)
	if err != nil {
		return "", err
	}
	research, err := h.latestWorkerResearch(ctx, claim.FocusID)
	if err != nil {
		return "", err
	}
	var artifact map[string]any
	if claim.ReadWorkID != 0 {
		artifact, err = h.workerArtifact(ctx, claim, claim.ReadWorkID)
		if err != nil {
			return "", err
		}
	}
	peerID, offerID := int64(0), int64(0)
	if len(peer) == 1 {
		peerID = id(peer[0]["id"])
	}
	if len(offer) == 1 {
		offerID = id(offer[0]["id"])
	}
	result, err := h.Store.Exec(ctx, "save_offered_context", "hive_worker_queue", workerFields(claim),
		`UPDATE hive_worker_queue SET offered_peer_id=?,offered_connection_id=?,seen_work_id=?,seen_guidance_id=?
		 WHERE drone_id=? AND request_id=? AND owner=? AND phase=?`, peerID, offerID, id(revisions[0]["work_id"]), id(revisions[0]["guidance_id"]), claim.DroneID, claim.RequestID, claim.Owner, claim.Phase)
	if err != nil {
		return "", err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return "", fmt.Errorf("worker lost ownership while preparing context: rows=%d error=%v", count, err)
	}
	return jsonText(map[string]any{"handoff": request[0]["contract"], "contract_revision": revision, "drone": drone[0], "phase": claim.Phase,
		"focus": focus[0], "own_latest_work": workerOne(own), "peer_work": workerOne(peer), "connection_offer": workerOne(offer),
		"memberships": memberships, "directory_offer": workerOne(directory), "read_work": artifact, "queen_guidance": workerOne(guidance),
		"team_report": report, "team_assessment": assessment, "research_work": research}), nil
}

func (h *Hive) workerInquiry(ctx context.Context, claim WorkerClaim, inquiryID int64) error {
	rows, err := h.workerQuery(ctx, claim, "worker_inquiry", "hive_inquiries", "SELECT id FROM hive_inquiries WHERE id=? AND request_id=?", inquiryID, claim.RequestID)
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return fmt.Errorf("inquiry %d does not belong to request %d", inquiryID, claim.RequestID)
	}
	return nil
}

// AdvanceWorker commits the action, consumed context and next phase together.
// The provider call has already finished; no transaction spans external work.
func (h *Hive) AdvanceWorker(ctx context.Context, claim WorkerClaim, action InquiryAction) error {
	if claim.Owner == "" || (claim.Phase != "contribute" && claim.Phase != "expand") {
		return fmt.Errorf("a claimed Contribute or Expand phase is required")
	}
	if strings.TrimSpace(action.Body) == "" || math.IsNaN(action.Relevance) || math.IsInf(action.Relevance, 0) || action.Relevance < 0 || action.Relevance > 1 ||
		math.IsNaN(action.InquiryValue) || math.IsInf(action.InquiryValue, 0) || action.InquiryValue < 0 || action.InquiryValue > 1 {
		return fmt.Errorf("worker action requires a body and both finite local scores in [0,1]")
	}
	switch action.Kind {
	case "PROPOSE", "PASS", "DISCOVER":
	case "JOIN", "CONTRIBUTE", "CONNECT", "SPAWN", "RESEARCH":
		if err := h.workerInquiry(ctx, claim, action.InquiryID); err != nil {
			return err
		}
	case "READ":
		if _, err := h.workerArtifact(ctx, claim, action.InquiryID); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown worker action %q", action.Kind)
	}
	if action.Kind == "CONTRIBUTE" || action.Kind == "CONNECT" || action.Kind == "RESEARCH" {
		rows, err := h.workerQuery(ctx, claim, "worker_membership", "hive_inquiry_members", "SELECT drone_id FROM hive_inquiry_members WHERE inquiry_id=? AND drone_id=?", action.InquiryID, claim.DroneID)
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			return fmt.Errorf("drone %d has not committed to inquiry %d", claim.DroneID, action.InquiryID)
		}
	}
	if action.Kind == "CONNECT" {
		if action.TargetID == action.InquiryID {
			return fmt.Errorf("CONNECT requires distinct inquiries")
		}
		if err := h.workerInquiry(ctx, claim, action.TargetID); err != nil {
			return err
		}
	}
	focusID := claim.FocusID
	var spawnedID, workID int64
	var spawnedOmit bool
	err := h.Store.Transaction(ctx, workerFields(claim), func(exec func(string, string, string, ...any) (sql.Result, error)) error {
		inquiryID := action.InquiryID
		if action.Kind == "PROPOSE" {
			result, err := exec("propose", "hive_inquiries", "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", claim.RequestID, action.Body, claim.DroneID, now())
			if err != nil {
				return err
			}
			inquiryID, err = result.LastInsertId()
			if err != nil {
				return err
			}
		}
		if action.Kind == "PROPOSE" || action.Kind == "JOIN" {
			if _, err := exec("commit", "hive_inquiry_members", "INSERT OR IGNORE INTO hive_inquiry_members(inquiry_id,drone_id,commitment,created) VALUES(?,?,?,?)", inquiryID, claim.DroneID, action.Body, now()); err != nil {
				return err
			}
			focusID = inquiryID
		}
		if action.Kind == "SPAWN" {
			result, err := exec("spawn", "hive_drones",
				`INSERT INTO hive_drones(id,name,contribute,expand,signal,created)
				 SELECT n.id,'drone-'||n.id,d.contribute,d.expand,?,? FROM hive_drones d
				 CROSS JOIN (SELECT COALESCE(MAX(id),0)+1 AS id FROM hive_drones) n WHERE d.id=?`, action.Body, now(), claim.DroneID)
			if err != nil {
				return err
			}
			if count, err := result.RowsAffected(); err != nil || count != 1 {
				return fmt.Errorf("spawn creator missing: rows=%d error=%v", count, err)
			}
			spawnedID, err = result.LastInsertId()
			if err != nil {
				return err
			}
			// Draw once at birth. The saved cohort is independent of later READs,
			// menu refreshes and changes in the Queen's selected inquiries.
			spawnedOmit = rand.Float64() < h.Config.Workers.NewbornOmitProbability
			if _, err = exec("queue_spawn", "hive_worker_queue",
				`INSERT INTO hive_worker_queue(drone_id,request_id,focus_id,discovery_id,queue_order,omit_oversized)
				 VALUES(?,?,?,?,(SELECT COALESCE(MAX(queue_order),0)+1 FROM hive_worker_queue),?)`, spawnedID, claim.RequestID, inquiryID, inquiryID, spawnedOmit); err != nil {
				return err
			}
		}
		if action.Kind != "PASS" {
			if action.Kind == "DISCOVER" || action.Kind == "READ" {
				inquiryID = claim.FocusID
			}
			var target any
			if action.Kind == "CONNECT" {
				target = action.TargetID
			}
			result, err := exec("worker_action", "hive_inquiry_work",
				"INSERT INTO hive_inquiry_work(inquiry_id,target_inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,?,?,?,?,?,?,?)",
				inquiryID, target, claim.DroneID, claim.Phase, action.Kind, action.Body, action.Relevance, action.InquiryValue, now())
			if err != nil {
				return err
			}
			workID, err = result.LastInsertId()
			if err != nil {
				return err
			}
		}
		readID := any(nil)
		if action.Kind == "READ" {
			readID = action.InquiryID
		}
		active := 0
		if action.Kind != "PASS" {
			active = 1
		}
		result, err := exec("advance_worker", "hive_worker_queue",
			`UPDATE hive_worker_queue SET
			 peer_after=CASE WHEN focus_id=? THEN MAX(peer_after,offered_peer_id) ELSE 0 END,
			 connection_after=CASE WHEN focus_id=? THEN MAX(connection_after,offered_connection_id) ELSE 0 END,
			 offered_peer_id=0,offered_connection_id=0,focus_id=?,read_work_id=?,
			 discovery_id=COALESCE(
			 (SELECT MIN(id) FROM hive_inquiries WHERE request_id=? AND proposed_by<>0 AND id<>focus_id AND id>discovery_id),
			 (SELECT MIN(id) FROM hive_inquiries WHERE request_id=? AND proposed_by<>0 AND id<>focus_id),discovery_id),
			 asleep_work_id=CASE WHEN phase='expand' AND turn_activity=0 AND ?=0 THEN seen_work_id ELSE NULL END,
			 asleep_guidance_id=CASE WHEN phase='expand' AND turn_activity=0 AND ?=0 THEN seen_guidance_id ELSE NULL END,
			 turn_activity=CASE WHEN phase='expand' THEN 0 ELSE MAX(turn_activity,?) END,
			 queue_order=CASE WHEN phase='expand' THEN (SELECT COALESCE(MAX(queue_order),0)+1 FROM hive_worker_queue) ELSE queue_order END,
			 owner=CASE WHEN phase='expand' THEN '' ELSE owner END,
			 phase=CASE WHEN phase='contribute' THEN 'expand' ELSE 'contribute' END
			 WHERE drone_id=? AND request_id=? AND owner=? AND phase=?`,
			focusID, focusID, focusID, readID, claim.RequestID, claim.RequestID,
			active, active, active, claim.DroneID, claim.RequestID, claim.Owner, claim.Phase)
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return fmt.Errorf("worker no longer owns this phase: rows=%d error=%v", count, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	fields := workerFields(claim)
	fields["action"], fields["work_id"], fields["focus_id"], fields["outcome"] = action, workID, focusID, "committed"
	if spawnedID != 0 {
		fields["spawned_drone_id"] = spawnedID
		fields["omit_oversized"] = spawnedOmit
	}
	h.Log.Log("hive", "worker_action_saved", fields)
	return nil
}

// ReleaseWorker leaves the saved phase and context positions intact. It is safe
// to call during cleanup after the phase has already released itself.
func (h *Hive) ReleaseWorker(ctx context.Context, claim WorkerClaim) error {
	_, err := h.Store.Exec(ctx, "release_worker", "hive_worker_queue", workerFields(claim),
		"UPDATE hive_worker_queue SET owner='' WHERE drone_id=? AND request_id=? AND owner=?", claim.DroneID, claim.RequestID, claim.Owner)
	return err
}
