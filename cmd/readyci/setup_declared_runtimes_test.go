package main

import (
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupDeclaredRuntimesPreserveSourceSemantics(t *testing.T) {
	root := t.TempDir()
	for path, value := range map[string]string{"go.mod": "module example.com/tests\ngo 1.24\ntoolchain go1.25.0\n", ".nvmrc": "24.18.0\n", "package.json": `{"engines":{"node":">=22.12"},"packageManager":"pnpm@11.15.1"}`, "pyproject.toml": "[project]\nrequires-python = '>=3.9,<4'\n", "Makefile": "init:\n\tpip install -r requirements-dev.txt\ntest:\n\tpython -m pytest tests\n", "requirements-dev.txt": "pytest\n"} {
		writeFileForRepoMapTest(t, filepath.Join(root, path), value)
	}
	cases := []struct {
		command string
		want    map[string]string
	}{{"go test ./...", map[string]string{"go": ">=1.24,>=1.25.0"}}, {"npm test", map[string]string{"node": "24.18.0,>=22.12", "pnpm": "11.15.1", "npm": "installed"}}, {"make -f Makefile -- test", map[string]string{"python": ">=3.9,<4"}}}
	for _, test := range cases {
		got, err := setupDeclaredRuntimeRequirements(root, []string{test.command})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(test.want) {
			t.Fatalf("%s got%v want%v", test.command, got, test.want)
		}
		for name, want := range test.want {
			if got[name] != want {
				t.Fatalf("%s runtime%s=%q want%q", test.command, name, got[name], want)
			}
		}
	}
}
func TestSetupDeclaredRuntimesRejectMovingVersionsAndMergeSelectedDirectory(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, ".nvmrc"), "lts/*\n")
	if _, err := setupDeclaredRuntimeRequirements(root, []string{"npm test"}); err == nil || !strings.Contains(err.Error(), ".nvmrc") {
		t.Fatalf("moving alias accepted: %v", err)
	}
	if requirements, err := setupDeclaredRuntimeRequirements(root, []string{"go test ./..."}); err != nil || len(requirements) != 1 || requirements["go"] != "installed" {
		t.Fatalf("unrelated Node declaration constrained Go: %v %v", requirements, err)
	}
	writeFileForRepoMapTest(t, filepath.Join(root, "sub/Makefile"), "test:\n\tpython -m pytest tests\n")
	writeFileForRepoMapTest(t, filepath.Join(root, "sub/pyproject.toml"), "[project]\nrequires-python = '>=3.11'\n")
	got, err := setupDeclaredRuntimeRequirements(root, []string{"make -C sub -f Makefile -- test"})
	if err != nil || !preflightapi.RuntimeVersionSatisfies("3.12.3", got["python"]) {
		t.Fatalf("selected Make directory declaration lost: %v %v", got, err)
	}
}

func TestSetupTOMLRuntimeDeclarationsUseActualTablesAndStrings(t *testing.T) {
	for _, test := range []struct {
		source string
		want   string
		bad    bool
	}{
		{"[project]\nrequires-python = \"\"\">=3.13\"\"\"\n", ">=3.13", false},
		{"[project]\nrequires-python = '''\n>=3.12,<4\n'''\n", ">=3.12,<4", false},
		{"[tool.example]\nrequires-python = '>=99'\n[project]\nname = 'test'\n", "installed", false},
		{"[project]\nrequires-python = 313\n", "", true},
		{"[project]\nrequires-python = \"\"\">=3.13\n", "", true},
		{"[project]\nrequires-python = '>=3.13' # comment\n", ">=3.13", false},
	} {
		root := t.TempDir()
		writeFileForRepoMapTest(t, filepath.Join(root, "pyproject.toml"), test.source)
		requirements, err := setupDeclaredRuntimeRequirements(root, []string{"python3 -m pytest"})
		if (err != nil) != test.bad {
			t.Fatalf("source%q requirements%v error%v", test.source, requirements, err)
		}
		if !test.bad && requirements["python"] != test.want {
			t.Fatalf("source%q got%q want%q", test.source, requirements["python"], test.want)
		}
	}
}

func TestSetupRuntimeDeclarationCommentsAndUnrelatedUnsafeMetadata(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "go.mod"), "module example.com/test\ngo 1.26.0 // minimum\ntoolchain go1.26.1 // preferred\n")
	writeFileForRepoMapTest(t, filepath.Join(root, ".nvmrc"), "$(touch NEVER_EXECUTE)\n")
	got, err := setupDeclaredRuntimeRequirements(root, []string{"go test ./..."})
	if err != nil || got["go"] != ">=1.26.0,>=1.26.1" {
		t.Fatalf("commented Go declarations lost or unrelated Node applied: %v %v", got, err)
	}
	writeFileForRepoMapTest(t, filepath.Join(root, "pyproject.toml"), "[project]\nrequires-python = '>=3.9,!=3.12.0'\n")
	got, err = setupDeclaredRuntimeRequirements(root, []string{"python3 -m pytest"})
	if err != nil || got["python"] != ">=3.9,!=3.12.0" {
		t.Fatalf("Python exclusion lost: %v %v", got, err)
	}
}
func TestSetupSelectedUnsupportedRuntimeDeclarationsDoNotDisappear(t *testing.T) {
	for _, test := range []struct{ command, path, value string }{{"bundle exec rake test", ".ruby-version", "3.4.0\n"}, {"mvn test", ".java-version", "21.0.0\n"}, {"dotnet test", "global.json", `{"sdk":{"version":"9.0.100"}}`}} {
		root := t.TempDir()
		writeFileForRepoMapTest(t, filepath.Join(root, test.path), test.value)
		if _, err := setupDeclaredRuntimeRequirements(root, []string{test.command}); err == nil || !strings.Contains(err.Error(), "unsupported runtime") {
			t.Fatalf("selected runtime declaration vanished for%s: %v", test.command, err)
		}
		if got, err := setupDeclaredRuntimeRequirements(root, []string{"go test ./..."}); err != nil || len(got) != 1 || got["go"] != "installed" {
			t.Fatalf("unrelated%s metadata constrained Go: %v %v", test.path, got, err)
		}
	}
}

func TestSetupPackageManagerIntegrityIsNotSilentlyDiscarded(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"packageManager":"pnpm@11.15.1+sha512.abcd"}`)
	if _, err := setupDeclaredRuntimeRequirements(root, []string{"pnpm test"}); err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("unverified package manager integrity was discarded: %v", err)
	}
}

func TestSetupRuntimeSymlinkAndEmptyDeclarationsFailInspection(t *testing.T) {
	for _, kind := range []string{"empty", "symlink"} {
		root := t.TempDir()
		if kind == "empty" {
			writeFileForRepoMapTest(t, filepath.Join(root, ".nvmrc"), "# no version\n")
		} else {
			writeFileForRepoMapTest(t, filepath.Join(root, "node-version.txt"), "22\n")
			if err := os.Symlink("node-version.txt", filepath.Join(root, ".nvmrc")); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := setupDeclaredRuntimeRequirements(root, []string{"node --test"}); err == nil {
			t.Fatalf("%s runtime declaration silently became installed-default", kind)
		}
	}
}

func TestSetupTOMLDescriptionsCannotCreateOrHideRuntimeDeclarations(t *testing.T) {
	cases := []struct{ path, body, command, runtime, want string }{
		{"pyproject.toml", "[project]\ndescription = '''\n[tool.example]\n'''\nrequires-python = '>=3.13'\n", "python3 -m pytest", "python", ">=3.13"},
		{"pyproject.toml", "[project]\ndescription = '''\nrequires-python = \">=99\"\n'''\nrequires-python = '>=3.10'\n", "python3 -m pytest", "python", ">=3.10"},
		{"pyproject.toml", "project . requires-python = '>=3.13'\n", "python3 -m pytest", "python", ">=3.13"},
		{"Cargo.toml", "[workspace.\"package\"]\nrust-version = '1.85'\n", "cargo test", "rust", ">=1.85"},
	}
	for _, test := range cases {
		root := t.TempDir()
		writeFileForRepoMapTest(t, filepath.Join(root, test.path), test.body)
		requirements, err := setupDeclaredRuntimeRequirements(root, []string{test.command})
		if err != nil || requirements[test.runtime] != test.want {
			t.Fatalf("source%q requirements%v err%v", test.body, requirements, err)
		}
	}
}

func TestSetupGoVersionFilePreservesExactToolchainAlongsideModuleMinimum(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "go.mod"), "module example.com/tests\ngo 1.27.0\n")
	writeFileForRepoMapTest(t, filepath.Join(root, ".go-version"), "1.27.2\n")
	requirements, err := setupDeclaredRuntimeRequirements(root, []string{"go test ./..."})
	if err != nil {
		t.Fatal(err)
	}
	if !preflightapi.RuntimeVersionSatisfies("1.27.2", requirements["go"]) || preflightapi.RuntimeVersionSatisfies("1.27.3", requirements["go"]) || preflightapi.RuntimeVersionSatisfies("1.27.1", requirements["go"]) {
		t.Fatalf("exact .go-version constraint disappeared %v", requirements)
	}
}
