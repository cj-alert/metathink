package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type InquiryTeam struct {
	InquiryID    int64   `json:"inquiry_id"`
	Members      []int64 `json:"members"`
	Contributors []int64 `json:"contributors"`
}
type InquiryExperimentResult struct {
	Status      string        `json:"status"`
	RequestID   int64         `json:"request_id"`
	Calls       int           `json:"calls"`
	ActiveTeams []InquiryTeam `json:"active_teams"`
}

// RunInquiryExperiment starts at a saved Queen handoff. Its observer is an
// explicit experiment stop, never a drone objective or Queen collapse decision.
func RunInquiryExperiment(ctx context.Context, c Config, dir, handoff string, client TextClient) (result InquiryExperimentResult, err error) {
	if strings.TrimSpace(handoff) == "" {
		return result, errors.New("saved handoff is empty")
	}
	for _, key := range []string{"inquiry_contribute", "inquiry_expand", "inquiry_actions", "inquiry_scores", "inquiry_format", "inquiry_root"} {
		if strings.TrimSpace(c.Prompts[key]) == "" {
			return result, fmt.Errorf("missing prompt %s", key)
		}
	}
	if _, e := os.Stat(filepath.Join(dir, c.Files.State)); e == nil {
		return result, errors.New("preserve existing state; prepare a fresh inquiry branch")
	} else if !errors.Is(e, os.ErrNotExist) {
		return result, e
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return result, err
	}
	h, err := OpenHive(c, dir, client)
	if err != nil {
		return result, err
	}
	defer h.Close()
	h.runID, err = h.exec(ctx, "create", "hive_runs", "INSERT INTO hive_runs(started,status,version,config) VALUES(?,?,?,?)", now(), "observing", Version, jsonText(c))
	if err != nil {
		return result, err
	}
	h.Log.Fields["run_id"] = h.runID
	h.requestID, err = h.exec(ctx, "create", "hive_requests", "INSERT INTO hive_requests(run_id,contract,status,created) VALUES(?,?,?,?)", h.runID, handoff, "active", now())
	if err != nil {
		return result, err
	}
	result.RequestID = h.requestID
	h.Log.Fields["request_id"] = h.requestID
	_, err = h.exec(ctx, "bind_request", "hive_runs", "UPDATE hive_runs SET request_id=? WHERE id=?", h.requestID, h.runID)
	if err != nil {
		return result, err
	}
	defer func() {
		result.Calls = h.Text.Calls
		if err != nil {
			result.Status = "failed"
		}
		if result.Status == "" {
			result.Status = "paused"
		}
		// Final state remains an unfinished Queen obligation, including an observer stop.
		finishCtx := context.Background()
		persistErr := h.Store.Transaction(finishCtx, h.Log.Fields, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
			if _, e := exec("pause", "hive_requests", "UPDATE hive_requests SET status=?,finish_reason=? WHERE id=?", "paused", result.Status, h.requestID); e != nil {
				return e
			}
			_, e := exec("finish_observation", "hive_runs", "UPDATE hive_runs SET status=?,finished=?,calls=? WHERE id=?", result.Status, now(), result.Calls, h.runID)
			return e
		})
		if persistErr != nil {
			result.Status = "failed"
			err = errors.Join(err, persistErr)
		}
		fields := map[string]any{"status": result.Status, "calls": result.Calls, "active_teams": result.ActiveTeams}
		if err != nil {
			fields["error"] = err.Error()
		}
		h.Log.Log("hive", "inquiry_observation_finished", fields)
	}()
	if err = h.seed(ctx); err != nil {
		return result, err
	}
	if _, err = h.exec(ctx, "create_root", "hive_inquiries", "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", h.requestID, c.Prompts["inquiry_root"], 0, now()); err != nil {
		return result, err
	}
	drones, e := h.query(ctx, "list", "hive_drones", "SELECT id,name,contribute,expand,signal FROM hive_drones ORDER BY id")
	if e != nil {
		return result, e
	}
	for {
		activity := false
		for _, drone := range drones {
			droneID := id(drone["id"])
			h.Log.Fields["drone_id"] = droneID
			for _, phase := range []string{"contribute", "expand"} {
				if e = ctx.Err(); e != nil {
					return result, e
				}
				h.Log.Fields["phase"] = phase
				input, e := h.inquiryInput(ctx, handoff, drone, phase)
				if e != nil {
					return result, e
				}
				prompt := c.Prompts["inquiry_"+phase] + "\n\n" + c.Prompts["inquiry_actions"] + "\n\n" + c.Prompts["inquiry_scores"] + "\n\n" + c.Prompts["inquiry_format"]
				reply, e := h.generatePrompt(ctx, "drone", c.Models.Drone, "inquiry_"+phase, prompt, input, str(drone[phase]))
				if e != nil {
					return result, e
				}
				h.Log.Log("hive", "inquiry_reply", map[string]any{"body": reply, "outcome": "received"})
				action, e := ParseInquiryAction(reply)
				if e != nil {
					h.Log.Log("hive", "inquiry_action_rejected", map[string]any{"error": e.Error(), "outcome": "format_error"})
					return result, e
				}
				h.Log.Log("hive", "inquiry_action", map[string]any{"action": action, "outcome": "parsed"})
				if e = h.applyInquiryAction(ctx, droneID, phase, action); e != nil {
					h.Log.Log("hive", "inquiry_action_rejected", map[string]any{"error": e.Error(), "outcome": "action_error"})
					return result, e
				}
				if action.Kind != "PASS" {
					activity = true
				}
				result.ActiveTeams, e = h.observedInquiryTeams(ctx)
				if e != nil {
					return result, e
				}
				// Multiple working teams is the user-requested observation endpoint.
				if len(result.ActiveTeams) > 1 {
					result.Status = "observed_multiple_teams"
					return result, nil
				}
			}
		}
		if !activity {
			result.Status = "idle"
			return result, nil
		}
	}
}

func (h *Hive) inquiryInput(ctx context.Context, handoff string, drone map[string]any, phase string) (string, error) {
	inquiries, e := h.query(ctx, "board", "hive_inquiries", "SELECT id,request_id,question,proposed_by,created FROM hive_inquiries WHERE request_id=? ORDER BY id", h.requestID)
	if e != nil {
		return "", e
	}
	members, e := h.query(ctx, "board", "hive_inquiry_members", "SELECT m.inquiry_id,m.drone_id,m.commitment,m.created FROM hive_inquiry_members m JOIN hive_inquiries i ON i.id=m.inquiry_id WHERE i.request_id=? ORDER BY m.inquiry_id,m.drone_id", h.requestID)
	if e != nil {
		return "", e
	}
	work, e := h.query(ctx, "board", "hive_inquiry_work", "SELECT w.* FROM hive_inquiry_work w JOIN hive_inquiries i ON i.id=w.inquiry_id WHERE i.request_id=? ORDER BY w.id", h.requestID)
	if e != nil {
		return "", e
	}
	return jsonText(map[string]any{"handoff": handoff, "drone": drone, "phase": phase, "inquiries": inquiries, "memberships": members, "work": work}), nil
}

func (h *Hive) requireInquiry(ctx context.Context, inquiryID int64) error {
	rows, e := h.query(ctx, "get", "hive_inquiries", "SELECT id FROM hive_inquiries WHERE id=? AND request_id=?", inquiryID, h.requestID)
	if e != nil {
		return e
	}
	if len(rows) != 1 {
		return fmt.Errorf("inquiry %d does not belong to this request", inquiryID)
	}
	return nil
}
func (h *Hive) requireInquiryMember(ctx context.Context, inquiryID, droneID int64) error {
	rows, e := h.query(ctx, "membership", "hive_inquiry_members", "SELECT drone_id FROM hive_inquiry_members WHERE inquiry_id=? AND drone_id=?", inquiryID, droneID)
	if e != nil {
		return e
	}
	if len(rows) != 1 {
		return fmt.Errorf("drone %d has not committed to inquiry %d", droneID, inquiryID)
	}
	return nil
}
func (h *Hive) applyInquiryAction(ctx context.Context, droneID int64, phase string, a InquiryAction) error {
	if a.Kind == "PASS" {
		// A pass is observable in diagnostics, not fabricated operational progress.
		h.Log.Log("hive", "inquiry_pass", map[string]any{"body": a.Body, "relevance": a.Relevance, "inquiry_value": a.InquiryValue, "outcome": "pass"})
		return nil
	}
	if a.Kind != "PROPOSE" {
		if e := h.requireInquiry(ctx, a.InquiryID); e != nil {
			return e
		}
	}
	if a.Kind == "CONTRIBUTE" || a.Kind == "CONNECT" {
		if e := h.requireInquiryMember(ctx, a.InquiryID, droneID); e != nil {
			return e
		}
	}
	if a.Kind == "CONNECT" {
		if a.TargetID == a.InquiryID {
			return errors.New("CONNECT requires distinct inquiries")
		}
		if e := h.requireInquiry(ctx, a.TargetID); e != nil {
			return e
		}
	}
	return h.Store.Transaction(ctx, h.Log.Fields, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
		inquiryID := a.InquiryID
		if a.Kind == "PROPOSE" {
			created, e := exec("propose", "hive_inquiries", "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", h.requestID, a.Body, droneID, now())
			if e != nil {
				return e
			}
			inquiryID, e = created.LastInsertId()
			if e != nil {
				return e
			}
		}
		if a.Kind == "PROPOSE" || a.Kind == "JOIN" {
			if _, e := exec("commit", "hive_inquiry_members", "INSERT OR IGNORE INTO hive_inquiry_members(inquiry_id,drone_id,commitment,created) VALUES(?,?,?,?)", inquiryID, droneID, a.Body, now()); e != nil {
				return e
			}
		}
		var target any
		if a.Kind == "CONNECT" {
			target = a.TargetID
		}
		_, e := exec("contribute", "hive_inquiry_work", "INSERT INTO hive_inquiry_work(inquiry_id,target_inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,?,?,?,?,?,?,?)", inquiryID, target, droneID, phase, a.Kind, a.Body, a.Relevance, a.InquiryValue, now())
		return e
	})
}

// This is developer observation of actual commitments/work, not fitness and
// not a global stopping rule. It never supplies counts or targets to prompts.
func (h *Hive) observedInquiryTeams(ctx context.Context) ([]InquiryTeam, error) {
	rows, e := h.query(ctx, "observe_teams", "hive_inquiry_members", "SELECT m.inquiry_id,m.drone_id,EXISTS(SELECT 1 FROM hive_inquiry_work w WHERE w.inquiry_id=m.inquiry_id AND w.drone_id=m.drone_id AND w.kind IN ('PROPOSE','JOIN','CONTRIBUTE')) AS contributed FROM hive_inquiry_members m JOIN hive_inquiries i ON i.id=m.inquiry_id WHERE i.request_id=? ORDER BY m.inquiry_id,m.drone_id", h.requestID)
	if e != nil {
		return nil, e
	}
	teams := map[int64]*InquiryTeam{}
	var ids []int64
	for _, row := range rows {
		inquiryID := id(row["inquiry_id"])
		team := teams[inquiryID]
		if team == nil {
			team = &InquiryTeam{InquiryID: inquiryID, Members: []int64{}, Contributors: []int64{}}
			teams[inquiryID] = team
			ids = append(ids, inquiryID)
		}
		team.Members = append(team.Members, id(row["drone_id"]))
		if id(row["contributed"]) != 0 {
			team.Contributors = append(team.Contributors, id(row["drone_id"]))
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	active := []InquiryTeam{}
	for _, inquiryID := range ids {
		team := teams[inquiryID]
		if len(team.Members) > 1 && len(team.Contributors) > 1 {
			active = append(active, *team)
		}
	}
	return active, nil
}

func writeInquiryResult(dir string, result InquiryExperimentResult) error {
	data, e := json.MarshalIndent(result, "", "  ")
	if e != nil {
		return e
	}
	return writeNew(filepath.Join(dir, "inquiry-result.json"), append(data, '\n'))
}
