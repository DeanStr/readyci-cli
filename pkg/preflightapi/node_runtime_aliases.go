package preflightapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

const RuntimeResolutionPolicySetupNodeLTSImport = "setup_node_lts_import_v1"

// RuntimeAliasRequest is a setup-only request. Moving selectors never enter a
// run specification or authorize runtime downloads.
type RuntimeAliasRequest struct {
	Runtime   string `json:"runtime"`
	Selector  string `json:"selector"`
	ActionRef string `json:"action_ref"`
}

func ResolveSetupRuntimeAlias(request RuntimeAliasRequest, now time.Time) (RuntimeResolution, error) {
	if request.ActionRef != NodeRuntimeAliasCatalogMetadata().ReviewedActionRef {
		return RuntimeResolution{}, fmt.Errorf("moving LTS selector requires the reviewed pinned setup-node action; choose an explicit numeric runtime for other action references")
	}
	return ResolveRuntimeAlias(request.Runtime, request.Selector, now)
}

// RuntimeResolution is setup-import provenance for a reviewed moving declaration.
// It is never configuration or execution input; the saved numeric requirement
// uses the ordinary server-owned runtime and archive policy.
type RuntimeResolution struct {
	Selector      string `json:"selector" yaml:"selector"`
	Policy        string `json:"policy" yaml:"policy"`
	Requirement   string `json:"requirement" yaml:"requirement"`
	CatalogDigest string `json:"catalog_digest" yaml:"catalog_digest"`
}

type NodeRuntimeAliasRelease struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
	LTS     string `json:"lts,omitempty"`
}

// NodeRuntimeAliasSeries is a scheduled Active LTS start from nodejs/Release.
type NodeRuntimeAliasSeries struct {
	Major    int    `json:"major"`
	LTSStart string `json:"lts_start"`
}

type NodeRuntimeAliasCatalog struct {
	Schema            string                    `json:"schema"`
	Policy            string                    `json:"policy"`
	SourceURL         string                    `json:"source_url"`
	SourceSHA256      string                    `json:"source_sha256"`
	ScheduleURL       string                    `json:"schedule_url"`
	ScheduleSHA256    string                    `json:"schedule_sha256"`
	ReviewedActionRef string                    `json:"reviewed_action_ref"`
	ReviewedAt        time.Time                 `json:"reviewed_at"`
	ValidBefore       time.Time                 `json:"valid_before"`
	Releases          []NodeRuntimeAliasRelease `json:"releases"`
	Series            []NodeRuntimeAliasSeries  `json:"series"`
}

// Releases are the stable series heads observed in the immutable setup-node
// manifest at review. The schedule bounds validity before the next transition;
// it cannot establish that a release was published or entered the manifest.
func NodeRuntimeAliasCatalogMetadata() NodeRuntimeAliasCatalog {
	return nodeRuntimeAliasCatalog20261025()
}

func nodeRuntimeAliasCatalog20261025() NodeRuntimeAliasCatalog {
	return NodeRuntimeAliasCatalog{
		Schema: "readyci.node_runtime_alias_catalog.v2", Policy: RuntimeResolutionPolicySetupNodeLTSImport,
		SourceURL:         "https://raw.githubusercontent.com/actions/node-versions/810ef37cb22ad2b0d8da543e1644e4afc366f496/versions-manifest.json",
		SourceSHA256:      "c99b512c5a0d27b742d420625dea067e4c608fd64dc7834dd615b9e8d8010812",
		ScheduleURL:       "https://raw.githubusercontent.com/nodejs/Release/de0b20f409ba90dcde129b4bfb85b47c3c422ecb/schedule.json",
		ScheduleSHA256:    "26c8e4743fc3a76d686a038c2df40426bc3bdc7a018cb83f3b88bbf89f45436f",
		ReviewedActionRef: "actions/setup-node@949feb2413d6458794dcd2491c4babbbce0c15c1",
		ReviewedAt:        time.Date(2026, 10, 25, 22, 53, 34, 0, time.UTC), ValidBefore: time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC),
		Releases: []NodeRuntimeAliasRelease{{Version: "26.12.1", Stable: true}, {Version: "24.22.1", Stable: true, LTS: "Krypton"}, {Version: "22.23.4", Stable: true, LTS: "Jod"}, {Version: "20.20.2", Stable: true, LTS: "Iron"}},
		Series:   []NodeRuntimeAliasSeries{{Major: 20, LTSStart: "2023-10-24"}, {Major: 22, LTSStart: "2024-10-29"}, {Major: 24, LTSStart: "2025-10-28"}, {Major: 26, LTSStart: "2026-10-28"}, {Major: 27, LTSStart: "2027-10-20"}},
	}
}

func NodeRuntimeAliasCatalogDigest() string {
	return nodeRuntimeAliasCatalogDigest(NodeRuntimeAliasCatalogMetadata())
}

func nodeRuntimeAliasCatalogDigest(catalog NodeRuntimeAliasCatalog) string {
	data, _ := json.Marshal(catalog)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// nodeRuntimeAliasRelease reports the newest stable LTS series in the observed
// manifest projection.
func nodeRuntimeAliasRelease(catalog NodeRuntimeAliasCatalog) (string, string, error) {
	var chosen runtimeVersion
	var version string
	for _, release := range catalog.Releases {
		parsed, err := parseRuntimeVersion(release.Version)
		if err != nil || parsed.count != 3 || parsed.String() != release.Version {
			return "", "", fmt.Errorf("reviewed Node alias catalog contains an invalid release")
		}
		if release.Stable && release.LTS != "" && (version == "" || compareRuntimeVersions(parsed, chosen) > 0) {
			chosen, version = parsed, release.Version
		}
	}
	if version == "" {
		return "", "", fmt.Errorf("reviewed Node alias catalog contains no stable LTS release")
	}
	return fmt.Sprint(chosen.parts[0]), version, nil
}

func canonicalRuntimeAliasFromCatalog(runtime, selector string, catalog NodeRuntimeAliasCatalog) (RuntimeResolution, error) {
	if runtime != "node" || selector != "lts/*" {
		return RuntimeResolution{}, fmt.Errorf("unsupported moving runtime selector %s %q; only Node lts/* with default check-latest=false is reviewed", runtime, selector)
	}
	requirement, _, err := nodeRuntimeAliasRelease(catalog)
	if err != nil {
		return RuntimeResolution{}, err
	}
	return RuntimeResolution{Selector: selector, Policy: catalog.Policy, Requirement: requirement, CatalogDigest: nodeRuntimeAliasCatalogDigest(catalog)}, nil
}

// ResolveRuntimeAlias resolves a moving declaration once, during setup import.
// Only the numeric requirement is saved in configuration or sent for execution.
func ResolveRuntimeAlias(runtime, selector string, now time.Time) (RuntimeResolution, error) {
	catalog := NodeRuntimeAliasCatalogMetadata()
	if now.Before(catalog.ReviewedAt) || !now.Before(catalog.ValidBefore) {
		return RuntimeResolution{}, fmt.Errorf("reviewed Node alias catalog is not valid at %s (valid from %s before %s); update the service import catalog or choose an explicit numeric runtime", now.UTC().Format(time.RFC3339), catalog.ReviewedAt.Format(time.RFC3339), catalog.ValidBefore.Format(time.RFC3339))
	}
	return canonicalRuntimeAliasFromCatalog(runtime, selector, catalog)
}

func CloneRuntimeResolutions(values map[string]RuntimeResolution) map[string]RuntimeResolution {
	if values == nil {
		return nil
	}
	result := make(map[string]RuntimeResolution, len(values))
	for name, value := range values {
		result[name] = value
	}
	return result
}
