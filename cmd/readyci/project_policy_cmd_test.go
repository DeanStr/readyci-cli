package main

import (
	"encoding/json"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectPolicyInputDiscoversConfiguration(t *testing.T) {
	for _, name := range []string{".readyci.yml", ".readyci.yaml"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := "version: 1\nproject_id: proj_test\nchecks:\n  test:\n    command: echo ok\n"
			if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir("nested", 0o700); err != nil {
				t.Fatal(err)
			}
			t.Chdir(filepath.Join(root, "nested"))
			project, yaml, err := projectPolicyCommandInput("https://api.readyci.test", "proj_test", "")
			if err != nil {
				t.Fatal(err)
			}
			if project != "proj_test" || yaml != content {
				t.Fatalf("project=%q yaml=%q", project, yaml)
			}
		})
	}
}

func TestProjectPolicyInputDoesNotSubstituteMissingExplicitFile(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile(".readyci.yml", []byte("version: 1\nproject_id: proj_test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := projectPolicyCommandInput("https://api.readyci.test", "proj_test", "missing.yml"); err == nil || !os.IsNotExist(err) {
		t.Fatalf("expected missing explicit file error, got %v", err)
	}
}

func TestProjectPolicyPreviewRejectsEmptyFileFlag(t *testing.T) {
	if err := projectPolicyPreviewCommand([]string{"--file="}); err == nil || !strings.Contains(err.Error(), "--file must not be empty") {
		t.Fatalf("expected empty file flag error, got %v", err)
	}
}

func TestProjectPolicyProposeUploadsDiscoveredFileAndPrintsReviewLink(t *testing.T) {
	t.Chdir(t.TempDir())
	config := "version: 1\nproject_id: proposal\nchecks:\n  test:\n    command: echo ok\n"
	if err := os.WriteFile(".readyci.yml", []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/v1/projects/proposal/verification-policy/proposals" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		var req preflightapi.PreviewProjectVerificationPolicyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ConfigYAML != config {
			t.Errorf("request: %+v %v", req, err)
		}
		writeCLITestJSON(w, http.StatusOK, preflightapi.ProjectVerificationPolicyProposal{ProposalID: "proposal_1", ReviewURL: "https://app.test/app/settings?policy_proposal=proposal_1", ExpiresAt: time.Now().Add(time.Hour)})
	}))
	defer server.Close()
	output, err := captureStdoutForCLITest(t, func() error {
		return projectPolicyCommand([]string{"propose", "--api-url", server.URL, "--token", "test", "--project", "proposal"})
	})
	if err != nil || calls != 1 || !strings.Contains(output, "https://app.test/app/settings?policy_proposal=proposal_1") {
		t.Fatalf("propose: %s %v calls=%d", output, err, calls)
	}
	output, err = captureStdoutForCLITest(t, func() error {
		return projectPolicyCommand([]string{"propose", "--api-url", server.URL, "--token", "test", "--project", "proposal", "--json"})
	})
	var proposal preflightapi.ProjectVerificationPolicyProposal
	if err != nil || json.Unmarshal([]byte(output), &proposal) != nil || proposal.ProposalID != "proposal_1" {
		t.Fatalf("JSON: %s %v", output, err)
	}
	if err := projectPolicyCommand([]string{"propose", "--file="}); err == nil {
		t.Fatal("empty file accepted")
	}
}
