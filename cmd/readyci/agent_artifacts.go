package main

import (
	"context"
	"fmt"
	"os/signal"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/artifactbundle"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

type agentArtifactHandoffOptions struct {
	APIURL        string
	APIToken      string
	RunID         string
	Paths         []string
	ContentSHA256 string
	JSONOutput    bool
	AllowEmpty    bool
	Confirm       bool
}

type agentArtifactHandoffReport struct {
	SchemaVersion  string                         `json:"schema_version"`
	Status         string                         `json:"status"`
	RunID          string                         `json:"run_id"`
	DryRun         bool                           `json:"dry_run"`
	Confirmed      bool                           `json:"confirmed"`
	Paths          []string                       `json:"paths"`
	Artifacts      []preflightapi.ArtifactSummary `json:"artifacts"`
	MatchedCount   int                            `json:"matched_count"`
	AppliedCount   int                            `json:"applied_count"`
	Files          []artifactbundle.File          `json:"files"`
	EvidencePacks  []agentEvidencePack            `json:"evidence_packs,omitempty"`
	ApplyCommand   string                         `json:"apply_command,omitempty"`
	ConfirmCommand string                         `json:"confirm_command,omitempty"`
	NextActions    []agentNextAction              `json:"next_actions"`
	Warnings       []agentWarning                 `json:"warnings,omitempty"`
	Error          string                         `json:"error,omitempty"`
}

func agentArtifactsCommand(args []string) error {
	if len(args) == 0 {
		return cliConfigError("usage: readyci agent artifacts <plan|apply> RUN_ID --paths PATTERN [--json]", nil)
	}
	switch args[0] {
	case "plan":
		return agentArtifactsPlanCommand(args[1:])
	case "apply":
		return agentArtifactsApplyCommand(args[1:])
	default:
		return cliConfigError(fmt.Sprintf("unknown agent artifacts command %q", args[0]), nil)
	}
}

func agentArtifactsPlanCommand(args []string) error {
	options, err := parseAgentArtifactHandoffArgs("plan", args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	artifacts, err := listArtifactsForAgentHandoff(ctx, options)
	if err != nil {
		return err
	}
	var result artifactApplyResult
	if len(artifacts) > 0 {
		result, err = buildArtifactApplyResult(artifactApplyOptions{
			APIURL:        options.APIURL,
			APIToken:      options.APIToken,
			RunID:         options.RunID,
			Paths:         options.Paths,
			ContentSHA256: artifacts[0].ContentSHA256,
			DryRun:        true,
			AllowEmpty:    true,
		})
	}
	report := agentArtifactPlanReport(options, artifacts, result)
	if err != nil {
		report.Status = "error"
		report.NextActions = []agentNextAction{}
		report.Error = err.Error()
		report.ApplyCommand = ""
		report.ConfirmCommand = ""
		report.Warnings = nil
		if options.JSONOutput {
			_ = writeJSONOutput(report)
		}
		return err
	}
	if options.JSONOutput {
		return writeJSONOutput(report)
	}
	if len(report.Artifacts) == 0 {
		fmt.Printf("no artifacts available for run %s\n", report.RunID)
		return nil
	}
	if report.Status == "no_matches" {
		fmt.Printf("no artifact files matched requested paths for run %s\n", report.RunID)
		return nil
	}
	fmt.Printf("artifact handoff ready for run %s\n", report.RunID)
	for _, artifact := range report.Artifacts {
		fmt.Printf("%s %s size_bytes=%d\n", artifact.ID, artifact.Path, artifact.SizeBytes)
	}
	fmt.Printf("preview: %s\n", report.ApplyCommand)
	fmt.Printf("apply: %s\n", report.ConfirmCommand)
	return nil
}

func agentArtifactsApplyCommand(args []string) error {
	options, err := parseAgentArtifactHandoffArgs("apply", args)
	if err != nil {
		return err
	}
	result, err := buildArtifactApplyResult(artifactApplyOptions{
		APIURL:        options.APIURL,
		APIToken:      options.APIToken,
		RunID:         options.RunID,
		Paths:         options.Paths,
		ContentSHA256: options.ContentSHA256,
		DryRun:        !options.Confirm,
		AllowEmpty:    options.AllowEmpty,
	})
	report := agentArtifactApplyReport(options, result)
	if err != nil {
		report.Status = "error"
		if strings.Contains(err.Error(), "no artifact files matched --paths") {
			report.Status = "no_matches"
		}
		report.NextActions = []agentNextAction{}
		report.Error = err.Error()
		if options.JSONOutput {
			_ = writeJSONOutput(report)
		}
		return err
	}
	if options.JSONOutput {
		return writeJSONOutput(report)
	}
	action := "would apply"
	if options.Confirm {
		action = "applied"
	}
	fmt.Printf("%s %d artifact file(s) from run %s\n", action, report.MatchedCount, report.RunID)
	for _, file := range report.Files {
		fmt.Printf("%s size_bytes=%d\n", file.Path, file.SizeBytes)
	}
	if !options.Confirm {
		fmt.Printf("apply: %s\n", report.ConfirmCommand)
	}
	return nil
}

func agentFixtureArtifactActions(apiURL, runID string, paths []string) []agentNextAction {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil
	}
	paths = uniqueNonEmptyStrings(paths)
	if len(paths) == 0 {
		return nil
	}
	command := agentArtifactApplyShellCommand(apiURL, runID, paths, "", false)
	return []agentNextAction{{
		Kind:    "artifacts_apply",
		Command: command,
		Path:    strings.Join(paths, ","),
		Reason:  "Preview generated fixture artifacts before applying them.",
	}}
}

func parseAgentArtifactHandoffArgs(command string, args []string) (agentArtifactHandoffOptions, error) {
	options := agentArtifactHandoffOptions{
		APIURL:   envDefault("READYCI_API_URL", defaultAPIURL()),
		APIToken: envDefault("READYCI_API_TOKEN", ""),
	}
	collectingPaths := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			collectingPaths = false
			options.JSONOutput = true
		case arg == "--allow-empty":
			collectingPaths = false
			options.AllowEmpty = true
		case arg == "--confirm":
			collectingPaths = false
			options.Confirm = true
		case arg == "--dry-run":
			collectingPaths = false
			options.Confirm = false
		case arg == "--api-url" || arg == "--token" || arg == "--paths" || arg == "--content-sha256":
			if i+1 >= len(args) {
				return agentArtifactHandoffOptions{}, cliConfigError(fmt.Sprintf("%s requires a value", arg), nil)
			}
			i++
			switch arg {
			case "--api-url":
				options.APIURL = args[i]
				collectingPaths = false
			case "--token":
				options.APIToken = args[i]
				collectingPaths = false
			case "--paths":
				options.Paths = append(options.Paths, splitCSV(args[i])...)
				collectingPaths = true
			case "--content-sha256":
				options.ContentSHA256 = args[i]
				collectingPaths = false
			}
		case strings.HasPrefix(arg, "--api-url="):
			collectingPaths = false
			options.APIURL = strings.TrimPrefix(arg, "--api-url=")
		case strings.HasPrefix(arg, "--token="):
			collectingPaths = false
			options.APIToken = strings.TrimPrefix(arg, "--token=")
		case strings.HasPrefix(arg, "--paths="):
			options.Paths = append(options.Paths, splitCSV(strings.TrimPrefix(arg, "--paths="))...)
			collectingPaths = true
		case strings.HasPrefix(arg, "--content-sha256="):
			collectingPaths = false
			options.ContentSHA256 = strings.TrimPrefix(arg, "--content-sha256=")
		case arg == "-h" || arg == "--help":
			return agentArtifactHandoffOptions{}, cliConfigError(fmt.Sprintf("usage: readyci agent artifacts %s RUN_ID --paths PATTERN [--paths PATTERN] [--json]", command), nil)
		case strings.HasPrefix(arg, "-"):
			return agentArtifactHandoffOptions{}, cliConfigError(fmt.Sprintf("unknown agent artifacts %s flag %q", command, arg), nil)
		default:
			if options.RunID != "" {
				if collectingPaths {
					options.Paths = append(options.Paths, splitCSV(arg)...)
					continue
				}
				return agentArtifactHandoffOptions{}, cliConfigError(fmt.Sprintf("usage: readyci agent artifacts %s RUN_ID --paths PATTERN [--paths PATTERN] [--json]", command), nil)
			}
			options.RunID = arg
			collectingPaths = false
		}
	}
	if strings.TrimSpace(options.RunID) == "" {
		return agentArtifactHandoffOptions{}, cliConfigError(fmt.Sprintf("usage: readyci agent artifacts %s RUN_ID --paths PATTERN [--paths PATTERN] [--json]", command), nil)
	}
	options.Paths = uniqueNonEmptyStrings(options.Paths)
	if len(options.Paths) == 0 {
		return agentArtifactHandoffOptions{}, fmt.Errorf("readyci agent artifacts %s requires at least one --paths pattern", command)
	}
	if options.ContentSHA256 != "" {
		if err := preflightapi.ValidateSHA256Hex(options.ContentSHA256); err != nil {
			return agentArtifactHandoffOptions{}, fmt.Errorf("invalid --content-sha256: %w", err)
		}
	}
	return options, nil
}

func listArtifactsForAgentHandoff(ctx context.Context, options agentArtifactHandoffOptions) ([]preflightapi.ArtifactSummary, error) {
	token, err := effectiveAPIToken(options.APIURL, options.APIToken)
	if err != nil {
		return nil, err
	}
	resp, err := api.NewClient(options.APIURL, api.WithToken(token)).ListArtifacts(ctx, options.RunID)
	if err != nil {
		return nil, err
	}
	artifacts := append([]preflightapi.ArtifactSummary{}, resp.Artifacts...)
	for _, artifact := range artifacts {
		if err := preflightapi.ValidateSHA256Hex(artifact.ContentSHA256); err != nil {
			return nil, fmt.Errorf(
				"artifact %q is not bound to stored content; expire or purge the prerelease artifact: %w",
				artifact.ID,
				err,
			)
		}
	}
	return artifacts, nil
}

func agentArtifactPlanReport(options agentArtifactHandoffOptions, artifacts []preflightapi.ArtifactSummary, result artifactApplyResult) agentArtifactHandoffReport {
	paths := uniqueNonEmptyStrings(options.Paths)
	contentSHA256 := result.ContentSHA256
	report := agentArtifactHandoffReport{
		SchemaVersion:  agentSchemaVersion,
		Status:         "ready",
		RunID:          strings.TrimSpace(options.RunID),
		DryRun:         true,
		Confirmed:      false,
		Paths:          paths,
		Artifacts:      append([]preflightapi.ArtifactSummary{}, artifacts...),
		MatchedCount:   result.MatchedCount,
		AppliedCount:   0,
		Files:          append([]artifactbundle.File{}, result.Files...),
		EvidencePacks:  evidencePacksFromArtifactFiles(options.APIURL, options.RunID, result.Files),
		ApplyCommand:   agentArtifactApplyShellCommand(options.APIURL, options.RunID, paths, contentSHA256, false),
		ConfirmCommand: agentArtifactApplyShellCommand(options.APIURL, options.RunID, paths, contentSHA256, true),
	}
	report.NextActions = []agentNextAction{
		{
			Kind:    "artifacts_apply_dry_run",
			Command: report.ApplyCommand,
			Path:    strings.Join(paths, ","),
			Reason:  "Preview generated artifacts before changing the working tree.",
		},
		{
			Kind:    "artifacts_apply_confirm",
			Command: report.ConfirmCommand,
			Path:    strings.Join(paths, ","),
			Reason:  "Apply the previewed artifact files only after the paths are expected.",
		},
	}
	if len(artifacts) == 0 {
		report.Status = "no_artifacts"
		report.NextActions = []agentNextAction{}
		report.ApplyCommand = ""
		report.ConfirmCommand = ""
		report.Warnings = []agentWarning{{Section: "artifacts", Message: "run has no downloadable artifacts"}}
	} else if report.MatchedCount == 0 {
		report.Status = "no_matches"
		report.NextActions = []agentNextAction{}
		report.ConfirmCommand = ""
		report.Warnings = []agentWarning{{Section: "artifacts", Message: "no artifact files matched requested paths"}}
	}
	return report
}

func agentArtifactApplyReport(options agentArtifactHandoffOptions, result artifactApplyResult) agentArtifactHandoffReport {
	paths := uniqueNonEmptyStrings(options.Paths)
	contentSHA256 := result.ContentSHA256
	status := "previewed"
	if options.Confirm {
		status = "applied"
	}
	report := agentArtifactHandoffReport{
		SchemaVersion:  agentSchemaVersion,
		Status:         status,
		RunID:          strings.TrimSpace(options.RunID),
		DryRun:         !options.Confirm,
		Confirmed:      options.Confirm,
		Paths:          paths,
		Artifacts:      []preflightapi.ArtifactSummary{},
		MatchedCount:   result.MatchedCount,
		AppliedCount:   result.AppliedCount,
		Files:          append([]artifactbundle.File{}, result.Files...),
		EvidencePacks:  evidencePacksFromArtifactFiles(options.APIURL, options.RunID, result.Files),
		ApplyCommand:   agentArtifactApplyShellCommand(options.APIURL, options.RunID, paths, contentSHA256, false),
		ConfirmCommand: agentArtifactApplyShellCommand(options.APIURL, options.RunID, paths, contentSHA256, true),
		NextActions:    []agentNextAction{},
	}
	if !options.Confirm {
		report.NextActions = []agentNextAction{{
			Kind:    "artifacts_apply_confirm",
			Command: agentArtifactApplyShellCommand(options.APIURL, options.RunID, paths, contentSHA256, true),
			Path:    strings.Join(paths, ","),
			Reason:  "Apply the previewed artifact files only after the paths are expected.",
		}}
	}
	return report
}

func agentArtifactApplyShellCommand(apiURL, runID string, paths []string, contentSHA256 string, confirm bool) string {
	command := "readyci agent artifacts apply"
	command += apiURLFlag(apiURL)
	command += " --json " + shellQuoteArg(strings.TrimSpace(runID))
	if contentSHA256 != "" {
		command += " --content-sha256 " + shellQuoteArg(contentSHA256)
	}
	if confirm {
		command += " --confirm"
	}
	for _, path := range uniqueNonEmptyStrings(paths) {
		command += " --paths " + shellQuoteArg(path)
	}
	return command
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
