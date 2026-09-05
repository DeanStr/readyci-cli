package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/verificationplan"
)

func TestExecuteRemoteRunRejectsUnsafeRequestIdentifiersBeforeSync(t *testing.T) {
	tests := []struct {
		name    string
		options remoteRunOptions
		want    string
	}{
		{
			name: "project id control character",
			options: remoteRunOptions{
				ProjectID: "proj\nshadow",
				Command:   "go test ./...",
			},
			want: "project_id must not contain control characters",
		},
		{
			name: "idempotency key control character",
			options: remoteRunOptions{
				ProjectID:      "proj_dev",
				IdempotencyKey: "task\n1",
				Command:        "go test ./...",
			},
			want: "idempotency_key must not contain control characters",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := executeRemoteRunInternal(tt.options, remoteRunOutputSilent)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("executeRemoteRunInternal error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestExecuteRemoteRunSendsNativeStepsWithoutCompiledCommand(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()
	setupDeltaFallbackRepo(t)

	var serverURL string
	var created preflightapi.CreateRunRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateRunResponse{
				RunID:     "run_steps",
				UploadURL: serverURL + "/upload",
				EventsURL: serverURL + "/events",
				Status:    preflightapi.StatusAwaitingUpload,
			})
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("{}\n"))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs/run_steps/upload-complete":
			writeCLITestJSON(w, http.StatusOK, preflightapi.CompleteUploadResponse{RunID: "run_steps", Status: preflightapi.StatusQueued})
		case r.Method == http.MethodGet && r.URL.Path == "/events":
			writeCLITestFinishedEvent(w)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	result, err := executeRemoteRunInternal(remoteRunOptions{
		APIURL:    server.URL,
		ProjectID: "proj_dev",
		Steps:     []preflightapi.RunStepSpec{{Name: "unit", Command: "go test ./..."}},
		CacheMode: "cache-only",
	}, remoteRunOutputCollect)
	if err != nil && !isSuccessfulExit(err) {
		t.Fatalf("run error = %v, want successful terminal exit", err)
	}
	if created.Command != "" {
		t.Fatalf("create command length = %d, want native steps without compiled command", len(created.Command))
	}
	if len(created.Steps) != 1 || created.Steps[0].Name != "unit" {
		t.Fatalf("create steps = %+v, want unit step", created.Steps)
	}
	if created.NetworkMode != preflightapi.DefaultNetworkMode || result.NetworkMode != preflightapi.DefaultNetworkMode {
		t.Fatalf("network mode = request %q result %q, want normalized default %q", created.NetworkMode, result.NetworkMode, preflightapi.DefaultNetworkMode)
	}
}

func TestExecuteRemoteRunIgnoresCacheLaneSizeForEphemeralCache(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()
	setupDeltaFallbackRepo(t)

	var serverURL string
	var created preflightapi.CreateRunRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateRunResponse{
				RunID:     "run_ephemeral_cache",
				UploadURL: serverURL + "/upload",
				EventsURL: serverURL + "/events",
				Status:    preflightapi.StatusAwaitingUpload,
			})
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("{}\n"))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs/run_ephemeral_cache/upload-complete":
			writeCLITestJSON(w, http.StatusOK, preflightapi.CompleteUploadResponse{RunID: "run_ephemeral_cache", Status: preflightapi.StatusQueued})
		case r.Method == http.MethodGet && r.URL.Path == "/events":
			writeCLITestFinishedEvent(w)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	_, err := executeRemoteRunInternal(remoteRunOptions{
		APIURL:             server.URL,
		ProjectID:          "proj_dev",
		Command:            "go test ./...",
		CacheMode:          "ephemeral",
		CacheLaneSizeBytes: 40 * 1024 * 1024 * 1024,
		NetworkMode:        "none",
	}, remoteRunOutputCollect)
	if err != nil && !isSuccessfulExit(err) {
		t.Fatalf("run error = %v, want successful terminal exit", err)
	}
	if created.CacheMode != "ephemeral" {
		t.Fatalf("create cache mode = %q, want ephemeral", created.CacheMode)
	}
	if created.CacheLaneSizeBytes != 0 {
		t.Fatalf("create cache size = %d, want ignored zero size", created.CacheLaneSizeBytes)
	}
}

func TestExecuteRemoteRunDropsStepMetadataWhenCommandWrapperIsRequired(t *testing.T) {
	cliTestWorkingDirectoryMu.Lock()
	defer cliTestWorkingDirectoryMu.Unlock()
	setupDeltaFallbackRepo(t)

	var serverURL string
	var created preflightapi.CreateRunRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateRunResponse{
				RunID:     "run_wrapped_steps",
				UploadURL: serverURL + "/upload",
				EventsURL: serverURL + "/events",
				Status:    preflightapi.StatusAwaitingUpload,
			})
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("{}\n"))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs/run_wrapped_steps/upload-complete":
			writeCLITestJSON(w, http.StatusOK, preflightapi.CompleteUploadResponse{RunID: "run_wrapped_steps", Status: preflightapi.StatusQueued})
		case r.Method == http.MethodGet && r.URL.Path == "/events":
			writeCLITestFinishedEvent(w)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	_, err := executeRemoteRunInternal(remoteRunOptions{
		APIURL:          server.URL,
		ProjectID:       "proj_dev",
		PrepareCommands: []string{"echo prepare"},
		Steps:           []preflightapi.RunStepSpec{{Name: "unit", Command: "go test ./..."}},
		CacheMode:       "cache-only",
		NetworkMode:     "none",
	}, remoteRunOutputCollect)
	if err != nil && !isSuccessfulExit(err) {
		t.Fatalf("run error = %v, want successful terminal exit", err)
	}
	if created.Command == "" || !strings.Contains(created.Command, "echo prepare") || !strings.Contains(created.Command, "go test ./...") {
		t.Fatalf("create command = %q, want wrapper with prepare and step command", created.Command)
	}
	if len(created.Steps) != 0 {
		t.Fatalf("create steps = %+v, want command-only wrapper when prepare is required", created.Steps)
	}
}

func TestCreateRunCommandAndStepsPreservesCanonicalStoredStepWrapper(t *testing.T) {
	steps := []preflightapi.RunStepSpec{
		{Name: "deps", Command: "npm ci"},
		{Name: "test", Command: "npm test", Env: map[string]string{"NODE_ENV": "test"}, TimeoutSeconds: 60},
	}
	wrapper, err := verificationplan.CommandForRunSpec(verificationplan.RunSpec{Steps: steps})
	if err != nil {
		t.Fatalf("build wrapper: %v", err)
	}

	command, createSteps := createRunCommandAndSteps(remoteRunOptions{
		Command: wrapper,
		Steps:   steps,
	}, wrapper)
	if command != wrapper {
		t.Fatalf("command = %q, want stored wrapper", command)
	}
	if len(createSteps) != len(steps) || createSteps[0].Name != "deps" || createSteps[1].TimeoutSeconds != 60 {
		t.Fatalf("create steps = %+v, want preserved legacy step metadata", createSteps)
	}
	createSteps[1].Env["NODE_ENV"] = "mutated"
	if steps[1].Env["NODE_ENV"] != "test" {
		t.Fatalf("create steps shared env map with original steps: %+v", steps)
	}
}

func TestCreateRunCommandAndStepsDropsNonCanonicalStoredWrapper(t *testing.T) {
	steps := []preflightapi.RunStepSpec{{Name: "unit", Command: "go test ./..."}}
	command, createSteps := createRunCommandAndSteps(remoteRunOptions{
		Command: "echo prepare\ngo test ./...",
		Steps:   steps,
	}, "echo prepare\ngo test ./...")
	if command == "" {
		t.Fatal("command is empty, want stored non-canonical wrapper")
	}
	if len(createSteps) != 0 {
		t.Fatalf("create steps = %+v, want dropped metadata for non-canonical wrapper", createSteps)
	}
}
