package preflightapi

// Artifact manifest states. Archives uploaded before manifests were recorded
// (or whose file list exceeded the recordable bound) have no file list; they
// can still be downloaded whole.
const (
	ArtifactManifestAvailable   = "available"
	ArtifactManifestUnavailable = "unavailable"
)

// Preview kinds the server will serve a single archive entry as. Anything
// else is "none" and is only available through the archive download.
const (
	ArtifactPreviewImage = "image"
	ArtifactPreviewText  = "text"
	ArtifactPreviewNone  = "none"
)

// Single-entry preview limits. Images above the image limit are refused;
// text above the text limit is served truncated on a UTF-8 boundary and the
// response carries ArtifactPreviewTruncatedHeader: "true".
const (
	MaxArtifactPreviewImageBytes   = 10 << 20
	MaxArtifactPreviewTextBytes    = 1 << 20
	ArtifactPreviewTruncatedHeader = "X-ReadyCI-Preview-Truncated"
)

// ArtifactManifestResponse lists the files inside a run's retained artifact
// archive, recorded when the worker uploaded it.
type ArtifactManifestResponse struct {
	RunID              string              `json:"run_id"`
	ArtifactID         string              `json:"artifact_id"`
	State              string              `json:"state"`
	TotalExpandedBytes int64               `json:"total_expanded_bytes"`
	Files              []ArtifactFileEntry `json:"files"`
}

type ArtifactFileEntry struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	Preview   string `json:"preview"`
}

// ArtifactFileManifest is the service-validated file list of one artifact
// archive, derived at upload and persisted atomically with that exact archive
// (artifacts.file_manifest). It is service state, not a client input.
type ArtifactFileManifest struct {
	Files              []ArtifactManifestFile `json:"files"`
	TotalExpandedBytes int64                  `json:"total_expanded_bytes"`
}

type ArtifactManifestFile struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
}
