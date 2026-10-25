package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/verificationplan"
)

// Requests at 611c6162cbc4ac2020a2f91c7cfa4f3abf9bbb60 declares dependency preparation separately
// from its test target. The selected tests must remain unchanged.
func TestSetupPreparesPythonMakeDependenciesWithoutChangingTests(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "init:\n\tpython -m pip install -r requirements-dev.txt\ntest:\n\tpython -m pytest tests\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "requirements-dev.txt"), "-e .[socks]\npytest>=2.8.0,<10\npytest-httpbin==2.1.0\n")
	template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, setupImportCandidates(repo, ""))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseProjectConfig([]byte(template), "detected")
	if err != nil {
		t.Fatal(err)
	}
	check := cfg.Checks["test"]
	if check.Command != "make -f Makefile -- test" {
		t.Fatalf("changed selected tests: %q", check.Command)
	}
	preparation := strings.Join(check.Prepare, "\n")
	for _, want := range []string{"python3 -m venv", "mktemp -d", "export VIRTUAL_ENV", "export PATH", "python -m pip install -r requirements-dev.txt"} {
		if !strings.Contains(preparation, want) {
			t.Fatalf("missing preparation %q:\n%s", want, preparation)
		}
	}
	for _, want := range []string{"pypi.org", "files.pythonhosted.org", "~/.cache/pip"} {
		if !strings.Contains(template, want) {
			t.Fatalf("missing declared dependency support %q:\n%s", want, template)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".venv")); !os.IsNotExist(err) {
		t.Fatalf("detection executed dependency preparation: %v", err)
	}
}

func TestSetupMakeSymlinkPreparationUsesLogicalDirectory(t *testing.T) {
	for _, command := range []string{"make -f Makefile -- test", "make test"} {
		t.Run(command, func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, "build", "entry.mk"), "init:\n\tpython -m pip install -r requirements-dev.txt\ntest:\n\tpython -m pytest tests\n")
			writeFileForRepoMapTest(t, filepath.Join(repo, "requirements-dev.txt"), "pytest\n")
			writeFileForRepoMapTest(t, filepath.Join(repo, ".python-version"), "3.12\n")
			writeFileForRepoMapTest(t, filepath.Join(repo, "build", ".python-version"), "3.11\n")
			if err := os.Symlink("build/entry.mk", filepath.Join(repo, "Makefile")); err != nil {
				t.Fatal(err)
			}
			check := setupPrepareDetectedCheck(repo, detectedCheck{Command: command})
			if check.Command != command || !containsString(check.Runtimes, "python") || check.RuntimeRequirements["python"] != "3.12" {
				t.Fatalf("logical Make runtime scope was lost: %+v", check)
			}
			if !containsString(check.Prepare, "python -m pip install -r requirements-dev.txt") {
				t.Fatalf("logical dependency preparation was lost: %v", check.Prepare)
			}
			if problems := setupDependencyReviewRequirements(repo, append([]string{command}, check.Prepare...)); len(problems) != 0 {
				t.Fatalf("internal Make source was rejected: %v", problems)
			}
		})
	}
}

func TestSetupMakeSymlinkCannotBypassHigherPrioritySource(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "test:\n\tgo test ./...\n")
	external := filepath.Join(t.TempDir(), "outside.mk")
	writeFileForRepoMapTest(t, external, "test:\n\tpython -m pytest private-tests\n")
	if err := os.Symlink(external, filepath.Join(repo, "GNUmakefile")); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"make test", "make -f GNUmakefile -- test"} {
		selections, problems := setupInspectMakeSelections(repo, command)
		if len(selections) != 0 || len(problems) == 0 {
			t.Fatalf("uninspectable source selected a different Makefile for %q: selections=%+v problems=%v", command, selections, problems)
		}
	}
}

func TestSetupPythonPreparationRunsWithNoSystemPythonAlias(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "init:\n\tpython -m pip install -r requirements-dev.txt\ntest:\n\tpython -m unittest discover -s tests\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "requirements-dev.txt"), "")
	writeFileForRepoMapTest(t, filepath.Join(repo, "tests/test_preparation.py"), "import os, sys, unittest\nclass Prepared(unittest.TestCase):\n    def test_venv(self):\n        self.assertNotEqual(sys.prefix, sys.base_prefix)\n        self.assertEqual(sys.prefix, os.environ['VIRTUAL_ENV'])\n")
	path := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bash", "make", "mktemp", "python3"} {
		executable, err := exec.LookPath(name)
		if err != nil {
			t.Skip(err)
		}
		if name == "python3" {
			// Keep the interpreter's real prefix when it is relocated (as in
			// actions/setup-python), while exposing no system "python" alias.
			launcher := filepath.Join(path, name)
			writeFileForRepoMapTest(t, launcher, "#!/bin/sh\nexec "+shellQuoteArg(executable)+" \"$@\"\n")
			if err := os.Chmod(launcher, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.Symlink(executable, filepath.Join(path, name)); err != nil {
			t.Fatal(err)
		}
	}
	run := func(command string) ([]byte, error) {
		cmd := exec.Command(filepath.Join(path, "bash"), "-c", command)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "PATH="+path, "PIP_NO_INDEX=1", "PIP_DISABLE_PIP_VERSION_CHECK=1", "TMPDIR="+t.TempDir())
		return cmd.CombinedOutput()
	}
	if output, err := run("make -f Makefile -- test"); err == nil || !strings.Contains(string(output), "python: No such file or directory") {
		t.Fatalf("missing-python reproduction failed: %v\n%s", err, output)
	}
	check := setupPrepareDetectedCheck(repo, detectedCheck{Command: "make -f Makefile -- test"})
	command, err := verificationplan.CommandForRunSpec(verificationplan.RunSpec{Command: check.Command, PrepareCommands: check.Prepare})
	if err != nil {
		t.Fatal(err)
	}
	if output, err := run(command); err != nil || !strings.Contains(string(output), "Ran 1 test") || !strings.Contains(string(output), "OK") {
		t.Fatalf("prepared upstream command failed: %v\n%s", err, output)
	}
}

func TestSetupPythonDependenciesRejectUnsafeOrAmbiguousDeclarations(t *testing.T) {
	for _, tc := range []struct{ name, setup string }{
		{"other setup action", "init:\n\tpython -m pip install -r requirements-dev.txt\n\t./custom-bootstrap\n"},
		{"prerequisite", "init: download\n\tpython -m pip install -r requirements-dev.txt\n"},
		{"conditional", "ifdef ENABLE\ninit:\n\tpython -m pip install -r requirements-dev.txt\nendif\n"},
		{"multiple setup targets", "init:\n\tpython -m pip install -r requirements-dev.txt\nbootstrap:\n\tpython -m pip install -r requirements-dev.txt\n"},
		{"dynamic requirement", "init:\n\tpython -m pip install -r $(REQUIREMENTS)\n"},
		{"shell execution", "init:\n\tpython -m pip install -r requirements-dev.txt && touch detection-executed\n"},
		{"redirect", "init:\n\tpython -m pip install -r requirements-dev.txt >setup.log\n"},
		{"missing requirement", "init:\n\tpython -m pip install -r absent.txt\n"},
		{"outside requirement", "init:\n\tpython -m pip install -r ../requirements-dev.txt\n"},
		{"no declaration", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "PROBE := $(shell touch detection-executed)\n"+tc.setup+"test:\n\tpython -m pytest tests\n")
			writeFileForRepoMapTest(t, filepath.Join(repo, "requirements-dev.txt"), "pytest\n")
			check := setupPrepareDetectedCheck(repo, detectedCheck{Command: "make -f Makefile -- test"})
			if len(check.Prepare) != 1 {
				t.Fatalf("translated unsupported dependency setup: %v", check.Prepare)
			}
			if requirements := setupDependencyReviewRequirements(repo, []string{check.Command}); len(requirements) == 0 {
				t.Fatal("unsupported dependency setup omitted its review requirement")
			}
			if _, err := os.Stat(filepath.Join(repo, "detection-executed")); !os.IsNotExist(err) {
				t.Fatalf("detection executed repository code: %v", err)
			}
		})
	}
}

func TestSetupPythonDependenciesKeepSelectedMakefileAndDirectory(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "init:\n\t./wrong-setup\ntest:\n\t./wrong-tests\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "sub dir/Makefile"), "init:\n\tpip3 install --requirement 'requirements test.txt'\ntest:\n\tpython -m pytest tests\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "sub dir/requirements test.txt"), "pytest\n")
	command := "make -C 'sub dir' -f Makefile -- test"
	check := setupPrepareDetectedCheck(repo, detectedCheck{Command: command})
	if check.Command != command || len(check.Prepare) != 2 || check.Prepare[1] != "(cd 'sub dir' && python -m pip install -r 'requirements test.txt')" {
		t.Fatalf("selected source scope changed: %+v", check)
	}
	if requirements := setupDependencyReviewRequirements(repo, []string{command}); len(requirements) != 0 {
		t.Fatalf("wrong Makefile affected preparation: %v", requirements)
	}
}

func TestSetupPythonDependenciesDoNotFollowManifestSymlinks(t *testing.T) {
	repo := t.TempDir()
	outside := filepath.Join(t.TempDir(), "requirements-dev.txt")
	writeFileForRepoMapTest(t, outside, "pytest\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "init:\n\tpython -m pip install -r requirements-dev.txt\ntest:\n\tpython -m pytest tests\n")
	if err := os.Symlink(outside, filepath.Join(repo, "requirements-dev.txt")); err != nil {
		t.Fatal(err)
	}
	if requirements := setupDependencyReviewRequirements(repo, []string{"make -f Makefile -- test"}); len(requirements) == 0 {
		t.Fatal("symlinked dependency declaration was translated")
	}
}

func TestSetupRuntimeInferenceReadsOnlyTheSelectedMakeScope(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "test: prepare\n\tpython -m unittest discover\nprepare:\n\tpython3 -m venv .venv\nloop: loop\n\tmake loop\nunrelated:\n\tgo test ./...\n")
	for _, command := range []string{"make -f Makefile -- test", "CI=true bash -e -c 'make -f Makefile -- test'", "CI=true bash --noprofile --norc -eo pipefail -c 'make -f Makefile -- test'"} {
		runtimes := setupCommandRuntimes(repo, command)
		if len(runtimes) != 1 || runtimes[0] != "python" {
			t.Fatalf("wrong runtime scope for %q: %v", command, runtimes)
		}
	}
	if runtimes := setupCommandRuntimes(repo, "make -f Makefile -- loop"); len(runtimes) != 0 {
		t.Fatalf("recursive Make generated runtime evidence: %v", runtimes)
	}
	if runtimes := setupCommandRuntimes(repo, "bash ignored-script.sh -c 'go test ./...'"); len(runtimes) != 0 {
		t.Fatalf("script arguments became commands: %v", runtimes)
	}
}

func TestSetupPythonDynamicPrerequisitesRequireExplicitPreparation(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "test: $(REQUIRED)\n\tpython -m pytest tests\n")
	if requirements := setupDependencyReviewRequirements(repo, []string{"make -f Makefile -- test"}); len(requirements) == 0 {
		t.Fatal("dynamic prerequisites hid the Python preparation requirement")
	}
}

func TestSetupDependencyInferencePreservesExplicitPreparation(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "init:\n\t./custom-bootstrap\ntest:\n\tpython -m pytest tests\n")
	for _, commands := range [][]string{
		{"make -f Makefile -- test", "scripts/prepare-tests.sh"},
		{"make -f Makefile -- test", setupPythonVenvPreparation, "python -m pip install -r requirements-ci.txt"},
		{"CI=true bash -e -c 'python -m pip install -r requirements-ci.txt' && CI=true bash -e -c 'make -f Makefile -- test'"},
	} {
		if requirements := setupDependencyReviewRequirements(repo, commands, commands[1:]...); len(requirements) != 0 {
			t.Fatalf("inferred setup overrode explicit preparation: %v", requirements)
		}
	}
}

func TestSetupStructuredStepsDoNotBypassDependencyReview(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "init:\n\t./custom-bootstrap\ntest:\n\tpython -m pytest tests\n")
	for _, template := range []string{
		"version: 1\nchecks:\n  test:\n    steps:\n      - command: echo prerequisite\n      - command: make test\n",
		"version: 1\nchecks:\n  test:\n    prepare: [scripts/prepare-tests.sh]\n    steps:\n      - command: make -n test\n",
		"version: 1\nchecks:\n  test:\n    steps:\n      - command: python -m pip install pytest\n      - command: make -n test\n",
	} {
		if err := validateSetupRuntimeTemplate(repo, template); err == nil {
			t.Fatalf("execution steps or preparation bypassed dependency review: %s", template)
		}
	}
}

func TestSetupUnsupportedDependencyPreparationDoesNotWriteConfiguration(t *testing.T) {
	for _, candidate := range []string{"", "make:Makefile:test"} {
		t.Run(defaultString(candidate, "automatic"), func(t *testing.T) {
			repo := chdirCLITestRepo(t, "unsupported-setup")
			writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "init:\n\t./custom-bootstrap\ntest:\n\tpython -m pytest tests\n")
			args := []string{"--write-config", "--api-url", "http://127.0.0.1:1"}
			if candidate != "" {
				args = append(args, "--candidate", candidate)
			}
			_, err := captureStdoutForCLITest(t, func() error { return setupCommand(args) })
			if err == nil || !strings.Contains(err.Error(), "explicit dependency preparation") {
				t.Fatalf("unsupported setup was not rejected: %v", err)
			}
			if _, err := os.Stat(filepath.Join(repo, ".readyci.yml")); !os.IsNotExist(err) {
				t.Fatalf("unsupported setup wrote a runnable configuration: %v", err)
			}
		})
	}
}

func TestSetupPythonPreparationRejectsTabbedConditionalAndAmbiguousRules(t *testing.T) {
	for _, tc := range []struct{ name, makefile string }{
		{"tabbed conditional", "ifdef\tENABLE\ninit:\n\tpython -m pip install -r requirements-dev.txt\nendif\ntest:\n\tpython -m pytest tests\n"},
		{"dynamic nested prerequisite", "init:\n\tpython -m pip install -r requirements-dev.txt\ntest: unit\nunit: $(EXTRA)\n\tpython -m pytest tests\n"},
		{"included override", "init:\n\tpython -m pip install -r requirements-dev.txt\ninclude setup.mk\ntest:\n\tpython -m pytest tests\n"},
		{"evaluated rule", "init:\n\tpython -m pip install -r requirements-dev.txt\n$(eval init: ; ./custom-bootstrap)\ntest:\n\tpython -m pytest tests\n"},
		{"custom recipe prefix", ".RECIPEPREFIX := >\ninit:\n>python -m pip install -r requirements-dev.txt\ntest:\n>python -m pytest tests\n"},
		{"override recipe prefix", "override .RECIPEPREFIX := >\ninit:\n>python -m pip install -r requirements-dev.txt\ntest:\n>python -m pytest tests\n"},
		{"export recipe prefix", "export .RECIPEPREFIX := >\ninit:\n>python -m pip install -r requirements-dev.txt\ntest:\n>python -m pytest tests\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), tc.makefile)
			writeFileForRepoMapTest(t, filepath.Join(repo, "requirements-dev.txt"), "pytest\n")
			writeFileForRepoMapTest(t, filepath.Join(repo, "setup.mk"), "init:\n\t./custom-bootstrap\n")
			if template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, setupImportCandidates(repo, "")); err == nil || template != "" {
				t.Fatalf("ambiguous Make preparation was accepted: %q %v", template, err)
			}
		})
	}
}

func TestSetupMakeJobsFlagsPreservePythonPreparationAndCommand(t *testing.T) {
	for _, command := range []string{"make -j2 test", "make -j 2 test", "make --jobs=2 test", "make --jobs 2 test", "make -j test"} {
		t.Run(command, func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "init:\n\tpython -m pip install -r requirements-dev.txt\ntest:\n\tpython -m pytest tests\n")
			writeFileForRepoMapTest(t, filepath.Join(repo, "requirements-dev.txt"), "pytest\n")
			writeFileForRepoMapTest(t, filepath.Join(repo, ".python-version"), "3.12\n")
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - run: "+command+"\n")
			template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, setupImportCandidates(repo, ""))
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := config.ParseProjectConfig([]byte(template), "generated")
			if err != nil {
				t.Fatal(err)
			}
			check := cfg.Checks["tests"]
			if check.Command != "" || len(check.Steps) != 1 || check.Steps[0].Command != "bash -e -c "+shellQuoteArg(command) || check.Steps[0].Env["CI"] != "true" || len(check.Prepare) != 2 || check.RuntimeRequirements["python"] != "3.12" || !strings.Contains(template, "pypi.org") {
				t.Fatalf("Make jobs flag hid runtime or dependencies: %+v\n%s", check, template)
			}
		})
	}
}

func TestSetupUninspectableMakeArgumentsRequireExplicitConfiguration(t *testing.T) {
	for _, command := range []string{"make CI=true test", "make --eval='test:;echo wrong' test", "make -n test", "make -I custom test", "make -j\"$JOBS\" test", "MAKEFILES=setup.mk make test", "env MAKEFILES=setup.mk make test", "MAKEFLAGS=-n make test", "export MAKEFLAGS=-n; make test", "MAKEFLAGS=-n bash -c 'make test'"} {
		t.Run(command, func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "init:\n\tpython -m pip install -r requirements-dev.txt\ntest:\n\tpython -m pytest tests\n")
			writeFileForRepoMapTest(t, filepath.Join(repo, "requirements-dev.txt"), "pytest\n")
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - run: "+command+"\n")
			// Either explicit configuration is required, or the repository's own
			// Make target is proposed without importing the upstream invocation.
			template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, setupImportCandidates(repo, ""))
			if err == nil && (strings.Contains(template, shellQuoteArg(command)) || strings.Contains(template, "# Selected:") || !strings.Contains(template, `# Not imported: "github-actions:.github/workflows/ci.yml:tests"`)) {
				t.Fatalf("uninspectable Make invocation was accepted:\n%s", template)
			}
		})
	}
}
