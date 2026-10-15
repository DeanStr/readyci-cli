package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func (c *Client) ListRuns(ctx context.Context, projectID, status string, limit int) (preflightapi.ListRunsResponse, error) {
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
	path := "/v1/runs"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return getJSON[preflightapi.ListRunsResponse](ctx, c, path)
}

func (c *Client) GetRun(ctx context.Context, runID string) (preflightapi.RunDetail, error) {
	return getJSON[preflightapi.RunDetail](ctx, c, "/v1/runs/"+url.PathEscape(runID))
}

func (c *Client) CancelRun(ctx context.Context, runID string, req preflightapi.CancelRunRequest) (preflightapi.CancelRunResponse, error) {
	return postJSONResponse[preflightapi.CancelRunResponse](ctx, c, "/v1/runs/"+url.PathEscape(runID)+"/cancel", req)
}

func (c *Client) GetRunGroup(ctx context.Context, groupID string) (preflightapi.RunGroupDetail, error) {
	return getJSON[preflightapi.RunGroupDetail](ctx, c, "/v1/run-groups/"+url.PathEscape(groupID))
}

func (c *Client) LatestRunGroup(ctx context.Context, projectID string) (preflightapi.RunGroupDetail, error) {
	query := url.Values{}
	if projectID != "" {
		query.Set("project_id", projectID)
	}
	path := "/v1/run-groups/latest"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return getJSON[preflightapi.RunGroupDetail](ctx, c, path)
}

func (c *Client) CancelRunGroup(ctx context.Context, groupID string, req preflightapi.CancelRunGroupRequest) (preflightapi.CancelRunGroupResponse, error) {
	return postJSONResponse[preflightapi.CancelRunGroupResponse](ctx, c, "/v1/run-groups/"+url.PathEscape(groupID)+"/cancel", req)
}

func (c *Client) GetRunByIdempotencyKey(ctx context.Context, projectID, key string) (preflightapi.RunDetail, error) {
	query := url.Values{}
	if projectID != "" {
		query.Set("project", projectID)
	}
	query.Set("key", key)
	return getJSON[preflightapi.RunDetail](ctx, c, "/v1/runs/by-idempotency-key?"+query.Encode())
}

func (c *Client) ListRunsByIdempotencyKey(ctx context.Context, projectID, key string) (preflightapi.RunDetailsResponse, error) {
	query := url.Values{}
	if projectID != "" {
		query.Set("project", projectID)
	}
	query.Set("key", key)
	query.Set("include_children", "true")
	return getJSON[preflightapi.RunDetailsResponse](ctx, c, "/v1/runs/by-idempotency-key?"+query.Encode())
}

func (c *Client) StreamRunEvents(ctx context.Context, runID string, handle func(preflightapi.RunEvent) error) error {
	return c.StreamEvents(ctx, c.baseURL+"/v1/runs/"+url.PathEscape(runID)+"/events", handle)
}

func (c *Client) ListRunEvents(ctx context.Context, runID string) (preflightapi.RunEventsResponse, error) {
	var combined preflightapi.RunEventsResponse
	combined.RunID = runID
	cursor := int64(0)
	for {
		page, err := c.ListRunEventsPage(ctx, runID, cursor, runEventsPageLimit)
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
			return preflightapi.RunEventsResponse{}, fmt.Errorf("events pagination did not advance for %s", runID)
		}
		cursor = page.NextCursor
	}
}

func (c *Client) ListRunEventsPage(ctx context.Context, runID string, cursor int64, limit int) (preflightapi.RunEventsResponse, error) {
	query := url.Values{}
	query.Set("format", "json")
	if cursor > 0 {
		query.Set("cursor", fmt.Sprintf("%d", cursor))
	}
	if limit > 0 {
		query.Set("limit", fmt.Sprintf("%d", limit))
	}
	return requestJSONWithLimit[preflightapi.RunEventsResponse](ctx, c, http.MethodGet, "/v1/runs/"+url.PathEscape(runID)+"/events?"+query.Encode(), nil, maxRunEventsJSONResponseBytes)
}

func (c *Client) ListRunEventsTail(ctx context.Context, runID string, limit int, errorsOnly bool, logsOnly bool, includeLifecycle bool) (preflightapi.RunEventsResponse, error) {
	query := url.Values{}
	query.Set("format", "json")
	if limit > 0 {
		query.Set("tail", fmt.Sprintf("%d", limit))
	}
	if errorsOnly {
		query.Set("errors_only", "true")
	}
	if logsOnly {
		query.Set("logs_only", "true")
	}
	if includeLifecycle {
		query.Set("include_lifecycle", "true")
	}
	return requestJSONWithLimit[preflightapi.RunEventsResponse](ctx, c, http.MethodGet, "/v1/runs/"+url.PathEscape(runID)+"/events?"+query.Encode(), nil, maxRunEventsJSONResponseBytes)
}

func (c *Client) ExchangeCLIAuthCode(ctx context.Context, code, codeVerifier, name string) (preflightapi.CreateAPITokenResponse, error) {
	return postJSONResponse[preflightapi.CreateAPITokenResponse](ctx, c, "/v1/auth/cli/exchange", preflightapi.CLIAuthCodeExchangeRequest{
		Code:         code,
		CodeVerifier: codeVerifier,
		Name:         name,
	})
}

func (c *Client) RevokeCurrentCLIToken(ctx context.Context) (preflightapi.RevokeAPITokenResponse, error) {
	return deleteJSON[preflightapi.RevokeAPITokenResponse](ctx, c, "/v1/auth/cli/token")
}
