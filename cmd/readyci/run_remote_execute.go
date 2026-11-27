package main

import (
	"fmt"
	"os"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
)

func printSourceUploadSummary(bundle syncbundlebuild.BuildResult) {
	fmt.Fprintf(os.Stderr, "readyci: source upload: %d source entries, %d untracked included, %d untracked omitted by sync rules or mode, %d tracked omitted by sync rules\n", len(bundle.Manifest.Baseline)+len(bundle.Manifest.Workspace), len(bundle.UntrackedFiles), len(bundle.OmittedUntrackedFiles), len(bundle.OmittedTrackedFiles))
	if len(bundle.OmittedUntrackedFiles) > 0 {
		sample := bundle.OmittedUntrackedFiles
		if len(sample) > 5 {
			sample = sample[:5]
		}
		fmt.Fprintf(os.Stderr, "readyci: omitted untracked paths (first %d): %q; run `readyci sync --dry-run` to inspect selection\n", len(sample), sample)
	}
	if len(bundle.OmittedTrackedFiles) > 0 {
		sample := bundle.OmittedTrackedFiles
		if len(sample) > 5 {
			sample = sample[:5]
		}
		fmt.Fprintf(os.Stderr, "readyci: omitted tracked paths (first %d): %q; these committed files are absent from the remote workspace\n", len(sample), sample)
	}
	report := syncDryRunReport{UntrackedCount: len(bundle.UntrackedFiles), UntrackedFiles: bundle.UntrackedFiles}
	for _, warning := range syncDryRunWarnings(report, bundle.Manifest) {
		fmt.Fprintf(os.Stderr, "readyci: warning: %s\n", warning)
	}
}
