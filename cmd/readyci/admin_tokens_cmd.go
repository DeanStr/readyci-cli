package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func adminTokensCommand(args []string) error {
	if len(args) == 0 {
		printUsage()
		return cliConfigError("usage: readyci admin tokens <list|create|revoke>", nil)
	}
	switch args[0] {
	case "list":
		return adminTokensListCommand(args[1:])
	case "create":
		return adminTokensCreateCommand(args[1:])
	case "revoke":
		return adminTokensRevokeCommand(args[1:])
	default:
		printUsage()
		return cliConfigError(fmt.Sprintf("unknown admin tokens command %q", args[0]), nil)
	}
}

func adminTokensListCommand(args []string) error {
	fs := flag.NewFlagSet("admin tokens list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci admin tokens list [--api-url URL] [--token TOKEN]", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminListAPITokens(ctx)
	if err != nil {
		return err
	}
	if len(resp.Tokens) == 0 {
		fmt.Println("no api tokens")
		return nil
	}
	fmt.Printf("%-22s %-18s %-9s %-20s %s\n", "TOKEN ID", "USER", "STATUS", "CREATED", "SCOPES")
	for _, token := range resp.Tokens {
		status := "active"
		if token.RevokedAt != nil {
			status = "revoked"
		} else if token.ExpiresAt != nil && token.ExpiresAt.Before(time.Now()) {
			status = "expired"
		}
		fmt.Printf("%-22s %-18s %-9s %-20s %s\n",
			token.ID, valueOrDash(token.UserID), status, token.CreatedAt.Format(time.RFC3339), strings.Join(token.Scopes, ","))
	}
	return nil
}

func adminTokensCreateCommand(args []string) error {
	fs := flag.NewFlagSet("admin tokens create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	name := fs.String("name", "CLI token", "API token name")
	teamID := fs.String("team", "", "team id to create the token for; defaults to the development team")
	scopes := fs.String("scopes", "runs:create,runs:read,cache:read,cache:purge,projects:read,projects:write,policies:read,secrets:read,secrets:write,account:read,tokens:read,tokens:write,members:read,members:write,billing:manage,feedback:write", "comma-separated token scopes")
	userEmail := fs.String("email", "", "optional user email to own the token")
	userName := fs.String("user-name", "", "optional user display name")
	role := fs.String("role", "owner", "team role for --email: owner, admin, member, or viewer")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci admin tokens create [--api-url URL] [--token TOKEN] [--name NAME] [--team TEAM] [--scopes LIST] [--email EMAIL]", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminCreateAPIToken(ctx, preflightapi.CreateAPITokenRequest{
		Name:      *name,
		TeamID:    *teamID,
		Scopes:    splitCSV(*scopes),
		UserEmail: *userEmail,
		UserName:  *userName,
		Role:      *role,
	})
	if err != nil {
		return err
	}
	fmt.Printf("token_id: %s\n", resp.Token.ID)
	fmt.Printf("scopes: %s\n", strings.Join(resp.Token.Scopes, ","))
	fmt.Printf("secret: %s\n", resp.Secret)
	return nil
}

func adminTokensRevokeCommand(args []string) error {
	fs := flag.NewFlagSet("admin tokens revoke", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, adminToken := adminAPIFlags(fs)
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci admin tokens revoke [--api-url URL] [--token TOKEN] TOKEN_ID", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	resp, err := api.NewClient(*apiURL, api.WithToken(*adminToken)).AdminRevokeAPIToken(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	fmt.Printf("%s revoked_at=%s\n", resp.Token.ID, timeOrDash(resp.Token.RevokedAt))
	return nil
}
