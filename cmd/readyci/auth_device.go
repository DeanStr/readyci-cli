package main

import (
	"context"
	"fmt"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

// deviceRedemptionPolls is how many poll intervals the CLI keeps polling after
// the request's expiry. The server still delivers an approval recorded before
// expiry during that window, so stopping at expiry would lose it.
const deviceRedemptionPolls = 2

// cliUserAgent identifies this CLI to the device approval page, which shows it
// to the approver next to the request's address.
func cliUserAgent() string {
	return fmt.Sprintf("readyci/%s (%s/%s)", version, runtime.GOOS, runtime.GOARCH)
}

func validateDeviceAuthorization(authorization preflightapi.CLIDeviceAuthorization) error {
	if authorization.DeviceCode == "" || authorization.UserCode == "" || authorization.VerificationURI == "" || authorization.IntervalSeconds < 1 || authorization.IntervalSeconds > 60 || !authorization.ExpiresAt.After(time.Now()) {
		return fmt.Errorf("invalid device authorization response")
	}
	verification, err := url.Parse(authorization.VerificationURI)
	if err != nil || (verification.Scheme != "https" && verification.Scheme != "http") || verification.Host == "" || verification.User != nil {
		return fmt.Errorf("invalid device authorization response: verification URL must be an absolute http(s) URL")
	}
	// The user must type the code; a link that carries it enables one-click
	// phishing of the approval.
	normalized := strings.ToUpper(authorization.VerificationURI)
	for _, secret := range []string{authorization.UserCode, strings.ReplaceAll(authorization.UserCode, "-", ""), authorization.DeviceCode} {
		if secret != "" && strings.Contains(normalized, strings.ToUpper(secret)) {
			return fmt.Errorf("invalid device authorization response: verification URL must not contain the login code")
		}
	}
	return nil
}

func deviceLogin(ctx context.Context, apiURL string) error {
	verifier, err := randomAuthToken()
	if err != nil {
		return err
	}
	client := api.NewClient(apiURL, api.WithUserAgent(cliUserAgent()))
	authorization, err := client.StartCLIDevice(ctx, preflightapi.PKCEChallengeS256(verifier))
	if err != nil {
		return err
	}
	if err := validateDeviceAuthorization(authorization); err != nil {
		return err
	}
	fmt.Printf("To approve this login, open this page in a browser signed in to Ready CI:\n  %s\nThen type this code: %s\n\n", authorization.VerificationURI, authorization.UserCode)
	fmt.Printf("The page shows where this request came from. Deny it unless you just ran readyci login --device yourself.\n")
	fmt.Printf("Device logins can set up, run and inspect checks, but cannot manage tokens, members or secrets, or purge caches.\nWaiting for approval…\n")
	interval := time.Duration(authorization.IntervalSeconds) * time.Second
	giveUp := authorization.ExpiresAt.Add(deviceRedemptionPolls * interval)
	for {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if time.Now().After(giveUp) {
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
			fmt.Printf("Logged in to %s\ntoken_id: %s\nscopes: %s\n", apiURL, credential.TokenID, strings.Join(result.Token.Token.Scopes, ","))
			return nil
		default:
			return fmt.Errorf("unsupported device authorization state %q", result.State)
		}
	}
}
