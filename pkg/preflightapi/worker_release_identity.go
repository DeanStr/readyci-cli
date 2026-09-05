package preflightapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const WorkerReleaseIdentitySchemaVersion = "readyci.worker_release_identity.v1"

// WorkerReleaseIdentity is the complete production environment promoted as a
// unit. Component digests are lowercase SHA-256 hex; Digest is the canonical
// sha256: identity of the normalized structure with Digest cleared.
type WorkerReleaseIdentity struct {
	SchemaVersion         string `json:"schema_version"`
	WorkerBinarySHA256    string `json:"worker_binary_sha256"`
	RootFSReleaseSHA256   string `json:"rootfs_release_sha256"`
	RuntimeManifestSHA256 string `json:"runtime_manifest_sha256"`
	ApprovedRuntimeSHA256 string `json:"approved_runtime_sha256"`
	IsolationConfigSHA256 string `json:"isolation_config_sha256"`
	Digest                string `json:"digest"`
}

func NewWorkerReleaseIdentity(binary, rootfs, runtimeManifest, approvedRuntime, isolation string) (WorkerReleaseIdentity, error) {
	identity := WorkerReleaseIdentity{
		SchemaVersion:         WorkerReleaseIdentitySchemaVersion,
		WorkerBinarySHA256:    strings.TrimSpace(binary),
		RootFSReleaseSHA256:   strings.TrimSpace(rootfs),
		RuntimeManifestSHA256: strings.TrimSpace(runtimeManifest),
		ApprovedRuntimeSHA256: strings.TrimSpace(approvedRuntime),
		IsolationConfigSHA256: strings.TrimSpace(isolation),
	}
	for name, value := range map[string]string{
		"worker_binary_sha256":    identity.WorkerBinarySHA256,
		"rootfs_release_sha256":   identity.RootFSReleaseSHA256,
		"runtime_manifest_sha256": identity.RuntimeManifestSHA256,
		"approved_runtime_sha256": identity.ApprovedRuntimeSHA256,
		"isolation_config_sha256": identity.IsolationConfigSHA256,
	} {
		if err := ValidateSHA256Hex(value); err != nil {
			return WorkerReleaseIdentity{}, fmt.Errorf("worker release identity %s: %w", name, err)
		}
	}
	digest, err := WorkerReleaseIdentityDigest(identity)
	if err != nil {
		return WorkerReleaseIdentity{}, err
	}
	identity.Digest = digest
	return identity, nil
}

func WorkerReleaseIdentityDigest(identity WorkerReleaseIdentity) (string, error) {
	identity.Digest = ""
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func ValidateWorkerReleaseIdentity(identity WorkerReleaseIdentity) error {
	if identity.SchemaVersion != WorkerReleaseIdentitySchemaVersion {
		return fmt.Errorf("unsupported worker release identity schema %q", identity.SchemaVersion)
	}
	normalized, err := NewWorkerReleaseIdentity(
		identity.WorkerBinarySHA256,
		identity.RootFSReleaseSHA256,
		identity.RuntimeManifestSHA256,
		identity.ApprovedRuntimeSHA256,
		identity.IsolationConfigSHA256,
	)
	if err != nil {
		return err
	}
	if identity != normalized {
		return fmt.Errorf("worker release identity is not normalized or digest does not match")
	}
	return nil
}
