package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func TestSetupNativeSelectionDoesNotBypassConditionalRootMakeTests(t *testing.T) {
	for _, tc := range []struct {
		file, target string
		linked       bool
	}{
		{file: "Makefile", target: "test"},
		{file: "makefile", target: "check"},
		{file: "GNUmakefile", target: "check-ci"},
		{file: "Makefile", target: "test", linked: true},
	} {
		name := tc.file + ":" + tc.target
		if tc.linked {
			name += "/internal-symlink"
		}
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, "go.mod"), "module example.test/fixture\n")
			marker := filepath.Join(repo, "must-not-execute")
			contents := "PROBE := $(shell touch " + marker + ")\nifeq ($(PRINT_HELP),y)\n" + tc.target + ":\n\t@echo help\nelse\n" + tc.target + ":\n\thack/run-tests.sh $(WHAT) $(TESTS)\nendif\n"
			if tc.linked {
				writeFileForRepoMapTest(t, filepath.Join(repo, "build", "root", "entry.mk"), contents)
				if err := os.Symlink("build/root/entry.mk", filepath.Join(repo, tc.file)); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFileForRepoMapTest(t, filepath.Join(repo, tc.file), contents)
			}
			candidates := setupImportCandidates(repo, "")
			wantID := "make:" + tc.file + ":" + tc.target
			found := false
			for _, candidate := range candidates {
				found = found || candidate.ID == wantID && len(candidate.Unsupported) > 0
			}
			if !found {
				t.Fatalf("fixture lost its unsupported logical root entrypoint: %+v", candidates)
			}
			template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, candidates)
			if err == nil || template != "" || !strings.Contains(err.Error(), "configuration required") || !strings.Contains(err.Error(), wantID) {
				t.Fatalf("conditional root scope was bypassed: template=%q err=%v", template, err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("selection evaluated repository Make expressions: %v", err)
			}
		})
	}
}

func TestSetupNativeSelectionConditionalMakeFailsBeforeWriting(t *testing.T) {
	chdirCLITestRepo(t, "conditional-root-make")
	writeFileForRepoMapTest(t, "go.mod", "module example.test/fixture\n")
	writeFileForRepoMapTest(t, "Makefile", "ifdef ENABLE_TESTS\ntest:\n\thack/run-tests.sh\nendif\n")
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	t.Setenv("READYCI_API_TOKEN", "")
	_, err := buildSetupReport(setupOptions{APIURL: "http://127.0.0.1:1", ProjectID: "fixture", Detect: true, WriteConfig: true})
	if err == nil || !strings.Contains(err.Error(), "configuration required") || !strings.Contains(err.Error(), "make:Makefile:test") {
		t.Fatalf("conditional root selection: %v", err)
	}
	if got := normalizeCLIError(err); got.Code != "config_invalid" || got.ExitCode != 2 {
		t.Fatalf("conditional root refusal lost its configuration exit: %+v", got)
	}
	if _, err := os.Stat(".readyci.yml"); !os.IsNotExist(err) {
		t.Fatalf("unsupported root selection wrote a configuration: %v", err)
	}
}

func TestSetupNativeSelectionKeepsGoDefaultsWithoutConditionalRootTests(t *testing.T) {
	for _, tc := range []struct {
		name, rootMake, nestedMake string
	}{
		{name: "Go only"},
		{name: "nested tests", nestedMake: "ifdef ENABLE_TESTS\ntest:\n\thack/component-tests.sh\nendif\n"},
		{name: "unrelated root build", rootMake: "ifdef RELEASE\nall:\n\tgo build ./...\nendif\n", nestedMake: "test:\n\thack/component-tests.sh\n"},
		{name: "supported root tests", rootMake: "test:\n\tgo test ./pkg/unit/...\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, "go.mod"), "module example.test/fixture\n")
			if tc.rootMake != "" {
				writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), tc.rootMake)
			}
			if tc.nestedMake != "" {
				writeFileForRepoMapTest(t, filepath.Join(repo, "component", "Makefile"), tc.nestedMake)
			}
			template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, setupImportCandidates(repo, ""))
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := config.ParseProjectConfig([]byte(template), "detected")
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Checks["go"].Command != "go test ./..." {
				t.Fatalf("ordinary Go default changed: %+v", cfg.Checks)
			}
			if tc.name == "supported root tests" && cfg.Checks["test"].Command != "make -f Makefile -- test" {
				t.Fatalf("supported root entrypoint changed: %+v", cfg.Checks)
			}
		})
	}
}

func TestSetupNativeSelectionAcceptsInternalMakefileSymlink(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "go.mod"), "module example.test/fixture\ngo 1.25.0\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "build", "entry.mk"), "test:\n\tgo test ./pkg/unit/...\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "build", "go.mod"), "module example.test/unrelated\ngo 1.26.0\n")
	if err := os.Symlink("build/entry.mk", filepath.Join(repo, "Makefile")); err != nil {
		t.Fatal(err)
	}
	template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, setupImportCandidates(repo, ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSetupRuntimeTemplate(repo, template); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseProjectConfig([]byte(template), "detected")
	if err != nil {
		t.Fatal(err)
	}
	check := cfg.Checks["test"]
	if check.Command != "make -f Makefile -- test" || check.RuntimeRequirements["go"] != ">=1.25.0" {
		t.Fatalf("symlink changed the logical entrypoint or runtime scope: %+v", check)
	}
}

func TestSetupNativeSelectionCanUseAConcreteUpstreamJob(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "go.mod"), "module example.test/fixture\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), "ifdef ENABLE_TESTS\ntest:\n\thack/run-tests.sh\nendif\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), "jobs:\n  unit:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./pkg/unit/...\n")
	template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, setupImportCandidates(repo, ""))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(template, "go test ./pkg/unit/...") || strings.Contains(template, "go test ./...") {
		t.Fatalf("concrete upstream scope was replaced: %s", template)
	}
}

func TestSetupNativeSelectionRespectsMakeTargetPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, firstFile, firstSource, lowerSource string
		wantError                                 bool
	}{
		{name: "GNUmakefile shadows conditional Makefile", firstFile: "GNUmakefile", firstSource: "test:\n\tgo test ./...\n", lowerSource: "ifdef OTHER_PLATFORM\ntest:\n\thack/other-tests.sh\nendif\n"},
		{name: "makefile shadows conditional Makefile", firstFile: "makefile", firstSource: "test:\n\tgo test ./...\n", lowerSource: "ifdef OTHER_PLATFORM\ntest:\n\thack/other-tests.sh\nendif\n"},
		{name: "conditional first target cannot fall back", firstFile: "GNUmakefile", firstSource: "ifdef ENABLE_TESTS\ntest:\n\thack/tests.sh\nendif\n", lowerSource: "test:\n\tgo test ./...\n", wantError: true},
		{name: "unrelated first target cannot hide conditional tests", firstFile: "GNUmakefile", firstSource: "all:\n\tgo build ./...\n", lowerSource: "ifdef ENABLE_TESTS\ntest:\n\thack/tests.sh\nendif\n", wantError: true},
		{name: "conditional override in selected file", firstFile: "GNUmakefile", firstSource: "test:\n\tgo test ./...\nifdef OTHER_PLATFORM\ntest:\n\thack/other-tests.sh\nendif\n", lowerSource: "test:\n\tgo test ./...\n", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, "go.mod"), "module example.test/fixture\n")
			writeFileForRepoMapTest(t, filepath.Join(repo, tc.firstFile), tc.firstSource)
			writeFileForRepoMapTest(t, filepath.Join(repo, "Makefile"), tc.lowerSource)
			template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, setupImportCandidates(repo, ""))
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "defined conditionally") {
					t.Fatalf("conditional test scope was bypassed: template=%s err=%v", template, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := config.ParseProjectConfig([]byte(template), "detected")
			if err != nil {
				t.Fatal(err)
			}
			if got, want := cfg.Checks["test"].Command, "make -f "+tc.firstFile+" -- test"; got != want {
				t.Fatalf("selected Make command = %q, want %q", got, want)
			}
		})
	}
}
