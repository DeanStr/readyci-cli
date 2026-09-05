package main

import (
	"encoding/json"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var cliTestWorkingDirectoryMu sync.Mutex

func chdirCLITestRepo(t *testing.T, name string) string {
	t.Helper()

	cliTestWorkingDirectoryMu.Lock()
	t.Cleanup(cliTestWorkingDirectoryMu.Unlock)

	repo := t.TempDir()
	if name != "" {
		repo = filepath.Join(repo, name)
		if err := os.Mkdir(repo, 0o755); err != nil {
			t.Fatalf("mkdir repo: %v", err)
		}
	}
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	})
	return repo
}

func runGitForCLITest(t *testing.T, args ...string) {
	t.Helper()
	output, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, output)
	}
}

func writeCLITestJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeCLITestFinishedEvent(w http.ResponseWriter) {
	exitCode := 0
	event := preflightapi.RunEvent{Type: preflightapi.EventRunFinished, Status: preflightapi.StatusSucceeded, ExitCode: &exitCode}
	data, _ := json.Marshal(event)
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write(append(append([]byte("data: "), data...), '\n', '\n'))
}

func captureStdoutForCLITest(t *testing.T, run func() error) (string, error) {
	t.Helper()
	previousStdout := os.Stdout
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = writeEnd
	defer func() {
		os.Stdout = previousStdout
	}()
	runErr := run()
	if err := writeEnd.Close(); err != nil {
		t.Fatalf("close stdout pipe: %v", err)
	}
	output, err := io.ReadAll(readEnd)
	if err != nil {
		t.Fatalf("read stdout pipe: %v", err)
	}
	if err := readEnd.Close(); err != nil {
		t.Fatalf("close stdout read pipe: %v", err)
	}
	return string(output), runErr
}

func captureStderrForCLITest(t *testing.T, run func() error) (string, error) {
	t.Helper()
	previousStderr := os.Stderr
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}
	os.Stderr = writeEnd
	defer func() {
		os.Stderr = previousStderr
	}()
	runErr := run()
	if err := writeEnd.Close(); err != nil {
		t.Fatalf("close stderr pipe: %v", err)
	}
	output, err := io.ReadAll(readEnd)
	if err != nil {
		t.Fatalf("read stderr pipe: %v", err)
	}
	if err := readEnd.Close(); err != nil {
		t.Fatalf("close stderr read pipe: %v", err)
	}
	return string(output), runErr
}

func requireCLITestOutputContains(t *testing.T, output string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(output, value) {
			t.Fatalf("output missing %q:\n%s", value, output)
		}
	}
}

func requireCLITestCommandOutputContains(t *testing.T, label string, run func() error, values ...string) string {
	t.Helper()
	output, err := captureStdoutForCLITest(t, run)
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	requireCLITestOutputContains(t, output, values...)
	return output
}

func hasSignal(signals []os.Signal, target os.Signal) bool {
	for _, signal := range signals {
		if signal == target {
			return true
		}
	}
	return false
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func containsSubstring(values []string, substring string) bool {
	for _, item := range values {
		if strings.Contains(item, substring) {
			return true
		}
	}
	return false
}

func containsDoctorDiagnostic(diagnostics []doctorDiagnostic, section, level string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Section == section && diagnostic.Level == level {
			return true
		}
	}
	return false
}
