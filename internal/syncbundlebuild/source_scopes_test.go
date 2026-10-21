package syncbundlebuild

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func scopeBuildOptions(policy syncbundle.SourcePolicyBinding, mode string) BuildOptions {
	ignore := false
	return BuildOptions{ChangeMode: mode, SourceScopes: policy.Scopes, SourcePolicyDigest: policy.Digest,
		ExcludePatterns: policy.ExcludePatterns, DefaultExcludePatterns: policy.DefaultExcludePatterns,
		DefaultUntrackedExcludePatterns: policy.DefaultUntrackedExcludePatterns, ReviewedFixtures: policy.ReviewedFixtures,
		IncludeUntracked: &policy.IncludeUntracked, UseProjectIgnore: &ignore}
}

func TestComponentProjectionsMatchIndependentSourceBuilds(t *testing.T) {
	for _, mode := range []string{"working", "staged", "all"} {
		t.Run(mode, func(t *testing.T) {
			repo := t.TempDir()
			initRepo(t, repo)
			const fixture = "PUBLIC_TEST_FIXTURE=placeholder\n"
			for _, rel := range []string{"apps/web/main.ts", "apps/server/main.rs", "shared.txt", "coverage/source.txt"} {
				writeFile(t, repo, rel, "old\n")
			}
			writeFile(t, repo, "apps/web/.env.fixture", fixture)
			runGitTest(t, repo, "add", ".")
			runGitTest(t, repo, "commit", "-m", "coherent component baseline")
			writeFile(t, repo, "apps/web/main.ts", "web change\n")
			writeFile(t, repo, "apps/server/main.rs", "server change\n")
			if err := os.Remove(filepath.Join(repo, "shared.txt")); err != nil {
				t.Fatal(err)
			}
			runGitTest(t, repo, "add", "-u")
			writeFile(t, repo, "apps/web/new.ts", "new web source\n")
			writeFile(t, repo, "apps/server/new.rs", "untracked server source\n")
			writeFile(t, repo, "apps/web/dist/generated.js", "generated\n")
			writeFile(t, repo, "apps/web/.env.secret", "never upload\n")
			writeFile(t, repo, ".readyciignore", "apps/web/main.ts\n")
			hash := sha256.Sum256([]byte(fixture))
			digest := "sha256:" + strings.Repeat("a", 64)
			policies := []syncbundle.SourcePolicyBinding{
				{Digest: digest, ExcludePatterns: []string{"apps/server"}, IncludeUntracked: mode != "staged", ReviewedFixtures: []syncbundle.ReviewedFixture{{Path: "apps/web/.env.fixture", SHA256: hex.EncodeToString(hash[:])}}},
				{Digest: digest, ExcludePatterns: []string{"apps/web"}, IncludeUntracked: false},
			}
			for i := range policies {
				policies[i].DefaultExcludePatterns = syncbundle.DefaultSourceExcludePatterns()
				policies[i].DefaultUntrackedExcludePatterns = syncbundle.DefaultUntrackedExcludePatterns()
				var err error
				policies[i], err = syncbundle.NormalizeSourcePolicyBinding(policies[i])
				if err != nil {
					t.Fatal(err)
				}
			}
			contract, err := syncbundle.NormalizeSourcePolicyBinding(syncbundle.SourcePolicyBinding{Digest: digest, Scopes: policies})
			if err != nil {
				t.Fatal(err)
			}
			options := scopeBuildOptions(contract, mode)
			var raw bytes.Buffer
			built, err := BuildWithOptions(context.Background(), repo, &raw, options)
			if err != nil {
				t.Fatal(err)
			}
			manifest, files, err := syncbundle.ReadBundleContents(bytes.NewReader(raw.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			for _, policy := range contract.Scopes {
				projected, err := syncbundle.ProjectSource(manifest, files, policy)
				if err != nil {
					t.Fatal(err)
				}
				var independent bytes.Buffer
				expected, err := BuildWithOptions(context.Background(), repo, &independent, scopeBuildOptions(policy, mode))
				if err != nil {
					t.Fatal(err)
				}
				if expected.Manifest.SnapshotDigest != projected.Manifest.SnapshotDigest || expected.Manifest.BaselineKey != projected.Manifest.BaselineKey || !slices.Equal(expected.Manifest.ChangedFiles, projected.Manifest.ChangedFiles) {
					t.Fatalf("scope projection differs from independent build: projected=%+v expected=%+v", projected.Manifest, expected.Manifest)
				}
				var output bytes.Buffer
				if err := syncbundle.WriteBundle(&output, projected.Manifest, projected.Baseline, projected.Workspace); err != nil {
					t.Fatal(err)
				}
				if err := syncbundle.Validate(bytes.NewReader(output.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := SnapshotWithOptions(context.Background(), repo, options)
			if err != nil || snapshot.SnapshotDigest != built.Manifest.SnapshotDigest {
				t.Fatalf("currentness differs from upload: %v", err)
			}
			for _, rel := range []string{"apps/server/new.rs", "apps/web/dist/generated.js", "apps/web/.env.secret"} {
				if _, exists := files["workspace/"+rel]; exists {
					t.Fatalf("unapproved source uploaded: %s", rel)
				}
			}
			if mode != "staged" {
				manifest.UntrackedFiles = nil
				var altered bytes.Buffer
				var baseline, workspace []syncbundle.ArchiveEntry
				for _, group := range []struct {
					name    string
					entries []syncbundle.Entry
					target  *[]syncbundle.ArchiveEntry
				}{{"baseline", manifest.Baseline, &baseline}, {"workspace", manifest.Workspace, &workspace}} {
					for _, entry := range group.entries {
						*group.target = append(*group.target, syncbundle.ArchiveEntry{Entry: entry, Data: files[group.name+"/"+entry.Path].Data})
					}
				}
				if err := syncbundle.WriteBundle(&altered, manifest, baseline, workspace); err == nil || (!strings.Contains(err.Error(), "snapshot_digest") && !strings.Contains(err.Error(), "baseline_key")) {
					t.Fatal("untracked provenance could be changed without changing the source identity")
				}
			}
		})
	}
}
