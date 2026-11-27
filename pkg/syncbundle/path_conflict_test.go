package syncbundle

import (
	"strings"
	"testing"
)

// A path and its descendant cannot both exist. Sorting puts siblings such as
// "d.txt" between them, so a neighbour check alone misses the conflict.
func TestManifestRejectsNestedEntriesSortedApart(t *testing.T) {
	file := func(rel string) Entry { return Entry{Path: rel, Type: EntryTypeFile, Mode: 0o644} }
	entries := []Entry{file("d"), file("d.txt"), file("d/x")}
	for name, manifest := range map[string]Manifest{
		"baseline":  {Version: FormatVersion, Baseline: entries, Deleted: []string{"d"}},
		"workspace": {Version: FormatVersion, Workspace: entries},
	} {
		if err := ValidateManifest(manifest); err == nil || !strings.Contains(err.Error(), `entry "d" conflicts with nested entry "d/x"`) {
			t.Fatalf("%s: error = %v, want nested conflict", name, err)
		}
	}
}
