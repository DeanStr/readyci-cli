package syncbundlebuild

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func validatePlannedReviewedFixtures(fixtures []syncbundle.ReviewedFixture, baseline, workspace []plannedEntry, tracked []string, mode sourceContentMode) error {
	if len(fixtures) == 0 {
		return nil
	}
	known := make(map[string]bool, len(baseline)+len(tracked))
	for _, rel := range tracked {
		known[rel] = true
	}
	for _, item := range baseline {
		known[item.entry.Path] = true
	}
	for _, fixture := range fixtures {
		if !known[fixture.Path] {
			return fmt.Errorf("reviewed fixture %q must be a regular Git-tracked file", fixture.Path)
		}
		found := false
		for _, entries := range [][]plannedEntry{baseline, workspace} {
			for _, item := range entries {
				if item.entry.Path != fixture.Path {
					continue
				}
				found = true
				if item.entry.Type != syncbundle.EntryTypeFile {
					return fmt.Errorf("reviewed fixture %q must be a regular Git-tracked file", fixture.Path)
				}
				digest := item.digest
				if mode == retainSourceContent {
					digest = sha256.Sum256(item.data)
				}
				if hex.EncodeToString(digest[:]) != fixture.SHA256 {
					return fmt.Errorf("reviewed fixture %q does not match its reviewed sha256; review the selected source bytes explicitly", fixture.Path)
				}
			}
		}
		if !found {
			return fmt.Errorf("reviewed fixture %q is missing from selected source", fixture.Path)
		}
	}
	return nil
}
