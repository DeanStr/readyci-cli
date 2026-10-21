package syncbundle

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestManifestAllowsInternalParentSymlink(t *testing.T) {
	manifest := Manifest{Version: FormatVersion, Baseline: []Entry{
		{Path: "target.txt", Type: EntryTypeFile, Mode: 0o644},
		{Path: "nested/link", Type: EntryTypeSymlink, Mode: 0o644, Linkname: "../target.txt"},
	}}
	if err := validateManifest(manifest); err != nil {
		t.Fatalf("safe internal parent link rejected: %v", err)
	}
}

func TestManifestSymlinkContainment(t *testing.T) {
	file := func(rel string) Entry { return Entry{Path: rel, Type: EntryTypeFile, Mode: 0o644} }
	link := func(rel, target string) Entry {
		return Entry{Path: rel, Type: EntryTypeSymlink, Mode: 0o644, Linkname: target}
	}
	for _, tt := range []struct {
		name                string
		baseline, workspace []Entry
		deleted             []string
		wantError           string
	}{
		{name: "internal parent", baseline: []Entry{file("target"), link("dir/link", "../target")}},
		{name: "dangling internal target", baseline: []Entry{link("dir/link", "../generated/output")}},
		{name: "root directory target", baseline: []Entry{link("dir/link", "..")}},
		{name: "literal target whitespace", baseline: []Entry{file("target "), link("dir/link", "../target ")}},
		{name: "expand before parent", baseline: []Entry{file("deep/path/child"), file("target"), link("alias", "deep/path"), link("link", "alias/../../target")}},
		{name: "repeat resolved alias without cycle", baseline: []Entry{file("dir/target"), link("alias", "dir"), link("link", "alias/../alias/target")}},
		{name: "parent through symlink hop escapes", baseline: []Entry{link("alias", "."), link("link", "alias/../outside")}, wantError: "escapes workspace"},
		{name: "nested parent escape", baseline: []Entry{link("dir/link", "../../outside")}, wantError: "escapes workspace"},
		{name: "direct cycle", baseline: []Entry{link("link", "link")}, wantError: "symlink cycle"},
		{name: "indirect cycle", baseline: []Entry{link("a", "dir/b"), link("dir/b", "../a")}, wantError: "symlink cycle"},
		{name: "git target", baseline: []Entry{link("dir/link", "../.git/config")}, wantError: "git control metadata"},
		{name: "git target before parent", baseline: []Entry{link("link", ".git/../target")}, wantError: "git control metadata"},
		{name: "git target through alias", baseline: []Entry{link("alias", "."), link("link", "alias/.GiT/HEAD")}, wantError: "git control metadata"},
		{name: "file before parent", baseline: []Entry{file("file"), link("link", "file/../target")}, wantError: "regular file"},
		{name: "file before current", baseline: []Entry{file("file"), link("link", "file/.")}, wantError: "regular file"},
		{name: "file before trailing slash", baseline: []Entry{file("file"), link("link", "file/")}, wantError: "regular file"},
		{name: "file through alias", baseline: []Entry{file("file"), link("alias", "file"), link("link", "alias/../target")}, wantError: "regular file"},
		{name: "trailing slash through file alias", baseline: []Entry{file("file"), link("alias", "file"), link("link", "alias/")}, wantError: "regular file"},
		{name: "overlay changes inherited hop", baseline: []Entry{file("deep/path/child"), link("alias", "deep/path"), link("link", "alias/../../target")}, workspace: []Entry{link("alias", ".")}, wantError: "workspace symlink"},
		{name: "overlay creates cycle with baseline", baseline: []Entry{link("a", "b"), file("b")}, workspace: []Entry{link("b", "a")}, wantError: "workspace symlink"},
		{name: "baseline escape removed by overlay", baseline: []Entry{link("link", "../outside")}, deleted: []string{"link"}, wantError: "baseline symlink"},
		{name: "overlay target through baseline", baseline: []Entry{file("data/target"), link("alias", "data")}, workspace: []Entry{link("dir/link", "../alias/target")}},
		{name: "deleted alias is dangling", baseline: []Entry{file("data/target"), link("alias", "data")}, workspace: []Entry{link("dir/link", "../alias/target")}, deleted: []string{"alias"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			manifest := Manifest{Version: FormatVersion, Baseline: tt.baseline, Workspace: tt.workspace, Deleted: tt.deleted}
			err := ValidateManifest(manifest)
			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("safe namespace rejected: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v; want %q", err, tt.wantError)
			}
		})
	}
}

func TestManifestSymlinkHopLimit(t *testing.T) {
	for _, count := range []int{maxSymlinkHops, maxSymlinkHops + 1} {
		entries := make([]Entry, 0, count)
		for i := 0; i < count; i++ {
			target := "target"
			if i+1 < count {
				target = fmt.Sprintf("link-%02d", i+1)
			}
			entries = append(entries, Entry{Path: fmt.Sprintf("link-%02d", i), Type: EntryTypeSymlink, Linkname: target})
		}
		err := ValidateManifest(Manifest{Version: FormatVersion, Baseline: entries})
		if count <= maxSymlinkHops && err != nil {
			t.Fatalf("%d hops rejected: %v", count, err)
		}
		if count > maxSymlinkHops && (err == nil || !strings.Contains(err.Error(), "symlink hops")) {
			t.Fatalf("%d hops error = %v", count, err)
		}
	}
}

func TestUntrustedBundleRejectsSymlinkHopEscape(t *testing.T) {
	manifest := Manifest{Version: FormatVersion, Workspace: []Entry{
		{Path: "alias", Type: EntryTypeSymlink, Linkname: "."},
		{Path: "link", Type: EntryTypeSymlink, Linkname: "alias/../outside"},
	}}
	var bundle bytes.Buffer
	writeTestBundleWithSymlinks(t, &bundle, manifest, nil, map[string]string{"workspace/alias": ".", "workspace/link": "alias/../outside"})
	for _, read := range []struct {
		name string
		fn   func() error
	}{
		{"manifest", func() error { _, err := ReadManifest(bytes.NewReader(bundle.Bytes())); return err }},
		{"full bundle", func() error { return Validate(bytes.NewReader(bundle.Bytes())) }},
	} {
		t.Run(read.name, func(t *testing.T) {
			if err := read.fn(); err == nil || !strings.Contains(err.Error(), "escapes workspace") {
				t.Fatalf("untrusted escape admitted: %v", err)
			}
		})
	}
	manifest.BaselineRef = true
	manifest.BaselineKey = strings.Repeat("a", 64)
	bundle.Reset()
	writeTestBundleWithSymlinks(t, &bundle, manifest, nil, map[string]string{"workspace/alias": ".", "workspace/link": "alias/../outside"})
	if err := Validate(bytes.NewReader(bundle.Bytes())); err == nil || !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("delta escape admitted: %v", err)
	}
}

func TestSymlinkTargetLengthBound(t *testing.T) {
	if err := ValidateSymlinkTargetSyntax(strings.Repeat("a", 4095)); err != nil {
		t.Fatalf("maximum target length rejected: %v", err)
	}
	if err := ValidateSymlinkTargetSyntax(strings.Repeat("a", 4096)); err == nil || !strings.Contains(err.Error(), "4095 bytes") {
		t.Fatalf("oversized symlink target error = %v", err)
	}
}

func BenchmarkValidateManifestSharedSymlinkTargets(b *testing.B) {
	entries := []Entry{{Path: "deep", Type: EntryTypeSymlink, Linkname: strings.Repeat("a/", 1000) + "z"}}
	for i := 0; i < 500; i++ {
		entries = append(entries, Entry{Path: fmt.Sprintf("link-%04d", i), Type: EntryTypeSymlink, Linkname: "deep"})
	}
	manifest := Manifest{Version: FormatVersion, Workspace: entries}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := ValidateManifest(manifest); err != nil {
			b.Fatal(err)
		}
	}
}

func TestManifestRepeatedSymlinkHopLimit(t *testing.T) {
	for _, references := range []int{maxSymlinkHops - 1, maxSymlinkHops} {
		manifest := Manifest{Version: FormatVersion, Baseline: []Entry{
			{Path: "alias", Type: EntryTypeSymlink, Linkname: "."},
			{Path: "link", Type: EntryTypeSymlink, Linkname: strings.Repeat("alias/", references) + "target"},
		}}
		err := ValidateManifest(manifest)
		if references+1 <= maxSymlinkHops && err != nil {
			t.Fatalf("%d total hops rejected: %v", references+1, err)
		}
		if references+1 > maxSymlinkHops && (err == nil || !strings.Contains(err.Error(), "symlink hops")) {
			t.Fatalf("%d total hops error = %v", references+1, err)
		}
	}
}
