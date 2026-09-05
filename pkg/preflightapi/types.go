package preflightapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/guestcontract"
)

// MaxTimeoutSeconds is the largest timeout_seconds value that fits in a time.Duration.

const MaxTimeoutSeconds = int64(1<<63-1) / int64(time.Second)

const AuthTokenHeader = "X-Preflight-Token"

const ClaimTokenHeader = "X-Preflight-Claim-Token"

const DefaultImage = "ubuntu-24.04-ci"

// LegacyDefaultImage remains a worker-side compatibility alias for configs
// generated before ubuntu-24.04-ci became the default runnable image.
const LegacyDefaultImage = "ubuntu-24.04-base"

const DefaultRegion = "default"

const DefaultRunnerSize = "standard"

const DefaultNetworkMode = "none"

const DefaultCacheLaneSizeBytes int64 = 10 * 1024 * 1024 * 1024

const MaxConcurrencyGroupLength = 128

const MaxRunServices = 4

const MaxRunServiceEnvVars = 32

const MaxRunServiceLogs = 32

const MaxRunServicePorts = 16

const MaxRunSteps = 32

const MaxRunStepEnvVars = 32

const MaxWorkerLabels = 32

const WorkerIDSystemLabel = "readyci-worker-id"

const RootFSReleaseSystemLabel = "readyci-rootfs-release"

const RuntimeManifestSystemLabel = "readyci-runtime-manifest"

const ApprovedRuntimeSystemLabel = "readyci-approved-runtime"

const WorkerReleaseSystemLabel = "readyci-worker-release"

// DockerCapabilitySystemLabel is a server-evaluated placement selector. It
// requires a worker that advertises Docker support for the run's image without
// changing the run's native execution mode.
const DockerCapabilitySystemLabel = "readyci-docker-capable"

const (
	WorkerReleaseStateCandidate  = "candidate"
	WorkerReleaseStateProduction = "production"
)

const MaxWorkerLabelKeyLength = 64

const MaxWorkerLabelValueLength = 128

const ReservedWorkerLabelPrefix = "readyci."

const ReservedWorkerSystemLabelPrefix = "readyci-"

// Deprecated: use guestcontract.FailedWorkspaceArtifactPath.
const FailedWorkspaceArtifactPath = guestcontract.FailedWorkspaceArtifactPath

// Deprecated: use guestcontract.FailedWorkspaceArtifactRetainedMessage.
const FailedWorkspaceArtifactRetainedMessage = guestcontract.FailedWorkspaceArtifactRetainedMessage

// Deprecated: use guestcontract.FailedWorkspaceArtifactSkippedMessage.
const FailedWorkspaceArtifactSkippedMessage = guestcontract.FailedWorkspaceArtifactSkippedMessage

const DevOpenWorkerToken = "dev-open-worker-upload"

const WorkerBundleUploadPathPrefix = "/v1/worker-local/runs/"

func TrustedSecretCommandHash(command string) string {
	sum := sha256.Sum256([]byte("readyci-secret-command-v1\n" + command))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TrustedSecretStepsHash(steps []RunStepSpec) string {
	type trustedStep struct {
		Name           string            `json:"name"`
		Command        string            `json:"command"`
		Always         bool              `json:"always"`
		Env            map[string]string `json:"env,omitempty"`
		TimeoutSeconds int               `json:"timeout_seconds"`
	}
	payload := struct {
		Steps []trustedStep `json:"steps"`
	}{Steps: make([]trustedStep, 0, len(steps))}
	for _, step := range steps {
		name := strings.TrimSpace(step.Name)
		command := strings.TrimSpace(step.Command)
		if name == "" || command == "" {
			continue
		}
		env := map[string]string(nil)
		if len(step.Env) > 0 {
			env = make(map[string]string, len(step.Env))
			for key, value := range step.Env {
				env[key] = value
			}
		}
		payload.Steps = append(payload.Steps, trustedStep{
			Name:           name,
			Command:        command,
			Always:         step.Always,
			Env:            env,
			TimeoutSeconds: step.TimeoutSeconds,
		})
	}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256([]byte("readyci-secret-steps-v1\n" + string(encoded)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

const (
	StatusAwaitingUpload = "awaiting_upload"
	StatusQueued         = "queued"
	StatusScheduled      = "scheduled"
	StatusStartingVM     = "starting_vm"
	StatusRunning        = "running"
	StatusSucceeded      = "succeeded"
	StatusFailed         = "failed"
	StatusCancelled      = "cancelled"
	StatusTimedOut       = "timed_out"
	StatusUploadExpired  = "upload_expired"
	StatusBlocked        = "blocked"
	StatusDenied         = "denied"
)

const (
	EventRunQueued        = "run_queued"
	EventRunScheduled     = "run_scheduled"
	EventUploadReceived   = "upload_received"
	EventUploadFailed     = "upload_failed"
	EventVMStarting       = "vm_starting"
	EventAgentReady       = "agent_ready"
	EventCommandStarted   = "command_started"
	EventStdout           = "stdout"
	EventStderr           = "stderr"
	EventCommandExited    = "command_exited"
	EventArtifactUploaded = "artifact_uploaded"
	EventLogTruncated     = "log_truncated"
	EventRunFinished      = "run_finished"
	EventRunFailed        = "run_failed"
	EventRunCancelled     = "run_cancelled"
	EventRunTimedOut      = "run_timed_out"
)

const (
	MaintenanceJobTypeExpiredArtifacts = "expired_artifacts"
	MaintenanceJobTypeBillingEvidence  = "billing_evidence_redaction"
	MaintenanceJobTypeCheckoutVelocity = "billing_checkout_velocity_prune"

	MaintenanceJobStatusRunning   = "running"
	MaintenanceJobStatusSucceeded = "succeeded"
	MaintenanceJobStatusFailed    = "failed"
	MaintenanceJobStatusCancelled = "cancelled"
)
