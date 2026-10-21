package syncbundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func reviewedFixtureForTest(path string, data []byte) ReviewedFixture {
	digest := sha256.Sum256(data)
	return ReviewedFixture{Path: path, SHA256: hex.EncodeToString(digest[:])}
}

func reviewedEntryForTest(path string, data []byte) ArchiveEntry {
	return ArchiveEntry{Entry: Entry{Path: path, Type: EntryTypeFile, Mode: 0o644, Size: int64(len(data))}, Data: data}
}

func TestNormalizeReviewedFixturesRequiresExactPathsAndFullHashes(t *testing.T) {
	valid := reviewedFixtureForTest("testdata/.env.example", []byte("TEST_VALUE=fixture\n"))
	for name, fixture := range map[string]ReviewedFixture{
		"absolute":         {Path: "/testdata/.env", SHA256: valid.SHA256},
		"parent":           {Path: "../.env", SHA256: valid.SHA256},
		"noncanonical":     {Path: "testdata/../.env", SHA256: valid.SHA256},
		"leading dot":      {Path: "./testdata/.env", SHA256: valid.SHA256},
		"directory":        {Path: "testdata/", SHA256: valid.SHA256},
		"glob":             {Path: "testdata/.env*", SHA256: valid.SHA256},
		"glob alternation": {Path: "testdata/{one,two}/.env", SHA256: valid.SHA256},
		"git metadata":     {Path: "nested/.git/config", SHA256: valid.SHA256},
		"uppercase git":    {Path: ".GIT/config", SHA256: valid.SHA256},
		"control":          {Path: "testdata/.env\n", SHA256: valid.SHA256},
		"short hash":       {Path: valid.Path, SHA256: "1234"},
		"uppercase hash":   {Path: valid.Path, SHA256: strings.ToUpper(valid.SHA256)},
		"prefixed hash":    {Path: valid.Path, SHA256: "sha256:" + valid.SHA256},
		"hash space":       {Path: valid.Path, SHA256: valid.SHA256 + " "},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeReviewedFixtures([]ReviewedFixture{fixture}); err == nil {
				t.Fatal("accepted invalid reviewed fixture")
			}
		})
	}
	if _, err := NormalizeReviewedFixtures([]ReviewedFixture{valid, valid}); err == nil {
		t.Fatal("accepted duplicate reviewed path")
	}
	other := reviewedFixtureForTest("a/.env", []byte("other"))
	input := []ReviewedFixture{valid, other}
	normalized, err := NormalizeReviewedFixtures(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(normalized, []ReviewedFixture{other, valid}) || input[0] != valid {
		t.Fatalf("canonical=%+v input=%+v", normalized, input)
	}
}

func TestValidateReviewedFixtureContentsChecksEveryTransmittedVariant(t *testing.T) {
	data := []byte("TEST_VALUE=fixture\n")
	fixture := reviewedFixtureForTest("testdata/.env.example", data)
	entry := reviewedEntryForTest(fixture.Path, data)
	manifest := Manifest{ReviewedFixtures: []ReviewedFixture{fixture}, Baseline: []Entry{entry.Entry}}
	if err := ValidateReviewedFixtureContents(manifest, []ArchiveEntry{entry}, nil); err != nil {
		t.Fatalf("approved baseline: %v", err)
	}
	for name, mutate := range map[string]func(*Manifest, *[]ArchiveEntry, *[]ArchiveEntry){
		"changed baseline bytes": func(_ *Manifest, baseline, _ *[]ArchiveEntry) {
			(*baseline)[0].Data = bytes.Repeat([]byte("x"), len(data))
		},
		"old baseline overwritten": func(m *Manifest, baseline, workspace *[]ArchiveEntry) {
			(*baseline)[0].Data = bytes.Repeat([]byte("x"), len(data))
			m.Workspace = []Entry{entry.Entry}
			*workspace = []ArchiveEntry{entry}
		},
		"changed overlay bytes": func(m *Manifest, _ *[]ArchiveEntry, workspace *[]ArchiveEntry) {
			m.Workspace = []Entry{entry.Entry}
			changed := entry
			changed.Data = bytes.Repeat([]byte("x"), len(data))
			*workspace = []ArchiveEntry{changed}
		},
		"missing bytes": func(_ *Manifest, baseline, _ *[]ArchiveEntry) { *baseline = nil },
		"deleted fixture": func(m *Manifest, _ *[]ArchiveEntry, _ *[]ArchiveEntry) {
			m.Deleted = []string{fixture.Path}
		},
		"deleted ancestor": func(m *Manifest, _ *[]ArchiveEntry, _ *[]ArchiveEntry) {
			m.Deleted = []string{"testdata"}
		},
		"symlink baseline overwritten": func(m *Manifest, baseline, workspace *[]ArchiveEntry) {
			link := Entry{Path: fixture.Path, Type: EntryTypeSymlink, Mode: 0o777, Linkname: "other"}
			m.Baseline = []Entry{link}
			*baseline = []ArchiveEntry{{Entry: link}}
			m.Workspace = []Entry{entry.Entry}
			*workspace = []ArchiveEntry{entry}
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := manifest
			baseline := []ArchiveEntry{entry}
			var workspace []ArchiveEntry
			mutate(&candidate, &baseline, &workspace)
			if err := ValidateReviewedFixtureContents(candidate, baseline, workspace); err == nil {
				t.Fatal("accepted unreviewed or missing fixture contents")
			}
		})
	}
}

func TestValidateReviewedFixtureContentsChecksDeltaOverlayBeforeRehydration(t *testing.T) {
	data := []byte("fixture")
	fixture := reviewedFixtureForTest("testdata/.env.example", data)
	entry := reviewedEntryForTest(fixture.Path, data)
	manifest := Manifest{BaselineRef: true, ReviewedFixtures: []ReviewedFixture{fixture}, Baseline: []Entry{entry.Entry}}
	if err := ValidateReviewedFixtureContents(manifest, nil, nil); err != nil {
		t.Fatalf("external baseline may defer bytes until rehydration: %v", err)
	}
	manifest.Workspace = []Entry{entry.Entry}
	bad := entry
	bad.Data = []byte("changed")
	if err := ValidateReviewedFixtureContents(manifest, nil, []ArchiveEntry{bad}); err == nil {
		t.Fatal("delta overlay bypassed reviewed hash validation")
	}
}

func TestReviewedFixtureSourceIsFrozenBeforeWriting(t *testing.T) {
	data := []byte("TEST_VALUE=fixture\n")
	fixture := reviewedFixtureForTest("testdata/.env.example", data)
	source := filepath.Join(t.TempDir(), "fixture")
	if err := os.WriteFile(source, data, 0o644); err != nil {
		t.Fatal(err)
	}
	entry := reviewedEntryForTest(fixture.Path, data)
	entry.Source, entry.Data = source, nil
	manifest := Manifest{ReviewedFixtures: []ReviewedFixture{fixture}, Workspace: []Entry{entry.Entry}}
	_, frozen, err := freezeReviewedFixtureSources(manifest, nil, []ArchiveEntry{entry})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, bytes.Repeat([]byte("x"), len(data)), 0o644); err != nil {
		t.Fatal(err)
	}
	if frozen[0].Source != "" || !bytes.Equal(frozen[0].Data, data) {
		t.Fatalf("reviewed bytes were not frozen: %+v", frozen[0])
	}
	if err := ValidateReviewedFixtureContents(manifest, nil, frozen); err != nil {
		t.Fatal(err)
	}
	if _, _, err := freezeReviewedFixtureSources(manifest, nil, []ArchiveEntry{entry}); err == nil {
		t.Fatal("accepted modified source contents")
	}
	if err := os.WriteFile(source, data, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest.Version = FormatVersion
	writer := &reviewedFixtureMutationWriter{mutate: func() {
		if err := os.WriteFile(source, bytes.Repeat([]byte("x"), len(data)), 0o644); err != nil {
			t.Fatal(err)
		}
	}}
	if err := WriteBundle(writer, manifest, nil, []ArchiveEntry{entry}); err != nil {
		t.Fatalf("reviewed source changed after writing began: %v", err)
	}
	_, files, err := ReadBundleContents(bytes.NewReader(writer.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(files["workspace/"+fixture.Path].Data, data) {
		t.Fatal("writer transmitted changed source bytes after reviewing the file")
	}
}

type reviewedFixtureMutationWriter struct {
	bytes.Buffer
	mutate func()
}

func (writer *reviewedFixtureMutationWriter) Write(data []byte) (int, error) {
	if writer.mutate != nil {
		mutate := writer.mutate
		writer.mutate = nil
		mutate()
	}
	return writer.Buffer.Write(data)
}

func TestReviewedFixtureDeclarationMustMatchBoundPolicy(t *testing.T) {
	fixture := reviewedFixtureForTest("testdata/.env.example", []byte("fixture"))
	entry := reviewedEntryForTest(fixture.Path, []byte("fixture"))
	manifest := Manifest{
		ReviewedFixtures: []ReviewedFixture{fixture},
		Workspace:        []Entry{entry.Entry},
		SourcePolicy:     &SourcePolicyBinding{},
	}
	if err := ValidateReviewedFixtureContents(manifest, nil, []ArchiveEntry{entry}); err == nil {
		t.Fatal("accepted candidate exception absent from bound policy")
	}
	manifest.SourcePolicy.ReviewedFixtures = []ReviewedFixture{fixture}
	if err := ValidateReviewedFixtureContents(manifest, nil, []ArchiveEntry{entry}); err != nil {
		t.Fatalf("matching declarations: %v", err)
	}
}

func TestReviewedFixturesBindContentIdentityAndSurviveBaselineExtraction(t *testing.T) {
	base := reviewedEntryForTest("testdata/.env.base", []byte("BASE=fixture\n"))
	work := reviewedEntryForTest("testdata/.env.work", []byte("WORK=fixture\n"))
	fixtures := []ReviewedFixture{reviewedFixtureForTest(base.Entry.Path, base.Data), reviewedFixtureForTest(work.Entry.Path, work.Data)}
	binding := ContentBinding{ReviewedFixtures: fixtures}
	key, err := BaselineContentKey([]ArchiveEntry{base}, binding)
	if err != nil {
		t.Fatal(err)
	}
	baseOnlyKey, err := BaselineContentKey([]ArchiveEntry{base}, ContentBinding{ReviewedFixtures: fixtures[:1]})
	if err != nil || key != baseOnlyKey {
		t.Fatalf("workspace-only exception altered baseline key: %s %s %v", key, baseOnlyKey, err)
	}
	unreviewedKey, err := BaselineContentKey([]ArchiveEntry{base}, ContentBinding{})
	if err != nil || key == unreviewedKey {
		t.Fatalf("baseline key did not bind reviewed exception: %s %s %v", key, unreviewedKey, err)
	}
	withReview, err := WorkspaceContentDigest([]ArchiveEntry{base}, []ArchiveEntry{work}, nil, binding)
	if err != nil {
		t.Fatal(err)
	}
	withoutReview, err := WorkspaceContentDigest([]ArchiveEntry{base}, []ArchiveEntry{work}, nil, ContentBinding{})
	if err != nil || withReview == withoutReview {
		t.Fatalf("snapshot did not bind reviewed exceptions: %v", err)
	}
	manifest := Manifest{
		Version:          FormatVersion,
		BaselineKey:      key,
		ReviewedFixtures: fixtures,
		Baseline:         []Entry{base.Entry},
		Workspace:        []Entry{work.Entry},
	}
	var full bytes.Buffer
	if err := WriteBundle(&full, manifest, []ArchiveEntry{base}, []ArchiveEntry{work}); err != nil {
		t.Fatal(err)
	}
	fullManifest, err := ValidateWithManifest(bytes.NewReader(full.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	var cached bytes.Buffer
	baselineManifest, err := WriteBaselineBundle(bytes.NewReader(full.Bytes()), &cached)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(baselineManifest.ReviewedFixtures, fixtures[:1]) || baselineManifest.BaselineKey != key {
		t.Fatalf("baseline did not preserve applicable exception: %+v", baselineManifest)
	}
	if _, err := ValidateWithManifest(bytes.NewReader(cached.Bytes())); err != nil {
		t.Fatal(err)
	}
	deltaManifest := fullManifest
	deltaManifest.BaselineRef = true
	var delta bytes.Buffer
	if err := WriteBundle(&delta, deltaManifest, nil, []ArchiveEntry{work}); err != nil {
		t.Fatal(err)
	}
	var rehydrated bytes.Buffer
	restored, err := RehydrateDeltaBundle(bytes.NewReader(cached.Bytes()), bytes.NewReader(delta.Bytes()), &rehydrated)
	if err != nil {
		t.Fatal(err)
	}
	if restored.SnapshotDigest != fullManifest.SnapshotDigest || !reflect.DeepEqual(restored.ReviewedFixtures, fixtures) {
		t.Fatalf("rehydrated source changed reviewed identity: %+v", restored)
	}
	// A candidate cannot use the old cache key to change the reviewed hash of
	// bytes which are absent from the delta but present in the retained baseline.
	deltaManifest.ReviewedFixtures = append([]ReviewedFixture(nil), fixtures...)
	deltaManifest.ReviewedFixtures[0].SHA256 = strings.Repeat("b", 64)
	deltaManifest.SnapshotDigest, err = WorkspaceContentDigest([]ArchiveEntry{base}, []ArchiveEntry{work}, nil, deltaManifest.ContentBinding())
	if err != nil {
		t.Fatal(err)
	}
	delta.Reset()
	if err := WriteBundle(&delta, deltaManifest, nil, []ArchiveEntry{work}); err != nil {
		t.Fatal(err)
	}
	rehydrated.Reset()
	if _, err := RehydrateDeltaBundle(bytes.NewReader(cached.Bytes()), bytes.NewReader(delta.Bytes()), &rehydrated); err == nil {
		t.Fatal("rehydration accepted stale reviewed baseline bytes")
	}
}
