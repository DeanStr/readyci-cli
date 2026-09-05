package api

import (
	"context"
	"net/http"
	"net/url"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func (c *Client) CreateVerificationSession(ctx context.Context, req preflightapi.CreateVerificationSessionRequest) (preflightapi.CreateVerificationSessionResponse, error) {
	return postJSONResponse[preflightapi.CreateVerificationSessionResponse](ctx, c, "/v1/verification-sessions", req)
}

func (c *Client) PrepareVerificationSessionSourceUpload(
	ctx context.Context,
	prepareURL string,
	req preflightapi.PrepareVerificationSessionSourceUploadRequest,
) (preflightapi.PrepareVerificationSessionSourceUploadResponse, error) {
	return postJSONResponse[preflightapi.PrepareVerificationSessionSourceUploadResponse](ctx, c, prepareURL, req)
}

func (c *Client) CompleteVerificationSessionSourceUpload(
	ctx context.Context,
	completeURL string,
	req preflightapi.CompleteVerificationSessionSourceUploadRequest,
) (preflightapi.VerificationSession, error) {
	return requestJSONPayloadWithTimeout[preflightapi.VerificationSession](ctx, c, http.MethodPost, completeURL, req, uploadTimeout)
}

func (c *Client) VerificationSession(ctx context.Context, sessionID string) (preflightapi.VerificationSession, error) {
	return getJSON[preflightapi.VerificationSession](ctx, c, "/v1/verification-sessions/"+url.PathEscape(sessionID))
}

func (c *Client) CancelVerificationSession(ctx context.Context, sessionID string) (preflightapi.VerificationSession, error) {
	return postJSONResponse[preflightapi.VerificationSession](ctx, c, "/v1/verification-sessions/"+url.PathEscape(sessionID)+"/cancel", struct{}{})
}

func (c *Client) RetryVerificationSession(ctx context.Context, sessionID string, req preflightapi.RetryVerificationSessionRequest) (preflightapi.RetryVerificationSessionResponse, error) {
	return postJSONResponse[preflightapi.RetryVerificationSessionResponse](ctx, c, "/v1/verification-sessions/"+url.PathEscape(sessionID)+"/retry", req)
}

func (c *Client) MarkVerificationSessionStale(ctx context.Context, sessionID, currentDigest string) (preflightapi.VerificationSession, error) {
	return postJSONResponse[preflightapi.VerificationSession](ctx, c, "/v1/verification-sessions/"+url.PathEscape(sessionID)+"/stale", preflightapi.MarkVerificationSessionStaleRequest{CurrentSnapshotDigest: currentDigest})
}
