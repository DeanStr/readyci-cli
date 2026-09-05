package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestGroupsCancelCommandUsesScopedEndpoint(t *testing.T) {
	var request preflightapi.CancelRunGroupRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/run-groups/group_1/cancel" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		writeCLITestJSON(w, http.StatusOK, preflightapi.CancelRunGroupResponse{
			RunGroupID:    "group_1",
			CancelledRuns: []string{"run_1"},
			Detail: preflightapi.RunGroupDetail{Summary: preflightapi.RunGroupSummary{
				RunGroupID: "group_1",
				Status:     preflightapi.StatusCancelled,
			}},
		})
	}))
	defer server.Close()
	output, err := captureStdoutForCLITest(t, func() error {
		return groupsCancelCommand([]string{"--api-url", server.URL, "--token", "pfl_test", "--reason", "operator request", "--json", "group_1"})
	})
	if err != nil || request.Reason != "operator request" || !strings.Contains(output, `"run_group_id": "group_1"`) {
		t.Fatalf("cancel = %v, request=%+v, output=%s", err, request, output)
	}
}

func TestGroupsStatusCommandPrintsDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/run-groups/group_1" {
			http.NotFound(w, r)
			return
		}
		writeCLITestJSON(w, http.StatusOK, preflightapi.RunGroupDetail{
			Summary:  preflightapi.RunGroupSummary{RunGroupID: "group_1", ProjectID: "project_1", Kind: "matrix", Status: preflightapi.StatusFailed, ChildCount: 1},
			Children: []preflightapi.RunGroupChild{{RunID: "run_1", LogicalChildID: "go", Attempt: 1, LatestAttempt: true, Status: preflightapi.StatusFailed}},
		})
	}))
	defer server.Close()
	output, err := captureStdoutForCLITest(t, func() error {
		return groupsStatusCommand([]string{"--api-url", server.URL, "--token", "pfl_test", "group_1"})
	})
	if err != nil || !strings.Contains(output, "child: run_1") {
		t.Fatalf("status = %v, output=%s", err, output)
	}
}

func TestGroupsStatusCommandPrintsJSONDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCLITestJSON(w, http.StatusOK, preflightapi.RunGroupDetail{
			Summary:  preflightapi.RunGroupSummary{RunGroupID: "group_json", ProjectID: "project_1", Kind: "matrix", Status: preflightapi.StatusSucceeded},
			Children: []preflightapi.RunGroupChild{},
		})
	}))
	defer server.Close()
	output, err := captureStdoutForCLITest(t, func() error {
		return groupsStatusCommand([]string{"--api-url", server.URL, "--token", "pfl_test", "--json", "group_json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	var detail preflightapi.RunGroupDetail
	if err := json.Unmarshal([]byte(output), &detail); err != nil {
		t.Fatalf("decode group JSON: %v\n%s", err, output)
	}
	if detail.Summary.RunGroupID != "group_json" || detail.Children == nil {
		t.Fatalf("group detail = %+v", detail)
	}
}

func TestGroupsRouterRejectsRemovedClientReplayCommands(t *testing.T) {
	for _, command := range []string{"retry", "rerun"} {
		err := groupsCommand([]string{command, "group_1"})
		if err == nil || !strings.Contains(err.Error(), "unknown groups command") {
			t.Fatalf("groups %s error = %v", command, err)
		}
	}
}

func TestGroupProjectIDUsesExplicitValue(t *testing.T) {
	got, err := groupProjectID("https://api.readyci.test", " owner/repo ")
	if err != nil || got != "owner/repo" {
		t.Fatalf("groupProjectID = %q, %v", got, err)
	}
}

func TestGroupsCancelCommandPrintsHumanSummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCLITestJSON(w, http.StatusOK, preflightapi.CancelRunGroupResponse{
			RunGroupID:    "group_test",
			CancelledRuns: []string{"run_one", "run_two"},
			Detail: preflightapi.RunGroupDetail{Summary: preflightapi.RunGroupSummary{
				RunGroupID: "group_test", Status: preflightapi.StatusCancelled, ChildCount: 2,
			}},
		})
	}))
	defer server.Close()
	output, err := captureStdoutForCLITest(t, func() error {
		return groupsCancelCommand([]string{"--api-url", server.URL, "--token", "pfl_group", "group_test"})
	})
	if err != nil {
		t.Fatal(err)
	}
	requireCLITestOutputContains(t, output, "cancelled_runs: 2", "cancelled: run_one", "cancelled: run_two", "status: cancelled")
}

func TestGroupsLatestCommandFetchesProjectAndPrintsHumanDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/run-groups/latest" || r.URL.Query().Get("project_id") != "airline" {
			http.NotFound(w, r)
			return
		}
		exitCode := 1
		writeCLITestJSON(w, http.StatusOK, preflightapi.RunGroupDetail{
			Summary: preflightapi.RunGroupSummary{
				RunGroupID: "group_latest", ProjectID: "airline", Kind: "verification", Name: "PR 42",
				Status: preflightapi.StatusFailed, ChildCount: 1, LatestAttemptCount: 1, FailedChildCount: 1,
			},
			Children: []preflightapi.RunGroupChild{{
				RunID: "run_go", LogicalChildID: "go", CommandAlias: "go test", MatrixVariant: "GO=1.25",
				Attempt: 1, LatestAttempt: true, Status: preflightapi.StatusFailed, ExitCode: &exitCode,
				Classification: preflightapi.ClassificationTestFailed,
			}},
		})
	}))
	defer server.Close()
	output, err := captureStdoutForCLITest(t, func() error {
		return groupsLatestCommand([]string{"--api-url", server.URL, "--token", "pfl_group", "--project", "airline"})
	})
	if err != nil {
		t.Fatal(err)
	}
	requireCLITestOutputContains(t, output, "run_group_id: group_latest", "name: PR 42", "matrix_variant: GO=1.25", "exit_code: 1", "classification: test_failed")
}

func TestPrintRunGroupDetailIncludesPolicyReasons(t *testing.T) {
	reason := "network_mode is not allowed by current project policy"
	output, err := captureStdoutForCLITest(t, func() error {
		printRunGroupDetail(preflightapi.RunGroupDetail{
			Summary:  preflightapi.RunGroupSummary{RunGroupID: "group_policy", ProjectID: "proj_dev", Kind: "affected", Status: preflightapi.StatusDenied, ChildCount: 1},
			Children: []preflightapi.RunGroupChild{{RunID: "run_denied", LogicalChildID: "deploy", Attempt: 1, LatestAttempt: true, Status: preflightapi.StatusDenied, PolicyReasons: []string{reason}}},
		})
		return nil
	})
	if err != nil || !strings.Contains(output, "policy_reason: "+reason) {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestGroupsLogsCommandAggregatesChildEventTails(t *testing.T) {
	tailRequests := make(map[string]bool)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/run-groups/group_test" {
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunGroupDetail{
				Summary: preflightapi.RunGroupSummary{RunGroupID: "group_test", ProjectID: "airline", Kind: "affected", Status: preflightapi.StatusRunning, ChildCount: 2, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
				Children: []preflightapi.RunGroupChild{
					{RunID: "run_go", LogicalChildID: "go", Attempt: 1, LatestAttempt: true, Status: preflightapi.StatusRunning},
					{RunID: "run_web", LogicalChildID: "web", Attempt: 1, LatestAttempt: true, Status: preflightapi.StatusQueued},
				},
			})
			return
		}
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/v1/runs/") || !strings.HasSuffix(r.URL.Path, "/events") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("tail") != "2" || r.URL.Query().Get("logs_only") != "true" || r.URL.Query().Get("include_lifecycle") != "true" {
			t.Fatalf("events query = %s", r.URL.RawQuery)
		}
		runID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/runs/"), "/events")
		tailRequests[runID] = true
		writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: runID, Events: []preflightapi.RunEvent{{Type: preflightapi.EventStdout, Data: runID + " log\n"}}})
	}))
	defer server.Close()
	output, err := captureStdoutForCLITest(t, func() error {
		return groupsLogsCommand([]string{"--api-url", server.URL, "--token", "pfl_group", "--tail", "2", "group_test"})
	})
	if err != nil || !tailRequests["run_go"] || !tailRequests["run_web"] || !strings.Contains(output, "run_go log") || !strings.Contains(output, "run_web log") {
		t.Fatalf("output=%s requests=%v err=%v", output, tailRequests, err)
	}
}
