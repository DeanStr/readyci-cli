package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func executeCheckMatrix(options remoteRunOptions, matrix config.MatrixConfig, retries int) error {
	return executeSessionPlans([]checkRunPlan{{Name: defaultString(options.CommandAlias, "check"), Options: options, Matrix: matrix, Retries: retries}}, nil, options.APIToken, false)
}

func watchCheck(plan checkRunPlan, warm []checkRunPlan, token string, interval time.Duration) error {
	options := plan.Options
	if interval <= 0 {
		return cliConfigError("--watch-interval must be positive", nil)
	}
	ctx, stop := signalContext()
	defer stop()

	fingerprint, err := workspaceFingerprint(ctx, workspaceFingerprintOptions{
		ExcludePatterns: options.SyncExclude, ReviewedFixtures: append([]syncbundle.ReviewedFixture(nil), options.SyncReviewedFixtures...),
		IncludeUntracked: options.IncludeUntracked,
		ChangeMode:       options.ChangeMode,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "readyci: watching %s every %s\n", displayRunName(options), interval)
	if err := executeSessionPlans([]checkRunPlan{plan}, warm, token, false); err != nil {
		if !isSuccessfulExit(err) {
			var exitErr exitCodeError
			if !errors.As(err, &exitErr) {
				return err
			}
		}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			next, err := workspaceFingerprint(ctx, workspaceFingerprintOptions{
				ExcludePatterns: options.SyncExclude, ReviewedFixtures: append([]syncbundle.ReviewedFixture(nil), options.SyncReviewedFixtures...),
				IncludeUntracked: options.IncludeUntracked,
				ChangeMode:       options.ChangeMode,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "readyci: watch skipped: %v\n", err)
				continue
			}
			if next == fingerprint {
				continue
			}
			fingerprint = next
			fmt.Fprintln(os.Stderr, "readyci: change detected")
			if err := executeSessionPlans([]checkRunPlan{plan}, warm, token, false); err != nil {
				if !isSuccessfulExit(err) {
					var exitErr exitCodeError
					if errors.As(err, &exitErr) {
						continue
					}
					return err
				}
			}
		}
	}
}

func isSuccessfulExit(err error) bool {
	var exitErr exitCodeError
	return errors.As(err, &exitErr) && exitErr.code == 0
}
