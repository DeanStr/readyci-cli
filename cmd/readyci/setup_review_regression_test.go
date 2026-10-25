package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestSetupWorkflowRejectsPlaceholderPackageTests(t *testing.T) {
	for _, command := range []string{"npm test", "npm run test", "pnpm test", "yarn test", "npm --if-present test"} {
		t.Run(command, func(t *testing.T) {
			root := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"echo 'TODO add tests'","check":"node --test"}}`)
			writeFileForRepoMapTest(t, filepath.Join(root, ".github/workflows/ci.yml"), "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - run: "+command+"\n")
			template, err := setupDetectedTemplate(root, "fixture", setupOptions{Profile: "remote-ci"}, setupImportCandidates(root, ""))
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := config.ParseProjectConfig([]byte(template), "generated")
			if err != nil {
				t.Fatal(err)
			}
			if _, imported := cfg.Checks["tests"]; imported || strings.Contains(template, "# Selected:") || cfg.Checks["node"].Command != "npm install && npm run check" {
				t.Fatalf("placeholder workflow became automatic verification:\n%s", template)
			}
			if !strings.Contains(template, `# Not imported: "github-actions:.github/workflows/ci.yml:tests"`) {
				t.Fatalf("language default hid the uncovered upstream job:\n%s", template)
			}
		})
	}
}

func TestSetupUnknownPackageScopeNeverSilentlyNarrowsWorkflowSelection(t *testing.T) {
	for _, tc := range []struct {
		name, body, hooks, steps string
		otherJob                 bool
		wantError                bool
	}{
		{"workspace-job", "node --test", "", "      - run: pnpm --filter app test\n", true, true},
		{"unknown-later-step", "node --test", "", "      - run: go test ./...\n      - run: pnpm --filter app test\n", false, true},
		{"unknown-later-command", "node --test", "", "      - run: go test ./... && pnpm --filter app test\n", false, true},
		{"pretest-job", "echo done", `"pretest":"node --test integration.test.js",`, "      - run: npm test\n", true, true},
		{"posttest-job", "echo done", `"posttest":"node --test integration.test.js",`, "      - run: npm test\n", true, true},
		{"known-placeholder", "echo TODO", "", "      - run: npm test\n", true, true},
		{"placeholder-hooks", "echo done", `"pretest":"echo setup","posttest":"echo finished",`, "      - run: npm test\n", true, true},
		{"non-test-job", "echo TODO", "", "      - run: go vet ./...\n", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"scripts":{`+tc.hooks+`"test":"`+tc.body+`"},"workspaces":["app"]}`)
			writeFileForRepoMapTest(t, filepath.Join(root, "app/package.json"), `{"scripts":{"test":"node --test"}}`)
			workflow := "jobs:\n  ci:\n    runs-on: ubuntu-latest\n    steps:\n" + tc.steps
			if tc.otherJob {
				workflow += "  unit:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n"
			}
			writeFileForRepoMapTest(t, filepath.Join(root, ".github/workflows/ci.yml"), workflow)
			selection := automaticSetupSelection(setupImportCandidates(root, ""))
			if (selection.Imported == nil) != tc.wantError {
				t.Fatalf("declared test jobs must not be silently dropped: %+v", selection)
			}
			if tc.wantError && (selection.Reason == "" || !setupSelectionReportsJob(selection, "ci")) {
				t.Fatalf("language default did not report the uncovered declared job: %+v", selection)
			}
			if !tc.wantError && selection.Imported.Name != "unit" {
				t.Fatalf("a non-test job blocked the concrete unit test: %+v", selection.Imported)
			}
		})
	}
}

func TestSetupPackageSelectionConfigurationPreservesUnknownWorkflowScope(t *testing.T) {
	for _, tc := range []struct {
		name, workflowEnv, jobEnv, stepEnv, command, npmrc, pnpmWorkspace string
		unknown                                                           bool
	}{
		{name: "env-wrapper", command: "env npm_config_workspace=app npm test", unknown: true},
		{name: "inline-assignment", command: "npm_config_workspace=app npm test", unknown: true},
		{name: "exported-assignment", command: "export npm_config_workspace=app; npm test", unknown: true},
		{name: "workflow-env", workflowEnv: "env: {NPM_CONFIG_WORKSPACE: app}\n", command: "npm test", unknown: true},
		{name: "job-env", jobEnv: "    env: {npm_config_workspace: app}\n", command: "npm test", unknown: true},
		{name: "job-env-uppercase", jobEnv: "    env: {NPM_CONFIG_WORKSPACE: app}\n", command: "npm test", unknown: true},
		{name: "step-env", stepEnv: "        env: {NPM_CONFIG_PREFIX: app}\n", command: "npm test", unknown: true},
		{name: "user-config", command: "env NPM_CONFIG_USERCONFIG=alternate.npmrc npm test", unknown: true},
		{name: "user-config-flag", command: "npm test --userconfig ./alternate.npmrc", unknown: true},
		{name: "global-config-flag", command: "npm test --globalconfig ./alternate.npmrc", unknown: true},
		{name: "project-config", command: "npm test", npmrc: "workspace[]=app\n", unknown: true},
		{name: "pnpm-workspace-config", command: "pnpm test", pnpmWorkspace: "packages: [app]\ndir: app\n", unknown: true},
		{name: "pnpm-workspace-patterns", command: "pnpm test", pnpmWorkspace: "packages: [app]\n"},
		{name: "cache-env", stepEnv: "        env: {NPM_CONFIG_CACHE: scratch}\n", command: "npm test"},
		{name: "registry-config", command: "npm test", npmrc: "registry=https://registry.npmjs.org/\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			body := "echo TODO"
			if !tc.unknown {
				body = "node --test"
			}
			writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"`+body+`"},"workspaces":["app"]}`)
			writeFileForRepoMapTest(t, filepath.Join(root, "app/package.json"), `{"name":"app","scripts":{"test":"node --test"}}`)
			if tc.npmrc != "" {
				writeFileForRepoMapTest(t, filepath.Join(root, ".npmrc"), tc.npmrc)
			}
			if tc.pnpmWorkspace != "" {
				writeFileForRepoMapTest(t, filepath.Join(root, "pnpm-workspace.yaml"), tc.pnpmWorkspace)
			}
			workflow := tc.workflowEnv + "jobs:\n  ci:\n    runs-on: ubuntu-latest\n" + tc.jobEnv + "    steps:\n      - run: " + tc.command + "\n" + tc.stepEnv
			if tc.unknown {
				workflow += "  unit:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n"
			}
			writeFileForRepoMapTest(t, filepath.Join(root, ".github/workflows/ci.yml"), workflow)
			candidates := setupImportCandidates(root, "")
			for _, candidate := range candidates {
				if candidate.Name == "ci" && (candidate.ScopeUnknown != tc.unknown || candidate.TestEntrypoint == tc.unknown) {
					t.Fatalf("package selection inspected the wrong script: entrypoint=%v unknown=%v want unknown=%v", candidate.TestEntrypoint, candidate.ScopeUnknown, tc.unknown)
				}
			}
			selection := automaticSetupSelection(candidates)
			if (selection.Imported == nil) != tc.unknown || tc.unknown && !setupSelectionReportsJob(selection, "ci") {
				t.Fatalf("package selection must not silently narrow scope: %+v", selection)
			}
		})
	}
}

func TestSetupWorkflowInspectsTheInvokedPackageScript(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"echo TODO"}}`)
	writeFileForRepoMapTest(t, filepath.Join(root, "real/package.json"), `{"scripts":{"test":"node --test"}}`)
	for _, tc := range []struct {
		command string
		dir     string
		want    bool
	}{
		{"npm test", "", false},
		{"bun test", "", true},
		{"bun run test", "", false},
		{"npm test -- --prefix real", "", false},
		{"npm test --cwd real", "", false},
		{"npm test --dir real", "", false},
		{"npm test -C real", "", true},
		{"npm -C real test", "", true},
		{"npm --prefix real test", "", true},
		{"npm --cache scratch --prefix real test", "", true},
		{"npm run --prefix real test", "", true},
		{"npm test --prefix real", "", true},
		{"npm test", "real", true},
		{"npm --prefix .. test", "", false},
		{"pnpm --filter real test", "", false},
		{"pnpm --dir real --workspace-root test", "", false},
		{"pnpm --dir real test $TEST_PATTERN", "", true},
		{"yarn --cwd real run test $TEST_PATTERN", "", true},
		{"bun --cwd real run test $TEST_PATTERN", "", true},
		{"yarn run test --cwd real", "", false},
		{"bun run test --cwd real", "", false},
		{"npm test && node --test", "", true},
		{"pnpm exec vitest run", "", true},
	} {
		t.Run(tc.command+" in "+tc.dir, func(t *testing.T) {
			step := repoWorkflowRunStep{Command: tc.command, WorkingDirectory: tc.dir}
			if got, _ := setupWorkflowStepRunsTests(root, step); got != tc.want {
				t.Fatalf("recognized test entrypoint=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestSetupWorkflowDoesNotTreatUninspectableManifestAsMissing(t *testing.T) {
	for _, kind := range []string{"oversized", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			manifest := filepath.Join(root, "package.json")
			if kind == "oversized" {
				writeFileForRepoMapTest(t, manifest, `{"scripts":{"test":"echo TODO"},"padding":"`+strings.Repeat("x", setupRuntimeDeclarationLimit)+`"}`)
			} else {
				writeFileForRepoMapTest(t, filepath.Join(root, "manifest.json"), `{"scripts":{"test":"echo TODO"}}`)
				if err := os.Symlink("manifest.json", manifest); err != nil {
					t.Fatal(err)
				}
			}
			if runs, unknown := setupWorkflowStepRunsTests(root, repoWorkflowRunStep{Command: "npm test"}); runs || !unknown {
				t.Fatal("uninspectable package scripts must remain unknown test scope")
			}
		})
	}
}

func TestSetupWorkflowForwardedScriptArgumentsAreNotManagerOptions(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"jest"}}`)
	for _, command := range []string{
		"pnpm test --filter ./jest-filter.js", "pnpm run test --filter ./jest-filter.js",
		"pnpm test $TEST_PATTERN", "pnpm run --silent test $TEST_PATTERN",
		"yarn test --filter ./jest-filter.js", "yarn run test $TEST_PATTERN",
		"bun run test --filter ./jest-filter.js", "bun run test $TEST_PATTERN",
	} {
		t.Run(command, func(t *testing.T) {
			if runs, unknown := setupWorkflowStepRunsTests(root, repoWorkflowRunStep{Command: command}); !runs || unknown {
				t.Fatal("valid script arguments prevented workflow import")
			}
		})
	}
	if runs, unknown := setupWorkflowStepRunsTests(root, repoWorkflowRunStep{Command: "pnpm --filter app test"}); runs || !unknown {
		t.Fatal("a manager-level workspace selector must remain unknown package scope")
	}
}

func TestSetupWorkflowRuntimeOverridesVersionDefaultsButPreservesEngines(t *testing.T) {
	for _, engine := range []string{">=22", ">=26"} {
		t.Run(engine, func(t *testing.T) {
			root := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"node --test"},"engines":{"node":"`+engine+`"}}`)
			writeFileForRepoMapTest(t, filepath.Join(root, ".nvmrc"), "22.0.0\n")
			writeFileForRepoMapTest(t, filepath.Join(root, ".github/workflows/ci.yml"), "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-node@v4\n        with:\n          node-version: '24.15.0'\n      - run: npm test\n")
			template, err := setupDetectedTemplate(root, "fixture", setupOptions{Profile: "remote-ci"}, setupImportCandidates(root, ""))
			if engine == ">=26" {
				if err == nil {
					t.Fatal("workflow version bypassed the package's compatibility constraint")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := config.ParseProjectConfig([]byte(template), "proposal")
			if err != nil || !preflightapi.RuntimeVersionSatisfies("24.15.0", cfg.Checks["tests"].RuntimeRequirements["node"]) {
				t.Fatalf("explicit workflow runtime was lost: %v %v", cfg, err)
			}
		})
	}
}

func TestSetupRuntimeSelectionPreservesCompatibilityAndIntegrityDeclarations(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "go.mod"), "module example.test/runtime\ngo 1.25.0\ntoolchain go1.26.0\n")
	writeFileForRepoMapTest(t, filepath.Join(root, ".go-version"), "1.24.0\n")
	selected := map[string]string{"go": "1.25.13"}
	requirements, err := setupDeclaredRuntimeRequirementsForSelection(root, []string{"go test ./..."}, selected)
	if err != nil || !preflightapi.RuntimeVersionSatisfies("1.25.13", requirements["go"]) || preflightapi.RuntimeVersionSatisfies("1.24.0", requirements["go"]) {
		t.Fatalf("explicit Go selection lost language minimum or retained a default: %v %v", requirements, err)
	}
	if _, err := setupDeclaredRuntimeRequirementsForSelection(root, []string{"go test ./..."}, map[string]string{"go": "installed"}); err == nil {
		t.Fatal("installed-only selection discarded contradictory repository defaults")
	}
	writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"packageManager":"pnpm@11.15.1+sha512.invalid"}`)
	if _, err := setupDeclaredRuntimeRequirementsForSelection(root, []string{"pnpm test"}, map[string]string{"node": "24", "pnpm": "11.15.1"}); err == nil {
		t.Fatal("explicit selection discarded package-manager integrity metadata")
	}
}

func TestSetupAutomaticPreviewResolvesProposalAndCapability(t *testing.T) {
	options, runs := newGuidedSetupReviewFixture(t)
	writeFileForRepoMapTest(t, "go.mod", "module example.test/preview\n\ngo 1.25.0\n")
	report, err := buildSetupReport(options)
	if err != nil {
		t.Fatal(err)
	}
	if report.ConfigStatus != "missing" || report.ProposedConfig == "" || len(report.Checks) == 0 || report.Environment.WorkerCapability == nil {
		t.Fatalf("automatic preview omitted its proposal or capability: %+v", report)
	}
	if report.Environment.NetworkMode == "" || report.Environment.RuntimeRequirements["go"] == "" {
		t.Fatalf("automatic preview omitted effective execution requirements: %+v", report.Environment)
	}
	if _, err := os.Stat(".readyci.yml"); !os.IsNotExist(err) || runs.Load() != 0 {
		t.Fatal("preview wrote a configuration or started execution")
	}
}

func setupSelectionReportsJob(selection setupAutomaticSelection, name string) bool {
	for _, candidate := range selection.NotImported {
		if candidate.Name == name {
			return true
		}
	}
	return false
}

func setupSelectionReportsCandidate(selection setupAutomaticSelection, id string) bool {
	for _, candidate := range selection.NotImported {
		if candidate.ID == id {
			return true
		}
	}
	return false
}
