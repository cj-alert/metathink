package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestV010QueenLatestSamplePreservesFIFOAndCheckpoint(t *testing.T) {
	h, _, _ := v10Fixture(t, 2, nil)
	ctx := v10Context(t)
	addWork := func(kind, body string) int64 {
		return v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,?,?,?,?,?,?)", 1, 1, "contribute", kind, body, 0.8, 0.7, now())
	}
	selectedID := addWork("JOIN", "The original oldest selected work.\r\n")
	addWork("CONTRIBUTE", "INTERMEDIATE_HISTORY_IS_NOT_INCLUDED")
	latestBody := "Recent connection changes the proposed falsifier.\r\nExact α and trailing spaces.  "
	latestID := addWork("CONNECT", latestBody)
	boundaryID := addWork("SPAWN", "SPAWN_IS_NOT_A_SUBSTANTIVE_LATEST_SAMPLE")
	v10Exec(t, h, "UPDATE hive_worker_queen SET focus_id=1,through_id=?,note=? WHERE request_id=?", selectedID, "Prior note remains unchanged.", h.requestID)
	v10Exec(t, h, "INSERT INTO hive_worker_queen_seen(request_id,inquiry_id,last_work_id) VALUES(?,?,?)", h.requestID, 1, 0)
	selected, err := h.query(ctx, "fixture_oldest", "hive_inquiry_work", "SELECT * FROM hive_inquiry_work WHERE id=?", selectedID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := h.workerQueenInput(ctx, 1, selected, "Prior note remains unchanged.")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Work         []map[string]any `json:"work"`
		LatestWork   map[string]any   `json:"latest_work"`
		CurrentFacts v10QueenFacts    `json:"current_facts"`
	}
	if err = json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Work) != 1 || id(got.Work[0]["id"]) != selectedID || got.Work[0]["body"] != selected[0]["body"] {
		t.Fatalf("latest context replaced or rewrote the FIFO item: %#v", got.Work)
	}
	if id(got.LatestWork["id"]) != latestID || got.LatestWork["body"] != latestBody || got.LatestWork["kind"] != "CONNECT" || id(got.LatestWork["drone_id"]) != 1 {
		t.Fatalf("latest context lost exact body or attribution: %#v", got.LatestWork)
	}
	if got.CurrentFacts.Focus.LatestWorkID != boundaryID || latestID > got.CurrentFacts.Focus.LatestWorkID || got.CurrentFacts.Focus.ReviewedThroughWorkID != 0 {
		t.Fatalf("latest sample changed or crossed the captured boundary: %+v", got.CurrentFacts.Focus)
	}
	for _, absent := range []string{"INTERMEDIATE_HISTORY_IS_NOT_INCLUDED", "SPAWN_IS_NOT_A_SUBSTANTIVE_LATEST_SAMPLE", v9DiagnosticCanary} {
		if strings.Contains(raw, absent) {
			t.Fatalf("latest sample leaked unrelated history or diagnostics %q", absent)
		}
	}
	if int64(v9Count(t, h.Store.db, "SELECT through_id FROM hive_worker_queen WHERE request_id=?", h.requestID)) != selectedID ||
		v9Count(t, h.Store.db, "SELECT last_work_id FROM hive_worker_queen_seen WHERE request_id=? AND inquiry_id=1", h.requestID) != 0 {
		t.Fatal("reading latest evidence advanced a selection or reviewed checkpoint")
	}

	// If the FIFO item is itself the latest substantive entry, do not duplicate it.
	selected, err = h.query(ctx, "fixture_latest", "hive_inquiry_work", "SELECT * FROM hive_inquiry_work WHERE id=?", latestID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = h.workerQueenInput(ctx, 1, selected, "Prior note remains unchanged.")
	if err != nil {
		t.Fatal(err)
	}
	var duplicate map[string]any
	if err = json.Unmarshal([]byte(raw), &duplicate); err != nil {
		t.Fatal(err)
	}
	if duplicate["latest_work"] != nil {
		t.Fatalf("latest original was duplicated: %#v", duplicate["latest_work"])
	}
	t.Logf("FIFO work %d retained; latest substantive work %d sampled within boundary %d; reviewed checkpoint stayed 0", selectedID, latestID, boundaryID)
}
