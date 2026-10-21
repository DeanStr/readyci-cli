package syncbundle

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestSubmoduleProvenanceChangesBothContentIdentities(t *testing.T) {
	entry := ArchiveEntry{Entry: Entry{Path: "vendor/lib/file.txt", Type: EntryTypeFile, Mode: 0o644, Size: 4}, Data: []byte("same")}
	left := ContentBinding{Submodules: []Submodule{{Path: "vendor/lib", Commit: strings.Repeat("a", 40)}}}
	right := ContentBinding{Submodules: []Submodule{{Path: "vendor/lib", Commit: strings.Repeat("b", 40)}}}
	leftKey, err := BaselineContentKey([]ArchiveEntry{entry}, left)
	if err != nil {
		t.Fatal(err)
	}
	rightKey, err := BaselineContentKey([]ArchiveEntry{entry}, right)
	if err != nil {
		t.Fatal(err)
	}
	leftDigest, err := WorkspaceContentDigest([]ArchiveEntry{entry}, nil, nil, left)
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := WorkspaceContentDigest([]ArchiveEntry{entry}, nil, nil, right)
	if err != nil {
		t.Fatal(err)
	}
	if leftKey == rightKey || leftDigest == rightDigest {
		t.Fatal("different commits with identical files shared a content identity")
	}
	if key, err := BaselineContentKey(nil, left); err != nil || key == "" {
		t.Fatalf("empty-tree module lost baseline identity: key=%q error=%v", key, err)
	}
}

func TestSubmoduleMetadataRejectsUnsafeOrAmbiguousPins(t *testing.T) {
	pin := Submodule{Path: "vendor/lib", Commit: strings.Repeat("a", 40)}
	for name, pins := range map[string][]Submodule{
		"absolute":           {{Path: "/vendor/lib", Commit: pin.Commit}},
		"escape":             {{Path: "../lib", Commit: pin.Commit}},
		"not normalized":     {{Path: "vendor/../lib", Commit: pin.Commit}},
		"git control":        {{Path: "vendor/.GiT/lib", Commit: pin.Commit}},
		"duplicate":          {pin, pin},
		"unsorted":           {{Path: "z", Commit: pin.Commit}, pin},
		"abbreviated commit": {{Path: pin.Path, Commit: "aaaaaaa"}},
		"uppercase commit":   {{Path: pin.Path, Commit: strings.Repeat("A", 40)}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateManifest(Manifest{Version: FormatVersion, Submodules: pins}); err == nil {
				t.Fatal("accepted invalid submodule metadata")
			}
		})
	}
	for _, ancestor := range []string{"vendor", "vendor/lib"} {
		manifest := Manifest{Version: FormatVersion, Submodules: []Submodule{pin}, Baseline: []Entry{{Path: ancestor, Type: EntryTypeFile, Mode: 0o644}}}
		if err := ValidateManifest(manifest); err == nil {
			t.Fatalf("pin conflicts with file %s", ancestor)
		}
	}
}

func TestSubmodulePinsSurviveBaselineCachingAndDeltaRehydration(t *testing.T) {
	entry := ArchiveEntry{Entry: Entry{Path: "vendor/lib/file.txt", Type: EntryTypeFile, Mode: 0o644, Size: 4}, Data: []byte("same")}
	pins := []Submodule{{Path: "vendor/lib", Commit: strings.Repeat("a", 40)}}
	binding := ContentBinding{Submodules: pins}
	key, err := BaselineContentKey([]ArchiveEntry{entry}, binding)
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Version: FormatVersion, Submodules: pins, BaselineKey: key, Baseline: []Entry{entry.Entry}}
	var full, baseline bytes.Buffer
	if err := WriteBundle(&full, manifest, []ArchiveEntry{entry}, nil); err != nil {
		t.Fatal(err)
	}
	baseManifest, err := WriteBaselineBundle(bytes.NewReader(full.Bytes()), &baseline)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(baseManifest.Submodules, pins) || baseManifest.BaselineKey != key {
		t.Fatalf("baseline lost pins: %+v", baseManifest)
	}
	manifest, err = ReadManifest(bytes.NewReader(full.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	manifest.BaselineRef = true
	var delta, merged bytes.Buffer
	if err := WriteBundle(&delta, manifest, nil, nil); err != nil {
		t.Fatal(err)
	}
	rehydrated, err := RehydrateDeltaBundle(bytes.NewReader(baseline.Bytes()), bytes.NewReader(delta.Bytes()), &merged)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rehydrated.Submodules, pins) || rehydrated.SnapshotDigest != manifest.SnapshotDigest {
		t.Fatalf("rehydration changed pins or snapshot: %+v", rehydrated)
	}
	manifest.Submodules = []Submodule{{Path: "vendor/lib", Commit: strings.Repeat("b", 40)}}
	manifest.SnapshotDigest, err = WorkspaceContentDigest([]ArchiveEntry{entry}, nil, nil, manifest.ContentBinding())
	if err != nil {
		t.Fatal(err)
	}
	delta.Reset()
	if err := WriteBundle(&delta, manifest, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := RehydrateDeltaBundle(bytes.NewReader(baseline.Bytes()), bytes.NewReader(delta.Bytes()), &bytes.Buffer{}); err == nil {
		t.Fatal("accepted baseline whose pins differ from the delta")
	}
}
