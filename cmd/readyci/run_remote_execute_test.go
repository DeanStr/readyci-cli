package main

import (
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/verificationplan"
)

func TestCreateRunCommandAndStepsPreservesCanonicalStoredStepWrapper(t *testing.T) {
	steps := []preflightapi.RunStepSpec{
		{Name: "deps", Command: "npm ci"},
		{Name: "test", Command: "npm test", Env: map[string]string{"NODE_ENV": "test"}, TimeoutSeconds: 60},
	}
	wrapper, err := verificationplan.CommandForRunSpec(verificationplan.RunSpec{Steps: steps})
	if err != nil {
		t.Fatalf("build wrapper: %v", err)
	}

	command, createSteps := createRunCommandAndSteps(remoteRunOptions{
		Command: wrapper,
		Steps:   steps,
	}, wrapper)
	if command != wrapper {
		t.Fatalf("command = %q, want stored wrapper", command)
	}
	if len(createSteps) != len(steps) || createSteps[0].Name != "deps" || createSteps[1].TimeoutSeconds != 60 {
		t.Fatalf("create steps = %+v, want preserved legacy step metadata", createSteps)
	}
	createSteps[1].Env["NODE_ENV"] = "mutated"
	if steps[1].Env["NODE_ENV"] != "test" {
		t.Fatalf("create steps shared env map with original steps: %+v", steps)
	}
}

func TestCreateRunCommandAndStepsDropsNonCanonicalStoredWrapper(t *testing.T) {
	steps := []preflightapi.RunStepSpec{{Name: "unit", Command: "go test ./..."}}
	command, createSteps := createRunCommandAndSteps(remoteRunOptions{
		Command: "echo prepare\ngo test ./...",
		Steps:   steps,
	}, "echo prepare\ngo test ./...")
	if command == "" {
		t.Fatal("command is empty, want stored non-canonical wrapper")
	}
	if len(createSteps) != 0 {
		t.Fatalf("create steps = %+v, want dropped metadata for non-canonical wrapper", createSteps)
	}
}
