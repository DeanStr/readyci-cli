package api

import (
	"context"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func (c *Client) ResolveRuntimeAlias(ctx context.Context, request preflightapi.RuntimeAliasRequest) (preflightapi.RuntimeResolution, error) {
	return postJSONResponse[preflightapi.RuntimeResolution](ctx, c, "/v1/runtime-aliases/resolve", request)
}
