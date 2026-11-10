package preflightapi

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
)

// runtimeCatalogData holds every release-reviewed runtime archive. It is data
// so scripts/readyci-runtime-catalog-refresh.py can propose additions for human
// review (platform/docs/runtime-archive-catalog.md). Changing it changes the
// provider capability digests and the worker release identity, so a merged
// change ships only as a new coordinated API and worker release.
//
//go:embed runtime_catalog.json
var runtimeCatalogData []byte

const runtimeCatalogSchema = "readyci.runtime_catalog.v1"

// maxRuntimeArchiveBytes matches the guest providers' compressed download bound.
const maxRuntimeArchiveBytes = 128 << 20

type runtimeCatalogFile struct {
	Schema   string                      `json:"schema"`
	Catalogs map[string][]RuntimeArchive `json:"catalogs"`
}

// runtimeArchiveKind is one archive family a provider may hold, with the only
// URL it may use. A provider's later kinds are companions: each requires the
// provider's first kind at the same version, as pnpm-native requires pnpm.
type runtimeArchiveKind struct {
	runtime string
	url     func(version string) string
}

type runtimeArchiveProviderSpec struct {
	kinds  []runtimeArchiveKind
	sha512 bool
}

// Adding a provider means adding its entry here and its catalog to the data file.
var runtimeArchiveProviderSpecs = map[string]runtimeArchiveProviderSpec{
	GoArchiveRuntimeProvider: {kinds: []runtimeArchiveKind{{url: func(version string) string {
		return "https://" + GoRuntimeDownloadHost + "/go/go" + version + ".linux-amd64.tar.gz"
	}}}},
	NodeArchiveRuntimeProvider: {sha512: true, kinds: []runtimeArchiveKind{{runtime: "node", url: func(version string) string {
		return "https://" + NodeRuntimeDownloadHost + "/dist/v" + version + "/node-v" + version + "-linux-x64.tar.gz"
	}}}},
	PnpmArchiveRuntimeProvider: {sha512: true, kinds: []runtimeArchiveKind{
		{runtime: "pnpm", url: func(version string) string {
			return "https://" + PnpmRuntimeDownloadHost + "/pnpm/-/pnpm-" + version + ".tgz"
		}},
		{runtime: "pnpm-native", url: func(version string) string {
			return "https://" + PnpmRuntimeDownloadHost + "/@pnpm/exe.linux-x64/-/exe.linux-x64-" + version + ".tgz"
		}},
	}},
}

var (
	runtimeCatalogs    = mustParseRuntimeCatalogs(runtimeCatalogData)
	goRuntimeCatalog   = runtimeCatalogs[GoArchiveRuntimeProvider]
	nodeRuntimeCatalog = runtimeCatalogs[NodeArchiveRuntimeProvider]
	pnpmRuntimeCatalog = runtimeCatalogs[PnpmArchiveRuntimeProvider]
)

var (
	runtimeArchiveSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)
	runtimeArchiveSHA512 = regexp.MustCompile(`^[0-9a-f]{128}$`)
)

// The catalog is compiled in, so an invalid file is a build defect rather than
// an operating condition. Fail at load instead of serving a partial catalog.
func mustParseRuntimeCatalogs(data []byte) map[string][]RuntimeArchive {
	catalogs, err := parseRuntimeCatalogs(data)
	if err != nil {
		panic("invalid embedded runtime catalog: " + err.Error())
	}
	return catalogs
}

func parseRuntimeCatalogs(data []byte) (map[string][]RuntimeArchive, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var file runtimeCatalogFile
	if err := decoder.Decode(&file); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing data after catalog")
	}
	if file.Schema != runtimeCatalogSchema {
		return nil, fmt.Errorf("unsupported schema %q", file.Schema)
	}
	if len(file.Catalogs) != len(runtimeArchiveProviderSpecs) {
		return nil, fmt.Errorf("catalog providers must be exactly %v", sortedRuntimeArchiveProviders())
	}
	for provider, archives := range file.Catalogs {
		spec, known := runtimeArchiveProviderSpecs[provider]
		if !known {
			return nil, fmt.Errorf("unknown provider %q", provider)
		}
		if err := validateRuntimeArchiveCatalog(spec, archives); err != nil {
			return nil, fmt.Errorf("%s: %w", provider, err)
		}
	}
	return file.Catalogs, nil
}

// validateRuntimeArchiveCatalog requires exact provider URLs and digests and a
// canonical order: ascending version, then kind order. Catalog digests depend on
// that order, so the refresh script inserts entries in the same position.
func validateRuntimeArchiveCatalog(spec runtimeArchiveProviderSpec, archives []RuntimeArchive) error {
	if len(archives) == 0 {
		return fmt.Errorf("catalog is empty")
	}
	primaries := map[string]bool{}
	var previous runtimeVersion
	previousRank := -1
	for index, archive := range archives {
		version, err := parseRuntimeVersion(archive.Version)
		if err != nil || version.count != 3 || archive.Version[0] == 'v' {
			return fmt.Errorf("archive %d has invalid version %q", index, archive.Version)
		}
		rank := -1
		for candidate, kind := range spec.kinds {
			if kind.runtime == archive.Runtime {
				rank = candidate
			}
		}
		if rank < 0 {
			return fmt.Errorf("%s has unsupported runtime %q", archive.Version, archive.Runtime)
		}
		if archive.OS != "linux" || archive.Arch != "amd64" || archive.URL != spec.kinds[rank].url(archive.Version) {
			return fmt.Errorf("%s %s is not the approved linux/amd64 URL", archive.Runtime, archive.Version)
		}
		if !runtimeArchiveSHA256.MatchString(archive.SHA256) || spec.sha512 != runtimeArchiveSHA512.MatchString(archive.SHA512) || !spec.sha512 && archive.SHA512 != "" {
			return fmt.Errorf("%s %s has invalid digests", archive.Runtime, archive.Version)
		}
		if archive.SizeBytes <= 0 || archive.SizeBytes > maxRuntimeArchiveBytes {
			return fmt.Errorf("%s %s has invalid size", archive.Runtime, archive.Version)
		}
		if index > 0 {
			order := compareRuntimeVersions(version, previous)
			if order < 0 || order == 0 && rank <= previousRank {
				return fmt.Errorf("%s %s is out of canonical order or duplicated", archive.Runtime, archive.Version)
			}
		}
		if rank == 0 {
			primaries[archive.Version] = true
		} else if !primaries[archive.Version] {
			return fmt.Errorf("%s %s has no %s archive of the same version", archive.Runtime, archive.Version, spec.kinds[0].runtime)
		}
		previous, previousRank = version, rank
	}
	return nil
}

func sortedRuntimeArchiveProviders() []string {
	providers := make([]string, 0, len(runtimeArchiveProviderSpecs))
	for provider := range runtimeArchiveProviderSpecs {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	return providers
}
