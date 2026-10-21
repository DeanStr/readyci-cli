package api

import (
	"context"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func (c *Client) StartCLIDevice(ctx context.Context, challenge string) (preflightapi.CLIDeviceAuthorization, error) {
	return postJSONResponse[preflightapi.CLIDeviceAuthorization](ctx, c, "/v1/auth/cli/device/start", preflightapi.CLIDeviceStartRequest{CodeChallenge: challenge})
}
func (c *Client) PollCLIDevice(ctx context.Context, device, verifier string) (preflightapi.CLIDevicePollResponse, error) {
	return postJSONResponse[preflightapi.CLIDevicePollResponse](ctx, c, "/v1/auth/cli/device/poll", preflightapi.CLIDevicePollRequest{DeviceCode: device, CodeVerifier: verifier})
}
