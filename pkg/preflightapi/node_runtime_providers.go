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

// Catalog inputs were checked against Node's signed release checksums and npm's
// signed distribution metadata on 2026-10-19. SHA-512 is the complete npm tarball
// digest used by Corepack packageManager integrity pins. pnpm 12 additionally
// requires its separately reviewed native distribution; no install script runs.
// Sources: https://nodejs.org/dist/v24.15.0/SHASUMS256.txt.asc,
// https://registry.npmjs.org/pnpm/10.33.4,
// https://registry.npmjs.org/pnpm/12.11.2 and
// https://registry.npmjs.org/@pnpm%2fexe.linux-x64/12.11.2.
var nodeRuntimeCatalog = []RuntimeArchive{
	{Version: "24.15.0", OS: "linux", Arch: "amd64", URL: "https://nodejs.org/dist/v24.15.0/node-v24.15.0-linux-x64.tar.gz", SHA256: "44836872d9aec49f1e6b52a9a922872db9a2b02d235a616a5681b6a85fec8d89", SizeBytes: 56579517, Runtime: "node", SHA512: "455150401b5e2ca49fd38a913e1a58fa993111853aaeeac5f793407d14e93548270ae267564d083bd4d4f77d23518124644cff873bf04ab8af663be03f71e099"},
}
var pnpmRuntimeCatalog = []RuntimeArchive{
	{Version: "10.33.4", OS: "linux", Arch: "amd64", URL: "https://registry.npmjs.org/pnpm/-/pnpm-10.33.4.tgz", SHA256: "8e70ddc6649b18bc3d895cf3a908c0291ea4c38039ad8722c47e018daf1e9cfc", SHA512: "1c67b3b359b2d408119ba1ed289f34b8fc3c6873412bec6fd264fbdc82489e510fcbecb9ce9d22dae7f3b76269d8441046014bdca53b9979cd7a561ad631b800", SizeBytes: 4566847, Runtime: "pnpm"},
	{Version: "12.11.2", OS: "linux", Arch: "amd64", URL: "https://registry.npmjs.org/pnpm/-/pnpm-12.11.2.tgz", SHA256: "eb57341b8b54fddaeededb15dbb77cc30a11750cecca301bfda02478fdff3a00", SHA512: "25a51652a196785af5f28bda6dfe8dc582ebefa4ac1d180221ce1390231c5fb76637822ea4b6e4633ce74acdcb0a77f725af11bad38177fdeddaee818a955cb9", SizeBytes: 1041013, Runtime: "pnpm"},
	{Version: "12.11.2", OS: "linux", Arch: "amd64", URL: "https://registry.npmjs.org/@pnpm/exe.linux-x64/-/exe.linux-x64-12.11.2.tgz", SHA256: "79008d3130845ee8a0fe4381e6b81815e80e1bd411702f34721b1cc63e6e20bf", SHA512: "14aed029edb8018829a49c0ede6a68a18a6d76ebaf206a8a24f8b969cecc7675a48d4cb8e8905f621096d20ae7916282f1839fd31faf1af8ae42a2448f74a941", SizeBytes: 23919843, Runtime: "pnpm-native"},
}

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
