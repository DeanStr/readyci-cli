package preflightapi

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// The golden digests are the capability digests workers advertise and the worker
// release identity binds. They were recorded from the Go-literal catalogs before
// the catalog moved to runtime_catalog.json, and the refresh script rewrites them
// with each proposal, so every capability change is an explicit reviewed diff.
func TestRuntimeCatalogDigestsMatchReviewedGolden(t *testing.T) {
	data, err := os.ReadFile("testdata/runtime_catalog_digests.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]string
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{
		GoArchiveRuntimeProvider:   GoRuntimeCatalogDigest(),
		NodeArchiveRuntimeProvider: NodeRuntimeCatalogDigest(),
		PnpmArchiveRuntimeProvider: PnpmRuntimeCatalogDigest(),
	}
	if !reflect.DeepEqual(got, golden) {
		t.Fatalf("runtime catalog digests = %v, golden %v", got, golden)
	}
	for capability, provider := range map[string]string{
		GoRuntimeProviderCapability():   GoArchiveRuntimeProvider,
		NodeRuntimeProviderCapability(): NodeArchiveRuntimeProvider,
		PnpmRuntimeProviderCapability(): PnpmArchiveRuntimeProvider,
	} {
		if capability != provider+":sha256:"+golden[provider] {
			t.Fatalf("capability %s is not bound to golden digest %s", capability, golden[provider])
		}
	}
}

// The refresh script and reviewers edit the same bytes Go embeds. One canonical
// encoding keeps a no-op refresh byte-identical and every diff minimal.
func TestRuntimeCatalogDataIsCanonical(t *testing.T) {
	encoded, err := json.MarshalIndent(runtimeCatalogFile{Schema: runtimeCatalogSchema, Catalogs: runtimeCatalogs}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(append(encoded, '\n'), runtimeCatalogData) {
		t.Fatal("runtime_catalog.json is not canonical: use two-space json.MarshalIndent output with a trailing newline")
	}
	for provider, archives := range map[string][]RuntimeArchive{
		GoArchiveRuntimeProvider:   GoRuntimeCatalog(),
		NodeArchiveRuntimeProvider: NodeRuntimeCatalog(),
		PnpmArchiveRuntimeProvider: PnpmRuntimeCatalog(),
	} {
		if !reflect.DeepEqual(archives, runtimeCatalogs[provider]) {
			t.Fatalf("%s accessor does not return the embedded catalog", provider)
		}
	}
}

func TestRuntimeCatalogRejectsUnreviewableData(t *testing.T) {
	valid := func() map[string]any {
		var file map[string]any
		if err := json.Unmarshal(runtimeCatalogData, &file); err != nil {
			t.Fatal(err)
		}
		return file
	}
	archive := func(file map[string]any, provider string, index int) map[string]any {
		return file["catalogs"].(map[string]any)[provider].([]any)[index].(map[string]any)
	}
	// Canonical order places each companion directly after its primary archive.
	companion := func(catalog []any) int {
		for index, entry := range catalog {
			if entry.(map[string]any)["runtime"] == "pnpm-native" {
				return index
			}
		}
		t.Fatal("catalog has no pnpm-native companion")
		return 0
	}
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"schema", func(file map[string]any) { file["schema"] = "readyci.runtime_catalog.v0" }, "unsupported schema"},
		{"unknown field", func(file map[string]any) {
			archive(file, GoArchiveRuntimeProvider, 0)["mirror"] = "https://example.test"
		}, "unknown field"},
		{"unknown provider", func(file map[string]any) {
			catalogs := file["catalogs"].(map[string]any)
			catalogs["java_archive_v1"] = catalogs[GoArchiveRuntimeProvider]
			delete(catalogs, GoArchiveRuntimeProvider)
		}, "unknown provider"},
		{"missing provider", func(file map[string]any) { delete(file["catalogs"].(map[string]any), NodeArchiveRuntimeProvider) }, "exactly"},
		{"empty provider", func(file map[string]any) { file["catalogs"].(map[string]any)[NodeArchiveRuntimeProvider] = []any{} }, "empty"},
		{"caller URL", func(file map[string]any) {
			archive(file, GoArchiveRuntimeProvider, 0)["url"] = "https://dl.google.com/go/go1.25.14.linux-amd64.tar.gz"
		}, "approved linux/amd64 URL"},
		{"other platform", func(file map[string]any) { archive(file, NodeArchiveRuntimeProvider, 0)["arch"] = "arm64" }, "approved linux/amd64 URL"},
		{"prefixed version", func(file map[string]any) { archive(file, GoArchiveRuntimeProvider, 0)["version"] = "v1.25.13" }, "invalid version"},
		{"partial version", func(file map[string]any) { archive(file, GoArchiveRuntimeProvider, 0)["version"] = "1.25" }, "invalid version"},
		{"uppercase digest", func(file map[string]any) {
			entry := archive(file, GoArchiveRuntimeProvider, 0)
			entry["sha256"] = strings.ToUpper(entry["sha256"].(string))
		}, "invalid digests"},
		{"missing sha512", func(file map[string]any) { delete(archive(file, NodeArchiveRuntimeProvider, 0), "sha512") }, "invalid digests"},
		{"unexpected sha512", func(file map[string]any) {
			archive(file, GoArchiveRuntimeProvider, 0)["sha512"] = strings.Repeat("a", 128)
		}, "invalid digests"},
		{"unsized", func(file map[string]any) { archive(file, GoArchiveRuntimeProvider, 0)["size_bytes"] = 0 }, "invalid size"},
		{"oversized", func(file map[string]any) {
			archive(file, GoArchiveRuntimeProvider, 0)["size_bytes"] = maxRuntimeArchiveBytes + 1
		}, "invalid size"},
		{"unsupported runtime", func(file map[string]any) { archive(file, GoArchiveRuntimeProvider, 0)["runtime"] = "node" }, "unsupported runtime"},
		{"out of order", func(file map[string]any) {
			catalog := file["catalogs"].(map[string]any)[GoArchiveRuntimeProvider].([]any)
			catalog[0], catalog[1] = catalog[1], catalog[0]
		}, "canonical order"},
		{"duplicate", func(file map[string]any) {
			catalogs := file["catalogs"].(map[string]any)
			catalog := catalogs[GoArchiveRuntimeProvider].([]any)
			catalogs[GoArchiveRuntimeProvider] = append([]any{catalog[0]}, catalog...)
		}, "canonical order"},
		{"companion first", func(file map[string]any) {
			catalog := file["catalogs"].(map[string]any)[PnpmArchiveRuntimeProvider].([]any)
			native := companion(catalog)
			catalog[native-1], catalog[native] = catalog[native], catalog[native-1]
		}, "no pnpm archive of the same version"},
		{"orphan companion", func(file map[string]any) {
			catalogs := file["catalogs"].(map[string]any)
			catalog := catalogs[PnpmArchiveRuntimeProvider].([]any)
			native := companion(catalog)
			catalogs[PnpmArchiveRuntimeProvider] = append(append([]any(nil), catalog[:native-1]...), catalog[native:]...)
		}, "no pnpm archive of the same version"},
	} {
		t.Run(test.name, func(t *testing.T) {
			file := valid()
			test.mutate(file)
			encoded, err := json.Marshal(file)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parseRuntimeCatalogs(encoded); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("parse error = %v, want %q", err, test.want)
			}
		})
	}
	if _, err := parseRuntimeCatalogs(append(append([]byte(nil), runtimeCatalogData...), []byte("{}")...)); err == nil {
		t.Fatal("catalog accepted trailing data")
	}
	if _, err := parseRuntimeCatalogs(runtimeCatalogData); err != nil {
		t.Fatalf("embedded catalog rejected: %v", err)
	}
}

// newestCatalogVersion is the oracle for "highest approved" expectations. The
// refresh bot only adds archives, so tests follow the reviewed catalog instead
// of pinning its current newest entry.
func newestCatalogVersion(t *testing.T, archives []RuntimeArchive, prefix string) string {
	t.Helper()
	newest := ""
	var best runtimeVersion
	for _, archive := range archives {
		version, err := parseRuntimeVersion(archive.Version)
		if err != nil || !strings.HasPrefix(archive.Version, prefix) {
			continue
		}
		if newest == "" || compareRuntimeVersions(version, best) > 0 {
			newest, best = archive.Version, version
		}
	}
	if newest == "" {
		t.Fatalf("catalog has no %q version", prefix)
	}
	return newest
}
