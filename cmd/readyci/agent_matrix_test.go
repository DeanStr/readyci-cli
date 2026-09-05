package main

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestExecuteAgentCheckMatrixHonorsMaxParallel(t *testing.T) {
	plan := checkRunPlan{
		Name: "ci",
		Options: remoteRunOptions{
			CommandAlias: "ci",
		},
		Matrix: config.MatrixConfig{
			Axes: map[string][]string{
				"SHARD": {"A", "B", "C"},
			},
			MaxParallel: 2,
		},
	}
	started := make(chan string, 3)
	release := make(chan struct{})
	run := func(options remoteRunOptions, retries int) (remoteRunResult, error) {
		started <- options.MatrixVariant
		<-release
		return remoteRunResult{RunID: "run_" + options.MatrixVariant, Status: preflightapi.StatusSucceeded}, nil
	}

	done := make(chan struct {
		reports []agentVerifyRunReport
		err     error
	}, 1)
	go func() {
		reports, err := executeAgentCheckMatrixWithRunner(plan, run)
		done <- struct {
			reports []agentVerifyRunReport
			err     error
		}{reports: reports, err: err}
	}()

	waitForStartedVariants(t, started, "SHARD=A", "SHARD=B")
	time.Sleep(20 * time.Millisecond)
	select {
	case variant := <-started:
		t.Fatalf("max_parallel scheduled %s before an active variant completed", variant)
	default:
	}
	close(release)

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("agent matrix error = %v, want nil", result.err)
		}
		if len(result.reports) != 3 {
			t.Fatalf("agent reports = %+v, want all 3 variants", result.reports)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent matrix max_parallel did not finish")
	}
}

func TestExecuteAgentCheckMatrixFailFastStopsScheduling(t *testing.T) {
	plan := checkRunPlan{
		Name: "ci",
		Options: remoteRunOptions{
			CommandAlias: "ci",
		},
		Matrix: config.MatrixConfig{
			Axes: map[string][]string{
				"SHARD": {"A", "B", "C"},
			},
			FailFast:    true,
			MaxParallel: 2,
		},
	}
	started := make(chan string, 3)
	releaseB := make(chan struct{})
	run := func(options remoteRunOptions, retries int) (remoteRunResult, error) {
		started <- options.MatrixVariant
		switch options.MatrixVariant {
		case "SHARD=A":
			return remoteRunResult{RunID: "run_a", Status: preflightapi.StatusFailed, ExitCode: 2}, exitCodeError{code: 2}
		case "SHARD=B":
			<-releaseB
			return remoteRunResult{RunID: "run_b", Status: preflightapi.StatusSucceeded}, nil
		default:
			return remoteRunResult{RunID: "run_c", Status: preflightapi.StatusSucceeded}, nil
		}
	}

	done := make(chan struct {
		reports []agentVerifyRunReport
		err     error
	}, 1)
	go func() {
		reports, err := executeAgentCheckMatrixWithRunner(plan, run)
		done <- struct {
			reports []agentVerifyRunReport
			err     error
		}{reports: reports, err: err}
	}()

	waitForStartedVariants(t, started, "SHARD=A", "SHARD=B")
	time.Sleep(20 * time.Millisecond)
	select {
	case variant := <-started:
		t.Fatalf("fail_fast scheduled %s before blocked sibling completed", variant)
	default:
	}
	close(releaseB)

	select {
	case result := <-done:
		var exitErr exitCodeError
		if !errors.As(result.err, &exitErr) || exitErr.code != 2 {
			t.Fatalf("agent matrix error = %v, want exit code 2", result.err)
		}
		if len(result.reports) != 2 {
			t.Fatalf("agent reports = %+v, want only already-started variants", result.reports)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent matrix fail_fast did not finish")
	}
}

func TestExecuteAgentAffectedPlansSkipsStatusSuccessAfterFailure(t *testing.T) {
	plans := []affectedCheckPlan{
		{Plan: checkRunPlan{Name: "first"}},
		{Plan: checkRunPlan{Name: "second", When: &checkWhenReport{Status: "success"}}},
	}
	var mu sync.Mutex
	var ran []string
	run := func(plan checkRunPlan) ([]agentVerifyRunReport, error) {
		mu.Lock()
		ran = append(ran, plan.Name)
		mu.Unlock()
		return []agentVerifyRunReport{{Check: plan.Name, Status: preflightapi.StatusFailed}}, exitCodeError{code: 1}
	}

	reports, err := executeAgentAffectedPlansWithRunner(plans, "token", "base", agentIntentPrePR, "verify", run)
	var exitErr exitCodeError
	if !errors.As(err, &exitErr) || exitErr.code != 1 {
		t.Fatalf("agent affected error = %v, want first check exit code 1", err)
	}
	if len(reports) != 1 || reports[0].Check != "first" || reports[0].Phase != "verify" {
		t.Fatalf("agent affected reports = %+v, want only first verify report", reports)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 1 || ran[0] != "first" {
		t.Fatalf("agent affected ran %v, want only first check", ran)
	}
}
