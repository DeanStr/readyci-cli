package netpolicy

import (
	"fmt"
	"testing"
)

func TestAllowlistEntryBudget(t *testing.T) {
	entries := make([]string, MaxAllowlistEntries+1)
	for i := range entries {
		entries[i] = fmt.Sprintf("host-%d.example.test", i)
	}
	if _, err := NormalizeAllowlistEntries(entries); err == nil {
		t.Fatal("oversized allowlist accepted")
	}
	if _, err := NormalizeAllowlistEntries(entries[:MaxAllowlistEntries]); err != nil {
		t.Fatal(err)
	}
}
