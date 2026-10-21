package api

import (
	"context"
	"net/url"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

const (
	completeUploadMaxAttempts   = 4
	completeUploadRetryMaxDelay = 60 * time.Second
)

func (c *Client) CreateRun(ctx context.Context, req preflightapi.CreateRunRequest) (preflightapi.CreateRunResponse, error) {
	return postJSONResponse[preflightapi.CreateRunResponse](ctx, c, "/v1/runs", req)
}

func (c *Client) CreateFeedback(ctx context.Context, req preflightapi.CreateFeedbackRequest) (preflightapi.CreateFeedbackResponse, error) {
	return postJSONResponse[preflightapi.CreateFeedbackResponse](ctx, c, "/v1/feedback", req)
}

func (c *Client) CompleteUpload(ctx context.Context, runID string) (preflightapi.CompleteUploadResponse, error) {
	return c.CompleteUploadWithRequest(ctx, runID, preflightapi.CompleteUploadRequest{})
}

func (c *Client) CompleteUploadWithRequest(ctx context.Context, runID string, req preflightapi.CompleteUploadRequest) (preflightapi.CompleteUploadResponse, error) {
	path := "/v1/runs/" + url.PathEscape(runID) + "/upload-complete"
	var out preflightapi.CompleteUploadResponse
	var err error
	for attempt := 0; attempt < completeUploadMaxAttempts; attempt++ {
		out = preflightapi.CompleteUploadResponse{}
		out, err = postJSONResponse[preflightapi.CompleteUploadResponse](ctx, c, path, req)
		if err == nil {
			return out, nil
		}
		if attempt == completeUploadMaxAttempts-1 || !retryableAPIRequestError(ctx, err) {
			return preflightapi.CompleteUploadResponse{}, err
		}
		if sleepErr := sleepContext(ctx, completeUploadRetryDelay(attempt, err)); sleepErr != nil {
			return preflightapi.CompleteUploadResponse{}, sleepErr
		}
	}
	return preflightapi.CompleteUploadResponse{}, err
}

func completeUploadRetryDelay(attempt int, err error) time.Duration {
	return apiRequestRetryDelay(attempt, err, completeUploadRetryMaxDelay)
}

func (c *Client) CacheUsage(ctx context.Context, projectID string) (preflightapi.CacheUsageResponse, error) {
	return getJSON[preflightapi.CacheUsageResponse](ctx, c, "/v1/projects/"+url.PathEscape(projectID)+"/cache")
}

func (c *Client) PurgeCache(ctx context.Context, projectID string) (preflightapi.PurgeCacheResponse, error) {
	return deleteJSON[preflightapi.PurgeCacheResponse](ctx, c, "/v1/projects/"+url.PathEscape(projectID)+"/cache")
}

func (c *Client) ProjectSettings(ctx context.Context, projectID string) (preflightapi.ProjectSettings, error) {
	return getJSON[preflightapi.ProjectSettings](ctx, c, "/v1/projects/"+url.PathEscape(projectID)+"/settings")
}

func (c *Client) AnalyzeProject(ctx context.Context, projectID string, req preflightapi.AnalyzeProjectRequest) (preflightapi.AnalyzeProjectResponse, error) {
	return postJSONResponse[preflightapi.AnalyzeProjectResponse](ctx, c, "/v1/projects/"+url.PathEscape(projectID)+"/analyze", req)
}

func (c *Client) ConfigSuggestions(ctx context.Context, projectID string, req preflightapi.ConfigSuggestionsRequest) (preflightapi.ConfigSuggestionsResponse, error) {
	return postJSONResponse[preflightapi.ConfigSuggestionsResponse](ctx, c, "/v1/projects/"+url.PathEscape(projectID)+"/config-suggestions", req)
}

func (c *Client) UpdateProjectSettings(ctx context.Context, projectID string, req preflightapi.UpsertProjectSettingsRequest) (preflightapi.ProjectSettings, error) {
	return putJSONResponse[preflightapi.ProjectSettings](ctx, c, "/v1/projects/"+url.PathEscape(projectID)+"/settings", req)
}

func (c *Client) ProjectVerificationPolicy(ctx context.Context, projectID string) (preflightapi.ProjectVerificationPolicyResponse, error) {
	return getJSON[preflightapi.ProjectVerificationPolicyResponse](ctx, c, "/v1/projects/"+url.PathEscape(projectID)+"/verification-policy")
}

func (c *Client) ProjectVerificationSourcePolicy(ctx context.Context, projectID, changeMode, check string) (preflightapi.ProjectVerificationSourcePolicyResponse, error) {
	path := "/v1/projects/" + url.PathEscape(projectID) + "/verification-policy/source-binding"
	query := url.Values{}
	query.Set("change_mode", changeMode)
	if check != "" {
		query.Set("check", check)
	}
	return getJSON[preflightapi.ProjectVerificationSourcePolicyResponse](ctx, c, path+"?"+query.Encode())
}

func (c *Client) PreviewProjectVerificationPolicy(ctx context.Context, projectID string, req preflightapi.PreviewProjectVerificationPolicyRequest) (preflightapi.ProjectVerificationPolicyPreview, error) {
	return postJSONResponse[preflightapi.ProjectVerificationPolicyPreview](ctx, c, "/v1/projects/"+url.PathEscape(projectID)+"/verification-policy/preview", req)
}

func (c *Client) ActivateProjectVerificationPolicy(ctx context.Context, projectID string, req preflightapi.ActivateProjectVerificationPolicyRequest) (preflightapi.ProjectVerificationPolicyResponse, error) {
	return putJSONResponse[preflightapi.ProjectVerificationPolicyResponse](ctx, c, "/v1/projects/"+url.PathEscape(projectID)+"/verification-policy", req)
}

func (c *Client) ListProjectSecrets(ctx context.Context, projectID string) (preflightapi.ListProjectSecretsResponse, error) {
	return getJSON[preflightapi.ListProjectSecretsResponse](ctx, c, "/v1/projects/"+url.PathEscape(projectID)+"/secrets")
}

func (c *Client) UpsertProjectSecret(ctx context.Context, projectID, name, value string) (preflightapi.UpsertProjectSecretResponse, error) {
	return putJSONResponse[preflightapi.UpsertProjectSecretResponse](ctx, c, "/v1/projects/"+url.PathEscape(projectID)+"/secrets/"+url.PathEscape(name), preflightapi.UpsertProjectSecretRequest{Value: value})
}

func (c *Client) DeleteProjectSecret(ctx context.Context, projectID, name, reason string) (preflightapi.DeleteProjectSecretResponse, error) {
	return deleteJSONResponse[preflightapi.DeleteProjectSecretResponse](ctx, c, "/v1/projects/"+url.PathEscape(projectID)+"/secrets/"+url.PathEscape(name), preflightapi.DeleteProjectSecretRequest{Reason: reason})
}

func (c *Client) AccountUsage(ctx context.Context) (preflightapi.AccountUsageResponse, error) {
	return getJSON[preflightapi.AccountUsageResponse](ctx, c, "/v1/account")
}

func (c *Client) WorkerCapability(ctx context.Context, req preflightapi.WorkerCapabilityCheckRequest) (preflightapi.WorkerCapabilityCheckResponse, error) {
	return postJSONResponse[preflightapi.WorkerCapabilityCheckResponse](ctx, c, "/v1/workers/capability", req)
}

func (c *Client) CreateUserAPIToken(ctx context.Context, req preflightapi.CreateAPITokenRequest) (preflightapi.CreateAPITokenResponse, error) {
	return postJSONResponse[preflightapi.CreateAPITokenResponse](ctx, c, "/v1/account/tokens", req)
}

func (c *Client) ProposeProjectVerificationPolicy(ctx context.Context, projectID string, req preflightapi.PreviewProjectVerificationPolicyRequest) (preflightapi.ProjectVerificationPolicyProposal, error) {
	return postJSONResponse[preflightapi.ProjectVerificationPolicyProposal](ctx, c, "/v1/projects/"+url.PathEscape(projectID)+"/verification-policy/proposals", req)
}
