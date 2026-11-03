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

// Setup does not evaluate Makefiles to infer runtimes or dependency setup. A
// selected Make target is an opaque harness, so includes, evaluated rules,
// custom recipe prefixes and setup targets neither change nor block it.
func TestSetupTreatsMakeRecipesAsOpaque(t *testing.T) {
	for _, tc := range []struct{ name, makefile string }{
		{"python dependency target", "init:\n\tpython -m pip install -r requirements-dev.txt\ntest:\n\tpython -m pytest tests\n"},
		{"unsupported setup target", "init:\n\t./custom-bootstrap\ntest:\n\tpython -m pytest tests\n"},
		{"dynamic prerequisite", "test: $(REQUIRED)\n\tpython -m pytest tests\n"},
		{"included rules", "include setup.mk\ntest:\n\tpython -m pytest tests\n"},
		{"evaluated rule", "$(eval init: ; ./custom-bootstrap)\ntest:\n\tpython -m pytest tests\n"},
		{"custom recipe prefix", ".RECIPEPREFIX := >\ntest:\n>python -m pytest tests\n"},
		{"gradle", "test:\n\t./gradlew test\n"},
		{"maven", "test:\n\tmvn -B test\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "PROBE := $(shell touch detection-executed)\n"+tc.makefile)
			writeFileForRepoMapTest(t, filepath.Join(repo, "requirements-dev.txt"), "pytest\n")
			writeFileForRepoMapTest(t, filepath.Join(repo, "setup.mk"), "init:\n\t./custom-bootstrap\n")
			template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, setupImportCandidates(repo, ""))
			if err != nil {
				t.Fatalf("opaque Make target blocked setup: %v", err)
			}
			cfg, err := config.ParseProjectConfig([]byte(template), "detected")
			if err != nil {
				t.Fatal(err)
			}
			check := cfg.Checks["test"]
			if check.Command != "make -f Makefile -- test" || len(check.Prepare) != 0 || len(check.RuntimeRequirements) != 0 || strings.Contains(template, "pypi.org") {
				t.Fatalf("Make recipes were evaluated for runtime or dependency setup: %+v\n%s", check, template)
			}
			if _, err := os.Stat(filepath.Join(repo, "detection-executed")); !os.IsNotExist(err) {
				t.Fatalf("detection executed repository code: %v", err)
			}
		})
	}
}

func TestSetupPythonDependencyPreparationIsAReviewNote(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "requirements-dev.txt"), "pytest\n")
	check := setupPrepareDetectedCheck(repo, detectedCheck{Command: "python -m pytest tests"})
	if len(check.Prepare) != 1 || check.Prepare[0] != setupPythonVenvPreparation {
		t.Fatalf("setup guessed dependency preparation: %v", check.Prepare)
	}
	if review := strings.Join(setupUnresolvedNotes(check.Unresolved), ";"); !strings.Contains(review, "Preparation review: Python tests") || !strings.Contains(review, "requirements-dev.txt") {
		t.Fatalf("missing Python dependency preparation was not reported: %v", check.Unresolved)
	}
	if len(check.Unresolved) != 1 || check.Unresolved[0].Kind != setupUnresolvedDependencyPreparationUnknown || check.Unresolved[0].Subject != "python" || check.Unresolved[0].Source != "requirements-dev.txt" {
		t.Fatalf("dependency preparation item lost its structure: %+v", check.Unresolved)
	}
	installed := setupPrepareDetectedCheck(repo, detectedCheck{Command: "python -m pip install -r requirements-dev.txt && python -m pytest tests"})
	if len(installed.Unresolved) != 0 {
		t.Fatalf("explicit dependency installation was reported as missing: %v", installed.Unresolved)
	}
	bare := setupPrepareDetectedCheck(t.TempDir(), detectedCheck{Command: "python -m pytest tests"})
	if len(bare.Unresolved) != 0 {
		t.Fatalf("a repository without Python manifests was reported: %v", bare.Unresolved)
	}
	// Explicit preparation or a dependency check is the repository's choice.
	if items := setupPythonDependencyReview(repo, []string{"python -m pytest tests"}, []string{setupPythonVenvPreparation, "make deps"}, false); len(items) != 0 {
		t.Fatalf("explicit preparation was reported as missing: %v", items)
	}
	if items := setupPythonDependencyReview(repo, []string{"python -m pytest tests"}, []string{setupPythonVenvPreparation}, true); len(items) != 0 {
		t.Fatalf("a dependency check was reported as missing preparation: %v", items)
	}
}

func TestSetupPythonPreparationRunsWithNoSystemPythonAlias(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "test:\n\tpython -m unittest discover -s tests\n")
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
	// A setup-python workflow selects Python for an opaque Make target.
	check := setupPrepareDetectedCheck(repo, detectedCheck{Command: "make -f Makefile -- test", Runtimes: []string{"python"}})
	command, err := verificationplan.CommandForRunSpec(verificationplan.RunSpec{Command: check.Command, PrepareCommands: check.Prepare})
	if err != nil {
		t.Fatal(err)
	}
	if output, err := run(command); err != nil || !strings.Contains(string(output), "Ran 1 test") || !strings.Contains(string(output), "OK") {
		t.Fatalf("prepared upstream command failed: %v\n%s", err, output)
	}
}

func TestSetupRuntimeInferenceIgnoresScriptArguments(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "test:\n\tpython -m unittest discover\n")
	for _, command := range []string{"make -f Makefile -- test", "CI=true bash -e -c 'make -f Makefile -- test'"} {
		if runtimes := setupCommandRuntimes(repo, command); len(runtimes) != 0 {
			t.Fatalf("Make recipe generated runtime evidence for %q: %v", command, runtimes)
		}
	}
	if runtimes := setupCommandRuntimes(repo, "CI=true bash --noprofile --norc -eo pipefail -c 'python -m pytest'"); len(runtimes) != 1 || runtimes[0] != "python" {
		t.Fatalf("literal wrapper payload lost its runtime: %v", runtimes)
	}
	if runtimes := setupCommandRuntimes(repo, "bash ignored-script.sh -c 'go test ./...'"); len(runtimes) != 0 {
		t.Fatalf("script arguments became commands: %v", runtimes)
	}
}

func TestSetupMissingPythonPreparationDoesNotBlockConfiguration(t *testing.T) {
	for _, candidate := range []string{"", "make:Makefile:test"} {
		t.Run(defaultString(candidate, "automatic"), func(t *testing.T) {
			repo := chdirCLITestRepo(t, "unsupported-setup")
			t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
			t.Setenv("READYCI_API_TOKEN", "")
			writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "init:\n\t./custom-bootstrap\ntest:\n\tpython -m pytest tests\n")
			writeFileForRepoMapTest(t, filepath.Join(repo, "requirements-dev.txt"), "pytest\n")
			args := []string{"--write-config", "--api-url", "http://127.0.0.1:1"}
			if candidate != "" {
				args = append(args, "--candidate", candidate)
			}
			if _, err := captureStdoutForCLITest(t, func() error { return setupCommand(args) }); err != nil {
				t.Fatalf("setup refused a Make test target without inferred preparation: %v", err)
			}
			saved, err := os.ReadFile(filepath.Join(repo, ".readyci.yml"))
			if err != nil || !strings.Contains(string(saved), `make -f Makefile -- test`) {
				t.Fatalf("configuration was not written: %v\n%s", err, saved)
			}
		})
	}
}

func TestSetupMakeDryRunIsNotATestEntrypoint(t *testing.T) {
	for _, command := range []string{"make -n test", "make --dry-run test", "make -q test", "make -kn test", "make --touch test"} {
		if setupCommandRunsTests(command) {
			t.Fatalf("%q runs no recipes but was treated as a test entrypoint", command)
		}
	}
	for _, command := range []string{"make test", "make -j2 test", "make -C tests test", "make -k test"} {
		if !setupCommandRunsTests(command) {
			t.Fatalf("%q was not treated as a test entrypoint", command)
		}
	}
}

func TestSetupImportedMakeJobUsesWorkflowRuntimeOnly(t *testing.T) {
	for _, command := range []string{"make -j2 test", "make -j 2 test", "make --jobs=2 test", "make --jobs 2 test", "make -j test"} {
		t.Run(command, func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "init:\n\tpython -m pip install -r requirements-dev.txt\ntest:\n\tpython -m pytest tests\n")
			writeFileForRepoMapTest(t, filepath.Join(repo, "requirements-dev.txt"), "pytest\n")
			writeFileForRepoMapTest(t, filepath.Join(repo, "pyproject.toml"), "[project]\nrequires-python = '>=3.10'\n")
			writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-python@v5\n        with:\n          python-version: '3.12'\n      - run: "+command+"\n")
			template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, setupImportCandidates(repo, ""))
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := config.ParseProjectConfig([]byte(template), "generated")
			if err != nil {
				t.Fatal(err)
			}
			check := cfg.Checks["tests"]
			if len(check.Steps) != 1 || check.Steps[0].Command != "bash -e -c "+shellQuoteArg(command) || len(check.Prepare) != 1 || check.RuntimeRequirements["python"] != "3.12,>=3.10" || !strings.Contains(template, "Preparation review: Python tests") {
				t.Fatalf("Make invocation changed the workflow runtime or preparation: %+v\n%s", check, template)
			}
		})
	}
}
