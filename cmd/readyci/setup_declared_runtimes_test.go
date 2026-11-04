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
	}{{"go test ./...", map[string]string{"go": ">=1.24,>=1.25.0"}}, {"npm test", map[string]string{"node": "24.18.0,>=22.12", "pnpm": "11.15.1", "npm": "installed"}}, {"python -m pytest tests", map[string]string{"python": ">=3.9,<4"}}}
	for _, test := range cases {
		got, notes := setupDeclaredRuntimeRequirements(root, []string{test.command})
		if len(notes) != 0 {
			t.Fatal(notes)
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
	if _, notes := setupDeclaredRuntimeRequirements(root, []string{"npm test"}); len(notes) == 0 || !strings.Contains(strings.Join(notes, "; "), ".nvmrc") {
		t.Fatalf("moving alias accepted: %v", notes)
	}
	if requirements, notes := setupDeclaredRuntimeRequirements(root, []string{"go test ./..."}); len(notes) != 0 || len(requirements) != 1 || requirements["go"] != "installed" {
		t.Fatalf("unrelated Node declaration constrained Go: %v %v", requirements, notes)
	}
	writeFileForRepoMapTest(t, filepath.Join(root, "sub/Makefile"), "test:\n\tpython -m pytest tests\n")
	writeFileForRepoMapTest(t, filepath.Join(root, "sub/pyproject.toml"), "[project]\nrequires-python = '>=3.11'\n")
	// Make is an opaque harness in its -C working directory: a selected
	// runtime (for example from setup-python) reads that directory's declarations.
	got, notes := setupDeclaredRuntimeRequirements(root, []string{"make -C sub -f Makefile -- test"}, "python")
	if len(notes) != 0 || got["python"] != ">=3.11" {
		t.Fatalf("selected Make directory declaration lost: %v %v", got, notes)
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
		requirements, notes := setupDeclaredRuntimeRequirements(root, []string{"python3 -m pytest"})
		if (len(notes) != 0) != test.bad {
			t.Fatalf("source%q requirements%v error%v", test.source, requirements, notes)
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
	got, notes := setupDeclaredRuntimeRequirements(root, []string{"go test ./..."})
	if len(notes) != 0 || got["go"] != ">=1.26.0,>=1.26.1" {
		t.Fatalf("commented Go declarations lost or unrelated Node applied: %v %v", got, notes)
	}
	writeFileForRepoMapTest(t, filepath.Join(root, "pyproject.toml"), "[project]\nrequires-python = '>=3.9,!=3.12.0'\n")
	got, notes = setupDeclaredRuntimeRequirements(root, []string{"python3 -m pytest"})
	if len(notes) != 0 || got["python"] != ">=3.9,!=3.12.0" {
		t.Fatalf("Python exclusion lost: %v %v", got, notes)
	}
}
func TestSetupSelectedUnsupportedRuntimeDeclarationsDoNotDisappear(t *testing.T) {
	for _, test := range []struct{ command, path, value string }{{"bundle exec rake test", ".ruby-version", "3.4.0\n"}, {"dotnet test", "global.json", `{"sdk":{"version":"9.0.100"}}`}} {
		root := t.TempDir()
		writeFileForRepoMapTest(t, filepath.Join(root, test.path), test.value)
		requirements, notes := setupDeclaredRuntimeRequirements(root, []string{test.command})
		if len(requirements) != 0 || len(notes) != 1 || !strings.Contains(notes[0], test.path) || !strings.Contains(notes[0], "not a supported runtime requirement family") {
			t.Fatalf("selected unsupported runtime declaration vanished or was emitted for %s: %v %v", test.command, requirements, notes)
		}
		if got, notes := setupDeclaredRuntimeRequirements(root, []string{"go test ./..."}); len(notes) != 0 || len(got) != 1 || got["go"] != "installed" {
			t.Fatalf("unrelated%s metadata constrained Go: %v %v", test.path, got, notes)
		}
	}
}

func TestSetupPackageManagerIntegrityIsNotSilentlyDiscarded(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "package.json"), `{"packageManager":"pnpm@11.15.1+sha512.abcd"}`)
	if _, notes := setupDeclaredRuntimeRequirements(root, []string{"pnpm test"}); len(notes) == 0 || !strings.Contains(strings.Join(notes, "; "), "integrity") {
		t.Fatalf("unverified package manager integrity was discarded: %v", notes)
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
		if _, notes := setupDeclaredRuntimeRequirements(root, []string{"node --test"}); len(notes) == 0 {
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
		requirements, notes := setupDeclaredRuntimeRequirements(root, []string{test.command})
		if len(notes) != 0 || requirements[test.runtime] != test.want {
			t.Fatalf("source%q requirements%v notes%v", test.body, requirements, notes)
		}
	}
}

func TestSetupGoVersionFilePreservesExactToolchainAlongsideModuleMinimum(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, "go.mod"), "module example.com/tests\ngo 1.27.0\n")
	writeFileForRepoMapTest(t, filepath.Join(root, ".go-version"), "1.27.2\n")
	requirements, notes := setupDeclaredRuntimeRequirements(root, []string{"go test ./..."})
	if len(notes) != 0 {
		t.Fatal(notes)
	}
	if !preflightapi.RuntimeVersionSatisfies("1.27.2", requirements["go"]) || preflightapi.RuntimeVersionSatisfies("1.27.3", requirements["go"]) || preflightapi.RuntimeVersionSatisfies("1.27.1", requirements["go"]) {
		t.Fatalf("exact .go-version constraint disappeared %v", requirements)
	}
}

func TestSetupJavaVersionDeclarationIsARuntimeRequirement(t *testing.T) {
	root := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(root, ".java-version"), "21\n")
	if requirements, notes := setupDeclaredRuntimeRequirements(root, []string{"./mvnw -B verify"}); len(notes) != 0 || requirements["java"] != "21" {
		t.Fatalf(".java-version was not applied to a Maven command: %v %v", requirements, notes)
	}
	if requirements, notes := setupDeclaredRuntimeRequirements(t.TempDir(), []string{"gradle test"}); len(notes) != 0 || requirements["java"] != "installed" {
		t.Fatalf("a Gradle command without a declaration did not require the installed Java: %v %v", requirements, notes)
	}
}
