package preflightapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/guestcontract"
)

type WorkerUploadReservation struct {
	WorkerID         string            `json:"worker_id"`
	UploadBaseURL    string            `json:"upload_base_url"`
	BaselineReusable bool              `json:"baseline_reusable,omitempty"`
	Baseline         WorkspaceBaseline `json:"baseline,omitempty"`
}

type WorkspaceBaseline struct {
	Key         string `json:"key"`
	TeamID      string `json:"team_id,omitempty"`
	ProjectID   string `json:"project_id"`
	CacheLaneID string `json:"cache_lane_id"`
}

func SignWorkerBundleUpload(runID, workerID, baselineKey string, expiresUnix int64, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "worker-bundle-upload-v1\n%s\n%s\n%s\n%d", runID, workerID, baselineKey, expiresUnix)
	return hex.EncodeToString(mac.Sum(nil))
}

func SignWorkerBundleUploadScoped(runID, workerID string, baseline WorkspaceBaseline, expiresUnix int64, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "worker-bundle-upload-v2\n%s\n%s\n%s\n%s\n%s\n%s\n%d", runID, workerID, baseline.Key, baseline.TeamID, baseline.ProjectID, baseline.CacheLaneID, expiresUnix)
	return hex.EncodeToString(mac.Sum(nil))
}

func VerifyWorkerBundleUpload(runID, workerID, baselineKey string, expiresUnix int64, signature, secret string, now time.Time) bool {
	if strings.TrimSpace(workerID) == "" || strings.TrimSpace(secret) == "" || strings.TrimSpace(signature) == "" {
		return false
	}
	if expiresUnix <= 0 || now.Unix() > expiresUnix {
		return false
	}
	want := SignWorkerBundleUpload(runID, workerID, baselineKey, expiresUnix, secret)
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(signature)), []byte(want)) == 1
}

func VerifyWorkerBundleUploadScoped(runID, workerID string, baseline WorkspaceBaseline, expiresUnix int64, signature, secret string, now time.Time) bool {
	if strings.TrimSpace(workerID) == "" || strings.TrimSpace(secret) == "" || strings.TrimSpace(signature) == "" {
		return false
	}
	if expiresUnix <= 0 || now.Unix() > expiresUnix {
		return false
	}
	want := SignWorkerBundleUploadScoped(runID, workerID, baseline, expiresUnix, secret)
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(signature)), []byte(want)) == 1
}

type WorkerBundleUploadProof struct {
	RunID       string `json:"run_id"`
	WorkerID    string `json:"worker_id"`
	BaselineKey string `json:"baseline_key,omitempty"`
	ExpiresUnix int64  `json:"expires"`
	SizeBytes   int64  `json:"size_bytes"`
	Signature   string `json:"signature"`
}

type WorkerBundleUploadResponse struct {
	Status string                  `json:"status"`
	Bytes  int64                   `json:"bytes,omitempty"`
	Proof  WorkerBundleUploadProof `json:"proof,omitempty"`
}

func SignWorkerBundleUploadProof(runID, workerID, baselineKey string, expiresUnix, sizeBytes int64, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "worker-bundle-upload-proof-v1\n%s\n%s\n%s\n%d\n%d", runID, workerID, baselineKey, expiresUnix, sizeBytes)
	return hex.EncodeToString(mac.Sum(nil))
}

func VerifyWorkerBundleUploadProof(proof WorkerBundleUploadProof, secret string, now time.Time) bool {
	if strings.TrimSpace(proof.RunID) == "" || strings.TrimSpace(proof.WorkerID) == "" || strings.TrimSpace(secret) == "" || strings.TrimSpace(proof.Signature) == "" {
		return false
	}
	if proof.ExpiresUnix <= 0 || proof.SizeBytes < 0 || now.Unix() > proof.ExpiresUnix {
		return false
	}
	want := SignWorkerBundleUploadProof(proof.RunID, proof.WorkerID, proof.BaselineKey, proof.ExpiresUnix, proof.SizeBytes, secret)
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(proof.Signature)), []byte(want)) == 1
}

const EnvironmentCapabilityProbeVersion = guestcontract.EnvironmentCapabilityProbeVersion

// EnvironmentCapabilityInventory records capabilities established by the guest
// acceptance probe for exact kernel and image bytes, only in the tested network
// modes. ReleaseIdentityDigest binds the advertisement to the promoted worker.
type EnvironmentCapabilityInventory struct {
	KernelSHA256          string   `json:"kernel_sha256"`
	RootFSSHA256          string   `json:"rootfs_sha256"`
	ReleaseIdentityDigest string   `json:"release_identity_digest"`
	ProbeVersion          string   `json:"probe_version"`
	NetworkModes          []string `json:"network_modes"`
	Capabilities          []string `json:"capabilities"`
}

func CloneEnvironmentCapabilities(values map[string]EnvironmentCapabilityInventory) map[string]EnvironmentCapabilityInventory {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]EnvironmentCapabilityInventory, len(values))
	for image, value := range values {
		value.Capabilities = CloneEnvironmentRequirements(value.Capabilities)
		value.NetworkModes = append([]string(nil), value.NetworkModes...)
		result[image] = value
	}
	return result
}

type WorkerCapabilityCheckRequest struct {
	Spec RunSpec `json:"spec"`
}

type WorkerCapabilityCheckResponse struct {
	RuntimePreparation        []RuntimeArchive        `json:"runtime_preparation,omitempty"`
	ScratchSizeBytes          int64                   `json:"scratch_size_bytes,omitempty"`
	Claimable                 bool                    `json:"claimable"`
	UploadCapable             bool                    `json:"upload_capable"`
	FreshWorkerCount          int                     `json:"fresh_worker_count"`
	MatchingWorkerCount       int                     `json:"matching_worker_count"`
	MatchingUploadWorkerCount int                     `json:"matching_upload_worker_count"`
	Reasons                   []string                `json:"reasons,omitempty"`
	UploadReasons             []string                `json:"upload_reasons,omitempty"`
	Policy                    *WorkerCapabilityPolicy `json:"policy,omitempty"`
}

// WorkerCapabilityPolicy reports current server-owned admission limits without
// creating a session, reserving capacity, or authorizing subsequent execution.
type WorkerCapabilityPolicy struct {
	Allowed    bool   `json:"allowed"`
	StatusCode int    `json:"status_code,omitempty"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
}

type AdminWorkerSummary struct {
	EnvironmentCapabilities map[string]EnvironmentCapabilityInventory `json:"environment_capabilities,omitempty"`
	RuntimeProviders        map[string][]string                       `json:"runtime_providers,omitempty"`
	RuntimeInventory        map[string]RuntimeInventory               `json:"runtime_inventory,omitempty"`
	ScratchSizeBytes        map[string]int64                          `json:"scratch_size_bytes,omitempty"`
	ID                      string                                    `json:"id"`
	PersistentWorkerID      string                                    `json:"persistent_worker_id,omitempty"`
	Region                  string                                    `json:"region"`
	Runtime                 string                                    `json:"runtime"`
	Version                 string                                    `json:"version,omitempty"`
	BuildDate               string                                    `json:"build_date,omitempty"`
	Hostname                string                                    `json:"hostname,omitempty"`
	Platform                string                                    `json:"platform,omitempty"`
	SupportedRunnerSizes    []string                                  `json:"supported_runner_sizes"`
	SupportedImages         []string                                  `json:"supported_images"`
	SupportedDockerImages   []string                                  `json:"supported_docker_images"`
	NetworkModes            []string                                  `json:"network_modes"`
	Labels                  map[string]string                         `json:"labels,omitempty"`
	ReleaseIdentity         *WorkerReleaseIdentity                    `json:"release_identity,omitempty"`
	ReleaseIdentityDigest   string                                    `json:"release_identity_digest,omitempty"`
	SupportsDocker          bool                                      `json:"supports_docker"`
	MaxActiveRuns           int                                       `json:"max_active_runs"`
	MaxActiveComputeSlots   int                                       `json:"max_active_compute_slots,omitempty"`
	ActiveRuns              int                                       `json:"active_runs"`
	NonTerminalRuns         int                                       `json:"non_terminal_runs"`
	BundleUploadBaseURL     string                                    `json:"bundle_upload_base_url,omitempty"`
	Draining                bool                                      `json:"draining"`
	DrainReason             string                                    `json:"drain_reason,omitempty"`
	ReleaseState            string                                    `json:"release_state"`
	CandidateSHA            string                                    `json:"candidate_sha,omitempty"`
	CandidateSince          *time.Time                                `json:"candidate_since,omitempty"`
	PromotedAt              *time.Time                                `json:"promoted_at,omitempty"`
	AcceptanceRunID         string                                    `json:"acceptance_run_id,omitempty"`
	LastSeenAt              *time.Time                                `json:"last_seen_at,omitempty"`
	LastClaimedAt           *time.Time                                `json:"last_claimed_at,omitempty"`
	CreatedAt               time.Time                                 `json:"created_at"`
	UpdatedAt               time.Time                                 `json:"updated_at"`
}

type AdminListWorkersResponse struct {
	Workers []AdminWorkerSummary `json:"workers"`
}

type AdminDrainWorkerRequest struct {
	Reason                 string `json:"reason,omitempty"`
	MinimumProductionSlots int    `json:"minimum_production_slots,omitempty"`
	MaintenanceCutover     bool   `json:"maintenance_cutover,omitempty"`
}

type AdminUndrainWorkerRequest struct {
	ExpectedDrainReason string `json:"expected_drain_reason,omitempty"`
}

type AdminDrainWorkerResponse struct {
	Worker AdminWorkerSummary `json:"worker"`
}

type AdminStageWorkerRequest struct {
	CandidateSHA           string `json:"candidate_sha"`
	MinimumProductionSlots int    `json:"minimum_production_slots,omitempty"`
	MaintenanceCutover     bool   `json:"maintenance_cutover,omitempty"`
}

type AdminPromoteWorkerRequest struct {
	CandidateSHA           string `json:"candidate_sha"`
	Version                string `json:"version"`
	RuntimeManifestDigest  string `json:"runtime_manifest_digest"`
	ApprovedRuntimeDigest  string `json:"approved_runtime_digest"`
	WorkerReleaseDigest    string `json:"worker_release_digest"`
	AcceptanceRunID        string `json:"acceptance_run_id"`
	MinimumProductionSlots int    `json:"minimum_production_slots,omitempty"`
}

type AdminWorkerReleaseResponse struct {
	Worker                   AdminWorkerSummary `json:"worker"`
	RemainingProductionSlots int                `json:"remaining_production_slots"`
}

type AdminDeleteWorkerRequest struct {
	Reason string `json:"reason,omitempty"`
}

type AdminDeleteWorkerResponse struct {
	WorkerID             string `json:"worker_id"`
	Deleted              bool   `json:"deleted"`
	ClearedRunReferences int    `json:"cleared_run_references"`
	DeletedCacheLanes    int    `json:"deleted_cache_lanes"`
}

type ClaimRunRequest struct {
	EnvironmentCapabilities map[string]EnvironmentCapabilityInventory `json:"environment_capabilities,omitempty"`
	RuntimeProviders        map[string][]string                       `json:"runtime_providers,omitempty"`
	RuntimeInventory        map[string]RuntimeInventory               `json:"runtime_inventory,omitempty"`
	ScratchSizeBytes        map[string]int64                          `json:"scratch_size_bytes,omitempty"`
	WorkerID                string                                    `json:"worker_id"`
	PersistentWorkerID      string                                    `json:"persistent_worker_id,omitempty"`
	Region                  string                                    `json:"region,omitempty"`
	Runtime                 string                                    `json:"runtime,omitempty"`
	Version                 string                                    `json:"version,omitempty"`
	BuildDate               string                                    `json:"build_date,omitempty"`
	Hostname                string                                    `json:"hostname,omitempty"`
	Platform                string                                    `json:"platform,omitempty"`
	SupportedRunnerSizes    []string                                  `json:"supported_runner_sizes"`
	SupportedImages         []string                                  `json:"supported_images"`
	SupportedDockerImages   []string                                  `json:"supported_docker_images"`
	SupportsDocker          bool                                      `json:"supports_docker"`
	NetworkModes            []string                                  `json:"network_modes"`
	Labels                  map[string]string                         `json:"labels,omitempty"`
	ReleaseIdentity         *WorkerReleaseIdentity                    `json:"release_identity,omitempty"`
	Draining                bool                                      `json:"draining,omitempty"`
	DrainReason             string                                    `json:"drain_reason,omitempty"`
	MaxActiveRuns           int                                       `json:"max_active_runs"`
	MaxActiveComputeSlots   int                                       `json:"max_active_compute_slots,omitempty"`
	CacheLaneSizeBytes      int64                                     `json:"cache_lane_size_bytes,omitempty"`
	BundleUploadBaseURL     string                                    `json:"bundle_upload_base_url,omitempty"`
	WorkspaceBaselineKeys   []string                                  `json:"workspace_baseline_keys,omitempty"`
	WorkspaceBaselines      []WorkspaceBaseline                       `json:"workspace_baselines,omitempty"`
}

type ClaimRunResponse struct {
	Run *RunSpec `json:"run,omitempty"`
}

type CreateWorkerLeaseRequest struct {
	WorkerID string `json:"worker_id"`
}

type CreateWorkerLeaseResponse struct {
	WorkerID  string    `json:"worker_id"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type CompleteCachePurgeRequest struct {
	WorkerID string `json:"worker_id"`
}
