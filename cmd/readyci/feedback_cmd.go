package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func feedbackCommand(args []string) error {
	fs := flag.NewFlagSet("feedback", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	runID := fs.String("run", "", "run id to attach context from, or latest")
	category := fs.String("category", preflightapi.FeedbackCategoryOther, "feedback category: bug, feature, billing, ops, or other")
	message := fs.String("message", "", "feedback message")
	contactEmail := fs.String("contact-email", "", "reply email for static-token or automation feedback; logged-in user identity takes precedence")
	contactName := fs.String("contact-name", "", "reply contact name for static-token or automation feedback")
	includeLogs := fs.Bool("include-logs", false, "record consent to include logs when log attachment support is enabled")
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	projectFlagSet := false
	fs.Visit(func(flag *flag.Flag) {
		if flag.Name == "project" {
			projectFlagSet = true
		}
	})
	if fs.NArg() > 0 {
		if strings.TrimSpace(*message) != "" {
			return cliConfigError("usage: readyci feedback [--api-url URL] [--token TOKEN] [--project PROJECT] [--run RUN_ID|latest] [--category CATEGORY] [--message TEXT] [--contact-email EMAIL] [--contact-name NAME] [--include-logs] [--json] [message]", nil)
		}
		*message = strings.Join(fs.Args(), " ")
	}
	if strings.TrimSpace(*message) == "" {
		return cliConfigError("usage: readyci feedback [--api-url URL] [--token TOKEN] [--project PROJECT] [--run RUN_ID|latest] [--category CATEGORY] [--message TEXT] [--contact-email EMAIL] [--contact-name NAME] [--include-logs] [--json] [message]", nil)
	}
	normalizedCategory, err := normalizeFeedbackCategoryCLI(*category)
	if err != nil {
		return cliConfigError(err.Error(), err)
	}
	client, err := authenticatedClient(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()

	if err := validateControlFreeValue("project_id", *projectID); err != nil {
		return cliConfigError(err.Error(), err)
	}
	projectIDValue := strings.TrimSpace(*projectID)
	runIDValue := strings.TrimSpace(*runID)
	var runContext preflightapi.FeedbackRunContext
	explicitRunID := runIDValue != "" && runIDValue != "latest"
	if explicitRunID && !projectFlagSet {
		projectIDValue = ""
	}
	if runIDValue == "latest" {
		if projectIDValue == "" {
			projectIDValue, err = effectiveProjectID(*apiURL, "")
			if err != nil {
				return err
			}
		}
		resp, err := client.ListRuns(ctx, projectIDValue, "", 1)
		if err != nil {
			return err
		}
		if len(resp.Runs) == 0 {
			if projectIDValue != "" {
				return fmt.Errorf("no runs found for project %s", projectIDValue)
			}
			return cliConfigError("no runs found", nil)
		}
		runIDValue = resp.Runs[0].RunID
		runContext = feedbackRunContextFromSummary(resp.Runs[0])
		projectIDValue = defaultString(projectIDValue, resp.Runs[0].ProjectID)
	}
	req := preflightapi.CreateFeedbackRequest{
		ProjectID:    projectIDValue,
		RunID:        runIDValue,
		Category:     normalizedCategory,
		Message:      strings.TrimSpace(*message),
		IncludeLogs:  *includeLogs,
		LogsIncluded: false,
		Contact: preflightapi.FeedbackContact{
			Name:  strings.TrimSpace(*contactName),
			Email: strings.TrimSpace(*contactEmail),
		},
		Client: preflightapi.ClientInfo{
			Version:  version,
			Platform: runtime.GOOS + "/" + runtime.GOARCH,
		},
		Run: runContext,
	}
	resp, err := client.CreateFeedback(ctx, req)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSONOutput(resp)
	}
	printFeedbackResponse(resp.Feedback)
	return nil
}

func feedbackRunContextFromSummary(summary preflightapi.RunSummary) preflightapi.FeedbackRunContext {
	return preflightapi.FeedbackRunContext{
		RunID:          summary.RunID,
		Status:         summary.Status,
		Command:        summary.Command,
		Classification: summary.Classification,
		FailureSummary: summary.FailureSummary,
		PrimaryFile:    summary.PrimaryFile,
	}
}

func normalizeFeedbackCategoryCLI(category string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(category)) {
	case "", preflightapi.FeedbackCategoryOther:
		return preflightapi.FeedbackCategoryOther, nil
	case preflightapi.FeedbackCategoryBug:
		return preflightapi.FeedbackCategoryBug, nil
	case preflightapi.FeedbackCategoryFeature:
		return preflightapi.FeedbackCategoryFeature, nil
	case preflightapi.FeedbackCategoryBilling:
		return preflightapi.FeedbackCategoryBilling, nil
	case preflightapi.FeedbackCategoryOps:
		return preflightapi.FeedbackCategoryOps, nil
	default:
		return "", fmt.Errorf("invalid feedback category %q; use bug, feature, billing, ops, or other", category)
	}
}

func printFeedbackResponse(feedback preflightapi.FeedbackRecord) {
	fmt.Printf("feedback_id: %s\n", feedback.FeedbackID)
	fmt.Printf("support_status: %s\n", feedback.TicketStatus)
	if feedback.ExternalTicketID != "" {
		fmt.Printf("support_id: %s\n", feedback.ExternalTicketID)
	}
	if feedback.ExternalTicketURL != "" {
		fmt.Printf("support_url: %s\n", feedback.ExternalTicketURL)
	}
	if feedback.IncludeLogs {
		fmt.Printf("logs_included: %t\n", feedback.LogsIncluded)
	}
}
