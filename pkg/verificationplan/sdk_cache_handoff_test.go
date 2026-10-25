//go:build unix

package verificationplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

// Use a tiny inert SDK instead of downloading Go. Each run has a fresh HOME,
// workspace and shell; only the configured persistent cache lane is shared.
func TestSDKCacheSurvivesFailedOfflineConsumer(t *testing.T) {
	cache, deniedDownloads := t.TempDir(), t.TempDir()
	curl := "#!/bin/sh\nprintf attempted >> \"$READYCI_WORKSPACE_DIR/download-attempted\"\nexit 97\n"
	if err := os.WriteFile(filepath.Join(deniedDownloads, "curl"), []byte(curl), 0700); err != nil {
		t.Fatal(err)
	}
	sdk := `"$HOME/.cache/readyci-fixture-sdk/go/bin/go"`
	bootstrap := `if [ ! -x ` + sdk + ` ]; then curl https://example.invalid/fixture-sdk; exit $?; fi; ` + sdk
	prepare := `mkdir -p "$HOME/.cache/readyci-fixture-sdk/go/bin"; printf '%s\n' '#!/bin/sh' 'printf fixture-go-version' > ` + sdk + `; chmod 700 ` + sdk
	for index, spec := range []RunSpec{
		{Command: prepare, NetworkMode: "allowlist"},
		{PrepareCommands: []string{bootstrap}, Command: "printf invoked >> calls; exit 17", AlwaysCommands: []string{"printf cleanup >> calls"}, NetworkMode: "none"},
		{PrepareCommands: []string{bootstrap}, Command: `test "$(` + sdk + `)" = fixture-go-version`, NetworkMode: "none"},
	} {
		workspace := t.TempDir()
		if err := os.WriteFile(filepath.Join(workspace, "deps.lock"), []byte("same-sealed-dependencies"), 0600); err != nil {
			t.Fatal(err)
		}
		spec.CachePaths = []string{"~/.cache/readyci-fixture-sdk"}
		spec.CacheKeyFiles = []string{"deps.lock"}
		output, code := runCachePublication(t, cache, workspace, spec, "PATH="+deniedDownloads+string(os.PathListSeparator)+os.Getenv("PATH"))
		want := 0
		if index == 1 {
			want = 17
			if got, err := os.ReadFile(filepath.Join(workspace, "calls")); err != nil || string(got) != "invokedcleanup" {
				t.Fatalf("failed command/cleanup did not run exactly once: %q, %v", got, err)
			}
		}
		if code != want {
			t.Fatalf("warm/failed-consumer/offline-consumer phase %d exit=%d, want %d: %s", index, code, want, output)
		}
		if _, err := os.Stat(filepath.Join(workspace, "download-attempted")); !os.IsNotExist(err) {
			t.Fatal("offline consumer attempted to download a previously prepared SDK")
		}
	}
}

func TestSDKCacheInterruptedWriterRemainsUncommitted(t *testing.T) {
	cache := t.TempDir()
	spec := RunSpec{CachePaths: []string{"sdk"}, Command: "printf prepared > sdk/version"}
	if output, code := runCachePublication(t, cache, t.TempDir(), spec); code != 0 {
		t.Fatalf("prepare SDK exit %d: %s", code, output)
	}
	// runCachePublication starts a dedicated process group. Kill that group,
	// including its wrapper, so no completion/finalization code can run.
	spec.Command = `printf incomplete > sdk/version; kill -KILL -- -$$`
	if output, code := runCachePublication(t, cache, t.TempDir(), spec); code == 0 {
		t.Fatalf("interrupted writer became successful: %s", output)
	}
	spec.Command = "test ! -e sdk/version"
	if output, code := runCachePublication(t, cache, t.TempDir(), spec); code != 0 {
		t.Fatalf("interrupted writer's partial SDK became reusable (exit %d): %s", code, strings.TrimSpace(string(output)))
	}
}

func TestSDKCacheStepInterruptionRemainsUncommitted(t *testing.T) {
	interruptions := []struct {
		name    string
		command string
		timeout int
		code    int
	}{
		{"deadline", "printf incomplete > sdk/version; sleep 8", 1, 124},
		{"signal", `printf incomplete > sdk/version; kill -TERM "$BASHPID"`, 0, 143},
	}
	for _, interruption := range interruptions {
		for _, always := range []bool{false, true} {
			name := interruption.name
			if always {
				name += "-always-after-failure"
			}
			t.Run(name, func(t *testing.T) {
				cache, workspace := t.TempDir(), t.TempDir()
				spec := RunSpec{CachePaths: []string{"sdk"}, Command: "printf prepared > sdk/version"}
				if output, code := runCachePublication(t, cache, t.TempDir(), spec); code != 0 {
					t.Fatalf("prepare SDK exit %d: %s", code, output)
				}
				step := preflightapi.RunStepSpec{Name: "interrupted-writer", Command: interruption.command, TimeoutSeconds: interruption.timeout, Always: always}
				spec.Command = ""
				spec.Steps = []preflightapi.RunStepSpec{step}
				want := interruption.code
				if always {
					spec.Steps = append([]preflightapi.RunStepSpec{{Name: "failed-test", Command: "exit 17"}}, spec.Steps...)
					want = 17 // The interruption must be sticky despite this earlier error.
				}
				spec.Steps = append(spec.Steps, preflightapi.RunStepSpec{Name: "cleanup", Command: "printf cleanup >> calls", Always: true})
				if output, code := runCachePublication(t, cache, workspace, spec); code != want {
					t.Fatalf("interrupted writer exit %d, want %d: %s", code, want, output)
				}
				if got, err := os.ReadFile(filepath.Join(workspace, "calls")); err != nil || string(got) != "cleanup" {
					t.Fatalf("cleanup did not execute exactly once: %q, %v", got, err)
				}
				consumer := RunSpec{CachePaths: []string{"sdk"}, Command: "test ! -e sdk/version"}
				if output, code := runCachePublication(t, cache, t.TempDir(), consumer); code != 0 {
					t.Fatalf("partial SDK from interrupted %s was committed (consumer exit %d): %s", name, code, output)
				}
			})
		}
	}
}

func TestSDKCacheCompletedAlwaysFailurePreservesPreparedSDK(t *testing.T) {
	cache := t.TempDir()
	spec := RunSpec{CachePaths: []string{"sdk"}, Command: "printf prepared > sdk/version"}
	if output, code := runCachePublication(t, cache, t.TempDir(), spec); code != 0 {
		t.Fatalf("prepare SDK exit %d: %s", code, output)
	}
	spec.Command = ""
	spec.Steps = []preflightapi.RunStepSpec{
		{Name: "failed-test", Command: "exit 17"},
		{Name: "failed-cleanup", Command: "exit 23", Always: true},
	}
	if output, code := runCachePublication(t, cache, t.TempDir(), spec); code != 17 {
		t.Fatalf("ordinary failure result changed to %d: %s", code, output)
	}
	spec.Steps = nil
	spec.Command = `test "$(cat sdk/version)" = prepared`
	if output, code := runCachePublication(t, cache, t.TempDir(), spec); code != 0 {
		t.Fatalf("completed ordinary cleanup failure invalidated prepared SDK (exit %d): %s", code, output)
	}
}
