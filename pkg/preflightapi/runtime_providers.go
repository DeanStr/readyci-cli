package preflightapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const GoArchiveRuntimeProvider = "go_archive_v1"
const GoRuntimeDownloadHost = "dl.google.com"

// RuntimeArchive is a release-reviewed immutable provider input. Callers may
// request runtime constraints, but may not choose URLs, checksums or archives.
// Source: https://go.dev/dl/?mode=json&include=all, captured 2026-10-19.
type RuntimeArchive struct {
	Version   string `json:"version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	Runtime   string `json:"runtime,omitempty"`
	SHA512    string `json:"sha512,omitempty"`
}

var goRuntimeCatalog = []RuntimeArchive{
	{Version: "1.25.13", OS: "linux", Arch: "amd64", URL: "https://dl.google.com/go/go1.25.13.linux-amd64.tar.gz", SHA256: "39042a078ea9ceebe3ecda4a7188f0f5b96e14a071d27923ba7f40b456e85ae3", SizeBytes: 59908810},
	{Version: "1.25.14", OS: "linux", Arch: "amd64", URL: "https://dl.google.com/go/go1.25.14.linux-amd64.tar.gz", SHA256: "a21ae5633a269bcd7e90cf767e48225633795e99d831742cbf3397064fee7712", SizeBytes: 59909419},
	{Version: "1.26.0", OS: "linux", Arch: "amd64", URL: "https://dl.google.com/go/go1.26.0.linux-amd64.tar.gz", SHA256: "aac1b08a0fb0c4e0a7c1555beb7b59180b05dfc5a3d62e40e9de90cd42f88235", SizeBytes: 66768540},
	{Version: "1.26.9", OS: "linux", Arch: "amd64", URL: "https://dl.google.com/go/go1.26.9.linux-amd64.tar.gz", SHA256: "42d158b4d8f7b61ac0a830567c940a86098fb7aac52e467a5ebec03ef5cc2f8d", SizeBytes: 66935201},
	{Version: "1.27.2", OS: "linux", Arch: "amd64", URL: "https://dl.google.com/go/go1.27.2.linux-amd64.tar.gz", SHA256: "ecbadb99091a3f46e31f5f934b068b1864eafa7995211b39eaddf76996045fe5", SizeBytes: 70590635},
}

func GoRuntimeProviderCapability() string {
	return GoArchiveRuntimeProvider + ":sha256:" + GoRuntimeCatalogDigest()
}

func GoRuntimeCatalog() []RuntimeArchive {
	return append([]RuntimeArchive(nil), goRuntimeCatalog...)
}
func GoRuntimeCatalogDigest() string {
	payload := struct {
		Schema   string           `json:"schema"`
		Provider string           `json:"provider"`
		Archives []RuntimeArchive `json:"archives"`
	}{Schema: "readyci.go_runtime_catalog.v1", Provider: GoArchiveRuntimeProvider, Archives: GoRuntimeCatalog()}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

type RuntimePreparation struct {
	Versions          map[string]string
	GoArchive         *RuntimeArchive
	NodeArchive       *RuntimeArchive
	PnpmArchive       *RuntimeArchive
	PnpmNativeArchive *RuntimeArchive
}

func (preparation RuntimePreparation) Artifacts() []RuntimeArchive {
	var archives []RuntimeArchive
	for _, archive := range []*RuntimeArchive{preparation.GoArchive, preparation.NodeArchive, preparation.PnpmArchive, preparation.PnpmNativeArchive} {
		if archive != nil {
			archives = append(archives, *archive)
		}
	}
	return archives
}

// RuntimePreparationDownloadAllowed is deliberately the same fixed origin the
// provider uses. A declaration never adds egress to the sealed run policy.
func RuntimePreparationDownloadAllowed(spec RunSpec) bool {
	return RuntimePreparationHostAllowed(spec, GoRuntimeDownloadHost)
}

// RuntimePreparationHostAllowed only inspects the sealed network policy. A
// provider must never add its download origin to that policy itself.
func RuntimePreparationHostAllowed(spec RunSpec, origin string) bool {
	switch strings.TrimSpace(spec.NetworkMode) {
	case "default":
		return true
	case "allowlist":
		for _, host := range spec.NetworkAllowlist {
			if strings.EqualFold(strings.TrimSpace(host), origin) {
				return true
			}
		}
	}
	return false
}

func NormalizeRuntimeProviders(providers map[string][]string) (map[string][]string, error) {
	if len(providers) > 64 {
		return nil, fmt.Errorf("too many image runtime providers")
	}
	result := make(map[string][]string, len(providers))
	for image, values := range providers {
		if image == "" || len(image) > 256 || strings.TrimSpace(image) != image || strings.ContainsAny(image, "\x00\r\n") {
			return nil, fmt.Errorf("invalid runtime provider image")
		}
		if len(values) == 0 || len(values) > 3 {
			return nil, fmt.Errorf("invalid runtime providers for image %s", image)
		}
		seen := map[string]bool{}
		for _, provider := range values {
			if seen[provider] || provider != GoRuntimeProviderCapability() && provider != NodeRuntimeProviderCapability() && provider != PnpmRuntimeProviderCapability() {
				return nil, fmt.Errorf("unsupported runtime provider %q", provider)
			}
			seen[provider] = true
		}
		result[image] = append([]string(nil), values...)
		sort.Strings(result[image])
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}
func CloneRuntimeProviders(providers map[string][]string) map[string][]string {
	if providers == nil {
		return nil
	}
	result := make(map[string][]string, len(providers))
	for image, values := range providers {
		result[image] = append([]string(nil), values...)
	}
	return result
}

// ResolveRuntimePreparation first uses installed versions. A missing numeric
// constraint may select the highest approved satisfying archive, deterministically.
// The installed keyword never turns into a download or selects a moving default.
func ResolveRuntimePreparation(spec RunSpec, inventory RuntimeInventory, providers []string) (RuntimePreparation, error) {
	requirements, err := NormalizeRuntimeRequirements(spec.RuntimeRequirements)
	if err != nil {
		return RuntimePreparation{}, err
	}
	result := RuntimePreparation{Versions: map[string]string{}}
	var names []string
	for name := range requirements {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		requirement := requirements[name]
		installed, err := SelectRuntimeVersions(map[string]string{name: requirement}, inventory)
		if err == nil {
			result.Versions[name] = installed[name]
			continue
		}
		if requirement == "installed" {
			return RuntimePreparation{}, err
		}
		var capability, origin, catalogDigest string
		var catalog []RuntimeArchive
		switch name {
		case "go":
			capability, origin, catalogDigest, catalog = GoRuntimeProviderCapability(), GoRuntimeDownloadHost, GoRuntimeCatalogDigest(), GoRuntimeCatalog()
		case "node":
			capability, origin, catalogDigest, catalog = NodeRuntimeProviderCapability(), NodeRuntimeDownloadHost, NodeRuntimeCatalogDigest(), NodeRuntimeCatalog()
		case "pnpm":
			capability, origin, catalogDigest, catalog = PnpmRuntimeProviderCapability(), PnpmRuntimeDownloadHost, PnpmRuntimeCatalogDigest(), PnpmRuntimeCatalog()
		default:
			return RuntimePreparation{}, err
		}
		if !runtimeProviderPresent(providers, capability) {
			return RuntimePreparation{}, fmt.Errorf("runtime %s requires %s; selected image has no matching installed toolchain or approved %s archive provider", name, requirement, name)
		}
		if !RuntimePreparationHostAllowed(spec, origin) {
			return RuntimePreparation{}, fmt.Errorf("runtime %s requires %s; approved SDK preparation needs existing network policy to allow %s", name, requirement, origin)
		}
		var chosen *RuntimeArchive
		_, requiredDigest, pinned := PackageManagerIntegrityRequirement(requirement)
		for _, artifact := range catalog {
			if artifact.Runtime != "" && artifact.Runtime != name || pinned && artifact.SHA512 != requiredDigest {
				continue
			}
			if RuntimeVersionSatisfies(artifact.Version, requirement) {
				candidate, _ := parseRuntimeVersion(artifact.Version)
				if chosen != nil {
					current, _ := parseRuntimeVersion(chosen.Version)
					if compareRuntimeVersions(candidate, current) <= 0 {
						continue
					}
				}
				selected := artifact
				chosen = &selected
				result.Versions[name] = artifact.Version
			}
		}
		if chosen == nil {
			return RuntimePreparation{}, fmt.Errorf("runtime %s requires %s; no reviewed %s archive in catalog %s satisfies its version and integrity", name, requirement, name, catalogDigest)
		}
		switch name {
		case "go":
			result.GoArchive = chosen
		case "node":
			result.NodeArchive = chosen
		case "pnpm":
			result.PnpmArchive = chosen
			for _, artifact := range catalog {
				if artifact.Runtime == "pnpm-native" && artifact.Version == chosen.Version {
					selected := artifact
					result.PnpmNativeArchive = &selected
				}
			}
		}
	}
	if result.PnpmArchive != nil {
		nodeRequirement := ">=18"
		if strings.HasPrefix(result.PnpmArchive.Version, "10.") {
			nodeRequirement = ">=18.12"
		}
		if result.Versions["node"] == "" && len(inventory["node"]) > 0 {
			result.Versions["node"] = inventory["node"][0]
		}
		if !RuntimeVersionSatisfies(result.Versions["node"], nodeRequirement) {
			return RuntimePreparation{}, fmt.Errorf("approved pnpm %s preparation requires a selected or installed Node.js version %s", result.PnpmArchive.Version, nodeRequirement)
		}
	}
	return result, nil
}

func runtimeProviderPresent(providers []string, required string) bool {
	for _, provider := range providers {
		if provider == required {
			return true
		}
	}
	return false
}
func RuntimePreparationMismatchReasons(spec RunSpec, inventory RuntimeInventory, providers []string) []string {
	if _, err := ResolveRuntimePreparation(spec, inventory, providers); err != nil {
		return []string{err.Error()}
	}
	return nil
}
