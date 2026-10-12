package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestExecutionSkipWarmSelectionPreviewWatchAndRerun(t *testing.T) {
	chdirCLITestRepo(t, "skip-warm")
	runGitForCLITest(t, "init")
	if err := os.WriteFile(".readyci.yml", []byte(`project_id: review
sync:
  include_untracked: true
network:
  mode: none
  warm_mode: allowlist
  warm_allowlist: [registry.npmjs.org]
checks:
  dependencies:
    command: npm ci
  test:
    command: npm test
affected:
  rules:
    - paths: ["**"]
      checks: [test]
`), 0644); err != nil {
		t.Fatal(err)
	}
	runGitForCLITest(t, "add", ".readyci.yml")
	runGitForCLITest(t, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-m", "fixture")
	if err := os.WriteFile("test.js", []byte("test change"), 0644); err != nil {
		t.Fatal(err)
	}
	var requests []preflightapi.CreateVerificationSessionRequest
	server := captureExecutionRequests(t, &requests)
	for _, command := range []string{"check", "affected"} {
		for _, skip := range []bool{false, true} {
			for _, watch := range []bool{false, true} {
				args := []string{command, "--api-url", server.URL}
				if skip {
					args = append(args, "--skip-warm")
				}
				if watch {
					args = append(args, "--watch")
				}
				if command == "check" {
					args = append(args, "test")
				}
				requests = nil
				runErr := run(args)
				if runErr == nil {
					t.Fatal("fixture should reject creation")
				}
				if len(requests) != 1 {
					t.Fatalf("%v: requests=%d err=%v", args, len(requests), runErr)
				}
				req := requests[0]
				if req.Recipe.SkipWarm != skip {
					t.Fatalf("%v: recipe lost skip choice", args)
				}
				warm := 0
				for _, node := range req.Plan.Nodes {
					if node.Phase == preflightapi.VerificationNodePhaseWarm {
						warm++
					} else if node.Run.NetworkMode != "none" {
						t.Fatalf("test network changed: %+v", node.Run)
					}
				}
				if (warm == 0) != skip {
					t.Fatalf("%v: warm nodes=%d", args, warm)
				}
				prepared, err := prepareExecutionRerun(preflightapi.VerificationSession{ProjectID: "review", Recipe: req.Recipe}, server.URL)
				if err != nil {
					t.Fatal(err)
				}
				if (len(prepared.WarmPlans) == 0) != skip || prepared.Plans[0].SkipWarm != skip {
					t.Fatalf("%v: rerun lost choice", args)
				}
			}
		}
		args := []string{command, "--api-url", server.URL, "--skip-warm", "--dry-run", "--json"}
		if command == "check" {
			args = append(args, "test")
		}
		output, err := captureStdoutForCLITest(t, func() error { return run(args) })
		if err != nil {
			t.Fatal(err)
		}
		var report struct {
			SkipWarm bool     `json:"skip_warm"`
			Warnings []string `json:"warnings"`
		}
		if err := json.Unmarshal([]byte(output), &report); err != nil {
			t.Fatal(err)
		}
		if !report.SkipWarm || !strings.Contains(strings.Join(report.Warnings, " "), "skipped (--skip-warm)") {
			t.Fatalf("preview=%s", output)
		}
	}
}
