package syncbundle

import (
	"bytes"
	"strings"
	"testing"
)

func TestArchiveEntriesEqualValidatesInMemoryBytes(t *testing.T) {
	entry := Entry{Path: "test.txt", Type: EntryTypeFile, Mode: 0644, Size: 4}
	for _, tc := range []struct {
		name        string
		left, right []byte
		equal       bool
		wantError   string
	}{
		{name: "equal", left: []byte("same"), right: []byte("same"), equal: true},
		{name: "different", left: []byte("same"), right: []byte("edit")},
		{name: "invalid left length", left: []byte("bad"), right: []byte("same"), wantError: "data size 3 does not match manifest size 4"},
		{name: "invalid right length", left: []byte("same"), right: []byte("bad"), wantError: "data size 3 does not match manifest size 4"},
		{name: "matching invalid lengths", left: []byte("bad"), right: []byte("bad"), wantError: "data size 3 does not match manifest size 4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			equal, err := archiveEntriesEqual(ArchiveEntry{Entry: entry, Data: tc.left}, ArchiveEntry{Entry: entry, Data: tc.right})
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error=%v, want %q", err, tc.wantError)
				}
				return
			}
			if err != nil || equal != tc.equal {
				t.Fatalf("equal=%t error=%v, want %t", equal, err, tc.equal)
			}
		})
	}
}

func BenchmarkWorkspaceChangedFilesUnchangedData(b *testing.B) {
	data := bytes.Repeat([]byte("unchanged"), 1<<20)
	entries := []ArchiveEntry{{Entry: Entry{Path: "payload.bin", Type: EntryTypeFile, Mode: 0644, Size: int64(len(data))}, Data: data}}
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		changed, err := WorkspaceChangedFiles(entries, nil, nil)
		if err != nil || len(changed) != 0 {
			b.Fatalf("changed=%v error=%v", changed, err)
		}
	}
}
