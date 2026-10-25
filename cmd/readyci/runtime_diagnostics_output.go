package main

import (
	"fmt"
	"sort"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func printRuntimeDiagnosticSummary(diagnostics preflightapi.RuntimeDiagnostics) {
	fmt.Printf("runtime_diagnostics: %s (%s)\n", sanitizeRunLogsSummaryText(diagnostics.InstanceID), sanitizeRunLogsSummaryText(diagnostics.Trigger))
	launcher := diagnostics.Launcher
	if launcher.Unavailable != "" {
		fmt.Printf("  launcher: %s\n", sanitizeRunLogsSummaryText(launcher.Unavailable))
	} else {
		fmt.Printf("  launcher: scope=%s exited=%t observed_before_teardown=%t", sanitizeRunLogsSummaryText(launcher.Scope), launcher.Exited, launcher.ObservedBeforeTeardown)
		if launcher.ExitCode != nil {
			fmt.Printf(" exit_code=%d", *launcher.ExitCode)
		}
		if launcher.Signal != "" {
			fmt.Printf(" signal=%s", sanitizeRunLogsSummaryText(launcher.Signal))
		}
		fmt.Println()
	}
	names := make([]string, 0, len(diagnostics.Logs))
	for name := range diagnostics.Logs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		log := diagnostics.Logs[name]
		if log.Unavailable != "" {
			fmt.Printf("  runtime_log %s: %s\n", sanitizeRunLogsSummaryText(name), sanitizeRunLogsSummaryText(log.Unavailable))
		} else {
			fmt.Printf("  runtime_log %s: retained=%d observed=%d truncated=%t\n", sanitizeRunLogsSummaryText(name), log.RetainedBytes, log.ObservedBytes, log.Truncated)
		}
	}
	fmt.Println("Runtime snapshots do not establish guest command completion or a guest OOM; use --json to inspect retained log bytes and limitations.")
}
