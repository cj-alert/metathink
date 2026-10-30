package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func v10DashboardEmbedded(t *testing.T, body []byte) DashboardSnapshot {
	t.Helper()
	text := string(body)
	start := strings.Index(text, "const snapshot = ")
	if start < 0 {
		t.Fatal("dashboard omitted its saved operational snapshot")
	}
	text = text[start+len("const snapshot = "):]
	end := strings.Index(text, ";\nconst refreshSeconds")
	if end < 0 {
		t.Fatal("dashboard snapshot boundary is missing")
	}
	var snapshot DashboardSnapshot
	if err := json.Unmarshal([]byte(text[:end]), &snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestV010DashboardReadsAttributedCurrentStateAndEscapesHTML(t *testing.T) {
	h, _, _ := v10Fixture(t, 3, nil)
	ctx := v10Context(t)
	savedContribute := "Contribute exact α.\r\nKeep two trailing spaces.  "
	savedExpand := "Expand exact β.\nWhat could change the inquiry?"
	v10Exec(t, h, "UPDATE hive_drones SET contribute=?,expand=? WHERE id=2", savedContribute, savedExpand)
	otherTeam := v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", h.requestID, "A second inquiry", 2, now())
	for _, pair := range [][2]int64{{1, 1}, {1, 2}, {otherTeam, 2}, {otherTeam, 3}} {
		v10Exec(t, h, "INSERT INTO hive_inquiry_members(inquiry_id,drone_id,commitment,created) VALUES(?,?,?,?)", pair[0], pair[1], "Explicit fixture commitment", now())
		v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,?,?,?,?,?,?)", pair[0], pair[1], "contribute", "JOIN", "An original contribution", 0.8, 0.7, now())
	}
	attack := "</script><script>alert('unsafe')</script> & α\r\nExact saved text.  "
	connectionID := v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,target_inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,?,?,?,?,?,?,?)", 1, otherTeam, 2, "expand", "CONNECT", attack, 0.6, 0.4, now())
	latestID := v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,?,?,?,?,?,?)", otherTeam, 3, "contribute", "CONTRIBUTE", "Latest attributed finding", 0.9, 0.5, now())
	claim := v10Claim(t, h, "dashboard-fixture-worker")
	v10Exec(t, h, "UPDATE hive_worker_queen SET owner=?,focus_id=?,through_id=?,note=? WHERE request_id=?", "dashboard-fixture-queen", 1, 1, "Current Queen note", h.requestID)
	v10Exec(t, h, "INSERT INTO hive_worker_queen_seen(request_id,inquiry_id,last_work_id) VALUES(?,?,?)", h.requestID, 1, 1)
	otherRequest := v10Exec(t, h, "INSERT INTO hive_requests(run_id,contract,status,created) VALUES(?,?,?,?)", h.runID, "OTHER_REQUEST_DASHBOARD_CANARY", "working", now())
	outside := v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", otherRequest, "OTHER_REQUEST_DASHBOARD_CANARY", 1, now())
	v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,?,?,?,?,?,?,?)", outside, 1, "contribute", "CONTRIBUTE", "OTHER_REQUEST_DASHBOARD_CANARY", 0.8, 0.7, now())
	state := func() []any {
		t.Helper()
		var saved []any
		for _, table := range []string{"hive_requests", "hive_drones", "hive_worker_queue", "hive_worker_queen", "hive_worker_queen_seen", "hive_inquiry_members", "hive_inquiry_work"} {
			rows, err := h.query(ctx, "fixture_dashboard_state", table, "SELECT * FROM "+table+" ORDER BY rowid")
			if err != nil { t.Fatal(err) }
			saved = append(saved, rows)
		}
		return saved
	}
	before := state()
	snapshot, err := h.dashboardSnapshot(ctx)
	if err != nil { t.Fatal(err) }
	if !reflect.DeepEqual(before, state()) { t.Fatal("dashboard read changed operational records or claims") }
	if snapshot.RequestID != h.requestID || snapshot.Status != "working" || snapshot.Goal != v9Handoff || snapshot.TeamCount != 2 || len(snapshot.Teams) != 2 || len(snapshot.Drones) != 3 || snapshot.WorkCount != 6 {
		t.Fatalf("dashboard changed current stage, goal or actual population: %+v", snapshot)
	}
	if snapshot.Queen.Owner != "dashboard-fixture-queen" || snapshot.Queen.ThroughID != 1 || snapshot.Queen.Note != "Current Queen note" || len(snapshot.Queen.Reviewed) != 1 {
		t.Fatalf("Queen ownership/note/checkpoint were lost: %+v", snapshot.Queen)
	}
	if len(snapshot.Links) != 1 || snapshot.Links[0].WorkID != connectionID || snapshot.Links[0].FromID != 1 || snapshot.Links[0].ToID != otherTeam || snapshot.Links[0].Body != attack {
		t.Fatalf("connection source or original body changed: %+v", snapshot.Links)
	}
	for _, drone := range snapshot.Drones {
		if drone.ID == 2 && (drone.Contribute != savedContribute || drone.Expand != savedExpand) { t.Fatalf("dashboard substituted or altered saved chromosomes: contribute=%q expand=%q", drone.Contribute, drone.Expand) }
		if drone.ID == 2 && (!reflect.DeepEqual(drone.Memberships, []int64{1, otherTeam}) || drone.LatestWork.ID != connectionID) { t.Fatalf("overlapping drone lost memberships/latest source: %+v", drone) }
		if drone.ID == 3 && (drone.LatestWork.ID != latestID || drone.LatestWork.Body != "Latest attributed finding") { t.Fatalf("latest drone work lost attribution: %+v", drone) }
	}
	active := 0
	for _, worker := range snapshot.Workers { if worker.Owner == claim.Owner { active++; if worker.DroneID != claim.DroneID || worker.Phase != "contribute" || worker.State != "active" { t.Fatalf("worker claim changed: %+v", worker) } } }
	if active != 1 { t.Fatalf("expected one actual drone owner, got %d", active) }
	body, err := renderDashboard(snapshot, 5)
	if err != nil { t.Fatal(err) }
	for _, forbidden := range []string{attack, "<script>alert('unsafe')</script>", "OTHER_REQUEST_DASHBOARD_CANARY", v9DiagnosticCanary, v9OldSeedCanary} {
		if strings.Contains(string(body), forbidden) { t.Fatalf("HTML leaked unsafe markup, unrelated state or diagnostics: %q", forbidden) }
	}
	decoded := v10DashboardEmbedded(t, body)
	if decoded.Links[0].Body != attack || decoded.Status != "working" { t.Fatal("HTML escaping rewrote the saved text or stage") }
	for _, drone := range decoded.Drones { if drone.ID == 2 && (drone.Contribute != savedContribute || drone.Expand != savedExpand) { t.Fatal("HTML payload changed the saved chromosome texts") } }
	if !strings.Contains(string(body), ".textContent") || !strings.Contains(string(body), "no inferred progress percentage") { t.Fatal("dashboard lost plain-text rendering or honest progress labeling") }
	t.Logf("two actual teams, three drones, shared drone 2, claim %s; exact script-shaped body preserved safely in embedded JSON", claim.Owner)
}

func TestV010DashboardStopWritesFinalSavedState(t *testing.T) {
	h, _, dir := v10Fixture(t, 2, nil)
	h.Config.Dashboard = DashboardSettings{Enabled: true, RefreshSeconds: 3600, File: "dashboard.html"}
	stop, err := startDashboard(v10Context(t), h)
	if err != nil { t.Fatal(err) }
	defer stop()
	v10Exec(t, h, "UPDATE hive_requests SET status='returned',result=?,finish_reason=? WHERE id=?", "Exact returned result.\r\n", "convergence_returned", h.requestID)
	stop() // Synchronous final write; no timer wait or provider is needed.
	body, err := os.ReadFile(filepath.Join(dir, "dashboard.html"))
	if err != nil { t.Fatal(err) }
	final := v10DashboardEmbedded(t, body)
	if final.Status != "returned" || final.Result != "Exact returned result.\r\n" || final.FinishReason != "convergence_returned" { t.Fatalf("stop left an outdated page: %+v", final) }
	if h.Text.Calls != 0 { t.Fatalf("dashboard made %d model calls", h.Text.Calls) }
	t.Log("final dashboard reflects the saved return immediately; no provider calls")
}
