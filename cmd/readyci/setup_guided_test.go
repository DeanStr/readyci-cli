package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

type setupPromptMutationReader struct {
	mutate func()
	input  *strings.Reader
}

func (reader *setupPromptMutationReader) Read(buffer []byte) (int, error) {
	if reader.mutate != nil {
		reader.mutate()
		reader.mutate = nil
	}
	return reader.input.Read(buffer)
}

func newGuidedSetupReviewFixture(t *testing.T) (setupOptions, *atomic.Int32) {
	t.Helper()
	return newGuidedSetupReviewFixtureWithAccount(t, http.StatusOK, "")
}

func newGuidedSetupReviewFixtureWithAccount(t *testing.T, accountStatus int, accountCode string) (setupOptions, *atomic.Int32) {
	t.Helper()
	chdirCLITestRepo(t, "consent")
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	runGitForCLITest(t, "init")
	runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
	runGitForCLITest(t, "config", "user.name", "Ready CI")
	if err := os.WriteFile("go.mod", []byte("module example.test/consent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForCLITest(t, "add", ".")
	runGitForCLITest(t, "commit", "-m", "fixture")
	var runs atomic.Int32
	var apiURL string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/account":
			if accountStatus != http.StatusOK {
				writeCLITestJSON(w, accountStatus, map[string]string{"code": accountCode, "error": "account details denied"})
				return
			}
			writeCLITestJSON(w, 200, preflightapi.AccountUsageResponse{TeamID: "team_test"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			runs.Add(1)
			writeCLITestJSON(w, 201, preflightapi.CreateRunResponse{RunID: "run_consent", UploadURL: apiURL + "/upload", Status: preflightapi.StatusAwaitingUpload})
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			w.WriteHeader(202)
		default:
			http.NotFound(w, r)
		}
	})
	server := httptest.NewServer(successfulExecutionTestHandler(t, handler))
	t.Cleanup(server.Close)
	apiURL = server.URL
	return setupOptions{APIURL: apiURL, APIToken: "pfl_execution_fixture", ProjectID: "consent", Profile: "remote-ci", Detect: true}, &runs
}

func TestGuidedSetupAccountPermissions(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		status     int
		allowed    bool
	}{
		{name: "execution token without account read", status: http.StatusForbidden, code: "auth_scope_required", allowed: true},
		{name: "invalid token", status: http.StatusUnauthorized, code: "auth_invalid"},
		{name: "revoked token", status: http.StatusUnauthorized, code: "auth_token_revoked"},
		{name: "other permission denial", status: http.StatusForbidden, code: "auth_forbidden"},
		{name: "uncoded permission denial", status: http.StatusForbidden},
		{name: "wrong status for scope denial", status: http.StatusUnauthorized, code: "auth_scope_required"},
		{name: "service failure", status: http.StatusInternalServerError, code: "platform_internal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options, runs := newGuidedSetupReviewFixtureWithAccount(t, tc.status, tc.code)
			report, err := buildSetupReport(options)
			if err != nil {
				t.Fatal(err)
			}
			if tc.allowed {
				if report.AuthStatus != "ok" || report.authError != nil || report.TeamID != "" || report.PlanID != "" {
					t.Fatalf("account scope denial was treated as invalid authentication: %+v", report)
				}
				if !strings.Contains(strings.Join(report.Warnings, "\n"), "account:read") {
					t.Fatal("missing explanation of unavailable account details")
				}
			}
			err = guidedSetup(options, strings.NewReader(""), io.Discard, true)
			if tc.allowed {
				if err != nil || runs.Load() != 1 {
					t.Fatalf("execution token could not complete setup: runs=%d, error=%v", runs.Load(), err)
				}
				if _, err := os.Stat(".readyci.yml"); err != nil {
					t.Fatalf("setup did not save configuration: %v", err)
				}
			} else {
				if err == nil || runs.Load() != 0 {
					t.Fatalf("setup continued after authentication/account failure: runs=%d, error=%v", runs.Load(), err)
				}
				if _, err := os.Stat(".readyci.yml"); !os.IsNotExist(err) {
					t.Fatal("setup wrote configuration after an account failure")
				}
			}
		})
	}
}

func TestGuidedSetupSavesExactlyTheReviewedTemplate(t *testing.T) {
	options, runs := newGuidedSetupReviewFixture(t)
	reviewed := detectedProjectTemplateWithProfile(".", "consent", "", "remote-ci")
	input := &setupPromptMutationReader{input: strings.NewReader("y\nn\n"), mutate: func() {
		if err := os.Remove("go.mod"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile("package.json", []byte(`{"scripts":{"test":"node --test"}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}}
	var output bytes.Buffer
	if err := guidedSetup(options, input, &output, false); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(".readyci.yml")
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != reviewed {
		t.Fatal("saved configuration differs from the template reviewed before confirmation")
	}
	if runs.Load() != 0 {
		t.Fatal("declining execution still started a remote run")
	}
}

func TestGuidedSetupRejectsUnknownCheckBeforeWriting(t *testing.T) {
	options, runs := newGuidedSetupReviewFixture(t)
	options.CheckName = "does-not-exist"
	if err := guidedSetup(options, strings.NewReader(""), io.Discard, true); err == nil {
		t.Fatal("setup accepted an unknown check")
	}
	if _, err := os.Stat(".readyci.yml"); !os.IsNotExist(err) {
		t.Fatal("setup wrote configuration before rejecting an unknown check")
	}
	if runs.Load() != 0 {
		t.Fatal("setup started remote work for an unknown check")
	}
}

func TestGuidedSetupUsesNormalizedCheckName(t *testing.T) {
	options, _ := newGuidedSetupReviewFixture(t)
	options.CheckName = " go "
	if err := guidedSetup(options, strings.NewReader("y\nn\n"), io.Discard, false); err != nil {
		t.Fatalf("setup did not use the check name displayed in its report: %v", err)
	}
}

func TestGuidedSetupRejectsConfigurationChangesDuringPrompts(t *testing.T) {
	for _, scenario := range []string{"create during save", "edit during replacement", "edit during execution"} {
		t.Run(scenario, func(t *testing.T) {
			options, runs := newGuidedSetupReviewFixture(t)
			original := "version: 1\nproject_id: consent\nchecks:\n  custom:\n    command: echo reviewed\n"
			changed := strings.ReplaceAll(original, "echo reviewed", "echo changed")
			path := ".readyci.yml"
			if scenario == "create during save" {
				path = ".readyci.yaml"
			} else if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
				t.Fatal(err)
			}
			options.Force = scenario == "edit during replacement"
			input := &setupPromptMutationReader{input: strings.NewReader("y\ny\n"), mutate: func() {
				if err := os.WriteFile(path, []byte(changed), 0o644); err != nil {
					t.Fatal(err)
				}
			}}
			if err := guidedSetup(options, input, io.Discard, false); err == nil {
				t.Fatal("setup accepted changed configuration without a fresh review")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != changed {
				t.Fatal("setup overwrote configuration changed during confirmation")
			}
			if runs.Load() != 0 {
				t.Fatal("setup executed a command that changed after preview")
			}
		})
	}
}

func TestGuidedSetupForceDoesNotWriteThroughConfigSymlink(t *testing.T) {
	options, runs := newGuidedSetupReviewFixture(t)
	target := filepath.Join(t.TempDir(), "external-config.yml")
	original := "version: 1\nproject_id: consent\nchecks:\n  custom:\n    command: echo external\n"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, ".readyci.yml"); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	options.Force = true
	if err := guidedSetup(options, strings.NewReader("y\nn\n"), io.Discard, false); err == nil {
		t.Fatal("setup accepted a symlink as a configuration write target")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != original {
		t.Fatal("setup changed a file outside the checkout through a configuration symlink")
	}
	if runs.Load() != 0 {
		t.Fatal("setup started remote work after an invalid write target")
	}
}

func TestGuidedSetupConsentAndExecution(t *testing.T) {
	for _, tc := range []struct {
		name, input               string
		yes, saved, ran, existing bool
	}{
		{name: "decline configuration", input: "n\n"},
		{name: "decline execution", input: "y\nn\n", saved: true},
		{name: "confirm both", input: "yes\nyes\n", saved: true, ran: true},
		{name: "automation", yes: true, saved: true, ran: true},
		{name: "preserve existing configuration", input: "y\n", saved: true, ran: true, existing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
			chdirCLITestRepo(t, "first-test")
			runGitForCLITest(t, "init")
			runGitForCLITest(t, "config", "user.email", "readyci@example.invalid")
			runGitForCLITest(t, "config", "user.name", "Ready CI")
			if err := os.WriteFile("go.mod", []byte("module example.test/first\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			original := "version: 1\nproject_id: existing\nchecks:\n  custom:\n    command: echo existing\n"
			if tc.existing {
				if err := os.WriteFile(".readyci.yml", []byte(original), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			runGitForCLITest(t, "add", ".")
			runGitForCLITest(t, "commit", "-m", "fixture")
			var runs atomic.Int32
			var apiURL string
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/v1/account":
					writeCLITestJSON(w, 200, preflightapi.AccountUsageResponse{TeamID: "team_test"})
				case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
					runs.Add(1)
					writeCLITestJSON(w, 201, preflightapi.CreateRunResponse{RunID: "run_first", UploadURL: apiURL + "/upload", Status: preflightapi.StatusAwaitingUpload})
				case r.Method == http.MethodPut && r.URL.Path == "/upload":
					w.WriteHeader(202)
				default:
					http.NotFound(w, r)
				}
			})
			server := httptest.NewServer(successfulExecutionTestHandler(t, handler))
			defer server.Close()
			apiURL = server.URL
			var output bytes.Buffer
			var err error
			if tc.yes {
				err = setupCommand([]string{"--yes", "--api-url", apiURL})
			} else {
				err = guidedSetup(setupOptions{APIURL: apiURL, APIToken: "pfl_execution_fixture", Profile: "remote-ci", Detect: true}, strings.NewReader(tc.input), &output, false)
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(".readyci.yml")
			if tc.saved && err != nil || !tc.saved && !os.IsNotExist(err) {
				t.Fatalf("saved=%t; read config: %v", tc.saved, err)
			}
			if tc.existing && string(data) != original {
				t.Fatal("setup changed existing configuration")
			}
			if (runs.Load() > 0) != tc.ran {
				t.Fatalf("runs=%d, expected execution=%t", runs.Load(), tc.ran)
			}
			if tc.ran && !tc.yes && !strings.Contains(output.String(), "Next time, run readyci check.") {
				t.Fatal("missing next-step instruction")
			}
		})
	}
}

func TestGuidedSetupPreconditionsDoNotWrite(t *testing.T) {
	for _, scenario := range []string{"missing authentication", "decline login", "invalid authentication", "invalid config", "end of input"} {
		t.Run(scenario, func(t *testing.T) {
			chdirCLITestRepo(t, "preconditions")
			t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
			t.Setenv("READYCI_API_TOKEN", "")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario == "invalid authentication" {
					writeCLITestJSON(w, 403, map[string]string{"error": "token denied"})
					return
				}
				writeCLITestJSON(w, 200, preflightapi.AccountUsageResponse{})
			}))
			defer server.Close()
			options := setupOptions{APIURL: server.URL, Profile: "remote-ci", Detect: true}
			if scenario == "invalid authentication" || scenario == "end of input" {
				options.APIToken = "fixture"
			}
			if scenario == "invalid config" {
				if err := os.WriteFile(".readyci.yml", []byte("version: 999\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			input := ""
			if scenario == "decline login" {
				input = "n\n"
			}
			var output bytes.Buffer
			err := guidedSetup(options, strings.NewReader(input), &output, scenario == "missing authentication")
			if scenario == "decline login" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("missing precondition error")
			}
			data, readErr := os.ReadFile(".readyci.yml")
			if scenario == "invalid config" {
				if readErr != nil || string(data) != "version: 999\n" {
					t.Fatal("invalid configuration was overwritten")
				}
			} else if !os.IsNotExist(readErr) {
				t.Fatal("setup wrote configuration before consent and authentication")
			}
		})
	}
}

func TestSetupInspectionAndInvalidModesDoNotWrite(t *testing.T) {
	chdirCLITestRepo(t, "inspect")
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	t.Setenv("READYCI_API_TOKEN", "")
	for _, flags := range [][]string{{"--preview"}, {"--json"}, {"--preview", "--json"}} {
		if err := setupCommand(flags); err != nil {
			t.Fatalf("%v: %v", flags, err)
		}
	}
	for _, flags := range [][]string{{"--yes", "--json"}, {"--yes", "--print"}, {"--yes", "--write-config"}, {"--preview", "--run"}, {"--preview", "--write-config"}} {
		if err := setupCommand(flags); err == nil {
			t.Fatalf("accepted conflicting options %v", flags)
		}
	}
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEnd.Close()
	writeEnd.Close()
	previous := os.Stdin
	os.Stdin = readEnd
	defer func() { os.Stdin = previous }()
	if err := setupCommand(nil); err == nil || !strings.Contains(err.Error(), "requires a terminal") {
		t.Fatalf("bare noninteractive setup error: %v", err)
	}
	if _, err := os.Stat(".readyci.yml"); !os.IsNotExist(err) {
		t.Fatal("inspection or invalid modes wrote configuration")
	}
}
