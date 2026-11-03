package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

// continue-on-error written as a matrix expression is evaluated for each
// variant, so only the variants that actually tolerate failure are blocked.
func TestSetupWorkflowContinueOnErrorIsEvaluatedPerMatrixVariant(t *testing.T) {
	for _, scope := range []string{"job", "step"} {
		t.Run(scope, func(t *testing.T) {
			repo := t.TempDir()
			jobField, stepField := "    continue-on-error: ${{ matrix.experimental }}\n", ""
			if scope == "step" {
				jobField, stepField = "", "        continue-on-error: ${{ matrix.experimental }}\n"
			}
			writeFileForRepoMapTest(t, filepath.Join(repo, "go.mod"), "module example.test/matrix\n\ngo 1.25\n")
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  test:\n    runs-on: ubuntu-latest\n"+jobField+
				"    strategy:\n      matrix:\n        go: ['1.25']\n        experimental: [false]\n        include:\n          - go: '1.26'\n            experimental: true\n"+
				"    steps:\n      - uses: actions/setup-go@v5\n        with:\n          go-version: ${{ matrix.go }}\n      - run: go test ./...\n"+stepField)
			var stable, experimental *repoImportCandidate
			for _, candidate := range setupImportCandidates(repo, "github-actions") {
				candidate := candidate
				switch candidate.Matrix["experimental"] {
				case "false":
					stable = &candidate
				case "true":
					experimental = &candidate
				}
			}
			if stable == nil || experimental == nil {
				t.Fatal("matrix variants are missing")
			}
			if len(stable.Unsupported) != 0 || stable.RuntimeRequirements["go"] != "1.25" {
				t.Fatalf("statically false continue-on-error blocked its variant: %+v", stable)
			}
			if !strings.Contains(strings.Join(experimental.Unsupported, ";"), "continue-on-error") {
				t.Fatalf("failure-tolerant variant was imported without review: %+v", experimental)
			}
			template := setupImportedTemplateWithScope(repo, setupOptions{Profile: "remote-ci"}, "fixture", *stable, setupImportCandidates(repo, "github-actions"))
			cfg, err := config.ParseProjectConfig([]byte(template), "variant")
			if err != nil || cfg.Checks["test"].RuntimeRequirements["go"] == "" {
				t.Fatalf("stable variant did not import: %v\n%s", err, template)
			}
		})
	}
}

func TestSetupPrintImportWithoutCandidateNamesTheMissingFlag(t *testing.T) {
	chdirCLITestRepo(t, "print-import")
	writeFileForRepoMapTest(t, ".github/workflows/ci.yml", "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n")
	_, err := captureStdoutForCLITest(t, func() error {
		return setupCommand([]string{"--print", "--import", "github-actions"})
	})
	if err == nil || !strings.Contains(err.Error(), "--import requires --candidate ID") || strings.Contains(err.Error(), "--yes") {
		t.Fatalf("missing candidate error was misleading: %v", err)
	}
}
