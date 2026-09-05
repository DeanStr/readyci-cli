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
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func projectCommand(args []string) error {
	if len(args) == 0 {
		printUsage()
		return cliConfigError("usage: readyci project <get|set|policy> [--project PROJECT]", nil)
	}
	switch args[0] {
	case "get":
		return projectGetCommand(args[1:])
	case "set":
		return projectSetCommand(args[1:])
	case "policy":
		return projectPolicyCommand(args[1:])
	default:
		printUsage()
		return cliConfigError(fmt.Sprintf("unknown project command %q", args[0]), nil)
	}
}

func projectGetCommand(args []string) error {
	fs := flag.NewFlagSet("project get", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci project get [--api-url URL] [--token TOKEN] [--project PROJECT]", nil)
	}
	projectIDValue, err := effectiveProjectID(*apiURL, *projectID)
	if err != nil {
		return err
	}
	client, err := authenticatedClient(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	settings, err := client.ProjectSettings(ctx, projectIDValue)
	if err != nil {
		return err
	}
	fmt.Printf("project_id: %s\n", settings.ProjectID)
	fmt.Printf("home_region: %s\n", preflightapi.NormalizeRegion(settings.HomeRegion))
	fmt.Printf("github_repository: %s\n", valueOrDash(settings.GitHubRepository))
	fmt.Printf("github_default_branch: %s\n", valueOrDash(settings.GitHubDefaultBranch))
	printTrustedSecretCommandHashes(settings.TrustedSecretCommandHashes)
	fmt.Printf("secrets: %d\n", len(settings.Secrets))
	return nil
}

func projectSetCommand(args []string) error {
	fs := flag.NewFlagSet("project set", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	homeRegion := fs.String("home-region", "", "default runner region for this project")
	githubRepo := fs.String("github-repo", "", "GitHub repository as owner/repo")
	defaultBranch := fs.String("default-branch", "", "default Git branch")
	var trustedHashFlags stringSliceFlag
	var trustedCommandFlags stringSliceFlag
	fs.Var(&trustedHashFlags, "trusted-secret-command-hash", "trusted command hash for native secret release; repeatable")
	fs.Var(&trustedCommandFlags, "trusted-secret-command", "command to hash and trust for native secret release; repeatable")
	clearTrustedHashes := fs.Bool("clear-trusted-secret-command-hashes", false, "clear trusted command hashes for native secret release")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci project set [--api-url URL] [--token TOKEN] [--project PROJECT] [--home-region REGION] [--github-repo OWNER/REPO] [--default-branch BRANCH] [--trusted-secret-command-hash HASH] [--trusted-secret-command COMMAND] [--clear-trusted-secret-command-hashes]", nil)
	}
	if !flagProvided(fs, "home-region") && !flagProvided(fs, "github-repo") && !flagProvided(fs, "default-branch") && len(trustedHashFlags) == 0 && len(trustedCommandFlags) == 0 && !*clearTrustedHashes {
		return cliConfigError("provide --home-region, --github-repo, --default-branch, --trusted-secret-command-hash, --trusted-secret-command, or --clear-trusted-secret-command-hashes", nil)
	}
	projectIDValue, err := effectiveProjectID(*apiURL, *projectID)
	if err != nil {
		return err
	}
	client, err := authenticatedClient(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	req := preflightapi.UpsertProjectSettingsRequest{}
	if flagProvided(fs, "home-region") {
		req.HomeRegion = homeRegion
	}
	if flagProvided(fs, "github-repo") {
		req.GitHubRepository = githubRepo
	}
	if flagProvided(fs, "default-branch") {
		req.GitHubDefaultBranch = defaultBranch
	}
	if *clearTrustedHashes || len(trustedHashFlags) > 0 || len(trustedCommandFlags) > 0 {
		req.TrustedSecretCommandHashes = []string{}
		if !*clearTrustedHashes {
			req.TrustedSecretCommandHashes = append(req.TrustedSecretCommandHashes, trustedHashFlags...)
			for _, command := range trustedCommandFlags {
				req.TrustedSecretCommandHashes = append(req.TrustedSecretCommandHashes, preflightapi.TrustedSecretCommandHash(command))
			}
		}
	}
	settings, err := client.UpdateProjectSettings(ctx, projectIDValue, req)
	if err != nil {
		return err
	}
	fmt.Printf("updated project %s\n", settings.ProjectID)
	fmt.Printf("home_region: %s\n", preflightapi.NormalizeRegion(settings.HomeRegion))
	fmt.Printf("github_repository: %s\n", valueOrDash(settings.GitHubRepository))
	fmt.Printf("github_default_branch: %s\n", valueOrDash(settings.GitHubDefaultBranch))
	printTrustedSecretCommandHashes(settings.TrustedSecretCommandHashes)
	return nil
}

func printTrustedSecretCommandHashes(hashes []string) {
	fmt.Printf("trusted_secret_command_hashes: %d\n", len(hashes))
	for _, hash := range hashes {
		fmt.Printf("trusted_secret_command_hash: %s\n", hash)
	}
}

func secretsCommand(args []string) error {
	if len(args) == 0 {
		printUsage()
		return cliConfigError("usage: readyci secrets <list|set|delete> [--project PROJECT]", nil)
	}
	switch args[0] {
	case "list":
		return secretsListCommand(args[1:])
	case "set":
		return secretsSetCommand(args[1:])
	case "delete", "rm":
		return secretsDeleteCommand(args[1:])
	default:
		printUsage()
		return cliConfigError(fmt.Sprintf("unknown secrets command %q", args[0]), nil)
	}
}

func secretsListCommand(args []string) error {
	fs := flag.NewFlagSet("secrets list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci secrets list [--api-url URL] [--token TOKEN] [--project PROJECT]", nil)
	}
	projectIDValue, err := effectiveProjectID(*apiURL, *projectID)
	if err != nil {
		return err
	}
	client, err := authenticatedClient(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	resp, err := client.ListProjectSecrets(ctx, projectIDValue)
	if err != nil {
		return err
	}
	if len(resp.Secrets) == 0 {
		fmt.Printf("no secrets for project %s\n", resp.ProjectID)
		return nil
	}
	fmt.Printf("secrets for project %s\n", resp.ProjectID)
	for _, secret := range resp.Secrets {
		fmt.Printf("%-32s updated_at=%s\n", secret.Name, secret.UpdatedAt.Format(time.RFC3339))
	}
	return nil
}

func secretsSetCommand(args []string) error {
	fs := flag.NewFlagSet("secrets set", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	valueFlag := fs.String("value", "", "secret value")
	fromEnv := fs.String("from-env", "", "read the secret value from this local environment variable")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		return cliConfigError("usage: readyci secrets set [--api-url URL] [--token TOKEN] [--project PROJECT] [--value VALUE|--from-env ENV] NAME [VALUE]", nil)
	}
	name := fs.Arg(0)
	value := *valueFlag
	if *fromEnv != "" {
		if *valueFlag != "" || fs.NArg() == 2 {
			return cliConfigError("--from-env cannot be combined with --value or a positional value", nil)
		}
		value = os.Getenv(*fromEnv)
	} else if fs.NArg() == 2 {
		if *valueFlag != "" {
			return cliConfigError("provide the secret value only once", nil)
		}
		value = fs.Arg(1)
	}
	if value == "" {
		return cliConfigError("secret value is required; use --value, --from-env, or NAME VALUE", nil)
	}
	projectIDValue, err := effectiveProjectID(*apiURL, *projectID)
	if err != nil {
		return err
	}
	client, err := authenticatedClient(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	resp, err := client.UpsertProjectSecret(ctx, projectIDValue, name, value)
	if err != nil {
		return err
	}
	fmt.Printf("saved secret %s for project %s\n", resp.Secret.Name, projectIDValue)
	return nil
}

func secretsDeleteCommand(args []string) error {
	fs := flag.NewFlagSet("secrets delete", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	reason := fs.String("reason", "", "reason for deleting the project secret (required)")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci secrets delete [--api-url URL] [--token TOKEN] [--project PROJECT] --reason REASON NAME", nil)
	}
	if strings.TrimSpace(*reason) == "" {
		return cliConfigError("--reason is required", nil)
	}
	projectIDValue, err := effectiveProjectID(*apiURL, *projectID)
	if err != nil {
		return err
	}
	client, err := authenticatedClient(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	resp, err := client.DeleteProjectSecret(ctx, projectIDValue, fs.Arg(0), strings.TrimSpace(*reason))
	if err != nil {
		return err
	}
	if resp.Deleted {
		fmt.Printf("deleted secret %s from project %s\n", resp.Name, resp.ProjectID)
	} else {
		fmt.Printf("secret %s was not set for project %s\n", resp.Name, resp.ProjectID)
	}
	return nil
}

func effectiveProjectID(apiURL, projectID string) (string, error) {
	if err := validateControlFreeValue("project_id", projectID); err != nil {
		return "", err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID != "" {
		return projectID, nil
	}
	cfg, _, err := config.LoadProjectConfig(".")
	if err != nil {
		return "", err
	}
	if err := validateControlFreeValue("project_id", cfg.ProjectID); err != nil {
		return "", err
	}
	return executionProjectID(apiURL, cfg.ProjectID)
}

func authenticatedClient(apiURL, token string) (*api.Client, error) {
	effectiveToken, err := effectiveAPIToken(apiURL, token)
	if err != nil {
		return nil, err
	}
	if effectiveToken == "" {
		return nil, cliAuthMissingError("No Ready CI credential is available.", nil)
	}
	return api.NewClient(apiURL, api.WithToken(effectiveToken)), nil
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), shutdownSignals()...)
}

func displayRunName(options remoteRunOptions) string {
	if options.CommandAlias != "" {
		return options.CommandAlias
	}
	return truncate(oneLine(options.Command), 64)
}
