package preflightapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const NodeArchiveRuntimeProvider = "node_archive_v1"
const NodeRuntimeDownloadHost = "nodejs.org"
const PnpmArchiveRuntimeProvider = "pnpm_archive_v1"
const PnpmRuntimeDownloadHost = "registry.npmjs.org"

// Node and pnpm archives live in runtime_catalog.json. Node entries are checked
// against Node's signed release checksums and pnpm entries against npm's signed
// distribution metadata. SHA-512 is the complete npm tarball digest used by
// Corepack packageManager integrity pins. pnpm 12 additionally requires its
// separately reviewed native distribution; no install script runs.

func NodeRuntimeCatalog() []RuntimeArchive {
	return append([]RuntimeArchive(nil), nodeRuntimeCatalog...)
}

func PnpmRuntimeCatalog() []RuntimeArchive {
	return append([]RuntimeArchive(nil), pnpmRuntimeCatalog...)
}

func runtimeArchiveCatalogDigest(provider string, archives []RuntimeArchive) string {
	payload := struct {
		Schema   string           `json:"schema"`
		Provider string           `json:"provider"`
		Archives []RuntimeArchive `json:"archives"`
	}{Schema: "readyci.runtime_archive_catalog.v1", Provider: provider, Archives: archives}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func NodeRuntimeCatalogDigest() string {
	return runtimeArchiveCatalogDigest(NodeArchiveRuntimeProvider, NodeRuntimeCatalog())
}

func PnpmRuntimeCatalogDigest() string {
	return runtimeArchiveCatalogDigest(PnpmArchiveRuntimeProvider, PnpmRuntimeCatalog())
}

func NodeRuntimeProviderCapability() string {
	return NodeArchiveRuntimeProvider + ":sha256:" + NodeRuntimeCatalogDigest()
}

func PnpmRuntimeProviderCapability() string {
	return PnpmArchiveRuntimeProvider + ":sha256:" + PnpmRuntimeCatalogDigest()
}

// ApprovedPnpmIntegrityArchive reports whether a reviewed pnpm archive matches
// a Corepack integrity pin. Installed image inventory can never prove such a
// pin, so this catalog is the only way a worker can satisfy one.
func ApprovedPnpmIntegrityArchive(requirement string) bool {
	version, digest, ok := PackageManagerIntegrityRequirement(requirement)
	if !ok {
		return false
	}
	for _, artifact := range pnpmRuntimeCatalog {
		if artifact.Runtime == "pnpm" && artifact.Version == version && artifact.SHA512 == digest {
			return true
		}
	}
	return false
}
