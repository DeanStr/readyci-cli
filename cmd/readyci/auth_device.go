package main

import (
	"context"
	"fmt"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func deviceLogin(ctx context.Context, apiURL string) error {
	verifier, err := randomAuthToken()
	if err != nil {
		return err
	}
	client := api.NewClient(apiURL)
	authorization, err := client.StartCLIDevice(ctx, preflightapi.PKCEChallengeS256(verifier))
	if err != nil {
		return err
	}
	if authorization.DeviceCode == "" || authorization.UserCode == "" || authorization.VerificationURI == "" || authorization.IntervalSeconds < 1 || authorization.IntervalSeconds > 60 || !authorization.ExpiresAt.After(time.Now()) {
		return fmt.Errorf("invalid device authorization response")
	}
	fmt.Printf("Open this URL on your browser device:\n%s\nMatching code: %s\nApprove only the login you started. Waiting for approval…\n", authorization.VerificationURI, authorization.UserCode)
	interval := time.Duration(authorization.IntervalSeconds) * time.Second
	for {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if !authorization.ExpiresAt.After(time.Now()) {
			return fmt.Errorf("device authorization expired; restart readyci login --device")
		}
		result, err := client.PollCLIDevice(ctx, authorization.DeviceCode, verifier)
		if err != nil {
			return err
		}
		switch result.State {
		case "pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "denied":
			return fmt.Errorf("device authorization was denied")
		case "expired":
			return fmt.Errorf("device authorization expired; restart readyci login --device")
		case "approved":
			if result.Token == nil || result.Token.Secret == "" || result.Token.Token.ID == "" {
				return fmt.Errorf("approved device authorization has no credential")
			}
			credential := storedCredential{APIURL: apiURL, Token: result.Token.Secret, TokenID: result.Token.Token.ID, ExpiresAt: result.Token.Token.ExpiresAt, CreatedAt: time.Now().UTC()}
			if err := saveStoredCredential(credential); err != nil {
				return err
			}
			fmt.Printf("Logged in to %s\ntoken_id: %s\n", apiURL, credential.TokenID)
			return nil
		default:
			return fmt.Errorf("unsupported device authorization state %q", result.State)
		}
	}
}
