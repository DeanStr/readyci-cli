package verificationplan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

func TestStructuredStepsPreservePreparedRuntimeAndDependencyEnvironment(t *testing.T) {
	for _, timeout := range []int{0, 10} {
		t.Run(strconv.Itoa(timeout), func(t *testing.T) {
			root := t.TempDir()
			selected := filepath.Join(root, "prepared-bin")
			if err := os.Mkdir(selected, 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"yarn", "python"} {
				if err := os.WriteFile(filepath.Join(selected, name), []byte("#!/bin/sh\nprintf 'prepared-"+name+"\\n'\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			defaults := filepath.Join(root, "image-default-bin")
			if err := os.Mkdir(defaults, 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"yarn", "python"} {
				if err := os.WriteFile(filepath.Join(defaults, name), []byte("#!/bin/sh\nprintf 'image-default-"+name+"\\n'\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			// The producer's readyci-rust.sh prepends immutable image binaries.
			// A private login profile recreates that exact ordering behavior
			// without changing this machine's global image or user profile.
			if err := os.WriteFile(filepath.Join(root, ".bash_profile"), []byte("export PATH="+ShellQuoteArg(defaults)+":$PATH\n"), 0600); err != nil {
				t.Fatal(err)
			}
			command, err := CommandForRunSpec(RunSpec{PrepareCommands: []string{"export VIRTUAL_ENV=" + ShellQuoteArg(selected), "export PATH=" + ShellQuoteArg(selected) + ":$PATH"}, Steps: []preflightapi.RunStepSpec{{Name: "tests", Command: "yarn --version && python --version && test \"$VIRTUAL_ENV\" = " + ShellQuoteArg(selected), TimeoutSeconds: timeout}}})
			if err != nil {
				t.Fatal(err)
			}
			run := exec.Command("/bin/bash", "-lc", command)
			run.Dir = root
			run.Env = append(os.Environ(), "HOME="+root)
			output, err := run.CombinedOutput()
			if err != nil || !strings.Contains(string(output), "prepared-yarn") || !strings.Contains(string(output), "prepared-python") {
				t.Fatalf("nested step reset prepared runtime/dependency PATH: %v %s", err, output)
			}
		})
	}
}
