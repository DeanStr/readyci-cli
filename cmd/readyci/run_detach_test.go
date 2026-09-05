package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestExecuteRemoteRunDetachReturnsAfterAcceptedRun(t *testing.T) {
	_ = chdirCLITestRepo(t, "")
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile("README.md", []byte("detach\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "init")

	eventsRequested := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateRunResponse{
				RunID:     "run_detached",
				EventsURL: serverURL(r) + "/events/run_detached",
				Status:    preflightapi.StatusQueued,
			})
		case r.URL.Path == "/events/run_detached":
			eventsRequested = true
			http.Error(w, "must not watch a detached run", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := executeRemoteRunInternal(remoteRunOptions{
		APIURL:      server.URL,
		APIToken:    "pfl_test",
		ProjectID:   "project_test",
		Command:     "go test ./...",
		ChangeMode:  "all",
		NetworkMode: preflightapi.DefaultNetworkMode,
		Detach:      true,
	}, remoteRunOutputSilent)
	if err != nil {
		t.Fatalf("detached run: %v", err)
	}
	if !result.Detached || result.RunID != "run_detached" || eventsRequested {
		t.Fatalf("detached result = %+v, events requested = %t", result, eventsRequested)
	}
}

func TestInterruptedRunCreateReconcilesAndCancelsAcceptedRun(t *testing.T) {
	createCalls := 0
	cancelCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			createCalls++
			writeCLITestJSON(w, http.StatusOK, preflightapi.CreateRunResponse{
				RunID:     "run_reconciled",
				EventsURL: serverURL(r) + "/v1/runs/run_reconciled/events",
				Status:    preflightapi.StatusQueued,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs/run_reconciled/cancel":
			cancelCalls++
			writeCLITestJSON(w, http.StatusOK, preflightapi.CancelRunResponse{RunID: "run_reconciled", Status: preflightapi.StatusCancelled})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	request := preflightapi.CreateRunRequest{ProjectID: "project_test", IdempotencyKey: "idem_reconcile", Command: "true", NetworkMode: preflightapi.DefaultNetworkMode}
	result, interrupted := reconcileAndCancelInterruptedRun(server.URL, api.NewClient(server.URL, api.WithToken("pfl_test")), &request, remoteRunResult{}, syscall.SIGINT, context.Canceled)
	if result.RunID != "run_reconciled" || result.Status != preflightapi.StatusCancelled {
		t.Fatalf("reconciled run = %+v", result)
	}
	if interrupted.ResourceID != result.RunID || interrupted.CancellationAcknowledged == nil || !*interrupted.CancellationAcknowledged || interrupted.CancellationError != nil {
		t.Fatalf("interruption = %+v", interrupted)
	}
	if interrupted.APIURL != server.URL {
		t.Fatalf("interruption API URL = %q, want %q", interrupted.APIURL, server.URL)
	}
	if createCalls != 1 || cancelCalls != 1 {
		t.Fatalf("create calls = %d, cancel calls = %d", createCalls, cancelCalls)
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}
