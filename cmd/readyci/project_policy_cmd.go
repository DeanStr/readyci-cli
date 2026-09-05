package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func projectPolicyCommand(args []string) error {
	if len(args) == 0 {
		return cliConfigError("usage: readyci project policy <get|preview> [--project PROJECT]; activation is browser-only", nil)
	}
	switch args[0] {
	case "get":
		return projectPolicyGetCommand(args[1:])
	case "preview":
		return projectPolicyPreviewCommand(args[1:])
	default:
		return cliConfigError(fmt.Sprintf("unknown project policy command %q", args[0]), nil)
	}
}

func projectPolicyGetCommand(args []string) error {
	fs := flag.NewFlagSet("project policy get", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci project policy get [--project PROJECT] [--json]", nil)
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
	response, err := client.ProjectVerificationPolicy(ctx, projectIDValue)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSONOutput(response)
	}
	printProjectVerificationPolicy(response)
	return nil
}

func projectPolicyPreviewCommand(args []string) error {
	fs := flag.NewFlagSet("project policy preview", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	filePath := fs.String("file", ".readyci.yml", "policy YAML file")
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci project policy preview [--project PROJECT] [--file .readyci.yml] [--json]", nil)
	}
	projectIDValue, configYAML, err := projectPolicyCommandInput(*apiURL, *projectID, *filePath)
	if err != nil {
		return err
	}
	client, err := authenticatedClient(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	preview, err := client.PreviewProjectVerificationPolicy(ctx, projectIDValue, preflightapi.PreviewProjectVerificationPolicyRequest{ConfigYAML: configYAML})
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSONOutput(preview)
	}
	printProjectVerificationPolicyPreview(preview)
	return nil
}

func projectPolicyCommandInput(apiURL, projectID, filePath string) (string, string, error) {
	projectIDValue, err := effectiveProjectID(apiURL, projectID)
	if err != nil {
		return "", "", err
	}
	data, err := os.ReadFile(strings.TrimSpace(filePath))
	if err != nil {
		return "", "", err
	}
	return projectIDValue, string(data), nil
}

func printProjectVerificationPolicy(response preflightapi.ProjectVerificationPolicyResponse) {
	fmt.Printf("project_id: %s\n", response.ProjectID)
	if response.Active == nil {
		fmt.Println("active_policy: none")
		fmt.Printf("revisions: %d\n", len(response.Revisions))
		return
	}
	fmt.Printf("active_revision: %d\n", response.Active.Revision)
	fmt.Printf("active_digest: %s\n", response.Active.Digest)
	fmt.Printf("checks: %s\n", strings.Join(response.Active.CheckNames, ","))
	fmt.Printf("affected_rules: %d\n", response.Active.AffectedRuleCount)
	fmt.Printf("revisions: %d\n", len(response.Revisions))
}

func printProjectVerificationPolicyPreview(preview preflightapi.ProjectVerificationPolicyPreview) {
	fmt.Printf("project_id: %s\n", preview.ProjectID)
	fmt.Printf("preview_digest: %s\n", preview.Digest)
	fmt.Printf("active_digest: %s\n", defaultString(preview.ActiveDigest, "none"))
	fmt.Printf("checks: %s\n", strings.Join(preview.CheckNames, ","))
	fmt.Printf("affected_rules: %d\n", preview.AffectedRuleCount)
	fmt.Printf("added_checks: %s\n", strings.Join(preview.AddedChecks, ","))
	fmt.Printf("removed_checks: %s\n", strings.Join(preview.RemovedChecks, ","))
	fmt.Printf("changed_checks: %s\n", strings.Join(preview.ChangedChecks, ","))
	for _, warning := range preview.Warnings {
		fmt.Printf("warning: %s\n", warning)
	}
}
