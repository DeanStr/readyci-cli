package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/artifactbundle"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func (c *Client) AdminListRuns(ctx context.Context, projectID, status string, limit int) (preflightapi.ListRunsResponse, error) {
	query := url.Values{}
	if projectID != "" {
		query.Set("project", projectID)
	}
	if status != "" {
		query.Set("status", status)
	}
	if limit > 0 {
		query.Set("limit", fmt.Sprintf("%d", limit))
	}
	path := "/v1/admin/runs"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return getJSON[preflightapi.ListRunsResponse](ctx, c, path)
}

func (c *Client) AdminGetRun(ctx context.Context, runID string) (preflightapi.RunDetail, error) {
	return getJSON[preflightapi.RunDetail](ctx, c, "/v1/admin/runs/"+url.PathEscape(runID))
}

func (c *Client) AdminListEvents(ctx context.Context, runID string) (preflightapi.RunEventsResponse, error) {
	var combined preflightapi.RunEventsResponse
	combined.RunID = runID
	cursor := int64(0)
	for {
		page, err := c.AdminListEventsPage(ctx, runID, cursor, runEventsPageLimit)
		if err != nil {
			return preflightapi.RunEventsResponse{}, err
		}
		if page.RunID != "" {
			combined.RunID = page.RunID
		}
		combined.Events = append(combined.Events, page.Events...)
		combined.NextCursor = page.NextCursor
		combined.HasMore = page.HasMore
		combined.Truncated = combined.Truncated || page.Truncated
		if !page.HasMore {
			return combined, nil
		}
		if page.NextCursor <= cursor {
			return preflightapi.RunEventsResponse{}, fmt.Errorf("admin events pagination did not advance for %s", runID)
		}
		cursor = page.NextCursor
	}
}

func (c *Client) AdminListEventsPage(ctx context.Context, runID string, cursor int64, limit int) (preflightapi.RunEventsResponse, error) {
	query := url.Values{}
	if cursor > 0 {
		query.Set("cursor", fmt.Sprintf("%d", cursor))
	}
	if limit > 0 {
		query.Set("limit", fmt.Sprintf("%d", limit))
	}
	path := "/v1/admin/runs/" + url.PathEscape(runID) + "/events"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return requestJSONWithLimit[preflightapi.RunEventsResponse](ctx, c, http.MethodGet, path, nil, maxRunEventsJSONResponseBytes)
}

func (c *Client) ListArtifacts(ctx context.Context, runID string) (preflightapi.ListArtifactsResponse, error) {
	return getJSON[preflightapi.ListArtifactsResponse](ctx, c, "/v1/runs/"+url.PathEscape(runID)+"/artifacts")
}

func (c *Client) DownloadArtifactArchive(ctx context.Context, runID string, writer io.Writer) error {
	reqCtx, cancel := context.WithTimeout(ctx, uploadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, c.baseURL+"/v1/runs/"+url.PathEscape(runID)+"/artifacts/archive", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "*/*")
	c.authorize(req)

	resp, err := (&http.Client{Timeout: uploadTimeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("download artifacts failed: %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	n, err := io.Copy(writer, io.LimitReader(resp.Body, artifactbundle.MaxCompressedBytes+1))
	if err != nil {
		return err
	}
	if n > artifactbundle.MaxCompressedBytes {
		return artifactbundle.ErrTooLarge
	}
	return nil
}

func (c *Client) AdminCancelRun(ctx context.Context, runID, reason string) (preflightapi.CancelRunResponse, error) {
	return postJSONResponse[preflightapi.CancelRunResponse](ctx, c, "/v1/admin/runs/"+url.PathEscape(runID)+"/cancel", preflightapi.CancelRunRequest{Reason: reason})
}

func (c *Client) AdminListAPITokens(ctx context.Context) (preflightapi.ListAPITokensResponse, error) {
	return getJSON[preflightapi.ListAPITokensResponse](ctx, c, "/v1/admin/tokens")
}

func (c *Client) AdminCreateAPIToken(ctx context.Context, req preflightapi.CreateAPITokenRequest) (preflightapi.CreateAPITokenResponse, error) {
	return postJSONResponse[preflightapi.CreateAPITokenResponse](ctx, c, "/v1/admin/tokens", req)
}

func (c *Client) AdminRevokeAPIToken(ctx context.Context, tokenID string) (preflightapi.RevokeAPITokenResponse, error) {
	return deleteJSON[preflightapi.RevokeAPITokenResponse](ctx, c, "/v1/admin/tokens/"+url.PathEscape(tokenID))
}

func (c *Client) AdminListTeams(ctx context.Context) (preflightapi.AdminListTeamsResponse, error) {
	return getJSON[preflightapi.AdminListTeamsResponse](ctx, c, "/v1/admin/teams")
}

func (c *Client) AdminTeamDetail(ctx context.Context, teamID string) (preflightapi.AdminTeamDetailResponse, error) {
	return getJSON[preflightapi.AdminTeamDetailResponse](ctx, c, "/v1/admin/teams/"+url.PathEscape(teamID))
}

func (c *Client) AdminDisableTeam(ctx context.Context, teamID, reason string) (preflightapi.AdminDisableTeamResponse, error) {
	return postJSONResponse[preflightapi.AdminDisableTeamResponse](ctx, c, "/v1/admin/teams/"+url.PathEscape(teamID)+"/disable", preflightapi.AdminDisableTeamRequest{Reason: reason})
}

func (c *Client) AdminReleaseBillingHold(ctx context.Context, teamID, reason string) (preflightapi.AdminReleaseBillingHoldResponse, error) {
	return postJSONResponse[preflightapi.AdminReleaseBillingHoldResponse](ctx, c, "/v1/admin/teams/"+url.PathEscape(teamID)+"/release-billing-hold", preflightapi.AdminReleaseBillingHoldRequest{Reason: reason})
}

func (c *Client) AdminBillingDisputeEvidence(ctx context.Context, teamID, provider, disputeID string) (preflightapi.BillingDisputeEvidenceResponse, error) {
	query := url.Values{}
	if provider != "" {
		query.Set("provider", provider)
	}
	query.Set("dispute_id", disputeID)
	return getJSON[preflightapi.BillingDisputeEvidenceResponse](ctx, c, "/v1/admin/teams/"+url.PathEscape(teamID)+"/billing-evidence?"+query.Encode())
}

func (c *Client) AdminCancelTeamRuns(ctx context.Context, teamID, reason string) (preflightapi.AdminCancelTeamRunsResponse, error) {
	return postJSONResponse[preflightapi.AdminCancelTeamRunsResponse](ctx, c, "/v1/admin/teams/"+url.PathEscape(teamID)+"/cancel-runs", preflightapi.AdminCancelTeamRunsRequest{Reason: reason})
}

func (c *Client) AdminListWorkers(ctx context.Context) (preflightapi.AdminListWorkersResponse, error) {
	return getJSON[preflightapi.AdminListWorkersResponse](ctx, c, "/v1/admin/workers")
}

func (c *Client) AdminDrainWorker(ctx context.Context, workerID, reason string) (preflightapi.AdminDrainWorkerResponse, error) {
	return postJSONResponse[preflightapi.AdminDrainWorkerResponse](ctx, c, "/v1/admin/workers/"+url.PathEscape(workerID)+"/drain", preflightapi.AdminDrainWorkerRequest{Reason: reason})
}

func (c *Client) AdminUndrainWorker(ctx context.Context, workerID string) (preflightapi.AdminDrainWorkerResponse, error) {
	return postJSONResponse[preflightapi.AdminDrainWorkerResponse](ctx, c, "/v1/admin/workers/"+url.PathEscape(workerID)+"/undrain", struct{}{})
}

func (c *Client) AdminStageWorkerRelease(ctx context.Context, workerID string, req preflightapi.AdminStageWorkerRequest) (preflightapi.AdminWorkerReleaseResponse, error) {
	return postJSONResponse[preflightapi.AdminWorkerReleaseResponse](ctx, c, "/v1/admin/workers/"+url.PathEscape(workerID)+"/stage-release", req)
}

func (c *Client) AdminPromoteWorkerRelease(ctx context.Context, workerID string, req preflightapi.AdminPromoteWorkerRequest) (preflightapi.AdminWorkerReleaseResponse, error) {
	return postJSONResponse[preflightapi.AdminWorkerReleaseResponse](ctx, c, "/v1/admin/workers/"+url.PathEscape(workerID)+"/promote-release", req)
}

func (c *Client) AdminDemoteWorkerRelease(ctx context.Context, workerID, reason string) (preflightapi.AdminWorkerReleaseResponse, error) {
	return postJSONResponse[preflightapi.AdminWorkerReleaseResponse](ctx, c, "/v1/admin/workers/"+url.PathEscape(workerID)+"/demote-release", preflightapi.AdminDrainWorkerRequest{Reason: reason})
}

func (c *Client) AdminDeleteWorker(ctx context.Context, workerID, reason string) (preflightapi.AdminDeleteWorkerResponse, error) {
	return deleteJSONResponse[preflightapi.AdminDeleteWorkerResponse](ctx, c, "/v1/admin/workers/"+url.PathEscape(workerID), preflightapi.AdminDeleteWorkerRequest{Reason: reason})
}

func (c *Client) AdminQueueStatus(ctx context.Context) (preflightapi.AdminQueueStatusResponse, error) {
	return getJSON[preflightapi.AdminQueueStatusResponse](ctx, c, "/v1/admin/queue/status")
}

func (c *Client) AdminQueueExplain(ctx context.Context, runID string) (preflightapi.AdminQueueExplainResponse, error) {
	return getJSON[preflightapi.AdminQueueExplainResponse](ctx, c, "/v1/admin/queue/explain/"+url.PathEscape(runID))
}

func (c *Client) AdminStorageAudit(ctx context.Context) (preflightapi.AdminStorageAuditResponse, error) {
	return getJSON[preflightapi.AdminStorageAuditResponse](ctx, c, "/v1/admin/storage/audit")
}

func (c *Client) AdminCleanupExpiredArtifacts(ctx context.Context, req preflightapi.AdminCleanupExpiredArtifactsRequest) (preflightapi.AdminCleanupExpiredArtifactsResponse, error) {
	return postJSONResponse[preflightapi.AdminCleanupExpiredArtifactsResponse](ctx, c, "/v1/admin/storage/cleanup-expired-artifacts", req)
}

func (c *Client) AdminCleanupCacheLanes(ctx context.Context, req preflightapi.AdminCleanupCacheLanesRequest) (preflightapi.AdminCleanupCacheLanesResponse, error) {
	return postJSONResponse[preflightapi.AdminCleanupCacheLanesResponse](ctx, c, "/v1/admin/storage/cleanup-cache-lanes", req)
}

func (c *Client) AdminCleanupExpiredArtifactsStatus(ctx context.Context) (preflightapi.AdminCleanupExpiredArtifactsStatusResponse, error) {
	return getJSON[preflightapi.AdminCleanupExpiredArtifactsStatusResponse](ctx, c, "/v1/admin/storage/cleanup-expired-artifacts/status")
}

func (c *Client) AdminCancelCleanupExpiredArtifacts(ctx context.Context, req preflightapi.AdminCancelCleanupExpiredArtifactsRequest) (preflightapi.AdminCancelCleanupExpiredArtifactsResponse, error) {
	return postJSONResponse[preflightapi.AdminCancelCleanupExpiredArtifactsResponse](ctx, c, "/v1/admin/storage/cleanup-expired-artifacts/cancel", req)
}

func (c *Client) AdminCreatePrivacyCase(ctx context.Context, req preflightapi.AdminCreatePrivacyCaseRequest) (preflightapi.AdminPrivacyCase, error) {
	return postJSONResponse[preflightapi.AdminPrivacyCase](ctx, c, "/v1/admin/privacy/cases", req)
}

func (c *Client) AdminPrivacyCase(ctx context.Context, caseID string) (preflightapi.AdminPrivacyCase, error) {
	return getJSON[preflightapi.AdminPrivacyCase](ctx, c, "/v1/admin/privacy/cases/"+url.PathEscape(caseID))
}

func (c *Client) AdminReviewPrivacyCase(ctx context.Context, caseID string, req preflightapi.AdminReviewPrivacyCaseRequest) (preflightapi.AdminPrivacyCase, error) {
	return postJSONResponse[preflightapi.AdminPrivacyCase](ctx, c, "/v1/admin/privacy/cases/"+url.PathEscape(caseID)+"/review", req)
}

func (c *Client) AdminPlanPrivacyCase(ctx context.Context, caseID, reason string) (preflightapi.AdminPrivacyErasurePlan, error) {
	return requestJSONPayloadWithTimeout[preflightapi.AdminPrivacyErasurePlan](
		ctx,
		c,
		http.MethodPost,
		"/v1/admin/privacy/cases/"+url.PathEscape(caseID)+"/plan",
		preflightapi.AdminPlanPrivacyCaseRequest{Reason: reason},
		uploadTimeout,
	)
}

func (c *Client) DownloadAdminPrivacyExport(ctx context.Context, caseID string, writer io.Writer) error {
	return c.downloadResponse(ctx, http.MethodGet, "/v1/admin/privacy/cases/"+url.PathEscape(caseID)+"/export", writer)
}

func (c *Client) AdminCompletePrivacyCase(ctx context.Context, caseID, reason string) (preflightapi.AdminPrivacyCase, error) {
	return postJSONResponse[preflightapi.AdminPrivacyCase](ctx, c, "/v1/admin/privacy/cases/"+url.PathEscape(caseID)+"/complete", preflightapi.AdminCompletePrivacyStepRequest{Reason: reason})
}

func (c *Client) AdminExecutePrivacyErasure(ctx context.Context, caseID string, req preflightapi.AdminExecutePrivacyErasureRequest) (preflightapi.AdminPrivacyCase, error) {
	return requestJSONPayloadWithTimeout[preflightapi.AdminPrivacyCase](
		ctx,
		c,
		http.MethodPost,
		"/v1/admin/privacy/cases/"+url.PathEscape(caseID)+"/erase",
		req,
		uploadTimeout,
	)
}

func (c *Client) AdminCompletePrivacyProvider(ctx context.Context, caseID, provider, reason string) (preflightapi.AdminPrivacyCase, error) {
	return postJSONResponse[preflightapi.AdminPrivacyCase](ctx, c, "/v1/admin/privacy/cases/"+url.PathEscape(caseID)+"/providers/"+url.PathEscape(provider)+"/complete", preflightapi.AdminCompletePrivacyStepRequest{Reason: reason})
}

func (c *Client) AdminCompletePrivacyRestoreReplay(ctx context.Context, caseID, reason string) (preflightapi.AdminPrivacyCase, error) {
	return postJSONResponse[preflightapi.AdminPrivacyCase](ctx, c, "/v1/admin/privacy/cases/"+url.PathEscape(caseID)+"/restore-replay/complete", preflightapi.AdminCompletePrivacyStepRequest{Reason: reason})
}
