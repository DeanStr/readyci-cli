package preflightapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPKCES256RFC7636VectorAndValidation(t *testing.T) {
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if PKCEChallengeS256(verifier) != challenge || !VerifyPKCES256(challenge, verifier) {
		t.Fatal("RFC 7636 S256 vector failed")
	}
	for _, value := range []string{"", strings.Repeat("a", 42), strings.Repeat("a", 129), strings.Repeat("a", 42) + " ", strings.Repeat("a", 42) + "+"} {
		if ValidPKCEVerifier(value) || VerifyPKCES256(challenge, value) {
			t.Fatal("invalid verifier accepted")
		}
	}
	for _, value := range []string{"", challenge + "=", challenge[:42] + "N", strings.Repeat("!", 43)} {
		if ValidPKCEChallenge(value) {
			t.Fatal("invalid or noncanonical challenge accepted")
		}
	}
	if VerifyPKCES256(challenge, strings.Repeat("a", 43)) {
		t.Fatal("wrong verifier accepted")
	}
	if !ValidPKCEVerifier(strings.Repeat("a", 124) + "._~-") {
		t.Fatal("valid verifier alphabet rejected")
	}
}

func TestRunSpecJSONPreservesZeroCacheLaneIndex(t *testing.T) {
	zero := 0
	data, err := json.Marshal(RunSpec{
		RunID:          "run_000001",
		CacheLaneID:    "cache_lane_000001",
		CacheLaneIndex: &zero,
	})
	if err != nil {
		t.Fatalf("marshal run spec: %v", err)
	}
	if !strings.Contains(string(data), `"cache_lane_index":0`) {
		t.Fatalf("json %s does not include zero cache lane index", data)
	}

	data, err = json.Marshal(RunSpec{RunID: "run_000002"})
	if err != nil {
		t.Fatalf("marshal empty run spec: %v", err)
	}
	if strings.Contains(string(data), "cache_lane_index") {
		t.Fatalf("json %s included absent cache lane index", data)
	}
}

func TestListRunsResponseCursorJSONKeepsLegacyNumericCursor(t *testing.T) {
	data, err := json.Marshal(ListRunsResponse{
		NextCursor:     2,
		NextPageCursor: "opaque_cursor",
		HasMore:        true,
	})
	if err != nil {
		t.Fatalf("marshal list runs response: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"next_cursor":2`) {
		t.Fatalf("json %s does not include numeric legacy next_cursor", data)
	}
	if strings.Contains(text, `"next_cursor":"2"`) {
		t.Fatalf("json %s encoded legacy next_cursor as a string", data)
	}
	if !strings.Contains(text, `"next_page_cursor":"opaque_cursor"`) {
		t.Fatalf("json %s does not include opaque next_page_cursor", data)
	}

	var response ListRunsResponse
	if err := json.Unmarshal([]byte(`{"next_cursor":2,"next_page_cursor":"opaque_cursor","has_more":true}`), &response); err != nil {
		t.Fatalf("unmarshal numeric list runs cursor: %v", err)
	}
	if response.NextCursor != 2 || response.NextPageCursor != "opaque_cursor" || !response.HasMore {
		t.Fatalf("response = %+v, want numeric and opaque cursors", response)
	}
}

func TestRunGroupDetailJSONContractSnapshot(t *testing.T) {
	createdAt := time.Date(2026, 6, 27, 1, 2, 3, 0, time.UTC)
	updatedAt := time.Date(2026, 6, 27, 1, 3, 4, 0, time.UTC)
	completedAt := time.Date(2026, 6, 27, 1, 4, 5, 0, time.UTC)
	queuedAt := time.Date(2026, 6, 27, 1, 2, 4, 0, time.UTC)
	claimedAt := time.Date(2026, 6, 27, 1, 2, 8, 0, time.UTC)
	leaseExpiresAt := time.Date(2026, 6, 27, 1, 22, 8, 0, time.UTC)
	finishedAt := time.Date(2026, 6, 27, 1, 4, 5, 0, time.UTC)
	exitCode := 1
	detail := RunGroupDetail{
		Summary: RunGroupSummary{
			RunGroupID:         "group_123",
			TaskID:             "task-123",
			ProjectID:          "proj_dev",
			Kind:               "affected",
			Name:               "affected working tree",
			Status:             StatusDenied,
			ChildCount:         2,
			LatestAttemptCount: 1,
			FailedChildCount:   1,
			CreatedAt:          createdAt,
			UpdatedAt:          updatedAt,
			SuccessorID:        "group_456",
			PredecessorID:      "group_000",
			CompletedAt:        &completedAt,
		},
		Children: []RunGroupChild{{
			RunID:                 "run_go_2",
			OriginalRunID:         "run_go_1",
			RetryOfRunID:          "run_go_1",
			LogicalChildID:        "go",
			LogicalChildName:      "Go tests",
			CommandAlias:          "go",
			MatrixVariant:         "GO=1",
			Attempt:               2,
			LatestAttempt:         true,
			Status:                StatusDenied,
			ExitCode:              &exitCode,
			Classification:        ClassificationPolicyDenied,
			Retryable:             false,
			AgentAction:           AgentActionReportSetup,
			FailureSummary:        "run denied by project policy",
			PolicyReasons:         []string{"network_mode is not allowed by current project policy"},
			ArtifactCount:         2,
			RetainedArtifactCount: 2,
			ArtifactsAvailable:    true,
			CreatedAt:             createdAt,
			QueuedAt:              &queuedAt,
			ClaimedAt:             &claimedAt,
			LeaseExpiresAt:        &leaseExpiresAt,
			FinishedAt:            &finishedAt,
		}},
	}
	data, err := json.MarshalIndent(detail, "", "  ")
	if err != nil {
		t.Fatalf("marshal run group detail: %v", err)
	}
	const want = `{
  "summary": {
    "run_group_id": "group_123",
    "task_id": "task-123",
    "project_id": "proj_dev",
    "kind": "affected",
    "name": "affected working tree",
    "status": "denied",
    "child_count": 2,
    "latest_attempt_count": 1,
    "failed_child_count": 1,
    "created_at": "2026-06-27T01:02:03Z",
    "updated_at": "2026-06-27T01:03:04Z",
    "successor_id": "group_456",
    "predecessor_id": "group_000",
    "completed_at": "2026-06-27T01:04:05Z"
  },
  "children": [
    {
      "run_id": "run_go_2",
      "original_run_id": "run_go_1",
      "retry_of_run_id": "run_go_1",
      "logical_child_id": "go",
      "logical_child_name": "Go tests",
      "command_alias": "go",
      "matrix_variant": "GO=1",
      "attempt": 2,
      "latest_attempt": true,
      "status": "denied",
      "exit_code": 1,
      "classification": "policy_denied",
      "retryable": false,
      "agent_action": "report_setup",
      "failure_summary": "run denied by project policy",
      "policy_reasons": [
        "network_mode is not allowed by current project policy"
      ],
      "artifact_count": 2,
      "retained_artifact_count": 2,
      "artifacts_available": true,
      "evidence_attested": false,
      "created_at": "2026-06-27T01:02:03Z",
      "queued_at": "2026-06-27T01:02:04Z",
      "claimed_at": "2026-06-27T01:02:08Z",
      "lease_expires_at": "2026-06-27T01:22:08Z",
      "finished_at": "2026-06-27T01:04:05Z"
    }
  ]
}`
	if string(data) != want {
		t.Fatalf("run group detail JSON contract drifted:\n%s", data)
	}
}

func TestRunGroupSummaryListJSONContractSnapshot(t *testing.T) {
	createdAt := time.Date(2026, 6, 27, 2, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 6, 27, 2, 1, 0, 0, time.UTC)
	response := ListRunGroupsResponse{Groups: []RunGroupSummary{{
		RunGroupID:         "group_latest",
		TaskID:             "task-latest",
		ProjectID:          "proj_dev",
		Kind:               "matrix",
		Status:             StatusRunning,
		ChildCount:         3,
		LatestAttemptCount: 3,
		FailedChildCount:   0,
		CreatedAt:          createdAt,
		UpdatedAt:          updatedAt,
	}}}
	data, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		t.Fatalf("marshal run group list: %v", err)
	}
	const want = `{
  "groups": [
    {
      "run_group_id": "group_latest",
      "task_id": "task-latest",
      "project_id": "proj_dev",
      "kind": "matrix",
      "status": "running",
      "child_count": 3,
      "latest_attempt_count": 3,
      "failed_child_count": 0,
      "created_at": "2026-06-27T02:00:00Z",
      "updated_at": "2026-06-27T02:01:00Z"
    }
  ]
}`
	if string(data) != want {
		t.Fatalf("run group list JSON contract drifted:\n%s", data)
	}
}
