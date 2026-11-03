package syncbundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const projectionFixture = "PUBLIC_TEST_FIXTURE=placeholder\n"

// projectionTestUpload is a validated multi-scope upload with symlinks,
// deletions, untracked origins, a reviewed fixture and component-only files.
func projectionTestUpload(t *testing.T, reorder func(baseline, workspace []ArchiveEntry)) ([]byte, Manifest, map[string]BundleFile) {
	t.Helper()
	digest := "sha256:" + strings.Repeat("a", 64)
	fixtureHash := sha256.Sum256([]byte(projectionFixture))
	scopes := []SourcePolicyBinding{
		{Digest: digest, ExcludePatterns: []string{"server"}, IncludeUntracked: true, ReviewedFixtures: []ReviewedFixture{{Path: "web/.env.fixture", SHA256: hex.EncodeToString(fixtureHash[:])}}},
		{Digest: digest, ExcludePatterns: []string{"web"}, IncludeUntracked: false},
	}
	for i := range scopes {
		scopes[i].DefaultExcludePatterns = DefaultSourceExcludePatterns()
		scopes[i].DefaultUntrackedExcludePatterns = DefaultUntrackedExcludePatterns()
	}
	policy, err := NormalizeSourcePolicyBinding(SourcePolicyBinding{Digest: digest, Scopes: scopes})
	if err != nil {
		t.Fatal(err)
	}
	file := func(rel, data string) ArchiveEntry {
		return ArchiveEntry{Entry: Entry{Path: rel, Type: EntryTypeFile, Mode: 0o644, Size: int64(len(data))}, Data: []byte(data)}
	}
	baseline := []ArchiveEntry{
		file("gone.txt", "removed\n"),
		{Entry: Entry{Path: "link", Type: EntryTypeSymlink, Mode: 0o777, Linkname: "shared.txt"}},
		file("server/main.rs", "fn main() {}\n"),
		file("shared.txt", "shared\n"),
		file("web/.env.fixture", projectionFixture),
		file("web/main.ts", "export {}\n"),
	}
	workspace := []ArchiveEntry{
		file("server/main.rs", "fn main() { serve() }\n"),
		file("shared.txt", "shared change\n"),
		file("web/main.ts", "export const x = 1\n"),
		file("web/new.ts", "untracked web source\n"),
	}
	manifest := Manifest{Version: FormatVersion, ChangeMode: ChangeModeWorking, Git: GitMetadata{Branch: "main", Head: strings.Repeat("b", 40), Dirty: true}, SourcePolicy: &policy, ReviewedFixtures: policy.ReviewedFixtures, UntrackedFiles: []string{"web/new.ts"}, Deleted: []string{"gone.txt"}}
	for _, item := range baseline {
		manifest.Baseline = append(manifest.Baseline, item.Entry)
	}
	for _, item := range workspace {
		manifest.Workspace = append(manifest.Workspace, item.Entry)
	}
	manifest.BaselineKey, err = BaselineContentKey(baseline, manifest.ContentBinding())
	if err != nil {
		t.Fatal(err)
	}
	if reorder != nil {
		baseline, workspace = slices.Clone(baseline), slices.Clone(workspace)
		reorder(baseline, workspace)
	}
	var raw bytes.Buffer
	if err := WriteBundle(&raw, manifest, baseline, workspace); err != nil {
		t.Fatal(err)
	}
	validated, files, err := ReadBundleContents(bytes.NewReader(raw.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	return raw.Bytes(), validated, files
}

// webAndServerScopes returns the scope admitting web sources and fixtures, then
// its sibling. Canonical scope order is not declaration order.
func webAndServerScopes(manifest Manifest) (SourcePolicyBinding, SourcePolicyBinding) {
	scopes := manifest.SourcePolicy.Scopes
	if len(scopes[0].ReviewedFixtures) > 0 {
		return scopes[0], scopes[1]
	}
	return scopes[1], scopes[0]
}

func projectionIdentity(manifest Manifest) ProjectionIdentity {
	return ProjectionIdentity{SnapshotDigest: manifest.SnapshotDigest, BaselineKey: manifest.BaselineKey, ChangedFiles: manifest.ChangedFiles, ManifestDigest: ManifestDigest(manifest)}
}

func TestStreamProjectionMatchesInMemoryProjection(t *testing.T) {
	raw, manifest, files := projectionTestUpload(t, nil)
	for _, policy := range manifest.SourcePolicy.Scopes {
		projected, err := ProjectSource(manifest, files, policy)
		if err != nil {
			t.Fatal(err)
		}
		var expected, streamed bytes.Buffer
		if err := WriteBundle(&expected, projected.Manifest, projected.Baseline, projected.Workspace); err != nil {
			t.Fatal(err)
		}
		var inspected Manifest
		got, err := StreamProjection(&streamed, bytes.NewReader(raw), policy, projectionIdentity(projected.Manifest), func(canonical Manifest) error {
			inspected = canonical
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(inspected, manifest) || !reflect.DeepEqual(got, projected.Manifest) {
			t.Fatalf("stream manifests differ: canonical=%v projected=%v", reflect.DeepEqual(inspected, manifest), reflect.DeepEqual(got, projected.Manifest))
		}
		if !bytes.Equal(streamed.Bytes(), expected.Bytes()) {
			t.Fatalf("streamed projection (%d bytes) differs from the in-memory projection (%d bytes)", streamed.Len(), expected.Len())
		}
		if _, err := ValidateWithManifest(bytes.NewReader(streamed.Bytes())); err != nil {
			t.Fatal(err)
		}
	}
	webScope, _ := webAndServerScopes(manifest)
	web, err := ProjectSource(manifest, files, webScope)
	if err != nil {
		t.Fatal(err)
	}
	if len(web.Manifest.ReviewedFixtures) != 1 || !slices.Contains(web.Manifest.Deleted, "gone.txt") || !slices.ContainsFunc(web.Manifest.Workspace, func(e Entry) bool { return e.Path == "web/new.ts" }) {
		t.Fatalf("fixture and deletion were not exercised: %+v", web.Manifest)
	}
}

func TestStreamProjectionFollowsArchiveOrder(t *testing.T) {
	raw, manifest, files := projectionTestUpload(t, func(baseline, workspace []ArchiveEntry) {
		slices.Reverse(baseline)
		slices.Reverse(workspace)
	})
	for _, policy := range manifest.SourcePolicy.Scopes {
		projected, err := ProjectSource(manifest, files, policy)
		if err != nil {
			t.Fatal(err)
		}
		var streamed bytes.Buffer
		if _, err := StreamProjection(&streamed, bytes.NewReader(raw), policy, projectionIdentity(projected.Manifest), nil); err != nil {
			t.Fatal(err)
		}
		got, gotFiles, err := ReadBundleContents(bytes.NewReader(streamed.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, projected.Manifest) || ManifestDigest(got) != ManifestDigest(projected.Manifest) {
			t.Fatal("reordered canonical archive changed the projected manifest")
		}
		for group, items := range map[string][]ArchiveEntry{"baseline/": projected.Baseline, "workspace/": projected.Workspace} {
			for _, item := range items {
				file, exists := gotFiles[group+item.Entry.Path]
				if !exists || !bytes.Equal(file.Data, item.Data) || file.Linkname != item.Entry.Linkname {
					t.Fatalf("projection changed %s%s", group, item.Entry.Path)
				}
			}
		}
		if len(gotFiles) != len(projected.Baseline)+len(projected.Workspace) {
			t.Fatalf("projection emitted %d entries, want %d", len(gotFiles), len(projected.Baseline)+len(projected.Workspace))
		}
	}
}

// rewriteBundle re-encodes a bundle, letting change rewrite or drop entries.
func rewriteBundle(t *testing.T, raw []byte, change func(header *tar.Header, data []byte) (*tar.Header, []byte)) []byte {
	t.Helper()
	gzipReader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(gzipReader)
	var out bytes.Buffer
	gzipWriter := gzip.NewWriter(&out)
	writer := tar.NewWriter(gzipWriter)
	var deferred []func()
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == "manifest.json" && change == nil {
			// Move the manifest to the end.
			header, data := header, data
			deferred = append(deferred, func() {
				if err := writer.WriteHeader(header); err != nil {
					t.Fatal(err)
				}
				_, _ = writer.Write(data)
			})
			continue
		}
		if change != nil {
			header, data = change(header, data)
			if header == nil {
				continue
			}
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	for _, write := range deferred {
		write()
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestStreamProjectionRejectsUnsealedOrInconsistentSources(t *testing.T) {
	raw, manifest, files := projectionTestUpload(t, nil)
	policy, other := webAndServerScopes(manifest)
	projected, err := ProjectSource(manifest, files, policy)
	if err != nil {
		t.Fatal(err)
	}
	identity := projectionIdentity(projected.Manifest)
	for name, test := range map[string]struct {
		canonical []byte
		policy    SourcePolicyBinding
		identity  func(*ProjectionIdentity)
		inspect   func(Manifest) error
	}{
		"manifest digest":    {raw, policy, func(id *ProjectionIdentity) { id.ManifestDigest = "sha256:" + strings.Repeat("0", 64) }, nil},
		"snapshot digest":    {raw, policy, func(id *ProjectionIdentity) { id.SnapshotDigest = manifest.SnapshotDigest }, nil},
		"changed files":      {raw, policy, func(id *ProjectionIdentity) { id.ChangedFiles = []string{"web/main.ts"} }, nil},
		"sibling scope":      {raw, other, nil, nil},
		"unregistered scope": {raw, SourcePolicyBinding{Digest: policy.Digest, ExcludePatterns: []string{"unregistered"}}, nil, nil},
		"canonical check":    {raw, policy, nil, func(Manifest) error { return errors.New("session mismatch") }},
		"trailing manifest":  {rewriteBundle(t, raw, nil), policy, nil, nil},
		"missing entry": {rewriteBundle(t, raw, func(h *tar.Header, data []byte) (*tar.Header, []byte) {
			if h.Name == "workspace/web/new.ts" {
				return nil, nil
			}
			return h, data
		}), policy, nil, nil},
		"declared type": {rewriteBundle(t, raw, func(h *tar.Header, data []byte) (*tar.Header, []byte) {
			if h.Name == "baseline/link" {
				return &tar.Header{Name: h.Name, Typeflag: tar.TypeReg, Mode: 0o644, Size: 10}, []byte("shared.txt")
			}
			return h, data
		}), policy, nil, nil},
		"reviewed fixture bytes": {rewriteBundle(t, raw, func(h *tar.Header, data []byte) (*tar.Header, []byte) {
			if h.Name == "baseline/web/.env.fixture" {
				return h, bytes.ToUpper(data)
			}
			return h, data
		}), policy, nil, nil},
		"unexpected path": {rewriteBundle(t, raw, func(h *tar.Header, data []byte) (*tar.Header, []byte) {
			if h.Name == "workspace/shared.txt" {
				h.Name = "other/shared.txt"
			}
			return h, data
		}), policy, nil, nil},
	} {
		t.Run(name, func(t *testing.T) {
			id := identity
			if test.identity != nil {
				test.identity(&id)
			}
			var out bytes.Buffer
			if _, err := StreamProjection(&out, bytes.NewReader(test.canonical), test.policy, id, test.inspect); err == nil {
				t.Fatal("inconsistent projection was streamed")
			}
		})
	}
	var out bytes.Buffer
	_, err = StreamProjection(&out, bytes.NewReader(raw), policy, ProjectionIdentity{SnapshotDigest: identity.SnapshotDigest, BaselineKey: identity.BaselineKey, ChangedFiles: identity.ChangedFiles}, nil)
	if err == nil || out.Len() != 0 {
		t.Fatalf("unsealed projection wrote %d bytes: %v", out.Len(), err)
	}
}

func TestMultiScopeBundleMustLeadWithManifest(t *testing.T) {
	raw, _, _ := projectionTestUpload(t, nil)
	if _, err := ValidateWithManifest(bytes.NewReader(rewriteBundle(t, raw, nil))); !errors.Is(err, ErrManifestNotLeading) {
		t.Fatalf("trailing multi-scope manifest = %v", err)
	}
}
