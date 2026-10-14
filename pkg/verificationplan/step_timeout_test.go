package verificationplan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestStepDeadlineKillsTermResistantChildrenAndRunsCleanup(t *testing.T) {
	for _, testCommand := range []string{
		"trap '' TERM; (sleep 8; printf leaked > leaked.txt) & wait",
		"bash -c 'trap \"\" TERM; sleep 8; printf leaked > leaked.txt' & wait",
	} {
		t.Run(testCommand, func(t *testing.T) {
			command, err := CommandForRunSpec(RunSpec{Steps: []preflightapi.RunStepSpec{
				{Name: "test", TimeoutSeconds: 1, Command: testCommand},
				{Name: "after", Command: "printf after > after.txt"},
				{Name: "cleanup", Command: "printf cleanup > cleanup.txt", Always: true},
			}})
			if err != nil {
				t.Fatal(err)
			}
			workdir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/bash", "-lc", command)
			cmd.Dir = workdir
			start := time.Now()
			out, err := cmd.CombinedOutput()
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 124 {
				t.Fatalf("exit = %v, want timeout 124; output=%s", err, out)
			}
			if elapsed := time.Since(start); elapsed > 4*time.Second {
				t.Fatalf("one-second step exceeded termination grace: %s", elapsed)
			}
			if _, err := os.Stat(filepath.Join(workdir, "cleanup.txt")); err != nil {
				t.Fatalf("always step did not execute: %v", err)
			}
			for _, name := range []string{"after.txt", "leaked.txt"} {
				if _, err := os.Stat(filepath.Join(workdir, name)); !os.IsNotExist(err) {
					t.Fatalf("unexpected %s: %v", name, err)
				}
			}
		})
	}
}

func TestStepDeadlinePreservesCommandExitAndLocale(t *testing.T) {
	for _, code := range []string{"0", "7", "137"} {
		command, err := CommandForRunSpec(RunSpec{Steps: []preflightapi.RunStepSpec{{Name: "test", TimeoutSeconds: 10, Command: "printf '%s' \"${LC_ALL-unset}\"; printf stderr >&2; exit " + code}}})
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("/bin/bash", "-lc", command)
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		out, err := cmd.CombinedOutput()
		if !strings.Contains(string(out), "Cstderr") {
			t.Fatalf("locale or stderr lost: %s", out)
		}
		if code == "0" && err != nil {
			t.Fatal(err)
		}
		if code != "0" {
			exit, ok := err.(*exec.ExitError)
			if !ok || strings.TrimSpace(code) != strings.TrimSpace(strings.TrimPrefix(exit.Error(), "exit status ")) {
				t.Fatalf("command exit %s was changed: %v", code, err)
			}
		}
	}
}

func TestStepDeadlinePreservesStdin(t *testing.T) {
	cmd := exec.Command("/bin/bash", "-lc", StepCommand("read -r value; printf '%s' \"$value\"", 10))
	cmd.Stdin = strings.NewReader("step input\n")
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "step input" {
		t.Fatalf("stdin was not preserved: output=%q err=%v", out, err)
	}
}
