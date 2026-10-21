package syncbundle

import (
	"encoding/json"
	"fmt"
	"io"
)

// AdmissionLimits is shared by preview, packaging and source admission. These
// are hard source limits, independent of the selected runner's resources.
type AdmissionLimits struct {
	CompressedBytes int64 `json:"compressed_bytes"`
	ExpandedBytes   int64 `json:"expanded_bytes"`
	ManifestBytes   int64 `json:"manifest_bytes"`
	Entries         int   `json:"entries"`
}

func SourceAdmissionLimits() AdmissionLimits {
	return AdmissionLimits{MaxCompressedBundleBytes, MaxExpandedBundleBytes, MaxManifestBytes, MaxBundleEntries}
}

type AdmissionUsage struct {
	ManifestBytes int64 `json:"manifest_bytes"`
	Entries       int   `json:"entries"`
}

// AdmissionError reports which bounded source resource needs attention. It is
// a configuration/source failure, not an execution failure or a retry signal.
type AdmissionError struct {
	Resource string
	Observed int64
	Limit    int64
}

func (err *AdmissionError) Error() string {
	switch err.Resource {
	case "manifest_bytes":
		return fmt.Sprintf("manifest.json exceeds %d bytes (%d bytes); reduce selected source metadata before upload", err.Limit, err.Observed)
	case "entries":
		return fmt.Sprintf("bundle contains more than %d entries (%d entries); reduce selected source before upload", err.Limit, err.Observed)
	case "expanded_bytes":
		return fmt.Sprintf("bundle decompressed stream exceeds %d bytes (%d bytes)", err.Limit, err.Observed)
	default:
		return fmt.Sprintf("compressed bundle exceeds %d bytes (%d bytes)", err.Limit, err.Observed)
	}
}

// ManifestAdmission uses the exact compact encoding emitted by WriteBundle.
// A delta still declares its baseline metadata but carries no baseline files.
func ManifestAdmission(manifest Manifest) (AdmissionUsage, error) {
	data, err := json.Marshal(manifest)
	if err != nil {
		return AdmissionUsage{}, err
	}
	usage := AdmissionUsage{ManifestBytes: int64(len(data)), Entries: 1 + len(manifest.Workspace)}
	if !manifest.BaselineRef {
		usage.Entries += len(manifest.Baseline)
	}
	if usage.ManifestBytes > MaxManifestBytes {
		return usage, &AdmissionError{"manifest_bytes", usage.ManifestBytes, MaxManifestBytes}
	}
	if usage.Entries > MaxBundleEntries {
		return usage, &AdmissionError{"entries", int64(usage.Entries), MaxBundleEntries}
	}
	return usage, nil
}

type admissionWriter struct {
	writer   io.Writer
	resource string
	limit    int64
	written  int64
}

func (writer *admissionWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > writer.limit-writer.written {
		return 0, &AdmissionError{writer.resource, writer.written + int64(len(data)), writer.limit}
	}
	n, err := writer.writer.Write(data)
	writer.written += int64(n)
	return n, err
}
