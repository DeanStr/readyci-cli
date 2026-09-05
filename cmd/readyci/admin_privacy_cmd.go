package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func adminPrivacyCommand(args []string) error {
	if len(args) == 0 {
		printUsage()
		return cliConfigError("usage: readyci admin privacy <create|get|review|plan|export|complete|erase|provider-complete|restore-complete>", nil)
	}
	switch args[0] {
	case "create":
		return adminPrivacyCreateCommand(args[1:])
	case "get":
		return adminPrivacyGetCommand(args[1:])
	case "review":
		return adminPrivacyReviewCommand(args[1:])
	case "plan":
		return adminPrivacyPlanCommand(args[1:])
	case "export":
		return adminPrivacyExportCommand(args[1:])
	case "complete":
		return adminPrivacyCompleteCommand(args[1:])
	case "erase":
		return adminPrivacyEraseCommand(args[1:])
	case "provider-complete":
		return adminPrivacyProviderCompleteCommand(args[1:])
	case "restore-complete":
		return adminPrivacyRestoreCompleteCommand(args[1:])
	default:
		return cliConfigError(fmt.Sprintf("unknown admin privacy command %q", args[0]), nil)
	}
}

func adminPrivacyCreateCommand(args []string) error {
	fs := flag.NewFlagSet("admin privacy create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, token := adminAPIFlags(fs)
	requestType := fs.String("request-type", "", "access, erasure, or portability")
	userID := fs.String("user-id", "", "subject Ready CI user ID")
	controllerRole := fs.String("controller-role", "", "controller or processor")
	identityVerified := fs.Bool("identity-verified", false, "confirm requester identity verification")
	authorityVerified := fs.Bool("authority-verified", false, "confirm requester authority verification")
	dueAtRaw := fs.String("due-at", "", "case deadline in RFC3339 format")
	reason := fs.String("reason", "", "case intake reason")
	holdReason := fs.String("hold-reason", "", "optional legal or security hold")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci admin privacy create --request-type TYPE --user-id USER --controller-role ROLE --due-at RFC3339 --reason TEXT [--identity-verified] [--authority-verified]", nil)
	}
	dueAt, err := time.Parse(time.RFC3339, strings.TrimSpace(*dueAtRaw))
	if err != nil {
		return cliConfigError("readyci admin privacy create requires a valid --due-at RFC3339 value", err)
	}
	if strings.TrimSpace(*requestType) == "" || strings.TrimSpace(*userID) == "" || strings.TrimSpace(*controllerRole) == "" || strings.TrimSpace(*reason) == "" {
		return cliConfigError("readyci admin privacy create requires --request-type, --user-id, --controller-role, and --reason", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	result, err := api.NewClient(*apiURL, api.WithToken(*token)).AdminCreatePrivacyCase(ctx, preflightapi.AdminCreatePrivacyCaseRequest{
		RequestType: strings.TrimSpace(*requestType), SubjectUserID: strings.TrimSpace(*userID),
		ControllerRole: strings.TrimSpace(*controllerRole), IdentityVerified: *identityVerified,
		AuthorityVerified: *authorityVerified, DueAt: dueAt, Reason: strings.TrimSpace(*reason), HoldReason: strings.TrimSpace(*holdReason),
	})
	if err != nil {
		return err
	}
	return writeJSONOutput(result)
}

func adminPrivacyGetCommand(args []string) error {
	fs := flag.NewFlagSet("admin privacy get", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, token := adminAPIFlags(fs)
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci admin privacy get CASE_ID", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	result, err := api.NewClient(*apiURL, api.WithToken(*token)).AdminPrivacyCase(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	return writeJSONOutput(result)
}

func adminPrivacyReviewCommand(args []string) error {
	fs := flag.NewFlagSet("admin privacy review", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, token := adminAPIFlags(fs)
	identityVerified := fs.Bool("identity-verified", false, "mark requester identity verified")
	identityUnverified := fs.Bool("identity-unverified", false, "revoke requester identity verification")
	authorityVerified := fs.Bool("authority-verified", false, "mark requester authority verified")
	authorityUnverified := fs.Bool("authority-unverified", false, "revoke requester authority verification")
	dueAtRaw := fs.String("due-at", "", "replace the deadline with an RFC3339 timestamp")
	holdReason := fs.String("hold-reason", "", "replace the legal or security hold reason")
	clearHold := fs.Bool("clear-hold", false, "clear the current legal or security hold")
	reason := fs.String("reason", "", "review evidence reference and reason")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 || strings.TrimSpace(*reason) == "" {
		return cliConfigError("usage: readyci admin privacy review --reason TEXT [--identity-verified|--identity-unverified] [--authority-verified|--authority-unverified] [--due-at RFC3339] [--hold-reason TEXT|--clear-hold] CASE_ID", nil)
	}
	if (*identityVerified && *identityUnverified) || (*authorityVerified && *authorityUnverified) || (*clearHold && strings.TrimSpace(*holdReason) != "") {
		return cliConfigError("privacy review flags contain conflicting verification or hold changes", nil)
	}
	req := preflightapi.AdminReviewPrivacyCaseRequest{Reason: strings.TrimSpace(*reason)}
	if *identityVerified || *identityUnverified {
		value := *identityVerified
		req.IdentityVerified = &value
	}
	if *authorityVerified || *authorityUnverified {
		value := *authorityVerified
		req.AuthorityVerified = &value
	}
	if raw := strings.TrimSpace(*dueAtRaw); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return cliConfigError("readyci admin privacy review requires a valid --due-at RFC3339 value", err)
		}
		req.DueAt = &value
	}
	if *clearHold || strings.TrimSpace(*holdReason) != "" {
		value := strings.TrimSpace(*holdReason)
		req.HoldReason = &value
	}
	if req.IdentityVerified == nil && req.AuthorityVerified == nil && req.DueAt == nil && req.HoldReason == nil {
		return cliConfigError("readyci admin privacy review requires at least one verification, deadline, or hold change", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	result, err := api.NewClient(*apiURL, api.WithToken(*token)).AdminReviewPrivacyCase(ctx, fs.Arg(0), req)
	if err != nil {
		return err
	}
	return writeJSONOutput(result)
}

func adminPrivacyPlanCommand(args []string) error {
	fs := flag.NewFlagSet("admin privacy plan", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, token := adminAPIFlags(fs)
	reason := fs.String("reason", "", "dry-run reason")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 || strings.TrimSpace(*reason) == "" {
		return cliConfigError("usage: readyci admin privacy plan --reason TEXT CASE_ID", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	result, err := api.NewClient(*apiURL, api.WithToken(*token)).AdminPlanPrivacyCase(ctx, fs.Arg(0), strings.TrimSpace(*reason))
	if err != nil {
		return err
	}
	return writeJSONOutput(result)
}

func adminPrivacyExportCommand(args []string) error {
	fs := flag.NewFlagSet("admin privacy export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, token := adminAPIFlags(fs)
	ack := fs.Bool("ack-sensitive-output", false, "confirm stdout contains personal data and must be encrypted for delivery")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 || !*ack {
		return cliConfigError("usage: readyci admin privacy export --ack-sensitive-output CASE_ID", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	return downloadAdminPrivacyExport(ctx, api.NewClient(*apiURL, api.WithToken(*token)), fs.Arg(0), os.Stdout)
}

type adminPrivacyExportDownloader interface {
	DownloadAdminPrivacyExport(context.Context, string, io.Writer) error
}

func downloadAdminPrivacyExport(ctx context.Context, client adminPrivacyExportDownloader, caseID string, output io.Writer) error {
	temp, err := createSecurePrivacyExportSpool()
	if err != nil {
		return fmt.Errorf("create secure privacy export spool: %w", err)
	}
	defer temp.Close()

	if err := client.DownloadAdminPrivacyExport(ctx, caseID, temp); err != nil {
		return err
	}
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind privacy export spool: %w", err)
	}
	if err := validateDownloadedPrivacyExport(temp, caseID); err != nil {
		return fmt.Errorf("invalid privacy export response: %w", err)
	}
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind validated privacy export spool: %w", err)
	}
	if _, err := io.Copy(output, temp); err != nil {
		return fmt.Errorf("write privacy export: %w", err)
	}
	return nil
}

func validateDownloadedPrivacyExport(reader io.Reader, expectedCaseID string) error {
	decoder := json.NewDecoder(reader)
	var export preflightapi.AdminPrivacyExport
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&export); err != nil {
		return fmt.Errorf("decode privacy export package: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("response contains more than one JSON value")
		}
		return fmt.Errorf("decode privacy export package: %w", err)
	}
	if export.SchemaVersion != preflightapi.PrivacyExportSchemaVersion {
		return fmt.Errorf("schema_version = %q, want %q", export.SchemaVersion, preflightapi.PrivacyExportSchemaVersion)
	}
	if export.CaseID != strings.TrimSpace(expectedCaseID) {
		return fmt.Errorf("case_id = %q, want %q", export.CaseID, strings.TrimSpace(expectedCaseID))
	}
	if strings.TrimSpace(export.Digest) == "" {
		return fmt.Errorf("digest is required")
	}
	if err := preflightapi.ValidatePrivacyExportDigest(export); err != nil {
		return err
	}
	return nil
}

func adminPrivacyCompleteCommand(args []string) error {
	fs := flag.NewFlagSet("admin privacy complete", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, token := adminAPIFlags(fs)
	reason := fs.String("reason", "", "secure delivery evidence reference")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 || strings.TrimSpace(*reason) == "" {
		return cliConfigError("usage: readyci admin privacy complete --reason TEXT CASE_ID", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	result, err := api.NewClient(*apiURL, api.WithToken(*token)).AdminCompletePrivacyCase(ctx, fs.Arg(0), strings.TrimSpace(*reason))
	if err != nil {
		return err
	}
	return writeJSONOutput(result)
}

func adminPrivacyEraseCommand(args []string) error {
	fs := flag.NewFlagSet("admin privacy erase", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, token := adminAPIFlags(fs)
	planDigest := fs.String("plan-digest", "", "exact reviewed dry-run digest")
	backupExpiresRaw := fs.String("backup-expires-at", "", "backup expiry in RFC3339 format")
	reason := fs.String("reason", "", "erasure execution reason")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	backupExpiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(*backupExpiresRaw))
	if err != nil {
		return cliConfigError("readyci admin privacy erase requires a valid --backup-expires-at RFC3339 value", err)
	}
	if fs.NArg() != 1 || strings.TrimSpace(*planDigest) == "" || strings.TrimSpace(*reason) == "" {
		return cliConfigError("usage: readyci admin privacy erase --plan-digest SHA256 --backup-expires-at RFC3339 --reason TEXT CASE_ID", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	result, err := api.NewClient(*apiURL, api.WithToken(*token)).AdminExecutePrivacyErasure(ctx, fs.Arg(0), preflightapi.AdminExecutePrivacyErasureRequest{
		PlanDigest: strings.TrimSpace(*planDigest), BackupExpiresAt: backupExpiresAt, Reason: strings.TrimSpace(*reason),
	})
	if err != nil {
		return err
	}
	return writeJSONOutput(result)
}

func adminPrivacyProviderCompleteCommand(args []string) error {
	fs := flag.NewFlagSet("admin privacy provider-complete", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, token := adminAPIFlags(fs)
	reason := fs.String("reason", "", "provider confirmation evidence reference")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 2 || strings.TrimSpace(*reason) == "" {
		return cliConfigError("usage: readyci admin privacy provider-complete --reason TEXT CASE_ID PROVIDER", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	result, err := api.NewClient(*apiURL, api.WithToken(*token)).AdminCompletePrivacyProvider(ctx, fs.Arg(0), fs.Arg(1), strings.TrimSpace(*reason))
	if err != nil {
		return err
	}
	return writeJSONOutput(result)
}

func adminPrivacyRestoreCompleteCommand(args []string) error {
	fs := flag.NewFlagSet("admin privacy restore-complete", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, token := adminAPIFlags(fs)
	reason := fs.String("reason", "", "restore replay verification evidence reference")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 || strings.TrimSpace(*reason) == "" {
		return cliConfigError("usage: readyci admin privacy restore-complete --reason TEXT CASE_ID", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	result, err := api.NewClient(*apiURL, api.WithToken(*token)).AdminCompletePrivacyRestoreReplay(ctx, fs.Arg(0), strings.TrimSpace(*reason))
	if err != nil {
		return err
	}
	return writeJSONOutput(result)
}
