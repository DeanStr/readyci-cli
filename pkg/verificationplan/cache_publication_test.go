//go:build unix

package verificationplan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

// Each invocation uses a fresh shell and workspace, sharing only the cache lane.
// These inert producers model installers that replace their output directory.
func TestCachePublicationReplacementSurvivesFreshConsumer(t *testing.T) {
	cache := t.TempDir()
	producer := RunSpec{
		Command:    "rm -rf packages; mkdir packages; printf prepared > packages/ready",
		CachePaths: []string{"packages"},
	}
	if output, code := runCachePublication(t, cache, t.TempDir(), producer); code != 0 {
		t.Fatalf("producer exit %d: %s", code, output)
	}
	consumer := RunSpec{
		Command:    `test "$(cat packages/ready)" = prepared`,
		CachePaths: []string{"packages"},
	}
	if output, code := runCachePublication(t, cache, t.TempDir(), consumer); code != 0 {
		t.Fatalf("fresh consumer lost replaced cache directory (exit %d): %s", code, output)
	}
}

func TestCachePublicationReplacesNonemptyBackingWithoutStaleFiles(t *testing.T) {
	cache := t.TempDir()
	for _, command := range []string{
		"printf old > packages/value; printf stale > packages/removed",
		"rm -rf packages; mkdir packages; printf new > packages/value; mkdir packages/nested; printf nested > packages/nested/ready",
	} {
		if output, code := runCachePublication(t, cache, t.TempDir(), RunSpec{Command: command, CachePaths: []string{"packages"}}); code != 0 {
			t.Fatalf("producer exit %d: %s", code, output)
		}
	}
	consumer := RunSpec{
		Command:    `test "$(cat packages/value)" = new && test "$(cat packages/nested/ready)" = nested && test ! -e packages/removed`,
		CachePaths: []string{"packages"},
	}
	if output, code := runCachePublication(t, cache, t.TempDir(), consumer); code != 0 {
		t.Fatalf("replacement merged or restored stale backing (exit %d): %s", code, output)
	}
}

func TestCachePublicationRemovalClearsPreviouslyCommittedContents(t *testing.T) {
	cache := t.TempDir()
	for _, command := range []string{"printf stale > packages/removed", "rm -rf packages"} {
		if output, code := runCachePublication(t, cache, t.TempDir(), RunSpec{Command: command, CachePaths: []string{"packages"}}); code != 0 {
			t.Fatalf("producer exit %d: %s", code, output)
		}
	}
	consumer := RunSpec{Command: "test -d packages && test ! -e packages/removed && printf fresh > packages/new", CachePaths: []string{"packages"}}
	if output, code := runCachePublication(t, cache, t.TempDir(), consumer); code != 0 {
		t.Fatalf("deleted cache content reappeared (exit %d): %s", code, output)
	}
}

func TestCachePublicationPreservesFileAndDirectoryReplacements(t *testing.T) {
	cache := t.TempDir()
	commands := []string{
		"printf stale > packages/removed",
		"rm -rf packages; printf file-value > packages",
		`test -f packages && test "$(cat packages)" = file-value`,
		"rm -f packages; mkdir packages; printf directory-value > packages/ready",
		`test -d packages && test "$(cat packages/ready)" = directory-value && test ! -e packages/removed`,
	}
	for i, command := range commands {
		if output, code := runCachePublication(t, cache, t.TempDir(), RunSpec{Command: command, CachePaths: []string{"packages"}}); code != 0 {
			t.Fatalf("file/directory transition %d exit %d: %s", i, code, output)
		}
	}
}

func TestCachePublicationRetainsInitialRegularFile(t *testing.T) {
	cache, workspace := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "packages"), []byte("source-value"), 0600); err != nil {
		t.Fatal(err)
	}
	spec := RunSpec{Command: `test -f packages && test "$(cat packages)" = source-value`, CachePaths: []string{"packages"}}
	for _, work := range []string{workspace, t.TempDir()} {
		if output, code := runCachePublication(t, cache, work, spec); code != 0 {
			t.Fatalf("initial file was discarded (exit %d): %s", code, output)
		}
	}
}

func TestCachePublicationSupportsQuotedWorkspaceAndHomePaths(t *testing.T) {
	for _, path := range []string{"build outputs/prepared packages", "~/.local/share/prepared packages", "build\toutputs/prepared packages"} {
		t.Run(path, func(t *testing.T) {
			cache := t.TempDir()
			resolved := ShellQuoteArg(path)
			if strings.HasPrefix(path, "~/") {
				resolved = `"$HOME"/` + ShellQuoteArg(strings.TrimPrefix(path, "~/"))
			}
			producer := RunSpec{Command: "p=" + resolved + `; rm -rf "$p"; mkdir -p "$p"; printf prepared > "$p/ready"`, CachePaths: []string{path}}
			consumer := RunSpec{Command: "p=" + resolved + `; test -L "$p" && test "$(cat "$p/ready")" = prepared`, CachePaths: []string{path}}
			for _, spec := range []RunSpec{producer, consumer} {
				if output, code := runCachePublication(t, cache, t.TempDir(), spec); code != 0 {
					t.Fatalf("quoted mapping exit %d: %s", code, output)
				}
			}
		})
	}
}

func TestCachePublicationDoesNotRestoreAnotherLockfileGeneration(t *testing.T) {
	cache := t.TempDir()
	spec := RunSpec{CachePaths: []string{"packages"}, CacheKeyFiles: []string{"deps.lock"}}
	for i, lock := range []string{"first", "second", "second"} {
		workspace := t.TempDir()
		if err := os.WriteFile(filepath.Join(workspace, "deps.lock"), []byte(lock), 0600); err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			spec.Command = "test ! -e packages/value && rm -rf packages && mkdir packages && printf " + ShellQuoteArg(lock) + " > packages/value"
		} else {
			spec.Command = `test "$(cat packages/value)" = second`
		}
		if output, code := runCachePublication(t, cache, workspace, spec); code != 0 {
			t.Fatalf("lockfile generation %d exit %d: %s", i, code, output)
		}
	}
}

func TestCachePublicationPrunesRegularFileGenerationsWithoutCrossingNamespaces(t *testing.T) {
	cache := t.TempDir()
	produce := func(lock, runtime, dependency, value string) string {
		t.Helper()
		workspace := t.TempDir()
		if err := os.WriteFile(filepath.Join(workspace, "deps.lock"), []byte(lock), 0600); err != nil {
			t.Fatal(err)
		}
		spec := RunSpec{
			Command:    "readlink packages > mapped-target; rm -rf packages; printf " + ShellQuoteArg(value) + " > packages",
			CachePaths: []string{"packages"}, CacheKeyFiles: []string{"deps.lock"},
			RuntimeRequirements: map[string]string{"node": runtime}, CacheDependencyCheck: dependency,
		}
		if output, code := runCachePublication(t, cache, workspace, spec); code != 0 {
			t.Fatalf("regular-file producer exit %d: %s", code, output)
		}
		raw, err := os.ReadFile(filepath.Join(workspace, "mapped-target"))
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSuffix(string(raw), "\n")
	}
	old := produce("old-lock", "22", "web-dependencies", "obsolete-main")
	siblingRuntime := produce("old-lock", "24", "web-dependencies", "sibling-runtime")
	siblingDependency := produce("old-lock", "22", "server-dependencies", "sibling-dependency")
	current := produce("new-lock", "22", "web-dependencies", "current-main")
	if _, err := os.Lstat(old); !os.IsNotExist(err) {
		t.Fatalf("old keyed regular-file generation remains: %v", err)
	}
	for path, want := range map[string]string{
		current: "current-main", siblingRuntime: "sibling-runtime", siblingDependency: "sibling-dependency",
	} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Fatalf("current or sibling namespace changed: got %q, want %q, error %v", got, want, err)
		}
	}
}

func TestCachePublicationRunsAtDeclaredBoundaries(t *testing.T) {
	const replace = "rm -rf packages; mkdir packages; printf ready > packages/ready"
	const observe = `test -L packages && test "$(cat packages/ready)" = ready`
	tests := map[string]RunSpec{
		"prepare-commands": {PrepareCommands: []string{replace, observe}, Command: observe},
		"steps": {Steps: []preflightapi.RunStepSpec{
			{Name: "produce", Command: replace}, {Name: "consume", Command: observe},
		}},
		"always-steps": {Steps: []preflightapi.RunStepSpec{
			{Name: "body", Command: ":"},
			{Name: "produce", Command: replace, Always: true},
			{Name: "consume", Command: observe, Always: true},
		}},
		"post":   {Command: replace, PostCommands: []string{observe}},
		"always": {Command: ":", AlwaysCommands: []string{replace, observe}},
	}
	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			spec.CachePaths = []string{"packages"}
			if output, code := runCachePublication(t, t.TempDir(), t.TempDir(), spec); code != 0 {
				t.Fatalf("declared boundary did not publish replacement (exit %d): %s", code, output)
			}
		})
	}
}

func TestCachePublicationKeepsInitialAttachmentAcrossPayloadEnvironmentChanges(t *testing.T) {
	spec := RunSpec{
		PrepareCommands: []string{
			"unset READYCI_CACHE_LANE_ID; rm -rf packages; mkdir packages; printf ready > packages/ready",
			`test -L packages && test "$(cat packages/ready)" = ready`,
		},
		Command:    `test "$(cat packages/ready)" = ready`,
		CachePaths: []string{"packages"},
	}
	if output, code := runCachePublication(t, t.TempDir(), t.TempDir(), spec); code != 0 {
		t.Fatalf("payload environment disabled publication at a declared boundary (exit %d): %s", code, output)
	}
}

func TestCachePublicationDoesNotRepairOpaqueInlineAssertion(t *testing.T) {
	workspace := t.TempDir()
	spec := RunSpec{
		Command:    "printf x >> invoked; rm -rf packages; mkdir packages; printf ready > packages/ready; test -L packages",
		CachePaths: []string{"packages"},
	}
	if output, code := runCachePublication(t, t.TempDir(), workspace, spec); code == 0 {
		t.Fatalf("inline assertion failure became success: %s", output)
	}
	if got, err := os.ReadFile(filepath.Join(workspace, "invoked")); err != nil || string(got) != "x" {
		t.Fatalf("opaque command was retried: %q, %v", got, err)
	}
}

func TestCachePublicationRejectsForeignSymlinkWithoutTouchingTarget(t *testing.T) {
	for _, initial := range []bool{false, true} {
		name := "replacement"
		if initial {
			name = "before-command"
		}
		t.Run(name, func(t *testing.T) {
			workspace, foreign := t.TempDir(), t.TempDir()
			if err := os.WriteFile(filepath.Join(foreign, "private"), []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			command := "printf ran > invoked"
			if initial {
				if err := os.Symlink(foreign, filepath.Join(workspace, "packages")); err != nil {
					t.Fatal(err)
				}
			} else {
				command = "rm -rf packages; ln -s " + ShellQuoteArg(foreign) + " packages"
			}
			if output, code := runCachePublication(t, t.TempDir(), workspace, RunSpec{Command: command, CachePaths: []string{"packages"}}); code == 0 {
				t.Fatalf("foreign symlink accepted as cache publication: %s", output)
			}
			if got, err := os.ReadFile(filepath.Join(foreign, "private")); err != nil || string(got) != "untouched" {
				t.Fatalf("foreign target changed: %q, %v", got, err)
			}
			if initial {
				if _, err := os.Lstat(filepath.Join(workspace, "invoked")); !os.IsNotExist(err) {
					t.Fatalf("payload ran after rejected initial mapping: %v", err)
				}
			}
		})
	}
}

func TestCachePublicationRejectsForeignBackingSymlink(t *testing.T) {
	for _, beforeCommand := range []bool{false, true} {
		name := "during-command"
		if beforeCommand {
			name = "between-commands"
		}
		t.Run(name, func(t *testing.T) {
			cache, seedWorkspace, foreign := t.TempDir(), t.TempDir(), t.TempDir()
			if err := os.WriteFile(filepath.Join(foreign, "private"), []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			seed := RunSpec{Command: "readlink packages > recorded-target; printf committed > packages/value", CachePaths: []string{"packages"}}
			if output, code := runCachePublication(t, cache, seedWorkspace, seed); code != 0 {
				t.Fatalf("seed exit %d: %s", code, output)
			}
			rawTarget, err := os.ReadFile(filepath.Join(seedWorkspace, "recorded-target"))
			if err != nil {
				t.Fatal(err)
			}
			target := strings.TrimSuffix(string(rawTarget), "\n")
			workspace := t.TempDir()
			command := "target=$(readlink packages) && rm -rf \"$target\" && ln -s " + ShellQuoteArg(foreign) + " \"$target\""
			if beforeCommand {
				if err := os.RemoveAll(target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(foreign, target); err != nil {
					t.Fatal(err)
				}
				command = "printf ran > invoked"
			}
			if output, code := runCachePublication(t, cache, workspace, RunSpec{Command: command, CachePaths: []string{"packages"}}); code == 0 {
				t.Fatalf("foreign backing accepted behind the expected source link: %s", output)
			}
			if got, err := os.Readlink(target); err != nil || got != foreign {
				t.Fatalf("foreign backing link was changed before refusal: %q, %v", got, err)
			}
			entries, err := os.ReadDir(foreign)
			if err != nil || len(entries) != 1 || entries[0].Name() != "private" {
				t.Fatalf("foreign backing was mutated: %v, %v", entries, err)
			}
			if got, err := os.ReadFile(filepath.Join(foreign, "private")); err != nil || string(got) != "untouched" {
				t.Fatalf("foreign content changed: %q, %v", got, err)
			}
			if beforeCommand {
				if _, err := os.Lstat(filepath.Join(workspace, "invoked")); !os.IsNotExist(err) {
					t.Fatalf("payload ran with foreign backing: %v", err)
				}
			}
		})
	}
}

func TestCachePublicationRejectsOverlappingMappingsBeforeMutation(t *testing.T) {
	for _, name := range []string{"parent-first", "child-first", "absolute-alias"} {
		t.Run(name, func(t *testing.T) {
			workspace := t.TempDir()
			original := filepath.Join(workspace, "packages")
			if err := os.MkdirAll(filepath.Join(original, "nested"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(original, "preserve"), []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			paths := []string{"packages", "packages/nested"}
			switch name {
			case "child-first":
				paths[0], paths[1] = paths[1], paths[0]
			case "absolute-alias":
				paths[1] = filepath.ToSlash(original)
			}
			spec := RunSpec{Command: "printf ran > invoked", CachePaths: paths}
			if _, err := CommandForRunSpec(spec); err != nil {
				return // Unsupported overlaps can be rejected at compilation.
			}
			if output, code := runCachePublication(t, t.TempDir(), workspace, spec); code == 0 {
				t.Fatalf("overlapping mappings accepted: %s", output)
			}
			if info, err := os.Lstat(original); err != nil || !info.IsDir() {
				t.Fatalf("original directory mutated before overlap rejection: %v, %v", info, err)
			}
			if got, err := os.ReadFile(filepath.Join(original, "preserve")); err != nil || string(got) != "original" {
				t.Fatalf("original data changed: %q, %v", got, err)
			}
			if _, err := os.Lstat(filepath.Join(workspace, "invoked")); !os.IsNotExist(err) {
				t.Fatalf("payload ran after overlap rejection: %v", err)
			}
		})
	}
}

func TestCachePublicationPreservesOriginalNonzeroAndRunsCleanupOnce(t *testing.T) {
	workspace := t.TempDir()
	spec := RunSpec{
		Command:        "printf payload >> order; rm -rf packages; mkdir packages; printf partial > packages/value; exit 17",
		AlwaysCommands: []string{"printf cleanup >> order"},
		CachePaths:     []string{"packages"},
	}
	if output, code := runCachePublication(t, t.TempDir(), workspace, spec); code != 17 {
		t.Fatalf("original payload exit changed to %d: %s", code, output)
	}
	if got, err := os.ReadFile(filepath.Join(workspace, "order")); err != nil || string(got) != "payloadcleanup" {
		t.Fatalf("payload/cleanup reran or were omitted: %q, %v", got, err)
	}
}

func TestCachePublicationRejectsSpecialFileWithoutRemovingReplacement(t *testing.T) {
	workspace := t.TempDir()
	spec := RunSpec{Command: "rm -rf packages; mkfifo packages", CachePaths: []string{"packages"}}
	if output, code := runCachePublication(t, t.TempDir(), workspace, spec); code == 0 {
		t.Fatalf("special file accepted as a prepared cache: %s", output)
	}
	if info, err := os.Lstat(filepath.Join(workspace, "packages")); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("unsupported replacement was removed before refusal: %v, %v", info, err)
	}
}

func TestCachePublicationFailureCannotBecomeSuccessfulPreparation(t *testing.T) {
	for _, originalExit := range []int{0, 17} {
		name := "successful-payload"
		if originalExit != 0 {
			name = "failed-payload"
		}
		t.Run(name, func(t *testing.T) {
			cache, workspace := t.TempDir(), t.TempDir()
			seed := RunSpec{Command: "printf committed > packages/value", CachePaths: []string{"packages"}}
			if output, code := runCachePublication(t, cache, t.TempDir(), seed); code != 0 {
				t.Fatalf("seed exit %d: %s", code, output)
			}
			// Fail either conventional publication operation only after the
			// payload has replaced the mapped directory. Setup remains real.
			bin := t.TempDir()
			for _, tool := range []string{"cp", "mv"} {
				real, err := exec.LookPath(tool)
				if err != nil {
					t.Fatal(err)
				}
				shim := "#!/bin/sh\nif [ -e \"$READYCI_WORKSPACE_DIR/fail-publication\" ]; then\n  echo 'injected cache publication failure' >&2\n  exit 73\nfi\nexec " + ShellQuoteArg(real) + " \"$@\"\n"
				if err := os.WriteFile(filepath.Join(bin, tool), []byte(shim), 0700); err != nil {
					t.Fatal(err)
				}
			}
			command := "printf x >> invoked; rm -rf packages; mkdir packages; printf uncommitted > packages/value; touch fail-publication"
			if originalExit != 0 {
				command += "; exit 17"
			}
			output, code := runCachePublication(t, cache, workspace, RunSpec{Command: command, CachePaths: []string{"packages"}}, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			if code == 0 || originalExit != 0 && code != originalExit {
				t.Fatalf("payload %d plus persistence failure returned %d: %s", originalExit, code, output)
			}
			if !strings.Contains(string(output), "injected cache publication failure") {
				t.Fatalf("publication fault was not exercised: %s", output)
			}
			if got, err := os.ReadFile(filepath.Join(workspace, "invoked")); err != nil || string(got) != "x" {
				t.Fatalf("payload was retried: %q, %v", got, err)
			}
			consumer := RunSpec{Command: `test ! -e packages/value || test "$(cat packages/value)" = committed`, CachePaths: []string{"packages"}}
			if output, code := runCachePublication(t, cache, t.TempDir(), consumer); code != 0 {
				t.Fatalf("fresh consumer accepted partial publication (exit %d): %s", code, output)
			}
		})
	}
}

func runCachePublication(t *testing.T, cache, workspace string, spec RunSpec, extraEnv ...string) ([]byte, int) {
	t.Helper()
	command, err := CommandForRunSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	command = strings.ReplaceAll(command, "/cache", filepath.ToSlash(cache))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	cmd.Dir = workspace
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"LANG=C", "LC_ALL=C",
		"READYCI_CACHE_LANE_ID=cache_lane_publication",
		"READYCI_WORKSPACE_DIR=" + workspace,
	}, extraEnv...)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("cache fixture exceeded its deadline: %v: %s", ctx.Err(), output)
	}
	if err == nil {
		return output, 0
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return output, exit.ExitCode()
	}
	t.Fatalf("run cache fixture: %v: %s", err, output)
	return nil, -1
}
