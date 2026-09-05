package verificationplan

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestBuildRemoteCommandAddsCacheAndFailedRetention(t *testing.T) {
	command, err := buildCommand(commandPhases{
		Prepare: []string{"scripts/bootstrap-web-deps.sh"},
		Command: "make test",
		Post:    []string{"scripts/collect-artifacts.sh"},
		Always:  []string{"scripts/cleanup.sh"},
	}, []string{"~/.cargo", "apps/web/.next/cache"}, []string{"Cargo.lock", "apps/web/pnpm-lock.yaml"}, true)
	if err != nil {
		t.Fatalf("build remote command: %v", err)
	}
	for _, want := range []string{
		"__preflight_setup_cache_paths",
		"__preflight_prune_failed_run_cache",
		"__preflight_compute_cache_key Cargo.lock apps/web/pnpm-lock.yaml",
		"__preflight_link_cache_path",
		"scripts/bootstrap-web-deps.sh",
		"make test",
		"scripts/collect-artifacts.sh",
		"scripts/cleanup.sh",
		".readyci/failed-workspace.tar.gz",
		"--exclude '*/.git/*'",
		"if [ $__preflight_status -eq 0 ]; then",
		"__preflight_cache_tmp=\"$__preflight_dest/workspace.tar.gz.tmp\"",
		"-czf \"$__preflight_cache_tmp\"",
		"__preflight_dest=\"/cache/failed-runs/$__preflight_run_id\"",
		"skipped failed workspace cache retention because /cache has less than 1 GiB available",
		"skipped failed workspace artifact because the workspace filesystem has less than 1 GiB available",
		"exit $__preflight_status",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("remote command missing %q:\n%s", want, command)
		}
	}
	propagatingPathSetups := 0
	for _, line := range strings.Split(command, "\n") {
		if strings.HasPrefix(line, "  __preflight_link_cache_path ") && strings.HasSuffix(line, " || return $?") {
			propagatingPathSetups++
		}
	}
	if propagatingPathSetups != 2 {
		t.Fatalf("cache setup does not propagate each mapping failure:\n%s", command)
	}
}

func TestCachePathSetupExpandsHomeRelativePath(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	cache := t.TempDir()
	cachePath := "~/.cache/readyci-test"
	script, err := cachePathSetupScript([]string{cachePath}, nil)
	if err != nil {
		t.Fatalf("cache path setup script: %v", err)
	}
	script = strings.ReplaceAll(script, "/cache", filepath.ToSlash(cache))
	command := script + "\n__preflight_setup_cache_paths\n"

	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("find bash: %v", err)
	}
	cmd := exec.Command(bashPath, "-lc", command)
	cmd.Dir = workspace
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "HOME=") || strings.HasPrefix(entry, "READYCI_CACHE_LANE_ID=") {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "HOME="+home, "READYCI_CACHE_LANE_ID=cache_lane_test")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run cache path setup: %v: %s", err, output)
	}

	linkedPath := filepath.Join(home, ".cache", "readyci-test")
	target, err := os.Readlink(linkedPath)
	if err != nil {
		t.Fatalf("read home-relative cache symlink: %v", err)
	}
	wantTarget := filepath.Join(cache, "paths", cachePathKey(cachePath))
	if target != wantTarget {
		t.Fatalf("cache symlink target = %q, want %q", target, wantTarget)
	}
	if _, err := os.Lstat(filepath.Join(home, "~")); !os.IsNotExist(err) {
		t.Fatalf("literal tilde directory exists under home: %v", err)
	}
}

func TestBuildRemoteCommandRunsStepsAndAlwaysStep(t *testing.T) {
	command, err := buildCommand(commandPhases{
		Steps: []preflightapi.RunStepSpec{
			{Name: "deps", Command: "printf deps >> order.txt; exit 0", Env: map[string]string{"STEP_ONLY": "deps"}},
			{Name: "test", Command: `printf test >> order.txt; printf "%s" "${STEP_ONLY-unset}" > env.txt; exit 7`},
			{Name: "cleanup", Command: "printf cleanup >> order.txt", Always: true},
		},
	}, nil, nil, false)
	if err != nil {
		t.Fatalf("build remote command: %v", err)
	}
	for _, want := range []string{"READYCI_STEP", "__preflight_step_0", "__preflight_step_2", "cleanup"} {
		if !strings.Contains(command, want) {
			t.Fatalf("step command missing %q:\n%s", want, command)
		}
	}
	if !strings.Contains(command, "__readyci_log_step cleanup\n  __preflight_step_2") {
		t.Fatalf("always step should emit its step marker before running cleanup:\n%s", command)
	}
	workdir := t.TempDir()
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("find bash: %v", err)
	}
	cmd := exec.Command(bashPath, "-lc", command)
	cmd.Dir = workdir
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("run step command succeeded, want failure; output=%s", output)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 7 {
		t.Fatalf("step exit error = %v output=%s, want exit 7", err, output)
	}
	data, err := os.ReadFile(filepath.Join(workdir, "order.txt"))
	if err != nil {
		t.Fatalf("read order: %v", err)
	}
	if got := string(data); got != "depstestcleanup" {
		t.Fatalf("order = %q, want depstestcleanup", got)
	}
	envData, err := os.ReadFile(filepath.Join(workdir, "env.txt"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if got := string(envData); got != "unset" {
		t.Fatalf("step env leaked into later step: %q", got)
	}
}

func TestRemoteCommandForOptionsPreservesStoredWrapperWithSteps(t *testing.T) {
	command, err := CommandForRunSpec(RunSpec{
		Command: "generated wrapper with prepare and cache",
		Steps: []preflightapi.RunStepSpec{
			{Name: "test", Command: "go test ./..."},
		},
		PrepareCommands: []string{"should not be rebuilt"},
		CachePaths:      []string{"target"},
		KeepFailed:      true,
	})
	if err != nil {
		t.Fatalf("remote command: %v", err)
	}
	if command != "generated wrapper with prepare and cache" {
		t.Fatalf("command = %q, want stored wrapper", command)
	}
}

func TestBuildRemoteCommandSkipsCacheKeyFilesWhenNoCacheAttached(t *testing.T) {
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "Cargo.lock"), []byte("lock"), 0o644); err != nil {
		t.Fatalf("write lockfile: %v", err)
	}
	command, err := buildCommand(commandPhases{
		Command: "printf ok > result.txt",
	}, []string{"target"}, []string{"Cargo.lock"}, false)
	if err != nil {
		t.Fatalf("build remote command: %v", err)
	}
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("find bash: %v", err)
	}

	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "READYCI_CACHE_LANE_ID=") ||
			strings.HasPrefix(entry, "PATH=") {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "PATH=/nonexistent")

	cmd := exec.Command(bashPath, "-lc", command)
	cmd.Dir = workdir
	cmd.Env = env
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run command without attached cache: %v: %s", err, output)
	}
	data, err := os.ReadFile(filepath.Join(workdir, "result.txt"))
	if err != nil {
		t.Fatalf("read command output: %v", err)
	}
	if got := string(data); got != "ok" {
		t.Fatalf("result = %q, want ok", got)
	}
}

func TestCacheKeyComputationDoesNotRequireAwk(t *testing.T) {
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "Cargo.lock"), []byte("lock"), 0o644); err != nil {
		t.Fatalf("write lockfile: %v", err)
	}
	script, err := cachePathSetupScript([]string{"target"}, []string{"Cargo.lock"})
	if err != nil {
		t.Fatalf("cache path setup script: %v", err)
	}

	const hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	fakeBin := writeCacheKeyToolBin(t, workdir, "#!/bin/sh\nprintf '"+hash+"  %s\\n' \"$1\"\n")
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("find bash: %v", err)
	}

	command := script + "\n__preflight_compute_cache_key Cargo.lock || exit $?\nprintf '%s' \"$__preflight_cache_key_suffix\" > suffix.txt\n"

	cmd := exec.Command(bashPath, "-lc", command)
	cmd.Dir = workdir
	cmd.Env = cacheKeyToolEnv(fakeBin, workdir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run cache key computation: %v: %s", err, output)
	}
	data, err := os.ReadFile(filepath.Join(workdir, "suffix.txt"))
	if err != nil {
		t.Fatalf("read suffix: %v", err)
	}
	if got := string(data); got != hash {
		t.Fatalf("cache key suffix = %q, want %q", got, hash)
	}
}

func TestCacheKeyComputationIgnoresWorkspacePath(t *testing.T) {
	script, err := cachePathSetupScript([]string{"target"}, []string{"Cargo.lock"})
	if err != nil {
		t.Fatalf("cache path setup script: %v", err)
	}

	const fileHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const stableHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const pathHash = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	shaScript := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  *readyci-cache-key-*)\n" +
		"    __found_abs=0\n" +
		"    while IFS= read -r __line; do\n" +
		"      case \"$__line\" in *\"  /\"*) __found_abs=1 ;; esac\n" +
		"    done < \"$1\"\n" +
		"    if [ \"$__found_abs\" -eq 1 ]; then\n" +
		"      printf '" + pathHash + "  %s\\n' \"$1\"\n" +
		"    else\n" +
		"      printf '" + stableHash + "  %s\\n' \"$1\"\n" +
		"    fi\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"esac\n" +
		"printf '" + fileHash + "  %s\\n' \"$1\"\n"
	fakeBin := writeCacheKeyToolBin(t, t.TempDir(), shaScript)

	first := runCacheKeySuffix(t, script, t.TempDir(), fakeBin)
	second := runCacheKeySuffix(t, script, t.TempDir(), fakeBin)
	if first != stableHash || second != stableHash {
		t.Fatalf("cache key suffixes = %q and %q, want stable path-independent %q", first, second, stableHash)
	}
}

func TestCacheKeyComputationPropagatesFinalHashFailure(t *testing.T) {
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "Cargo.lock"), []byte("lock"), 0o644); err != nil {
		t.Fatalf("write lockfile: %v", err)
	}
	script, err := cachePathSetupScript([]string{"target"}, []string{"Cargo.lock"})
	if err != nil {
		t.Fatalf("cache path setup script: %v", err)
	}

	const hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	fakeBin := writeCacheKeyToolBin(t, workdir, "#!/bin/sh\ncase \"$1\" in\n  *readyci-cache-key-*) exit 73 ;;\nesac\nprintf '"+hash+"  %s\\n' \"$1\"\n")
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("find bash: %v", err)
	}

	command := script + "\n__preflight_compute_cache_key Cargo.lock\nprintf '%s' \"$?\" > status.txt\n"
	cmd := exec.Command(bashPath, "-lc", command)
	cmd.Dir = workdir
	cmd.Env = cacheKeyToolEnv(fakeBin, workdir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run cache key computation: %v: %s", err, output)
	}
	data, err := os.ReadFile(filepath.Join(workdir, "status.txt"))
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if got := string(data); got != "73" {
		t.Fatalf("cache key status = %q, want 73", got)
	}
}

func TestCachePathSetupPrunesStaleKeyGenerations(t *testing.T) {
	workdir := t.TempDir()
	cacheRoot := filepath.Join(workdir, "cache")
	repoRoot := filepath.Join(workdir, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "Cargo.lock"), []byte("lock"), 0o644); err != nil {
		t.Fatalf("write lockfile: %v", err)
	}
	key := cachePathKey("target")
	oldSuffix := strings.Repeat("a", 64)
	oldGeneration := filepath.Join(cacheRoot, "paths", key, oldSuffix)
	if err := os.MkdirAll(oldGeneration, 0o755); err != nil {
		t.Fatalf("mkdir old generation: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oldGeneration, "old.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("write old generation: %v", err)
	}

	script, err := cachePathSetupScript([]string{"target"}, []string{"Cargo.lock"})
	if err != nil {
		t.Fatalf("cache path setup script: %v", err)
	}
	script = strings.ReplaceAll(script, "/cache", cacheRoot)
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("find bash: %v", err)
	}
	command := script + "\n__preflight_setup_cache_paths || exit $?\nreadlink target > linked.txt\n"
	cmd := exec.Command(bashPath, "-lc", command)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "READYCI_CACHE_LANE_ID=cache_lane_test")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run cache path setup: %v: %s", err, output)
	}
	if _, err := os.Stat(oldGeneration); !os.IsNotExist(err) {
		t.Fatalf("old generation still exists or stat failed with %v", err)
	}
	linked, err := os.ReadFile(filepath.Join(repoRoot, "linked.txt"))
	if err != nil {
		t.Fatalf("read link target: %v", err)
	}
	currentGeneration := strings.TrimSpace(string(linked))
	if !strings.HasPrefix(currentGeneration, filepath.Join(cacheRoot, "paths", key)+string(os.PathSeparator)) {
		t.Fatalf("linked target = %q, want current cache generation under %s", currentGeneration, filepath.Join(cacheRoot, "paths", key))
	}
	if _, err := os.Stat(currentGeneration); err != nil {
		t.Fatalf("current generation stat: %v", err)
	}
}

func TestCachePathSetupPrunesFailedRunSnapshots(t *testing.T) {
	workdir := t.TempDir()
	cacheRoot := filepath.Join(workdir, "cache")
	repoRoot := filepath.Join(workdir, "repo")
	failedRunDir := filepath.Join(cacheRoot, "failed-runs", "run_old")
	if err := os.MkdirAll(failedRunDir, 0o755); err != nil {
		t.Fatalf("mkdir failed run: %v", err)
	}
	if err := os.WriteFile(filepath.Join(failedRunDir, "workspace.tar.gz"), []byte("old"), 0o644); err != nil {
		t.Fatalf("write failed run snapshot: %v", err)
	}
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	script, err := cachePathSetupScript([]string{"target"}, nil)
	if err != nil {
		t.Fatalf("cache path setup script: %v", err)
	}
	script = strings.ReplaceAll(script, "/cache", cacheRoot)
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("find bash: %v", err)
	}

	command := script + "\n__preflight_setup_cache_paths || exit $?\n"
	cmd := exec.Command(bashPath, "-lc", command)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "READYCI_CACHE_LANE_ID=cache_lane_test")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run cache path setup: %v: %s", err, output)
	}
	if _, err := os.Stat(failedRunDir); !os.IsNotExist(err) {
		t.Fatalf("failed run snapshot still exists or stat failed with %v", err)
	}
}

func TestCachePathSetupReportsPathAndFilesystemUsageOnFailure(t *testing.T) {
	workdir := t.TempDir()
	cacheRoot := filepath.Join(workdir, "cache")
	repoRoot := filepath.Join(workdir, "repo")
	if err := os.MkdirAll(filepath.Join(cacheRoot, "paths"), 0o755); err != nil {
		t.Fatalf("mkdir cache paths: %v", err)
	}
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	key := cachePathKey("target")
	blockedTarget := filepath.Join(cacheRoot, "paths", key)
	if err := os.WriteFile(blockedTarget, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write blocking cache file: %v", err)
	}

	script, err := cachePathSetupScript([]string{"target"}, nil)
	if err != nil {
		t.Fatalf("cache path setup script: %v", err)
	}
	script = strings.ReplaceAll(script, "/cache", cacheRoot)
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("find bash: %v", err)
	}

	command := script + "\n__preflight_setup_cache_paths\n"
	cmd := exec.Command(bashPath, "-lc", command)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "READYCI_CACHE_LANE_ID=cache_lane_test")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("cache setup succeeded, want failure; output=%s", output)
	}
	for _, want := range []string{
		"readyci: cache path setup failed during mkdir",
		"target -> " + blockedTarget,
		"readyci: cache filesystem usage:",
		"Filesystem",
	} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("cache setup output %q does not contain %q", output, want)
		}
	}
}

func writeCacheKeyToolBin(t *testing.T, workdir, sha256sumScript string) string {
	t.Helper()
	fakeBin := filepath.Join(workdir, "bin")
	if err := os.Mkdir(fakeBin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	rmPath, err := exec.LookPath("rm")
	if err != nil {
		t.Fatalf("find rm: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "rm"), []byte("#!/bin/sh\nexec "+ShellQuoteArg(rmPath)+" \"$@\"\n"), 0o755); err != nil {
		t.Fatalf("write rm wrapper: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "sha256sum"), []byte(sha256sumScript), 0o755); err != nil {
		t.Fatalf("write sha256sum wrapper: %v", err)
	}
	return fakeBin
}

func runCacheKeySuffix(t *testing.T, script, workdir, fakeBin string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(workdir, "Cargo.lock"), []byte("lock"), 0o644); err != nil {
		t.Fatalf("write lockfile: %v", err)
	}
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("find bash: %v", err)
	}
	command := script + "\n__preflight_compute_cache_key Cargo.lock || exit $?\nprintf '%s' \"$__preflight_cache_key_suffix\" > suffix.txt\n"
	cmd := exec.Command(bashPath, "-lc", command)
	cmd.Dir = workdir
	cmd.Env = cacheKeyToolEnv(fakeBin, workdir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run cache key computation: %v: %s", err, output)
	}
	data, err := os.ReadFile(filepath.Join(workdir, "suffix.txt"))
	if err != nil {
		t.Fatalf("read suffix: %v", err)
	}
	return string(data)
}

func cacheKeyToolEnv(fakeBin, tmpDir string) []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "PATH=") || strings.HasPrefix(entry, "TMPDIR=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "PATH="+fakeBin, "TMPDIR="+tmpDir)
}

func TestBuildRemoteCommandStopsPhaseListAndRunsAlwaysAfterPrepareFailure(t *testing.T) {
	workdir := t.TempDir()
	command, err := buildCommand(commandPhases{
		Prepare: []string{
			"printf prepare > phase.log",
			"exit 17",
			"printf skipped >> phase.log",
		},
		Command: "printf command >> phase.log",
		Always:  []string{"printf always >> phase.log"},
	}, nil, nil, false)
	if err != nil {
		t.Fatalf("build remote command: %v", err)
	}

	cmd := exec.Command("bash", "-lc", command)
	cmd.Dir = workdir
	err = cmd.Run()
	exitErr := &exec.ExitError{}
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 17 {
		t.Fatalf("command error = %v, want exit code 17", err)
	}
	data, err := os.ReadFile(filepath.Join(workdir, "phase.log"))
	if err != nil {
		t.Fatalf("read phase log: %v", err)
	}
	if got := string(data); got != "preparealways" {
		t.Fatalf("phase log = %q, want preparealways", got)
	}
}

func TestBuildRemoteCommandEmitsPhaseMarkers(t *testing.T) {
	command, err := buildCommand(commandPhases{
		Prepare: []string{"printf prepare"},
		Command: "printf command",
		Post:    []string{"printf post"},
		Always:  []string{"printf always"},
	}, nil, nil, false)
	if err != nil {
		t.Fatalf("build remote command: %v", err)
	}
	for _, want := range []string{
		"__readyci_log_phase prepare",
		"__readyci_log_phase command",
		"__readyci_log_phase post",
		"__readyci_log_phase always",
		"READYCI_PHASE",
		"__readyci_log_phase() {\n  printf '\\036READYCI_PHASE:%s\\036\\n' \"$1\"\n  printf '\\036READYCI_PHASE:%s\\036\\n' \"$1\" >&2\n  __readyci_log_step ''",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("remote command missing %q:\n%s", want, command)
		}
	}
}

func TestBuildRemoteCommandRunsPrepareAndCommandInSameShell(t *testing.T) {
	workdir := t.TempDir()
	if err := os.Mkdir(filepath.Join(workdir, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	command, err := buildCommand(commandPhases{
		Prepare: []string{
			"export READYCI_PHASE_VALUE=prepared",
			"cd nested",
		},
		Command: "printf '%s:%s' \"$READYCI_PHASE_VALUE\" \"${PWD##*/}\" > ../phase.log",
	}, nil, nil, false)
	if err != nil {
		t.Fatalf("build remote command: %v", err)
	}

	cmd := exec.Command("bash", "-lc", command)
	cmd.Dir = workdir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run command: %v: %s", err, output)
	}
	data, err := os.ReadFile(filepath.Join(workdir, "phase.log"))
	if err != nil {
		t.Fatalf("read phase log: %v", err)
	}
	if got := string(data); got != "prepared:nested" {
		t.Fatalf("phase log = %q, want prepared:nested", got)
	}
}
