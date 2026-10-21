package preflightapi

import (
	"strings"
	"testing"
)

func TestGoRuntimeProviderRequiresServerCatalogAndExistingNetworkPolicy(t *testing.T) {
	cases := []struct {
		name, requirement, network string
		hosts                      []string
		inventory                  RuntimeInventory
		providers                  []string
		want                       string
		download                   bool
	}{
		{name: "installed remains preferred", requirement: ">=1.25", inventory: RuntimeInventory{"go": {"1.25.13"}}, want: "1.25.13"},
		{name: "explicit approved archive", requirement: ">=1.27.2", network: "allowlist", hosts: []string{"dl.google.com"}, providers: []string{GoRuntimeProviderCapability()}, want: "1.27.2", download: true},
		{name: "default authorized egress", requirement: "1.26.0", network: "default", providers: []string{GoRuntimeProviderCapability()}, want: "1.26.0", download: true},
		{name: "highest approved patch in series", requirement: "1.26", network: "default", providers: []string{GoRuntimeProviderCapability()}, want: "1.26.9", download: true},
		{name: "highest approved older series patch", requirement: "1.25", network: "default", providers: []string{GoRuntimeProviderCapability()}, want: "1.25.14", download: true},
		{name: "presence never downloads", requirement: "installed", network: "default", providers: []string{GoRuntimeProviderCapability()}},
		{name: "no provider", requirement: "1.27.2", network: "default"},
		{name: "offline stays offline", requirement: "1.27.2", network: "none", providers: []string{GoRuntimeProviderCapability()}},
		{name: "different allowed host", requirement: "1.27.2", network: "allowlist", hosts: []string{"go.dev"}, providers: []string{GoRuntimeProviderCapability()}},
		{name: "deceptive hostname", requirement: "1.27.2", network: "allowlist", hosts: []string{"dl.google.com.attacker.test"}, providers: []string{GoRuntimeProviderCapability()}},
		{name: "uncatalogued version", requirement: "1.27.3", network: "default", providers: []string{GoRuntimeProviderCapability()}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			spec := RunSpec{RuntimeRequirements: map[string]string{"go": test.requirement}, NetworkMode: test.network, NetworkAllowlist: test.hosts}
			prepared, err := ResolveRuntimePreparation(spec, test.inventory, test.providers)
			if test.want == "" {
				if err == nil {
					t.Fatalf("missingruntimebecameavailable %+v", prepared)
				}
				return
			}
			if err != nil || prepared.Versions["go"] != test.want || (prepared.GoArchive != nil) != test.download {
				t.Fatalf("prepared%+v error%v", prepared, err)
			}
			if prepared.GoArchive != nil && (!strings.HasPrefix(prepared.GoArchive.URL, "https://dl.google.com/go/go"+test.want+".") || len(prepared.GoArchive.SHA256) != 64 || prepared.GoArchive.SizeBytes <= 0) {
				t.Fatalf("unbound artifact %+v", prepared.GoArchive)
			}
		})
	}
	if _, err := ResolveRuntimePreparation(RunSpec{RuntimeRequirements: map[string]string{"python": "3.13.0"}, NetworkMode: "default"}, nil, []string{GoRuntimeProviderCapability()}); err == nil {
		t.Fatal("Go provider provisioned unsupported language")
	}
}
func TestGoRuntimeCatalogIsImmutableAndExact(t *testing.T) {
	first := GoRuntimeCatalog()
	digest := GoRuntimeCatalogDigest()
	if len(digest) != 64 || len(first) != 5 {
		t.Fatalf("invalid catalog %v %s", first, digest)
	}
	first[0].SHA256 = strings.Repeat("a", 64)
	if GoRuntimeCatalogDigest() != digest {
		t.Fatal("caller can mutate approved runtimecatalog")
	}
	selected, err := ResolveRuntimePreparation(RunSpec{RuntimeRequirements: map[string]string{"go": ">=1.26"}, NetworkMode: "default"}, nil, []string{GoRuntimeProviderCapability()})
	if err != nil || selected.GoArchive.Version != "1.27.2" {
		t.Fatalf("catalogselection not deterministic highest satisfying %+v %v", selected, err)
	}
}
func TestRuntimeProviderMetadataIsBoundedAndCloned(t *testing.T) {
	for _, metadata := range []map[string][]string{{"image": {"arbitrary_url"}}, {"image": {}}, {"image": {GoRuntimeProviderCapability(), GoRuntimeProviderCapability()}}, {"bad\nimage": {GoRuntimeProviderCapability()}}} {
		if _, err := NormalizeRuntimeProviders(metadata); err == nil {
			t.Fatalf("invalid provider metadata accepted %v", metadata)
		}
	}
	initial := map[string][]string{"image": {GoRuntimeProviderCapability()}}
	copied := CloneRuntimeProviders(initial)
	copied["image"][0] = "changed"
	if initial["image"][0] != GoRuntimeProviderCapability() {
		t.Fatal("provider metadata mutablethroughclone")
	}
}
