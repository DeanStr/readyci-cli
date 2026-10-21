package preflightapi

import (
	"reflect"
	"strings"
	"testing"
)

func TestApprovedNodeAndPackageManagerPreparationIsPolicyAndIntegrityBound(t *testing.T) {
	pnpm := PnpmRuntimeCatalog()[0]
	pin := pnpm.Version + "+sha512." + pnpm.SHA512
	providers := []string{NodeRuntimeProviderCapability(), PnpmRuntimeProviderCapability()}
	inventory := RuntimeInventory{"node": {"24.18.0"}, "pnpm": {"11.15.1"}}
	for _, test := range []struct {
		name         string
		requirements map[string]string
		network      string
		hosts        []string
		providers    []string
		want         bool
		artifacts    int
	}{
		{name: "missing exact Node", requirements: map[string]string{"node": "24.15.0"}, network: "allowlist", hosts: []string{NodeRuntimeDownloadHost}, providers: providers, want: true, artifacts: 1},
		{name: "full pnpm distribution integrity", requirements: map[string]string{"pnpm": pin}, network: "allowlist", hosts: []string{PnpmRuntimeDownloadHost}, providers: providers, want: true, artifacts: 1},
		{name: "pnpm12 includes native dependency", requirements: map[string]string{"pnpm": "12.11.2"}, network: "default", providers: providers, want: true, artifacts: 2},
		{name: "both families", requirements: map[string]string{"node": "24.15.0", "pnpm": pin}, network: "allowlist", hosts: []string{NodeRuntimeDownloadHost, PnpmRuntimeDownloadHost}, providers: providers, want: true, artifacts: 2},
		{name: "installed version stays installed", requirements: map[string]string{"node": "24", "pnpm": "11"}, network: "none", want: true},
		{name: "unknown digest", requirements: map[string]string{"pnpm": "10.33.4+sha512." + strings.Repeat("0", 128)}, network: "default", providers: providers},
		{name: "unknown version", requirements: map[string]string{"pnpm": "12.11.3"}, network: "default", providers: providers},
		{name: "missing provider", requirements: map[string]string{"pnpm": pin}, network: "default"},
		{name: "wrong provider", requirements: map[string]string{"node": "24.15.0"}, network: "default", providers: []string{PnpmRuntimeProviderCapability()}},
		{name: "offline", requirements: map[string]string{"node": "24.15.0"}, network: "none", providers: providers},
		{name: "wrong origin", requirements: map[string]string{"pnpm": pin}, network: "allowlist", hosts: []string{"registry.npmjs.org.attacker.test"}, providers: providers},
		{name: "presence never downloads", requirements: map[string]string{"yarn": "installed"}, network: "default", providers: providers},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolveRuntimePreparation(RunSpec{RuntimeRequirements: test.requirements, NetworkMode: test.network, NetworkAllowlist: test.hosts}, inventory, test.providers)
			if (err == nil) != test.want || test.want && len(got.Artifacts()) != test.artifacts {
				t.Fatalf("preparation=%+v error=%v", got, err)
			}
			if test.want && got.PnpmArchive != nil && got.Versions["node"] == "" {
				t.Fatal("package manager preparation did not select an actual Node runtime")
			}
		})
	}
	if _, err := ResolveRuntimePreparation(RunSpec{RuntimeRequirements: map[string]string{"node": "17", "pnpm": pin}, NetworkMode: "default"}, RuntimeInventory{"node": {"17.0.0"}}, providers); err == nil {
		t.Fatal("pnpm provider accepted unsupported selected Node bootstrap")
	}
	if _, err := ResolveRuntimePreparation(RunSpec{RuntimeRequirements: map[string]string{"node": "18.11.0", "pnpm": pin}, NetworkMode: "default"}, RuntimeInventory{"node": {"18.11.0"}}, providers); err == nil {
		t.Fatal("pnpm10 provider ignored its declared Node>=18.12 engine requirement")
	}
	// An integrity pin is not satisfied by a coincidentally equal image version.
	if _, err := ResolveRuntimePreparation(RunSpec{RuntimeRequirements: map[string]string{"pnpm": pin}, NetworkMode: "none"}, RuntimeInventory{"node": {"24.18.0"}, "pnpm": {pnpm.Version}}, providers); err == nil {
		t.Fatal("version-only inventory bypassed distribution integrity and sealed egress")
	}
}

func TestApprovedRuntimeCatalogsAreImmutableAndProviderMetadataIsExact(t *testing.T) {
	if digest := GoRuntimeCatalogDigest(); digest != "c27a02fac8930cc750a879044ca68eb9755268ec01b093c97824d575ad3ce188" {
		t.Fatalf("adding other provider families changed the existing Go catalog: %s", digest)
	}
	for _, catalog := range []struct {
		get    func() []RuntimeArchive
		digest func() string
	}{{NodeRuntimeCatalog, NodeRuntimeCatalogDigest}, {PnpmRuntimeCatalog, PnpmRuntimeCatalogDigest}} {
		original := catalog.digest()
		copy := catalog.get()
		copy[0].SHA256 = strings.Repeat("0", 64)
		if catalog.digest() != original || len(original) != 64 {
			t.Fatal("caller can change reviewed archive catalog")
		}
	}
	all := []string{GoRuntimeProviderCapability(), NodeRuntimeProviderCapability(), PnpmRuntimeProviderCapability()}
	got, err := NormalizeRuntimeProviders(map[string][]string{"image": all})
	if err != nil || !reflect.DeepEqual(got["image"], all) {
		t.Fatalf("valid bounded providers rejected: %v %v", got, err)
	}
	for _, values := range [][]string{{NodeArchiveRuntimeProvider}, {PnpmArchiveRuntimeProvider + ":sha256:" + strings.Repeat("0", 64)}, {NodeRuntimeProviderCapability(), NodeRuntimeProviderCapability()}} {
		if _, err := NormalizeRuntimeProviders(map[string][]string{"image": values}); err == nil {
			t.Fatalf("unbound or duplicate provider accepted: %v", values)
		}
	}
}
