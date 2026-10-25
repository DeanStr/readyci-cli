package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func readyCIAgentSkillSetupSection(t *testing.T) string {
	t.Helper()
	skill := readyCIAgentSkill()
	start, end := strings.Index(skill, "## Set up a repository"), strings.Index(skill, "## Run verification")
	if start < 0 || end < start {
		t.Fatalf("skill lost its setup protocol:\n%s", skill)
	}
	return skill[start:end]
}

func setupProtocolJSONFields(types ...reflect.Type) map[string]bool {
	fields := map[string]bool{}
	for _, typ := range types {
		for i := 0; i < typ.NumField(); i++ {
			if name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ","); name != "" && name != "-" {
				fields[name] = true
			}
		}
	}
	return fields
}

// The skill is executable guidance: every report field it names must exist,
// and its offline setup commands must run exactly as written.
func TestAgentSkillSetupProtocolMatchesTheCLI(t *testing.T) {
	section := readyCIAgentSkillSetupSection(t)
	for _, want := range []string{
		"readyci setup --preview --json", "readyci setup --candidate ID --write-config", "--force",
		"readyci config validate", "readyci check --dry-run --json CHECK", "readyci check --json CHECK",
		"Prove that tests ran", "readyci runs tests --json RUN_ID", "Never skip or delete tests",
		"readyci project policy propose --file .readyci.yml", "uncommitted",
	} {
		if !strings.Contains(section, want) {
			t.Fatalf("setup protocol lacks %q:\n%s", want, section)
		}
	}
	if !strings.Contains(readyCIAgentSkill(), "reports configuration required") {
		t.Fatal("skill description does not trigger on configuration required")
	}
	fields := setupProtocolJSONFields(reflect.TypeOf(setupReport{}), reflect.TypeOf(repoImportCandidate{}))
	for _, name := range []string{"selection_status", "selection_reason", "warnings", "import_scope", "proposed_config", "import_candidates", "id", "command", "unsupported", "automatic_unsupported"} {
		if !fields[name] || !strings.Contains(section, "`"+name+"`") {
			t.Fatalf("setup protocol field %q is not a setup report field or is not named", name)
		}
	}

	chdirCLITestRepo(t, "agent-setup")
	runGitForCLITest(t, "init", "-q")
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	t.Setenv("READYCI_API_TOKEN", "")
	writeFileForRepoMapTest(t, "go.mod", "module example.test/fixture\n\ngo 1.24\n")
	writeFileForRepoMapTest(t, ".github/workflows/ci.yml", "jobs:\n  lint:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: golangci/golangci-lint-action@v6\n  test:\n    runs-on: ubuntu-latest\n    strategy:\n      matrix:\n        go: ['1.24', '1.25']\n    steps:\n      - uses: actions/setup-go@v5\n        with:\n          go-version: ${{ matrix.go }}\n      - run: go test ./...\n")
	preview := func(args ...string) setupReport {
		t.Helper()
		output, err := captureStdoutForCLITest(t, func() error {
			return setupCommand(append([]string{"--preview", "--json", "--project", "fixture"}, args...))
		})
		if err != nil {
			t.Fatal(err)
		}
		var report setupReport
		if err := json.Unmarshal([]byte(output), &report); err != nil {
			t.Fatalf("decode setup preview: %v\n%s", err, output)
		}
		return report
	}
	report := preview()
	if report.SelectionStatus != "selected" || !strings.Contains(report.SelectionReason, "not imported") || report.ProposedConfig == "" {
		t.Fatalf("preview did not expose the language default and its gaps: %+v", report)
	}
	variant := ""
	for _, candidate := range report.ImportCandidates {
		if candidate.ParentJobID != "" && len(candidate.Unsupported) == 0 {
			variant = candidate.ID
			break
		}
	}
	if variant == "" {
		t.Fatalf("preview listed no importable matrix variant: %+v", report.ImportCandidates)
	}
	run := func(command func([]string) error, args ...string) string {
		t.Helper()
		output, err := captureStdoutForCLITest(t, func() error { return command(args) })
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, output)
		}
		return output
	}
	run(setupCommand, "--write-config", "--project", "fixture")
	if replaced := preview("--force"); len(replaced.ImportCandidates) == 0 {
		t.Fatal("--force preview did not relist candidates for an existing configuration")
	}
	run(setupCommand, "--candidate", variant, "--write-config", "--force", "--project", "fixture")
	saved, err := os.ReadFile(".readyci.yml")
	if err != nil || !strings.Contains(string(saved), "# Selected: \""+variant+"\"") {
		t.Fatalf("explicit variant import was not saved: %v\n%s", err, saved)
	}
	if output := run(configCommand, "validate"); !strings.Contains(output, "status: ok") {
		t.Fatalf("saved configuration did not validate:\n%s", output)
	}
	var dryRun struct {
		Check string `json:"check"`
	}
	if output := run(checkCommand, "--dry-run", "--json", "test"); json.Unmarshal([]byte(output), &dryRun) != nil || dryRun.Check != "test" {
		t.Fatalf("dry run did not resolve the imported check:\n%s", output)
	}
}

// Every install target carries the same setup protocol, so agents follow one
// contract whether they load a skill, AGENTS.md, Codex instructions, or a shell
// snippet.
func TestAgentInstructionTargetsShareTheSetupProtocol(t *testing.T) {
	body := strings.TrimPrefix(readyCIRepositorySetupSection("HEADING"), "\nHEADING\n")
	if !strings.Contains(body, "Prove that tests ran") {
		t.Fatalf("setup protocol body is empty or changed shape:\n%s", body)
	}
	for name, template := range map[string]string{
		"skill":         readyCIAgentSkill(),
		"agents-md":     readyCIAgentsSnippet(),
		"codex":         readyCICodexSnippet(),
		"generic-shell": readyCIGenericShellSnippet(),
	} {
		if strings.Count(template, body) != 1 {
			t.Fatalf("%s target does not carry the setup protocol exactly once", name)
		}
		if tokenLeakLike(template) {
			t.Fatalf("%s target looks like it contains a token value", name)
		}
	}
}
