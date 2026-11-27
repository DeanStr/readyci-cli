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
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
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
	printAvailableRegions(settings.AvailableRegions)
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
	snapshot := fs.String("trusted-secret-snapshot", "", "workspace snapshot digest approved for --trusted-secret-command")
	var trustedHashFlags stringSliceFlag
	var trustedCommandFlags stringSliceFlag
	var removeApprovals stringSliceFlag
	fs.Var(&removeApprovals, "remove-trusted-secret-command-hash", "revoke the exact HASH@DIGEST approval; repeatable")
	fs.Var(&trustedHashFlags, "trusted-secret-command-hash", "HASH@DIGEST approval to add; repeatable")
	fs.Var(&trustedCommandFlags, "trusted-secret-command", "command to hash and trust for native secret release; repeatable")
	clearTrustedHashes := fs.Bool("clear-trusted-secret-command-hashes", false, "clear trusted command hashes for native secret release")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci project set [--api-url URL] [--token TOKEN] [--project PROJECT] [--home-region REGION] [--github-repo OWNER/REPO] [--default-branch BRANCH] [--trusted-secret-command COMMAND --trusted-secret-snapshot DIGEST] [--trusted-secret-command-hash HASH@DIGEST] [--clear-trusted-secret-command-hashes | --remove-trusted-secret-command-hash HASH@DIGEST]", nil)
	}
	addingApprovals := len(trustedHashFlags) > 0 || len(trustedCommandFlags) > 0
	if len(removeApprovals) > 0 && (addingApprovals || *clearTrustedHashes) {
		return cliConfigError("remove approvals in a separate update from adding or clearing them", nil)
	}
	if addingApprovals && *clearTrustedHashes {
		return cliConfigError("cannot set and clear trusted secret approvals together", nil)
	}
	// A hash flag already names its digest; the snapshot only pairs with commands.
	if flagProvided(fs, "trusted-secret-snapshot") != (len(trustedCommandFlags) > 0) || (len(trustedCommandFlags) > 0 && syncbundle.ValidateSnapshotDigest(*snapshot) != nil) {
		return cliConfigError("--trusted-secret-command requires --trusted-secret-snapshot with a valid workspace digest, and the snapshot applies only to commands", nil)
	}
	if !flagProvided(fs, "home-region") && !flagProvided(fs, "github-repo") && !flagProvided(fs, "default-branch") && !addingApprovals && len(removeApprovals) == 0 && !*clearTrustedHashes {
		return cliConfigError("provide --home-region, --github-repo, --default-branch, --trusted-secret-command-hash, --trusted-secret-command, --remove-trusted-secret-command-hash, or --clear-trusted-secret-command-hashes", nil)
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
	for _, command := range trustedCommandFlags {
		trustedHashFlags = append(trustedHashFlags, preflightapi.TrustedSecretCommandApproval(preflightapi.TrustedSecretCommandHash(command), *snapshot))
	}
	switch {
	case *clearTrustedHashes:
		req.ClearTrustedSecretCommandHashes = true
	case len(removeApprovals) > 0:
		req.RemoveTrustedSecretCommandHashes, err = parseSecretApprovals(removeApprovals)
	case addingApprovals:
		req.AddTrustedSecretCommandHashes, err = parseSecretApprovals(trustedHashFlags)
	}
	if err != nil {
		return err
	}
	settings, err := client.UpdateProjectSettings(ctx, projectIDValue, req)
	if err != nil {
		return err
	}
	fmt.Printf("updated project %s\n", settings.ProjectID)
	fmt.Printf("home_region: %s\n", preflightapi.NormalizeRegion(settings.HomeRegion))
	printAvailableRegions(settings.AvailableRegions)
	fmt.Printf("github_repository: %s\n", valueOrDash(settings.GitHubRepository))
	fmt.Printf("github_default_branch: %s\n", valueOrDash(settings.GitHubDefaultBranch))
	printTrustedSecretCommandHashes(settings.TrustedSecretCommandHashes)
	return nil
}

func printAvailableRegions(regions []string) {
	if len(regions) == 0 {
		return
	}
	fmt.Printf("available_regions: %s\n", strings.Join(regions, ", "))
}

func parseSecretApprovals(entries []string) ([]string, error) {
	approvals := make([]string, 0, len(entries))
	for _, entry := range entries {
		hash, digest, paired := strings.Cut(entry, "@")
		if !paired {
			return nil, cliConfigError("secret approvals must be HASH@DIGEST pairs, as printed by readyci project get", nil)
		}
		if err := syncbundle.ValidateSnapshotDigest(hash); err != nil {
			return nil, cliConfigError("invalid trusted secret command hash", err)
		}
		if err := syncbundle.ValidateSnapshotDigest(digest); err != nil {
			return nil, cliConfigError("each command hash requires a valid workspace digest", err)
		}
		approvals = append(approvals, preflightapi.TrustedSecretCommandApproval(hash, digest))
	}
	return approvals, nil
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
		fmt.Printf("%-32s requires_approval=%t updated_at=%s\n", secret.Name, secret.RequiresApproval, secret.UpdatedAt.Format(time.RFC3339))
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
	requiresApproval := fs.Bool("requires-approval", true, "release the secret only to approved commands and workspaces; false also releases it to unapproved runs, so use false only for low-value credentials such as sandbox keys")
	const usage = "usage: readyci secrets set [--api-url URL] [--token TOKEN] [--project PROJECT] [--value VALUE|--from-env ENV] [--requires-approval=false] NAME [VALUE]"
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		return cliConfigError(usage, nil)
	}
	// Presence, not content, decides whether a value was given: an empty
	// --value "$UNSET" must fail instead of changing only the approval rule
	// of the credential it was meant to replace.
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	name := fs.Arg(0)
	value := *valueFlag
	if set["from-env"] {
		if set["value"] || fs.NArg() == 2 {
			return cliConfigError("--from-env cannot be combined with --value or a positional value", nil)
		}
		value = os.Getenv(*fromEnv)
	} else if fs.NArg() == 2 {
		if set["value"] {
			return cliConfigError("provide the secret value only once", nil)
		}
		value = fs.Arg(1)
		// Flags after NAME are not parsed, so "NAME --requires-approval=false"
		// would store the flag text as the secret value.
		if strings.HasPrefix(value, "-") {
			return cliConfigError("flags must come before NAME; pass a value that starts with - through --value or --from-env", nil)
		}
	}
	valueGiven := set["value"] || set["from-env"] || fs.NArg() == 2
	if value == "" && (valueGiven || !set["requires-approval"]) {
		return cliConfigError("secret value is required; use --value, --from-env, or NAME VALUE, or pass only --requires-approval to change an existing secret", nil)
	}
	var req preflightapi.UpsertProjectSecretRequest
	if valueGiven {
		req.Value = &value
	}
	if set["requires-approval"] {
		req.RequiresApproval = requiresApproval
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
	resp, err := client.UpsertProjectSecret(ctx, projectIDValue, name, req)
	if err != nil {
		return err
	}
	fmt.Printf("saved secret %s for project %s; requires_approval=%t\n", resp.Secret.Name, projectIDValue, resp.Secret.RequiresApproval)
	if !resp.Secret.RequiresApproval {
		fmt.Fprintln(os.Stderr, "readyci: warning: unapproved runs receive this secret and can print it, so anyone who can request secrets or read the project's runs may see it; those runs must use network_mode none or a registered policy's allowlist")
	}
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
