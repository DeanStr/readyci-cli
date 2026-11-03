package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestSetupRuntimeRequirementsFollowPackageDirectoryFlags(t *testing.T) {
	for _, command := range []string{
		"npm --prefix web test", "npm --prefix=web test", "npm test --prefix web",
		"npm run --prefix web test", "npm -C web test",
		"pnpm --dir web test", "pnpm -C web run test $TEST_PATTERN",
		"yarn --cwd web test", "yarn --cwd=web run test $TEST_PATTERN",
	} {
		t.Run(command, func(t *testing.T) {
			root := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(root, "web/package.json"), `{"engines":{"node":">=99"}}`)
			requirements, notes := setupDeclaredRuntimeRequirements(root, []string{command})
			if len(notes) != 0 || requirements["node"] != ">=99" {
				t.Fatalf("selected package runtime was lost: requirements=%v error=%v", requirements, notes)
			}
		})
	}
}

func TestSetupRuntimeScopePreservesNpxRunnerArguments(t *testing.T) {
	for _, command := range []string{
		"npx vitest run --dir tests",
		"npx vitest run $TEST_PATTERN",
		"npx --yes --package=vitest -- vitest run --dir tests",
		"npx -p vitest vitest run --dir tests",
		"npx run --dir tests",
		"npx --prefix web vitest run --dir tests",
		"npx --prefix web vitest run --prefix ../outside $TEST_PATTERN",
	} {
		t.Run(command, func(t *testing.T) {
			root := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"engines":{"node":">=24"}}`)
			writeFileForRepoMapTest(t, filepath.Join(root, "web/package.json"), `{"engines":{"node":">=26"}}`)
			requirements, notes := setupDeclaredRuntimeRequirements(root, []string{command})
			want := ">=24"
			if strings.Contains(command, "web") {
				want = ">=26"
			}
			if len(notes) != 0 || !preflightapi.RuntimeRequirementImplies(requirements["node"], want) || !preflightapi.RuntimeVersionSatisfies("26.0.0", requirements["node"]) {
				t.Fatalf("runner arguments changed manager scope: requirements=%v error=%v", requirements, notes)
			}
		})
	}
	for _, command := range []string{"npx --prefix $PACKAGE vitest run", "npx --workspace app vitest run"} {
		root := t.TempDir()
		if requirements, notes := setupDeclaredRuntimeRequirements(root, []string{command}); len(notes) == 0 {
			t.Fatalf("unknown manager scope accepted: command=%s requirements=%v", command, requirements)
		}
	}
	for _, command := range []string{"bunx --cwd web vitest run --dir tests", "bunx --cwd web run --dir tests $TEST_PATTERN"} {
		call := shellCommandsFromCommand(command)[0]
		if selection, ok := setupRuntimePackageSelection("bunx", ".", call.Args[1:]); !ok || selection.Directory != "web" {
			t.Fatalf("bunx runner arguments changed manager scope: selection=%+v ok=%v", selection, ok)
		}
	}
}

func TestSetupRuntimeScopePreservesExecWorkingDirectory(t *testing.T) {
	for _, command := range []string{
		"npx --prefix web vitest run",
		"npm --prefix web exec -- vitest run",
		"npm --prefix web x -- vitest run",
	} {
		t.Run(command, func(t *testing.T) {
			root := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"engines":{"node":">=26"}}`)
			writeFileForRepoMapTest(t, filepath.Join(root, "web/package.json"), `{"engines":{"node":">=22"}}`)
			requirements, notes := setupDeclaredRuntimeRequirements(root, []string{command})
			if len(notes) != 0 || preflightapi.RuntimeVersionSatisfies("22.0.0", requirements["node"]) || !preflightapi.RuntimeVersionSatisfies("26.0.0", requirements["node"]) {
				t.Fatalf("dependency prefix replaced execution directory: requirements=%v error=%v", requirements, notes)
			}
			requirements, notes = setupDeclaredRuntimeRequirements(root, []string{"npm --prefix web test"})
			if len(notes) != 0 || !preflightapi.RuntimeVersionSatisfies("22.0.0", requirements["node"]) {
				t.Fatalf("script prefix lost its actual package directory: requirements=%v error=%v", requirements, notes)
			}
		})
	}
}

func TestSetupRuntimeScopeRejectsHiddenPackageSelection(t *testing.T) {
	for _, command := range []string{
		"cd web && env npm_config_workspaces=true npm_config_include_workspace_root=true npm test",
		"cd web && NPM_CONFIG_WORKSPACES=true npm test",
		"NPM_CONFIG_WORKSPACES=true bash -c 'cd web && npm test'",
		"bash -c 'cd web && NPM_CONFIG_WORKSPACES=true npm test'",
		"cd web && env PNPM_CONFIG_FILTER=app pnpm test",
		"cd web && env NPM_CONFIG_WORKSPACES=true npx vitest run",
	} {
		t.Run(command, func(t *testing.T) {
			root := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"workspaces":["web"],"engines":{"node":">=24"}}`)
			writeFileForRepoMapTest(t, filepath.Join(root, "web/package.json"), `{"engines":{"node":">=22"}}`)
			if requirements, notes := setupDeclaredRuntimeRequirements(root, []string{command}); len(notes) == 0 || !strings.Contains(strings.Join(notes, "; "), "explicit") {
				t.Fatalf("hidden package selection accepted: requirements=%v error=%v", requirements, notes)
			}
		})
	}
	for _, manager := range []string{"npm", "pnpm", "npx"} {
		root := t.TempDir()
		writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"workspaces":["web"],"engines":{"node":">=24"}}`)
		if manager == "pnpm" {
			writeFileForRepoMapTest(t, filepath.Join(root, "pnpm-workspace.yaml"), "packages: [web]\n")
		}
		writeFileForRepoMapTest(t, filepath.Join(root, "web/package.json"), `{"engines":{"node":">=22"}}`)
		writeFileForRepoMapTest(t, filepath.Join(root, ".npmrc"), "workspaces=true\ninclude-workspace-root=true\n")
		if requirements, notes := setupDeclaredRuntimeRequirements(root, []string{"cd web && " + manager + " test"}); len(notes) == 0 {
			t.Fatalf("config package selection accepted: manager=%s requirements=%v", manager, requirements)
		}
	}
}

func TestSetupRuntimeScopeChecksStepEnvironment(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"workspaces":["web"],"engines":{"node":">=24"}}`)
	writeFileForRepoMapTest(t, filepath.Join(root, "web/package.json"), `{"engines":{"node":">=22"}}`)
	template := "version: 1\nchecks:\n  unit:\n    runtime_requirements:\n      node: '22'\n      npm: installed\n    steps:\n      - command: cd web && npm test\n        env:\n          NPM_CONFIG_WORKSPACES: 'true'\n"
	if notes := setupTemplateRuntimeNotes(t, root, template); !strings.Contains(strings.Join(notes, ";"), "explicit") {
		t.Fatalf("step environment lost: notes=%v\n%s", notes, template)
	}
}

func TestSetupRuntimeScopePreservesNestedStepEnvironment(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"workspaces":["web"],"engines":{"node":">=24"}}`)
	writeFileForRepoMapTest(t, filepath.Join(root, "web/package.json"), `{"engines":{"node":">=22"}}`)
	template := "version: 1\nchecks:\n  unit:\n    runtime_requirements: {node: '22', npm: installed}\n    steps:\n      - command: cd web && bash -c 'npm test'\n        env:\n          NPM_CONFIG_WORKSPACES: 'true'\n          NPM_CONFIG_INCLUDE_WORKSPACE_ROOT: 'true'\n"
	if notes := setupTemplateRuntimeNotes(t, root, template); !strings.Contains(strings.Join(notes, ";"), "explicit") {
		t.Fatalf("nested step environment lost: notes=%v", notes)
	}
	writeFileForRepoMapTest(t, filepath.Join(root, "go.mod"), "module example.com/root\ngo 1.25.0\n")
	writeFileForRepoMapTest(t, filepath.Join(root, "web/go.mod"), "module example.com/web\ngo 1.27.0\n")
	steps := importedWorkflowSteps([]repoWorkflowRunStep{{Command: "cd web && go test ./...", Env: map[string]string{"NPM_CONFIG_WORKSPACES": "true"}}})
	command := setupRuntimeCommandWithEnvironment(steps[0].Command, steps[0].Env)
	requirements, notes := setupDeclaredRuntimeRequirements(root, []string{command})
	if len(notes) != 0 || preflightapi.RuntimeVersionSatisfies("1.25.0", requirements["go"]) || !preflightapi.RuntimeVersionSatisfies("1.27.0", requirements["go"]) {
		t.Fatalf("environment projection changed another runtime's directory: requirements=%v error=%v", requirements, notes)
	}
}

func TestSetupRuntimeScopeUsesEffectiveNpmConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, command, workspaces, configPath string
		blocked                               bool
	}{
		{name: "independent parent", command: "cd web && npm test", configPath: ".npmrc"},
		{name: "owner", command: "cd web && npm test", workspaces: `,"workspaces":["web"]`, configPath: ".npmrc", blocked: true},
		{name: "explicit prefix", command: "npm --prefix web test", workspaces: `,"workspaces":["web"]`, configPath: ".npmrc"},
		{name: "explicit current prefix", command: "cd web && npm --prefix . test", workspaces: `,"workspaces":["web"]`, configPath: ".npmrc"},
		{name: "nearest project", command: "cd web/src && npm test", configPath: "web/.npmrc", blocked: true},
		{name: "unused subdirectory", command: "cd web/src && npm test", configPath: "web/src/.npmrc"},
		{name: "ignored workspace child config", command: "cd web && npm test", workspaces: `,"workspaces":["web"]`, configPath: "web/.npmrc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"private":true`+tc.workspaces+`}`)
			writeFileForRepoMapTest(t, filepath.Join(root, "web/package.json"), `{"engines":{"node":">=24"}}`)
			writeFileForRepoMapTest(t, filepath.Join(root, "web/src/probe"), "component\n")
			writeFileForRepoMapTest(t, filepath.Join(root, tc.configPath), "workspaces=true\ninclude-workspace-root=true\n")
			requirements, notes := setupDeclaredRuntimeRequirements(root, []string{tc.command})
			if (len(notes) != 0) != tc.blocked || len(notes) == 0 && requirements["node"] != ">=24" {
				t.Fatalf("incorrect project config boundary: requirements=%v error=%v blocked=%v", requirements, notes, tc.blocked)
			}
		})
	}
}

func TestSetupRuntimeScopeAcceptsGeneratedYarnCheck(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"node --test"},"engines":{"node":">=22"},"packageManager":"yarn@1.22.22"}`)
	writeFileForRepoMapTest(t, filepath.Join(root, "yarn.lock"), "# yarn lockfile v1\n")
	command := nodeCheckCommand(root, "test")
	requirements, notes := setupDeclaredRuntimeRequirements(root, []string{command})
	if len(notes) != 0 || requirements["node"] != ">=22" || requirements["yarn"] != "1.22.22" {
		t.Fatalf("generated Yarn command rejected: command=%s requirements=%v error=%v", command, requirements, notes)
	}
}

func TestSetupRuntimeScopePreservesEveryOpaqueHarness(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "go.mod"), "module example.com/root\ngo 1.27.0\n")
	writeFileForRepoMapTest(t, filepath.Join(root, "sub/go.mod"), "module example.com/sub\ngo 1.25.0\n")
	commands := []string{"./scripts/test", "cd sub && go test ./..."}
	requirements, notes := setupDeclaredRuntimeRequirements(root, commands)
	if len(notes) != 0 || !preflightapi.RuntimeVersionSatisfies("1.27.0", requirements["go"]) || preflightapi.RuntimeVersionSatisfies("1.25.0", requirements["go"]) {
		t.Fatalf("opaque harness minimum disappeared: requirements=%v error=%v", requirements, notes)
	}
}

func TestSetupRuntimeScopePreservesMixedMakeRecipes(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "Makefile"), "test: python-tests\n\tgo test ./...\npython-tests:\n\t./scripts/test-python\n")
	writeFileForRepoMapTest(t, filepath.Join(root, "scripts/test-python"), "#!/bin/sh\nexec python3 -m pytest\n")
	writeFileForRepoMapTest(t, filepath.Join(root, "pyproject.toml"), "[project]\nrequires-python = '>=3.13'\n")
	writeFileForRepoMapTest(t, filepath.Join(root, "sub/pyproject.toml"), "[project]\nrequires-python = '>=3.10'\n")
	commands := []string{"make test", "cd sub && python3 -m pytest"}
	requirements, notes := setupDeclaredRuntimeNotesForSelection(root, commands, map[string]string{"python": "3.13", "go": "1.25.13"})
	if len(notes) != 0 || preflightapi.RuntimeVersionSatisfies("3.12.3", requirements["python"]) || !preflightapi.RuntimeVersionSatisfies("3.13.0", requirements["python"]) {
		t.Fatalf("mixed Make recipe lost opaque runtime: requirements=%v notes=%v", requirements, notes)
	}
	// The opaque Make directory's minimum is still reviewed when the selection
	// contradicts it; the selection is kept and the declaration is disclosed.
	requirements, notes = setupDeclaredRuntimeNotesForSelection(root, commands, map[string]string{"python": "3.12", "go": "1.25.13"})
	if requirements["python"] != "3.12,>=3.10" || len(notes) != 1 || !strings.Contains(notes[0], `pyproject.toml (requires-python) ">=3.13"`) {
		t.Fatalf("contradicted opaque runtime minimum was sealed or hidden: requirements=%v notes=%v", requirements, notes)
	}
}

func TestSetupRuntimeScopeUsesMakeWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "web/Makefile"), "test:\n\tnpm test\n")
	writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"engines":{"node":"<24"}}`)
	writeFileForRepoMapTest(t, filepath.Join(root, "web/package.json"), `{"engines":{"node":">=24"}}`)
	for _, command := range []string{"make -C web test", "make --directory=web test", "cd web && make test"} {
		requirements, notes := setupDeclaredRuntimeRequirements(root, []string{command}, "node")
		if len(notes) != 0 || requirements["node"] != ">=24" {
			t.Fatalf("Make working directory was not used for %q: requirements=%v error=%v", command, requirements, notes)
		}
	}
	// Recipes are never evaluated, so Make alone selects no runtime.
	if requirements, notes := setupDeclaredRuntimeRequirements(root, []string{"make -C web test"}); len(notes) != 0 || len(requirements) != 0 {
		t.Fatalf("Make recipe inferred a runtime: requirements=%v error=%v", requirements, notes)
	}
}

func TestSetupRuntimeScopePreservesDynamicExecutableDeclarations(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "pyproject.toml"), "[project]\nrequires-python = '>=3.13'\n")
	requirements, notes := setupDeclaredRuntimeNotesForSelection(root, []string{`"$PYTHON" -m pytest`}, map[string]string{"python": "3.13"})
	if len(notes) != 0 || requirements["python"] != "3.13,>=3.13" {
		t.Fatalf("dynamic executable minimum disappeared: requirements=%v notes=%v", requirements, notes)
	}
	requirements, notes = setupDeclaredRuntimeNotesForSelection(root, []string{`"$PYTHON" -m pytest`}, map[string]string{"python": "3.12"})
	if requirements["python"] != "3.12" || len(notes) != 1 || !strings.Contains(notes[0], "conflicts") {
		t.Fatalf("selected Python below project minimum was not disclosed: requirements=%v notes=%v", requirements, notes)
	}
	if _, notes := setupDeclaredRuntimeNotesForSelection(root, []string{`cd "$PROJECT" && "$PYTHON" -m pytest`}, map[string]string{"python": "3.13"}); len(notes) == 0 {
		t.Fatal("dynamic working directory was accepted")
	}
}

func TestSetupRuntimeScopeIgnoresUnrelatedToolQueriesAndFilePreparation(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"engines":{"node":"<24"}}`)
	writeFileForRepoMapTest(t, filepath.Join(root, "web/package.json"), `{"engines":{"node":">=24"}}`)
	commands := []string{"pnpm --version", "npx --version", "mkdir -p output", "npm --prefix web test"}
	requirements, notes := setupDeclaredRuntimeRequirements(root, commands)
	if len(notes) != 0 || requirements["node"] != ">=24" {
		t.Fatalf("tool query or file preparation selected root package: requirements=%v error=%v", requirements, notes)
	}
}

func TestSetupRuntimeScopeInheritsOwningWorkspaceManager(t *testing.T) {
	for _, workspace := range []string{"package.json", "pnpm-workspace.yaml"} {
		t.Run(workspace, func(t *testing.T) {
			root := t.TempDir()
			manifest := `{"packageManager":"pnpm@` + setupSveltePnpmIntegrityPin + `","engines":{"node":"<24"}}`
			if workspace == "package.json" {
				manifest = strings.TrimSuffix(manifest, "}") + `,"workspaces":["web"]}`
			} else {
				writeFileForRepoMapTest(t, filepath.Join(root, workspace), "packages: [web]\n")
			}
			writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), manifest)
			writeFileForRepoMapTest(t, filepath.Join(root, "web/package.json"), `{"engines":{"node":">=24"}}`)
			requirements, notes := setupDeclaredRuntimeRequirements(root, []string{"pnpm --dir web test"})
			if len(notes) != 0 || requirements["pnpm"] != setupSveltePnpmIntegrityPin || requirements["node"] != ">=24" {
				t.Fatalf("workspace manager or child engines lost: requirements=%v error=%v", requirements, notes)
			}
			if _, err := preflightapi.MergeRuntimeRequirements(map[string]string{"pnpm": "11.15.1"}, requirements); err == nil {
				t.Fatal("conflicting selected manager was accepted")
			}
		})
	}
}

func TestSetupRuntimeScopeDoesNotInheritIndependentPackageManager(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"packageManager":"pnpm@10.11.0","workspaces":["other"]}`)
	writeFileForRepoMapTest(t, filepath.Join(root, "web/package.json"), `{"engines":{"node":">=24"}}`)
	requirements, notes := setupDeclaredRuntimeRequirements(root, []string{"npm --prefix web test"})
	if len(notes) != 0 || requirements["pnpm"] != "" || requirements["node"] != ">=24" {
		t.Fatalf("unrelated manager inherited: requirements=%v error=%v", requirements, notes)
	}
}

func TestSetupRuntimeScopeRejectsUnsupportedWorkspacePatterns(t *testing.T) {
	for _, pattern := range []string{"{web,other}", "@(web|other)", "[web"} {
		for _, workspace := range []string{"package.json", "pnpm-workspace.yaml"} {
			t.Run(workspace+"/"+pattern, func(t *testing.T) {
				root := t.TempDir()
				manifest := `{"packageManager":"pnpm@11.15.1"}`
				if workspace == "package.json" {
					manifest = strings.TrimSuffix(manifest, "}") + `,"workspaces":["` + pattern + `"]}`
				} else {
					writeFileForRepoMapTest(t, filepath.Join(root, workspace), "packages: ['"+pattern+"']\n")
				}
				writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), manifest)
				writeFileForRepoMapTest(t, filepath.Join(root, "web/package.json"), `{"engines":{"node":">=24"}}`)
				if requirements, notes := setupDeclaredRuntimeRequirements(root, []string{"pnpm --dir web test"}); len(notes) == 0 || !strings.Contains(strings.Join(notes, "; "), "workspace") {
					t.Fatalf("unsupported workspace pattern silently lost manager pin: requirements=%v error=%v", requirements, notes)
				}
			})
		}
	}
}

func TestSetupRuntimeScopePreservesPackageScriptRuntimeSelections(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"go test ./..."}}`)
	writeFileForRepoMapTest(t, filepath.Join(root, "go.mod"), "module example.com/root\ngo 1.27.0\n")
	for _, command := range []string{"npm test", "pnpm test", "yarn test"} {
		requirements, notes := setupDeclaredRuntimeNotesForSelection(root, []string{command}, map[string]string{"go": ">=1.25"})
		if len(notes) != 0 || preflightapi.RuntimeVersionSatisfies("1.25.13", requirements["go"]) || !preflightapi.RuntimeVersionSatisfies("1.27.0", requirements["go"]) {
			t.Fatalf("selected runtime lost behind %s: requirements=%v error=%v", command, requirements, notes)
		}
	}
	requirements, notes := setupDeclaredRuntimeRequirements(root, []string{"npm test"}, "go")
	if len(notes) != 0 || preflightapi.RuntimeVersionSatisfies("1.25.13", requirements["go"]) {
		t.Fatalf("additional runtime selection lost: requirements=%v error=%v", requirements, notes)
	}
	writeFileForRepoMapTest(t, filepath.Join(root, "web/package.json"), `{"scripts":{"test":"go test ./..."}}`)
	writeFileForRepoMapTest(t, filepath.Join(root, "web/go.mod"), "module example.com/web\ngo 1.28.0\n")
	requirements, notes = setupDeclaredRuntimeNotesForSelection(root, []string{"go mod download", "npm --prefix web test"}, map[string]string{"go": ">=1.27.0"})
	if len(notes) != 0 || preflightapi.RuntimeVersionSatisfies("1.27.0", requirements["go"]) || !preflightapi.RuntimeVersionSatisfies("1.28.0", requirements["go"]) {
		t.Fatalf("preparation masked package script runtime: requirements=%v error=%v", requirements, notes)
	}
}

func TestSetupRuntimeScopeInspectsInvokedPackageScripts(t *testing.T) {
	for _, body := range []string{"go test ./...", "npm run test:go", "./scripts/test-go", "bash -c 'go test ./...'", "make test"} {
		t.Run(body, func(t *testing.T) {
			root := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(root, "go.mod"), "module example.com/root\ngo 1.25.0\n")
			writeFileForRepoMapTest(t, filepath.Join(root, "web/go.mod"), "module example.com/web\ngo 1.27.0\n")
			writeFileForRepoMapTest(t, filepath.Join(root, "web/package.json"), `{"scripts":{"test":"`+body+`","test:go":"go test ./..."}}`)
			writeFileForRepoMapTest(t, filepath.Join(root, "web/Makefile"), "test:\n\tgo test ./...\n")
			requirements, notes := setupDeclaredRuntimeRequirements(root, []string{"go mod download", "cd web && npm test"})
			if len(notes) != 0 || preflightapi.RuntimeVersionSatisfies("1.25.0", requirements["go"]) || !preflightapi.RuntimeVersionSatisfies("1.27.0", requirements["go"]) {
				t.Fatalf("inferred runtime lost behind package script: requirements=%v error=%v", requirements, notes)
			}
		})
	}
}

func TestSetupRuntimeScopeKeepsDirectInvocationOfLifecycleNamedScripts(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"node --test","pretest":"go test ./..."}}`)
	writeFileForRepoMapTest(t, filepath.Join(root, "go.mod"), "module example.com/root\ngo 1.27.0\n")
	requirements, notes := setupDeclaredRuntimeRequirements(root, []string{"npm test && npm run pretest"})
	if len(notes) != 0 || !preflightapi.RuntimeVersionSatisfies("1.27.0", requirements["go"]) || preflightapi.RuntimeVersionSatisfies("1.25.0", requirements["go"]) {
		t.Fatalf("direct invocation was hidden by lifecycle discovery: requirements=%v error=%v", requirements, notes)
	}
}

func TestSetupRuntimeScopeDistinguishesManagerBuiltinsFromScripts(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"scripts":{"node":"python3 -m pytest","config":"python3 -m pytest","info":"python3 -m pytest","bin":"python3 -m pytest","ci":"python3 -m pytest","i":"python3 -m pytest","x":"python3 -m pytest","test":"python3 -m pytest"}}`)
	writeFileForRepoMapTest(t, filepath.Join(root, "pyproject.toml"), "[project]\nrequires-python = '>=3.13'\n")
	for _, command := range []string{"yarn node --test", "yarn config list", "yarn info", "yarn bin", "pnpm ci", "bun test"} {
		if runtimes := setupCommandRuntimes(root, command); containsString(runtimes, "python") {
			t.Fatalf("built-in expanded an unused package script: command=%s runtimes=%v", command, runtimes)
		}
	}
	for _, command := range []string{"yarn run node", "yarn ci", "yarn i", "yarn x", "pnpm node", "pnpm x", "bun run test"} {
		if runtimes := setupCommandRuntimes(root, command); !containsString(runtimes, "python") {
			t.Fatalf("actual package script did not expand: command=%s runtimes=%v", command, runtimes)
		}
	}
}

func TestSetupRuntimeScopeAcceptsInstallOptions(t *testing.T) {
	for _, command := range []string{
		"pnpm --frozen-lockfile install && pnpm test",
		"yarn --immutable && yarn test",
		"pnpm install --dir web --frozen-lockfile",
		"yarn --cwd web --immutable",
	} {
		t.Run(command, func(t *testing.T) {
			root := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"engines":{"node":">=24"}}`)
			writeFileForRepoMapTest(t, filepath.Join(root, "web/package.json"), `{"engines":{"node":">=26"}}`)
			requirements, notes := setupDeclaredRuntimeRequirements(root, []string{command})
			want := ">=24"
			if strings.Contains(command, "web") {
				want = ">=26"
			}
			if len(notes) != 0 || requirements["node"] != want {
				t.Fatalf("install scope lost: requirements=%v error=%v", requirements, notes)
			}
		})
	}
}

func TestSetupComponentRuntimeDoesNotMergeUnrelatedRootPackage(t *testing.T) {
	for _, command := range []string{
		"cd web && npm test",
		"CI=true bash -e -c 'cd web || exit\nnpm test' && printf finished",
		"npm --prefix web test",
		"cd web/src && npm test",
	} {
		t.Run(command, func(t *testing.T) {
			root := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"engines":{"node":"<24"},"packageManager":"pnpm@9.0.0"}`)
			writeFileForRepoMapTest(t, filepath.Join(root, "web/package.json"), `{"engines":{"node":">=24"},"packageManager":"npm@11.0.0"}`)
			writeFileForRepoMapTest(t, filepath.Join(root, "web/src/probe"), "component\n")
			requirements, notes := setupDeclaredRuntimeNotesForSelection(root, []string{command}, map[string]string{"node": "24"})
			if len(notes) != 0 || !preflightapi.RuntimeVersionSatisfies("24.18.0", requirements["node"]) || preflightapi.RuntimeVersionSatisfies("22.0.0", requirements["node"]) || requirements["npm"] != "11.0.0" || requirements["pnpm"] != "" {
				t.Fatalf("unrelated root package constrained component: requirements=%v error=%v", requirements, notes)
			}
		})
	}
}

func TestSetupRuntimeDeclarationsStayWithTheirRuntimeInvocation(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"engines":{"node":"<24"}}`)
	writeFileForRepoMapTest(t, filepath.Join(root, "pyproject.toml"), "[project]\nrequires-python = '>=3.10'\n")
	writeFileForRepoMapTest(t, filepath.Join(root, "web/package.json"), `{"engines":{"node":">=24"},"scripts":{"test":"node --test"}}`)
	writeFileForRepoMapTest(t, filepath.Join(root, "web/pyproject.toml"), "[project]\nrequires-python = '>=99'\n")
	commands := []string{"python3 -m pytest", "npm --prefix web test"}
	for _, test := range []struct{ selected, want map[string]string }{
		{nil, map[string]string{"node": ">=24", "python": ">=3.10"}},
		{map[string]string{"node": "24", "python": "3.12"}, map[string]string{"node": "24,>=24", "python": "3.12,>=3.10"}},
	} {
		requirements, notes := setupDeclaredRuntimeNotesForSelection(root, commands, test.selected)
		if len(notes) != 0 || requirements["node"] != test.want["node"] || requirements["python"] != test.want["python"] {
			t.Fatalf("runtime declarations crossed command directories: requirements=%v notes=%v selected=%v", requirements, notes, test.selected)
		}
	}
}

func TestSetupRuntimeDeclarationsIncludeEverySelectedPackage(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"engines":{"node":"<24"}}`)
	writeFileForRepoMapTest(t, filepath.Join(root, "web/package.json"), `{"engines":{"node":">=24"}}`)
	if requirements, notes := setupDeclaredRuntimeRequirements(root, []string{"npm test", "npm --prefix web test"}); len(notes) == 0 {
		t.Fatalf("incompatible selected packages were accepted: %v", requirements)
	}
}

func TestSetupComponentRuntimeKeepsNearestAncestorDefaults(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, ".nvmrc"), "24.18.0\n")
	writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"engines":{"node":"<24"}}`)
	writeFileForRepoMapTest(t, filepath.Join(root, "web/package.json"), `{"engines":{"node":">=24"}}`)
	writeFileForRepoMapTest(t, filepath.Join(root, "other/package.json"), `{"engines":{"node":">=24"}}`)
	requirements, notes := setupDeclaredRuntimeRequirements(root, []string{"npm --prefix web test", "npm --prefix other test"})
	if len(notes) != 0 || !preflightapi.RuntimeVersionSatisfies("24.18.0", requirements["node"]) || preflightapi.RuntimeVersionSatisfies("24.18.1", requirements["node"]) {
		t.Fatalf("applicable ancestor default disappeared or unrelated root engines applied: requirements=%v error=%v", requirements, notes)
	}
	declarations, warnings := setupRuntimeRequirements(root, []string{"npm --prefix web test", "npm --prefix other test"})
	count := 0
	for _, declaration := range declarations {
		if declaration.Source == ".nvmrc" {
			count++
		}
	}
	if len(warnings) > 0 || count != 1 {
		t.Fatalf("shared ancestor declaration was repeated: declarations=%v warnings=%v", declarations, warnings)
	}
}

func TestSetupComponentPythonBootstrapDoesNotSelectRootProject(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "pyproject.toml"), "[project]\nrequires-python = '<3.12'\n")
	writeFileForRepoMapTest(t, filepath.Join(root, "component/pyproject.toml"), "[project]\nrequires-python = '>=3.12'\n")
	check := setupPrepareDetectedCheck(root, detectedCheck{Command: "cd component && python3 -m pytest", RuntimeRequirements: map[string]string{"python": "3.12"}})
	if strings.Contains(strings.Join(setupUnresolvedNotes(check.Unresolved), ";"), "Runtime") || !preflightapi.RuntimeVersionSatisfies("3.12.3", check.RuntimeRequirements["python"]) || !containsString(check.Prepare, setupPythonVenvPreparation) {
		t.Fatalf("generated bootstrap applied root project's requirements: %+v", check)
	}
}

func TestSetupRuntimeDirectorySelectionRejectsUnknownOrUnconfinedPackages(t *testing.T) {
	for _, command := range []string{"npm --prefix ../outside test", "npm --prefix /outside test", "npm --prefix $PACKAGE test", "pnpm --filter app test"} {
		t.Run(command, func(t *testing.T) {
			root := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"engines":{"node":"24"}}`)
			if requirements, notes := setupDeclaredRuntimeRequirements(root, []string{command}); len(notes) == 0 {
				t.Fatalf("unknown package scope became a root runtime: %v", requirements)
			}
		})
	}
}

func TestSetupImportedComponentRuntimeScope(t *testing.T) {
	for _, selection := range []string{"prefix", "working-directory"} {
		t.Run(selection, func(t *testing.T) {
			root := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"engines":{"node":"<24"}}`)
			writeFileForRepoMapTest(t, filepath.Join(root, "web/package.json"), `{"scripts":{"test":"node --test"},"engines":{"node":">=24"}}`)
			step := "      - run: npm --prefix web test\n"
			if selection == "working-directory" {
				step = "      - run: npm test\n        working-directory: web\n"
			}
			writeFileForRepoMapTest(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  unit:\n    runs-on: ubuntu-24.04\n    steps:\n      - uses: actions/setup-node@v4\n        with:\n          node-version: '24'\n"+step)
			candidates := setupImportCandidates(root, "github-actions")
			if len(candidates) != 1 {
				t.Fatalf("candidates=%+v", candidates)
			}
			candidate, err := validateSetupImport(candidates[0])
			if err != nil {
				t.Fatal(err)
			}
			template := setupImportedTemplateForRoot(root, setupOptions{Profile: "remote-ci"}, "component", *candidate)
			if notes := setupTemplateRuntimeNotes(t, root, template); len(notes) != 0 {
				t.Fatalf("valid component workflow produced runtime notes: %v\n%s", notes, template)
			}
			cfg, err := config.ParseProjectConfig([]byte(template), "component workflow")
			if err != nil {
				t.Fatal(err)
			}
			check := cfg.Checks["unit"]
			if !preflightapi.RuntimeVersionSatisfies("24.18.0", check.RuntimeRequirements["node"]) || preflightapi.RuntimeVersionSatisfies("22.0.0", check.RuntimeRequirements["node"]) || len(check.Steps) != 1 || !strings.Contains(check.Steps[0].Command, "npm") {
				t.Fatalf("component workflow runtime or command changed: %+v", check)
			}
		})
	}
}

// setupTemplateRuntimeNotes reviews each check of a configuration the way
// setup's environment reconciliation does.
func setupTemplateRuntimeNotes(t *testing.T, root, template string) []string {
	t.Helper()
	cfg, err := config.ParseProjectConfig([]byte(template), "runtime review")
	if err != nil {
		t.Fatal(err)
	}
	var notes []string
	for _, name := range sortedCheckNames(cfg.Checks) {
		check := cfg.Checks[name]
		commands := append([]string{check.Command}, check.Prepare...)
		for _, step := range check.Steps {
			commands = append(commands, setupRuntimeCommandWithEnvironment(step.Command, step.Env))
		}
		_, checkNotes := setupDeclaredRuntimeNotesForSelection(root, commands, check.RuntimeRequirements)
		notes = append(notes, checkNotes...)
	}
	return notes
}
