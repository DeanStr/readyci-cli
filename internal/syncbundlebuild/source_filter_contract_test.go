package syncbundlebuild

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func TestGeneratedDefaultsPreserveTrackedSource(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	// Go keeps the dist bootstrap implementation in this tracked directory.
	writeFile(t, repo, "src/cmd/dist/build.go", "package main\n")
	writeFile(t, repo, "target/testdata/source.txt", "tracked test fixture\n")
	writeFile(t, repo, "coverage/fixtures/source.txt", "tracked test fixture\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "tracked source under conventional output names")
	writeFile(t, repo, "dist/generated.js", "untracked generated output\n")
	writeFile(t, repo, "src/cmd/dist/generated.txt", "untracked generated output\n")
	writeFile(t, repo, "target/generated.txt", "untracked generated output\n")

	for _, mode := range []string{syncbundle.ChangeModeWorking, syncbundle.ChangeModeAll, syncbundle.ChangeModeStaged} {
		t.Run(mode, func(t *testing.T) {
			manifest, err := SnapshotWithOptions(context.Background(), repo, BuildOptions{ChangeMode: mode})
			if err != nil {
				t.Fatal(err)
			}
			paths := syncbundle.ManifestPaths(manifest)
			for _, required := range []string{"src/cmd/dist/build.go", "target/testdata/source.txt", "coverage/fixtures/source.txt"} {
				if !contains(paths, "baseline/"+required) {
					t.Errorf("tracked source %q was omitted by a generated-output default: %v", required, paths)
				}
			}
			for _, excluded := range []string{"dist/generated.js", "src/cmd/dist/generated.txt", "target/generated.txt"} {
				if contains(paths, "workspace/"+excluded) {
					t.Errorf("untracked generated output %q was included", excluded)
				}
			}
		})
	}
}

func TestReviewedTrackedDotenvFixtureBindsSelectedBytes(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	const rel = "tests/test_apps/.env"
	const contents = "READYCI_TEST_FIXTURE=public-placeholder\n"
	writeFile(t, repo, rel, contents)
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "public dotenv fixture")
	digest := sha256.Sum256([]byte(contents))
	options := BuildOptions{ReviewedFixtures: []syncbundle.ReviewedFixture{{Path: rel, SHA256: hex.EncodeToString(digest[:])}}}
	var bundle bytes.Buffer
	built, err := BuildWithOptions(context.Background(), repo, &bundle, options)
	if err != nil {
		t.Fatalf("reviewed tracked fixture: %v", err)
	}
	if !contains(syncbundle.ManifestPaths(built.Manifest), "baseline/"+rel) {
		t.Fatalf("reviewed fixture was silently omitted: %+v", built.Manifest)
	}
	workspace := t.TempDir()
	if _, err := extractBundleForTest(context.Background(), bytes.NewReader(bundle.Bytes()), workspace); err != nil {
		t.Fatal(err)
	}
	assertFile(t, workspace, rel, contents)
	snapshot, err := SnapshotWithOptions(context.Background(), repo, options)
	if err != nil || snapshot.SnapshotDigest != built.Manifest.SnapshotDigest {
		t.Fatalf("snapshot and upload disagree: snapshot=%+v err=%v", snapshot, err)
	}
	writeFile(t, repo, rel, "READYCI_TEST_FIXTURE=changed-without-review\n")
	for _, probe := range []struct {
		name string
		run  func() error
	}{
		{"upload", func() error {
			var output bytes.Buffer
			_, err := BuildWithOptions(context.Background(), repo, &output, options)
			if output.Len() != 0 {
				t.Fatal("unreviewed fixture bytes reached archive writer")
			}
			return err
		}},
		{"snapshot", func() error { _, err := SnapshotWithOptions(context.Background(), repo, options); return err }},
		{"changed-files", func() error { _, err := ChangedFiles(context.Background(), repo, options); return err }},
	} {
		t.Run(probe.name, func(t *testing.T) {
			if err := probe.run(); err == nil || !strings.Contains(err.Error(), "reviewed sha256") {
				t.Fatalf("changed reviewed fixture error = %v", err)
			}
		})
	}
	// Staged selection still describes the reviewed index bytes, independently
	// of changed, unreviewed working-tree contents.
	options.ChangeMode = syncbundle.ChangeModeStaged
	if _, err := SnapshotWithOptions(context.Background(), repo, options); err != nil {
		t.Fatalf("reviewed staged fixture rejected because working bytes changed: %v", err)
	}
}

func TestReviewedFixtureCannotOverrideExplicitExclusionsOrTracking(t *testing.T) {
	const contents = "READYCI_TEST_FIXTURE=public-placeholder\n"
	digest := sha256.Sum256([]byte(contents))
	for _, kind := range []string{"untracked", "directory", "explicit-exclude", "project-ignore", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			repo := t.TempDir()
			initRepo(t, repo)
			const rel = "fixtures/public.txt"
			if kind == "directory" {
				writeFile(t, repo, rel+"/child.txt", contents)
			} else if kind == "symlink" {
				writeFile(t, repo, "fixtures/target.txt", contents)
				if err := os.Symlink("target.txt", filepath.Join(repo, filepath.FromSlash(rel))); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFile(t, repo, rel, contents)
			}
			if kind != "untracked" {
				runGitTest(t, repo, "add", ".")
				runGitTest(t, repo, "commit", "-m", "fixture")
			}
			options := BuildOptions{ReviewedFixtures: []syncbundle.ReviewedFixture{{Path: rel, SHA256: hex.EncodeToString(digest[:])}}}
			if kind == "explicit-exclude" {
				options.ExcludePatterns = []string{"fixtures/**"}
			}
			if kind == "project-ignore" {
				writeFile(t, repo, ".readyciignore", "fixtures/**\n")
			}
			if _, err := SnapshotWithOptions(context.Background(), repo, options); err == nil {
				t.Fatalf("reviewed fixture bypassed %s constraint", kind)
			}
		})
	}
}

func TestExplicitExclusionsStillRemoveTrackedGeneratedNamedSource(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	writeFile(t, repo, "src/cmd/dist/build.go", "package main\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "tracked bootstrap source")
	manifest, err := SnapshotWithOptions(context.Background(), repo, BuildOptions{ExcludePatterns: []string{"dist"}})
	if err != nil {
		t.Fatal(err)
	}
	if contains(syncbundle.ManifestPaths(manifest), "baseline/src/cmd/dist/build.go") {
		t.Fatal("explicit exclusion was weakened by Git tracking")
	}
}

func TestTrackedGeneratedNamedEditsReachEverySourceConsumer(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	const rel = "src/cmd/dist/build.go"
	writeFile(t, repo, rel, "package main\n// baseline\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "tracked bootstrap source")
	writeFile(t, repo, rel, "package main\n// selected edit\n")
	writeFile(t, repo, "src/cmd/dist/untracked.txt", "generated output\n")
	for _, mode := range []string{syncbundle.ChangeModeWorking, syncbundle.ChangeModeAll, syncbundle.ChangeModeStaged} {
		t.Run(mode, func(t *testing.T) {
			if mode == syncbundle.ChangeModeStaged {
				runGitTest(t, repo, "add", rel)
			}
			options := BuildOptions{ChangeMode: mode}
			var archive bytes.Buffer
			built, err := BuildWithOptions(context.Background(), repo, &archive, options)
			if err != nil {
				t.Fatal(err)
			}
			if !contains(syncbundle.ManifestPaths(built.Manifest), "workspace/"+rel) {
				t.Fatalf("selected tracked edit missing from workspace overlay: %+v", built.Manifest)
			}
			changed, err := ChangedFiles(context.Background(), repo, options)
			if err != nil || !contains(changed.ChangedFiles, rel) || contains(changed.ChangedFiles, "src/cmd/dist/untracked.txt") {
				t.Fatalf("changed-file preview disagrees with selected source: %+v err=%v", changed, err)
			}
			filter, err := NewPathFilter(context.Background(), repo, options)
			if err != nil {
				t.Fatal(err)
			}
			if included, err := filter.Includes(rel); err != nil || !included {
				t.Fatalf("affected/watch filter omitted tracked edit: included=%v err=%v", included, err)
			}
			if included, err := filter.Includes("src/cmd/dist/untracked.txt"); err != nil || included {
				t.Fatalf("affected/watch filter included generated output: included=%v err=%v", included, err)
			}
		})
	}
}

func TestReviewedNewIndexFixtureSurvivesCachedBaselineRoundTrip(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	writeFile(t, repo, "tracked.txt", "ordinary baseline\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline before fixture")
	const rel = "tests/test_apps/.env"
	const contents = "READYCI_TEST_FIXTURE=public-placeholder\n"
	writeFile(t, repo, rel, contents)
	runGitTest(t, repo, "add", rel)
	digest := sha256.Sum256([]byte(contents))
	includeUntracked := false
	options := BuildOptions{IncludeUntracked: &includeUntracked,
		ReviewedFixtures: []syncbundle.ReviewedFixture{{Path: rel, SHA256: hex.EncodeToString(digest[:])}}}
	var full, baseline, delta, rehydrated bytes.Buffer
	built, err := BuildWithOptions(context.Background(), repo, &full, options)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(syncbundle.ManifestPaths(built.Manifest), "workspace/"+rel) {
		t.Fatal("new Git-index fixture was omitted as untracked")
	}
	if _, err := syncbundle.WriteBaselineBundle(bytes.NewReader(full.Bytes()), &baseline); err != nil {
		t.Fatalf("cache baseline extraction: %v", err)
	}
	options.OmitBaseline = true
	if _, err := BuildWithOptions(context.Background(), repo, &delta, options); err != nil {
		t.Fatalf("delta source packaging: %v", err)
	}
	manifest, err := syncbundle.RehydrateDeltaBundle(bytes.NewReader(baseline.Bytes()), bytes.NewReader(delta.Bytes()), &rehydrated)
	if err != nil {
		t.Fatalf("cache baseline rehydration: %v", err)
	}
	if manifest.SnapshotDigest != built.Manifest.SnapshotDigest {
		t.Fatalf("fixture identity changed after baseline reuse: %q != %q", manifest.SnapshotDigest, built.Manifest.SnapshotDigest)
	}
	workspace := t.TempDir()
	if _, err := extractBundleForTest(context.Background(), bytes.NewReader(rehydrated.Bytes()), workspace); err != nil {
		t.Fatal(err)
	}
	assertFile(t, workspace, rel, contents)
}

func TestTrackedDotenvFixturesRemainPrivateWithoutExplicitReview(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	writeFile(t, repo, "tests/test_apps/.env", "READYCI_TEST_FIXTURE=public-placeholder\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "fixture resembling Flask dotenv test input")
	manifest, err := SnapshotWithOptions(context.Background(), repo, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if contains(syncbundle.ManifestPaths(manifest), "baseline/tests/test_apps/.env") {
		t.Fatal("being Git-tracked alone authorized uploading a secret-shaped fixture")
	}
}

func TestRecordedEmptyDefaultsCannotDisableConfidentiality(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	writeFile(t, repo, "tests/test_apps/.env", "READYCI_TEST_FIXTURE=public-placeholder\n")
	writeFile(t, repo, "tracked.txt", "selected source\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "tracked source and secret-shaped fixture")
	options := BuildOptions{DefaultExcludePatterns: []string{}, DefaultUntrackedExcludePatterns: []string{}}
	var output bytes.Buffer
	built, err := BuildWithOptions(context.Background(), repo, &output, options)
	if err != nil {
		t.Fatalf("replaying explicit additional defaults should preserve the confidentiality floor: %v", err)
	}
	if contains(syncbundle.ManifestPaths(built.Manifest), "baseline/tests/test_apps/.env") {
		t.Fatal("recorded empty defaults disabled the source confidentiality floor")
	}
	filter, err := NewPathFilter(context.Background(), repo, options)
	if err != nil {
		t.Fatal(err)
	}
	if included, err := filter.Includes("tests/test_apps/.env"); err != nil || included {
		t.Fatalf("watch/affected filter disagreed with confidentiality admission: included=%v error=%v", included, err)
	}
}
