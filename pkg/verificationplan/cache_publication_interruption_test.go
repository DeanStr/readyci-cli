//go:build unix

package verificationplan

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCachePublicationInterruptedProducerCannotPreparePartialGeneration(t *testing.T) {
	for _, replace := range []bool{false, true} {
		name := "unchanged-link"
		if replace {
			name = "replaced-directory"
		}
		t.Run(name, func(t *testing.T) {
			cache := t.TempDir()
			seed := RunSpec{
				Command:    "set -eu\nprintf committed > packages/ready; printf committed > packages/complete",
				CachePaths: []string{"packages"},
			}
			if output, code := runCachePublication(t, cache, t.TempDir(), seed); code != 0 {
				t.Fatalf("seed committed generation exit %d: %s", code, output)
			}

			producer := "set -eu\ntest -L packages || exit 71\n"
			if replace {
				producer += "rm -rf packages; mkdir packages\n"
			}
			producer += "printf partial > packages/ready\n" +
				"rm -f packages/complete\n" +
				"printf partial > packages/partial-only\n" +
				"printf ready > \"$READYCI_INTERRUPT_READY\"\n" +
				"while :; do sleep 1; done"
			interruptCachePublication(t, cache, t.TempDir(), RunSpec{
				Command: producer, CachePaths: []string{"packages"},
			})
			assertFreshCachePublicationConsumerIsSafe(t, cache)
		})
	}
}

func TestCachePublicationInterruptedCopyCannotCommitNewGeneration(t *testing.T) {
	cache := t.TempDir()
	seed := RunSpec{
		Command:    "set -eu\nprintf committed > packages/ready; printf committed > packages/complete",
		CachePaths: []string{"packages"},
	}
	if output, code := runCachePublication(t, cache, t.TempDir(), seed); code != 0 {
		t.Fatalf("seed committed generation exit %d: %s", code, output)
	}
	realCopy, err := exec.LookPath("cp")
	if err != nil {
		t.Fatal(err)
	}
	realCopy, err = filepath.Abs(realCopy)
	if err != nil {
		t.Fatal(err)
	}
	shim := t.TempDir()
	// Setup copies stay ordinary. Only after the inert payload has finished
	// replacing its output do we pause a real successful publication copy,
	// before control returns to the wrapper to publish the generation.
	copyShim := `#!/bin/sh
set -eu
if [ ! -f "$READYCI_PUBLICATION_ACTIVATE" ]; then
  exec "$READYCI_REAL_CP" "$@"
fi
"$READYCI_REAL_CP" "$@"
printf ready > "$READYCI_INTERRUPT_READY"
while :; do sleep 1; done
`
	if err := os.WriteFile(filepath.Join(shim, "cp"), []byte(copyShim), 0755); err != nil {
		t.Fatal(err)
	}
	signals := t.TempDir()
	activate := filepath.Join(signals, "payload-complete")
	invocations := filepath.Join(signals, "producer-invocations")
	producer := RunSpec{
		Command: `set -eu
printf invoked >> "$READYCI_PRODUCER_INVOCATIONS"
rm -rf packages
mkdir packages
printf unpublished > packages/ready
printf unpublished > packages/complete
printf activate > "$READYCI_PUBLICATION_ACTIVATE"`,
		CachePaths: []string{"packages"},
	}
	interruptCachePublication(t, cache, t.TempDir(), producer,
		"PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"),
		"READYCI_REAL_CP="+realCopy,
		"READYCI_PUBLICATION_ACTIVATE="+activate,
		"READYCI_PRODUCER_INVOCATIONS="+invocations,
	)
	assertFreshCachePublicationConsumerIsSafe(t, cache)
	called, err := os.ReadFile(invocations)
	if err != nil {
		t.Fatal(err)
	}
	if string(called) != "invoked" {
		t.Fatalf("interrupted publication reran the payload: %q", called)
	}
}

func assertFreshCachePublicationConsumerIsSafe(t *testing.T, cache string) {
	t.Helper()
	// This is a distinct process and workspace. Observe the data rather
	// than the publisher's private pending/commit representation.
	workspace := t.TempDir()
	consumer := RunSpec{
		Command: `printf started > consumer-started
if [ "$(cat packages/ready 2>/dev/null)" = committed ] &&
   [ "$(cat packages/complete 2>/dev/null)" = committed ] &&
   [ ! -e packages/partial-only ]; then
  printf committed > consumer-observation
elif [ ! -e packages/ready ] && [ ! -e packages/complete ] && [ ! -e packages/partial-only ]; then
  printf invalidated > consumer-observation
else
  printf partial-generation > consumer-observation
fi`,
		CachePaths: []string{"packages"},
	}
	output, code := runCachePublication(t, cache, workspace, consumer)
	observed, err := os.ReadFile(filepath.Join(workspace, "consumer-observation"))
	if errors.Is(err, os.ErrNotExist) {
		if _, started := os.Stat(filepath.Join(workspace, "consumer-started")); code == 0 || !errors.Is(started, os.ErrNotExist) {
			t.Fatalf("consumer neither refused before execution nor observed a safe generation (exit %d): %s", code, output)
		}
		return // A pending generation may fail closed before payload execution.
	}
	if err != nil {
		t.Fatal(err)
	}
	if string(observed) != "committed" && string(observed) != "invalidated" {
		t.Fatalf("fresh consumer observed interrupted producer's uncommitted generation (exit %d, observation %q): %s", code, observed, output)
	}
}

func interruptCachePublication(t *testing.T, cache, workspace string, spec RunSpec, extraEnv ...string) {
	t.Helper()
	command, err := CommandForRunSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	command = strings.ReplaceAll(command, "/cache", filepath.ToSlash(cache))
	ready := filepath.Join(t.TempDir(), "producer-ready")
	cmd := exec.Command("bash", "-c", command)
	cmd.Dir = workspace
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"LANG=C", "LC_ALL=C",
		"READYCI_CACHE_LANE_ID=cache_lane_publication",
		"READYCI_WORKSPACE_DIR=" + workspace,
		"READYCI_INTERRUPT_READY=" + ready,
	}
	cmd.Env = append(cmd.Env, extraEnv...)
	// Kill only the process group created for this inert producer, including
	// its blocking sleep. No unrelated shell or test process joins this group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	waited := false
	defer func() {
		if waited {
			return
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("owned cache producer process group did not stop during cleanup")
		}
	}()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	waiting := true
	for waiting {
		select {
		case err := <-done:
			waited = true
			t.Fatalf("producer exited before interruption point: %v: %s", err, output.Bytes())
		case <-deadline.C:
			t.Fatal("cache producer did not reach its bounded interruption point")
		case <-poll.C:
			raw, err := os.ReadFile(ready)
			if err == nil && string(raw) == "ready" {
				waiting = false
			} else if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
		}
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill owned producer group: %v", err)
	}
	select {
	case err := <-done:
		waited = true
		var exited *exec.ExitError
		if !errors.As(err, &exited) {
			t.Fatalf("interrupted producer exit = %v, want SIGKILL: %s", err, output.Bytes())
		}
		status, ok := exited.Sys().(syscall.WaitStatus)
		if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
			t.Fatalf("interrupted producer status = %v, want SIGKILL: %s", exited, output.Bytes())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owned cache producer process group did not stop after SIGKILL")
	}
}
