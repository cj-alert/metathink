package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

var v7NetworkAttempts atomic.Int64

type v7BlockedHTTP struct{}

func (v7BlockedHTTP) RoundTrip(*http.Request) (*http.Response, error) {
	v7NetworkAttempts.Add(1)
	return nil, fmt.Errorf("network is forbidden in focused local tests")
}

func TestMain(m *testing.M) {
	http.DefaultTransport = v7BlockedHTTP{}
	http.DefaultClient = &http.Client{Transport: v7BlockedHTTP{}}
	code := m.Run()
	attempts := v7NetworkAttempts.Load()
	fmt.Fprintf(os.Stdout, "focused_local_network_attempts=%d\n", attempts)
	if attempts != 0 {
		code = 1
	}
	os.Exit(code)
}

type v7TextFunc func(context.Context, TextRequest) (TextResponse, error)

func (f v7TextFunc) Complete(ctx context.Context, in TextRequest) (TextResponse, error) {
	return f(ctx, in)
}

func v7Config(t *testing.T) Config {
	t.Helper()
	c, err := LoadConfig("config.json")
	if err != nil {
		t.Fatal(err)
	}
	c.Meta.APIKeyFile = ""
	c.Meta.APIKeyEnv = "METATHOUGHT_V007_TEST_KEY"
	t.Setenv(c.Meta.APIKeyEnv, "")
	c.Meta.BaseURL = "https://local-fixture.invalid/v1"
	c.Models.Drone, c.Models.Queen = "fixture-drone", "fixture-queen"
	c.Hive.DroneCount = 2
	return c
}

func v7ContractText(label string, names ...string) string {
	if len(names) == 0 {
		names = []string{"correctness", "usefulness"}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# OBJECTIVE\n%s objective\n# INTENT\n%s intent\n# CONTEXT\n%s confirmed context\n# RETURN\n%s supported findings with uncertainty\n# CRITERIA\n", label, label, label, label)
	for _, name := range names {
		fmt.Fprintf(&b, "## %s\nMeaning: Evidence for %s\nZero: No supporting evidence\nOne: Fully supported\nTarget: >0.5\nWeight: none\n", name, name)
	}
	return b.String()
}

func v7OpenHive(t *testing.T, client TextClient) *Hive {
	t.Helper()
	if client == nil {
		t.Fatal("focused tests require an injected client")
	}
	h, err := OpenHive(v7Config(t), t.TempDir(), client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h
}

func v7Exec(t *testing.T, h *Hive, query string, args ...any) int64 {
	t.Helper()
	result, err := h.Store.Exec(context.Background(), "fixture", schemaTarget(query), h.Log.Fields, query, args...)
	if err != nil {
		t.Fatal(err)
	}
	rowID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return rowID
}

func v7SeedRequest(t *testing.T, h *Hive, contract string) (requestID, teamID int64) {
	t.Helper()
	parsed, err := ParseContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	h.Contract = parsed
	h.runID = v7Exec(t, h, "INSERT INTO hive_runs(started,status,version,config) VALUES(?,?,?,?)", now(), "running", Version, jsonText(h.Config))
	teamID = v7Exec(t, h, "INSERT INTO hive_teams(summary,created) VALUES(?,?)", "local test team", now())
	requestID = v7Exec(t, h, "INSERT INTO hive_requests(run_id,contract,status,team_id,created) VALUES(?,?,?,?,?)", h.runID, contract, "active", teamID, now())
	h.requestID = requestID
	h.Log.Fields["request_id"], h.Log.Fields["run_id"], h.Log.Fields["team_id"] = requestID, h.runID, teamID
	v7Exec(t, h, "UPDATE hive_runs SET request_id=? WHERE id=?", requestID, h.runID)
	for droneID := int64(1); droneID <= 2; droneID++ {
		v7Exec(t, h, "INSERT OR IGNORE INTO hive_drones(id,name,contribute,expand,signal,created) VALUES(?,?,?,?,?,?)", droneID, fmt.Sprintf("fixture-%d", droneID), "Develop evidence", "Question assumptions", "local fixture", now())
		v7Exec(t, h, "INSERT INTO hive_members(team_id,drone_id) VALUES(?,?)", teamID, droneID)
		v7Exec(t, h, "INSERT INTO hive_commitments(request_id,team_id,drone_id,created) VALUES(?,?,?,?)", requestID, teamID, droneID, now())
	}
	return requestID, teamID
}

func v7AddCandidate(t *testing.T, h *Hive, teamID int64, body string, scored bool) int64 {
	t.Helper()
	candidateID := v7Exec(t, h, "INSERT INTO hive_contributions(request_id,team_id,drone_id,tick,kind,body,created) VALUES(?,?,?,?,?,?,?)", h.requestID, teamID, 1, 1, "candidate", body, now())
	if scored {
		for droneID := int64(1); droneID <= 2; droneID++ {
			err := h.Store.Transaction(context.Background(), h.Log.Fields, func(exec func(string, string, string, ...any) (sql.Result, error)) error {
				for _, criterion := range h.Contract.Criteria {
					if _, err := exec("fixture_score", "hive_assessments", "INSERT INTO hive_assessments(request_id,team_id,drone_id,candidate_id,tick,criterion,score,evidence,uncertainty,created) VALUES(?,?,?,?,?,?,?,?,?,?)", h.requestID, teamID, droneID, candidateID, 1, criterion.Name, 0.7, "local fixture evidence", "fixture only", now()); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	return candidateID
}
