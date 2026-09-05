package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func agentTokenCommand(args []string) error {
	if len(args) == 0 {
		return cliConfigError("usage: readyci agent token <create>", nil)
	}
	switch args[0] {
	case "create":
		return agentTokenCreateCommand(args[1:])
	default:
		return cliConfigError(fmt.Sprintf("unknown agent token command %q", args[0]), nil)
	}
}

func agentTokenCreateCommand(args []string) error {
	fs := flag.NewFlagSet("agent token create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL := fs.String("api-url", envDefault("READYCI_API_URL", defaultAPIURL()), "Ready CI API URL")
	apiToken := apiTokenFlag(fs, "bearer token with tokens:write")
	name := fs.String("name", "Ready CI verification agent", "API token name")
	preset := fs.String("preset", "verify-only", "agent token preset")
	ttl := fs.String("ttl", "", "optional token TTL, such as 30d or 12h")
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci agent token create [--preset verify-only] [--name NAME] [--ttl 30d] [--json]", nil)
	}
	scopes, ok := agentTokenPresets[*preset]
	if !ok {
		return cliConfigError(fmt.Sprintf("unknown agent token preset %q (available: %s)", *preset, strings.Join(sortedAgentTokenPresets(), ", ")), nil)
	}
	var expiresAt *time.Time
	if strings.TrimSpace(*ttl) != "" {
		parsed, err := parseAgentTTL(*ttl)
		if err != nil {
			return cliConfigError(err.Error(), err)
		}
		expiry := time.Now().UTC().Add(parsed)
		expiresAt = &expiry
	}
	client, err := authenticatedClient(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	resp, err := client.CreateUserAPIToken(ctx, preflightapi.CreateAPITokenRequest{
		Name:      *name,
		Scopes:    append([]string(nil), scopes...),
		ExpiresAt: expiresAt,
	})
	if err != nil {
		if tokenCreateForbidden(err) {
			return fmt.Errorf("%w\n\ncreating agent tokens requires a valid logged-in or parent API token with tokens:write; sign in again with an owner/admin account or create a scoped token in the dashboard", err)
		}
		return err
	}
	if *jsonOutput {
		return writeJSONOutput(resp)
	}
	fmt.Printf("token_id: %s\n", resp.Token.ID)
	fmt.Printf("preset: %s\n", *preset)
	fmt.Printf("scopes: %s\n", strings.Join(resp.Token.Scopes, ","))
	if resp.Token.ExpiresAt != nil {
		fmt.Printf("expires_at: %s\n", resp.Token.ExpiresAt.Format(time.RFC3339))
	}
	fmt.Printf("secret: %s\n", resp.Secret)
	return nil
}

func tokenCreateForbidden(err error) bool {
	var responseErr api.ResponseError
	return errors.As(err, &responseErr) && responseErr.StatusCode == 403
}

func parseAgentTTL(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("ttl is empty")
	}
	if strings.HasSuffix(value, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(value, "d"))
		if err != nil || days <= 0 {
			return 0, fmt.Errorf("invalid ttl %q", value)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("invalid ttl %q", value)
	}
	return duration, nil
}

func sortedAgentTokenPresets() []string {
	names := make([]string, 0, len(agentTokenPresets))
	for name := range agentTokenPresets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
