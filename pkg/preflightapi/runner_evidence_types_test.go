package preflightapi

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func TestRunnerEvidenceAttestationBindsStableExecution(t *testing.T) {
	secretsRequested := false
	laneIndex := 2
	spec := RunSpec{
		RunID:               "run_evidence",
		TeamID:              "team_evidence",
		RunGroupID:          "group_evidence",
		RunGroupKind:        "verification_session",
		LogicalChildID:      "test",
		Attempt:             1,
		ProjectID:           "proj_evidence",
		Region:              "us-east-1",
		Image:               DefaultImage,
		Command:             "go test ./...",
		RunnerSize:          "standard",
		TimeoutSeconds:      1200,
		CacheMode:           "cache-only",
		CacheLaneID:         "lane_evidence",
		CacheLaneIndex:      &laneIndex,
		CacheLaneSizeBytes:  1024,
		NetworkMode:         "none",
		ChangeMode:          "working",
		SnapshotDigest:      "sha256:" + strings.Repeat("a", 64),
		ChangedFilesDigest:  "sha256:" + strings.Repeat("b", 64),
		SecretsRequested:    &secretsRequested,
		WorkerReleaseState:  WorkerReleaseStateProduction,
		WorkerReleaseDigest: "sha256:" + strings.Repeat("d", 64),
		ClaimToken:          "claim-secret",
		LeaseExpiresAt:      timePointer(time.Now().Add(time.Hour)),
	}
	attestation, err := NewRunnerEvidenceAttestation(spec, StatusSucceeded, 0)
	if err != nil {
		t.Fatalf("new attestation: %v", err)
	}
	if attestation.ExecutionDigest == "" || attestation.WorkspaceDigest != spec.SnapshotDigest {
		t.Fatalf("attestation = %+v", attestation)
	}

	ephemeral := spec
	ephemeral.ClaimToken = "different"
	ephemeral.LeaseExpiresAt = timePointer(time.Now().Add(2 * time.Hour))
	same, err := NewRunnerEvidenceAttestation(ephemeral, StatusSucceeded, 0)
	if err != nil {
		t.Fatalf("new attestation with ephemeral changes: %v", err)
	}
	if same.ExecutionDigest != attestation.ExecutionDigest {
		t.Fatalf("ephemeral fields changed execution digest from %s to %s", attestation.ExecutionDigest, same.ExecutionDigest)
	}

	changed := spec
	changed.Command = "go test ./... -race"
	different, err := NewRunnerEvidenceAttestation(changed, StatusSucceeded, 0)
	if err != nil {
		t.Fatalf("new changed attestation: %v", err)
	}
	if different.ExecutionDigest == attestation.ExecutionDigest {
		t.Fatal("command change did not change execution digest")
	}
}

func TestRunnerEvidenceEligibilityFailsClosedForUnboundSecretsAndIncompleteIdentity(t *testing.T) {
	secretsRequested := false
	spec := RunSpec{
		RunID:               "run_evidence",
		RunGroupID:          "group_evidence",
		RunGroupKind:        "verification_session",
		LogicalChildID:      "test",
		Attempt:             1,
		SnapshotDigest:      "sha256:" + strings.Repeat("a", 64),
		SecretsRequested:    &secretsRequested,
		WorkerReleaseState:  WorkerReleaseStateProduction,
		WorkerReleaseDigest: "sha256:" + strings.Repeat("d", 64),
	}
	if !RunnerEvidenceEligible(spec) {
		t.Fatal("complete non-secret verification run should be eligible")
	}
	spec.SecretsRequested = nil
	if RunnerEvidenceEligible(spec) {
		t.Fatal("legacy implicit secret request should not be eligible")
	}
	requested := true
	spec.SecretsRequested = &requested
	if RunnerEvidenceEligible(spec) {
		t.Fatal("secret-bearing verification run without a revision should not be eligible")
	}
	emptyRevisionSet := sha256.Sum256([]byte("[]"))
	spec.SecretRevisionDigest = "sha256:" + hex.EncodeToString(emptyRevisionSet[:])
	if RunnerEvidenceEligible(spec) {
		t.Fatal("secret-bearing verification run with an empty revision set should not be eligible")
	}
	spec.SecretRevisionDigest = "sha256:" + strings.Repeat("e", 64)
	if !RunnerEvidenceEligible(spec) {
		t.Fatal("secret-bearing verification run with a bound revision should be eligible")
	}
	boundDigest, err := RunnerExecutionDigest(spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.SecretRevisionDigest = "sha256:" + strings.Repeat("f", 64)
	rotatedDigest, err := RunnerExecutionDigest(spec)
	if err != nil {
		t.Fatal(err)
	}
	if rotatedDigest == boundDigest {
		t.Fatal("secret revision change did not change execution digest")
	}
	spec.SecretsRequested = &secretsRequested
	spec.SecretRevisionDigest = ""
	spec.WorkerReleaseState = WorkerReleaseStateCandidate
	if RunnerEvidenceEligible(spec) {
		t.Fatal("candidate worker claim should not be eligible")
	}
}

func TestRunnerEvidenceReceiptDigestIsReproducible(t *testing.T) {
	receipt := RunnerEvidenceReceipt{
		SchemaVersion: RunnerEvidenceReceiptSchemaVersion,
		Attestation: RunnerEvidenceAttestation{
			SchemaVersion:   RunnerEvidenceAttestationSchemaVersion,
			RunID:           "run_evidence",
			RunGroupID:      "group_evidence",
			LogicalChildID:  "test",
			Attempt:         1,
			WorkspaceDigest: "sha256:" + strings.Repeat("a", 64),
			ExecutionDigest: "sha256:" + strings.Repeat("b", 64),
			Status:          StatusSucceeded,
			ExitCode:        0,
		},
		WorkerID:            "worker_evidence",
		WorkerVersion:       "worker-sha",
		WorkerRelease:       WorkerReleaseStateProduction,
		WorkerReleaseDigest: "sha256:" + strings.Repeat("d", 64),
		ClaimSequence:       1,
		ArtifactCount:       0,
		ArtifactDigest:      "sha256:" + strings.Repeat("c", 64),
		AttestedAt:          time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
	}
	first, err := RunnerEvidenceReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.Digest = first
	second, err := RunnerEvidenceReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("receipt digest changed from %s to %s", first, second)
	}
	if err := ValidateRunnerEvidenceReceipt(receipt); err != nil {
		t.Fatalf("validate receipt: %v", err)
	}
	tampered := receipt
	tampered.ArtifactCount++
	if err := ValidateRunnerEvidenceReceipt(tampered); err == nil {
		t.Fatal("tampered receipt validated successfully")
	}
	missingWorkerVersion := receipt
	missingWorkerVersion.WorkerVersion = ""
	missingWorkerVersion.Digest, err = RunnerEvidenceReceiptDigest(missingWorkerVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRunnerEvidenceReceipt(missingWorkerVersion); err == nil {
		t.Fatal("receipt without worker_version validated successfully")
	}
}

func TestRunnerEvidenceReceiptDigestBindsArtifactContent(t *testing.T) {
	receipt := RunnerEvidenceReceipt{
		SchemaVersion: RunnerEvidenceReceiptSchemaVersion,
		Attestation: RunnerEvidenceAttestation{
			SchemaVersion:   RunnerEvidenceAttestationSchemaVersion,
			RunID:           "run_evidence",
			RunGroupID:      "group_evidence",
			LogicalChildID:  "test",
			Attempt:         1,
			WorkspaceDigest: "sha256:" + strings.Repeat("a", 64),
			ExecutionDigest: "sha256:" + strings.Repeat("b", 64),
			Status:          StatusSucceeded,
			ExitCode:        0,
		},
		WorkerID:              "worker_evidence",
		WorkerVersion:         "worker-sha",
		WorkerRelease:         WorkerReleaseStateProduction,
		WorkerReleaseDigest:   "sha256:" + strings.Repeat("f", 64),
		ClaimSequence:         1,
		ArtifactCount:         1,
		ArtifactDigest:        "sha256:" + strings.Repeat("c", 64),
		ArtifactContentSHA256: strings.Repeat("d", 64),
		AttestedAt:            time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
	}
	first, err := RunnerEvidenceReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ArtifactContentSHA256 = strings.Repeat("e", 64)
	second, err := RunnerEvidenceReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("different artifact bytes produced identical receipt digest %s", first)
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
}
