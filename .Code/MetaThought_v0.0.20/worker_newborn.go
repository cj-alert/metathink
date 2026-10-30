package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
)

// The existing nullable cohort column retains its historical 0/1 meanings.
// NULL identifies a seed; the new value 2 is independent of Queen omission.
type NewbornCohort int

const (
	NewbornOpen NewbornCohort = iota
	NewbornOmit
	NewbornFocus
)

func (c NewbornCohort) String() string {
	switch c {
	case NewbornOpen:
		return "open"
	case NewbornOmit:
		return "omit"
	case NewbornFocus:
		return "focus"
	default:
		return "invalid"
	}
}

func workerNewbornCohort(value any) (NewbornCohort, bool, error) {
	if value == nil {
		return NewbornOpen, false, nil
	}
	c := NewbornCohort(id(value))
	if c < NewbornOpen || c > NewbornFocus {
		return c, true, fmt.Errorf("unknown saved newborn cohort %v", value)
	}
	return c, true, nil
}

func drawNewbornCohort(settings WorkerSettings) NewbornCohort {
	draw := rand.Float64()
	if draw < settings.NewbornOmitProbability {
		return NewbornOmit
	}
	if draw < settings.NewbornOmitProbability+settings.NewbornFocusProbability {
		return NewbornFocus
	}
	return NewbornOpen
}

type WorkerInitialFocus struct {
	Kind                       string `json:"kind"`
	InquiryID                  int64  `json:"assigned_inquiry_id"`
	InvitedInquiryID           int64  `json:"invited_inquiry_id"`
	FocusBasis                 string `json:"focus_basis"`
	PopulationBeforeAssignment int64  `json:"population_before_assignment"`
	MemberCountBefore          int64  `json:"assigned_member_count_before"`
	ContributorCountBefore     int64  `json:"assigned_contributor_count_before"`
	Pending                    bool   `json:"initial_focus_pending"`
}

func (h *Hive) workerInitialFocus(ctx context.Context, claim WorkerClaim) (*WorkerInitialFocus, error) {
	rows, err := h.workerQuery(ctx, claim, "worker_initial_focus", "hive_inquiry_members",
		`SELECT m.inquiry_id,m.commitment,NOT EXISTS(
		 SELECT 1 FROM hive_inquiry_work w WHERE w.inquiry_id=m.inquiry_id AND w.drone_id=m.drone_id AND w.kind='CONTRIBUTE') AS pending
		 FROM hive_inquiry_members m JOIN hive_inquiries i ON i.id=m.inquiry_id
		 WHERE m.drone_id=? AND i.request_id=?
		 AND CASE WHEN json_valid(m.commitment) THEN json_extract(m.commitment,'$.kind') END='initial_focus'`, claim.DroneID, claim.RequestID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("drone %d has multiple initial focus commitments", claim.DroneID)
	}
	var assignment WorkerInitialFocus
	if err := json.Unmarshal([]byte(str(rows[0]["commitment"])), &assignment); err != nil {
		return nil, fmt.Errorf("saved initial focus: %w", err)
	}
	assignment.InquiryID = id(rows[0]["inquiry_id"])
	assignment.Pending = id(rows[0]["pending"]) != 0
	return &assignment, nil
}

func (h *Hive) workerInitialFocusAction(ctx context.Context, claim WorkerClaim, action InquiryAction) error {
	queue, err := h.workerQuery(ctx, claim, "worker_initial_cohort", "hive_worker_queue",
		"SELECT omit_oversized FROM hive_worker_queue WHERE drone_id=? AND request_id=? AND owner=? AND phase=?", claim.DroneID, claim.RequestID, claim.Owner, claim.Phase)
	if err != nil {
		return err
	}
	if len(queue) != 1 {
		return fmt.Errorf("worker does not own this phase")
	}
	cohort, _, err := workerNewbornCohort(queue[0]["omit_oversized"])
	if err != nil || cohort != NewbornFocus {
		return err
	}
	assignment, err := h.workerInitialFocus(ctx, claim)
	if err != nil || assignment == nil || !assignment.Pending {
		return err
	}
	if action.Kind == "READ" || action.Kind == "DISCOVER" {
		return nil
	}
	if (action.Kind == "CONTRIBUTE" || action.Kind == "RESEARCH") && action.InquiryID == assignment.InquiryID {
		return nil
	}
	return fmt.Errorf("drone %d must first CONTRIBUTE to assigned inquiry %d; %s cannot reroute the initial contribution", claim.DroneID, assignment.InquiryID, action.Kind)
}

func (h *Hive) workerNewbornBackground(ctx context.Context, claim WorkerClaim, rootID int64) ([]map[string]any, error) {
	rows, err := h.workerQuery(ctx, claim, "worker_newborn_inquiries", "hive_inquiries", `SELECT i.id,i.question,
	 (SELECT MAX(w.id) FROM hive_inquiry_work w WHERE w.inquiry_id=i.id AND w.kind IN ('PROPOSE','JOIN','CONTRIBUTE')) AS latest_work_id
	 FROM hive_inquiries i WHERE i.request_id=? AND i.id<>? ORDER BY i.id`, claim.RequestID, rootID)
	return rows, err
}

func initialFocusFields(fields map[string]any, assignment *WorkerInitialFocus) {
	fields["focus_applied"], fields["eligible"] = assignment != nil, assignment != nil
	if assignment == nil {
		fields["assigned_inquiry_id"] = nil
		fields["focus_unavailable"] = "no_research_inquiries"
		return
	}
	fields["assigned_inquiry_id"], fields["invited_inquiry_id"] = assignment.InquiryID, assignment.InvitedInquiryID
	fields["focus_basis"], fields["population_before_assignment"] = assignment.FocusBasis, assignment.PopulationBeforeAssignment
	fields["assigned_member_count_before"], fields["assigned_contributor_count_before"] = assignment.MemberCountBefore, assignment.ContributorCountBefore
	fields["initial_focus_pending"] = assignment.Pending
}
