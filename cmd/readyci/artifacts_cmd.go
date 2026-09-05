package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/internal/artifactapply"
	"github.com/DeanStr/readyci-cli/pkg/artifactbundle"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func artifactsCommand(args []string) error {
	if len(args) == 0 {
		printUsage()
		return cliConfigError("usage: readyci artifacts <ls|download|apply> RUN_ID", nil)
	}
	switch args[0] {
	case "ls":
		return artifactsListCommand(args[1:])
	case "download":
		return artifactsDownloadCommand(args[1:])
	case "apply":
		return artifactsApplyCommand(args[1:])
	default:
		printUsage()
		return cliConfigError(fmt.Sprintf("unknown artifacts command %q", args[0]), nil)
	}
}

func artifactsListCommand(args []string) error {
	fs := flag.NewFlagSet("artifacts ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci artifacts ls [--api-url URL] [--token TOKEN] RUN_ID", nil)
	}

	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	token, err := effectiveAPIToken(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	resp, err := api.NewClient(*apiURL, api.WithToken(token)).ListArtifacts(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	if len(resp.Artifacts) == 0 {
		fmt.Printf("no artifacts for run %s\n", resp.RunID)
		return nil
	}
	fmt.Printf("artifacts for run %s\n", resp.RunID)
	for _, artifact := range resp.Artifacts {
		fmt.Printf("%s %s size_bytes=%d expires_at=%s\n",
			artifact.ID, artifact.Name, artifact.SizeBytes, artifact.ExpiresAt.Format(time.RFC3339))
	}
	return nil
}

func artifactsDownloadCommand(args []string) error {
	fs := flag.NewFlagSet("artifacts download", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	output := fs.String("output", "", "output tar.gz path")
	force := fs.Bool("force", false, "overwrite an existing output file")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci artifacts download [--api-url URL] [--token TOKEN] [--output FILE] [--force] RUN_ID", nil)
	}
	return downloadArtifactsToFile(artifactDownloadOptions{
		APIURL:   *apiURL,
		APIToken: *apiToken,
		RunID:    fs.Arg(0),
		Output:   *output,
		Force:    *force,
	})
}

type artifactDownloadOptions struct {
	APIURL   string
	APIToken string
	RunID    string
	Output   string
	Force    bool
}

func downloadArtifactsToFile(options artifactDownloadOptions) error {
	outputPath := options.Output
	if outputPath == "" {
		outputPath = "readyci-artifacts-" + options.RunID + ".tar.gz"
	}
	outputMode := os.FileMode(0o644)
	if options.Force {
		if info, err := os.Stat(outputPath); err == nil {
			outputMode = info.Mode().Perm()
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else {
		if _, err := os.Stat(outputPath); err == nil {
			return fmt.Errorf("%s already exists; use --force to overwrite it", outputPath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	tmpDir := filepath.Dir(outputPath)
	tmpBase := filepath.Base(outputPath)
	file, err := os.CreateTemp(tmpDir, "."+tmpBase+"-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := file.Name()
	closed := false
	removeTemp := true
	defer func() {
		if !closed {
			_ = file.Close()
		}
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	token, err := effectiveAPIToken(options.APIURL, options.APIToken)
	if err != nil {
		return err
	}
	if err := api.NewClient(options.APIURL, api.WithToken(token)).DownloadArtifactArchive(ctx, options.RunID, file); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	closed = true
	if err := os.Chmod(tmpPath, outputMode); err != nil {
		return err
	}
	if options.Force {
		if err := os.Rename(tmpPath, outputPath); err != nil {
			return err
		}
		removeTemp = false
	} else if err := os.Link(tmpPath, outputPath); err != nil {
		return err
	}
	fmt.Printf("downloaded artifacts to %s\n", outputPath)
	return nil
}

type artifactApplyOptions struct {
	APIURL        string
	APIToken      string
	RunID         string
	Paths         []string
	ContentSHA256 string
	DryRun        bool
	JSONOutput    bool
	AllowEmpty    bool
}

func artifactsApplyCommand(args []string) error {
	options, err := parseArtifactApplyArgs(args)
	if err != nil {
		return err
	}
	return applyArtifactsFromRun(options)
}

type artifactApplyResult struct {
	RunID         string                `json:"run_id"`
	ContentSHA256 string                `json:"content_sha256"`
	DryRun        bool                  `json:"dry_run"`
	AllowEmpty    bool                  `json:"allow_empty"`
	MatchedCount  int                   `json:"matched_count"`
	AppliedCount  int                   `json:"applied_count"`
	Files         []artifactbundle.File `json:"files"`
}

func parseArtifactApplyArgs(args []string) (artifactApplyOptions, error) {
	options := artifactApplyOptions{
		APIURL:   envDefault("READYCI_API_URL", defaultAPIURL()),
		APIToken: envDefault("READYCI_API_TOKEN", ""),
	}
	collectingPaths := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--dry-run":
			collectingPaths = false
			options.DryRun = true
		case arg == "--json":
			collectingPaths = false
			options.JSONOutput = true
		case arg == "--allow-empty":
			collectingPaths = false
			options.AllowEmpty = true
		case arg == "--api-url" || arg == "--token" || arg == "--paths" || arg == "--content-sha256":
			if i+1 >= len(args) {
				return artifactApplyOptions{}, cliConfigError(fmt.Sprintf("%s requires a value", arg), nil)
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
			return artifactApplyOptions{}, cliConfigError("usage: readyci artifacts apply RUN_ID --paths PATTERN [--paths PATTERN] [--dry-run] [--json] [--allow-empty]", nil)
		case strings.HasPrefix(arg, "-"):
			return artifactApplyOptions{}, cliConfigError(fmt.Sprintf("unknown artifacts apply flag %q", arg), nil)
		default:
			if options.RunID != "" {
				if collectingPaths {
					options.Paths = append(options.Paths, splitCSV(arg)...)
					continue
				}
				return artifactApplyOptions{}, cliConfigError("usage: readyci artifacts apply RUN_ID --paths PATTERN [--paths PATTERN] [--dry-run] [--json] [--allow-empty]", nil)
			}
			options.RunID = arg
			collectingPaths = false
		}
	}
	if strings.TrimSpace(options.RunID) == "" {
		return artifactApplyOptions{}, cliConfigError("usage: readyci artifacts apply RUN_ID --paths PATTERN [--paths PATTERN] [--dry-run] [--json] [--allow-empty]", nil)
	}
	if len(options.Paths) == 0 {
		return artifactApplyOptions{}, cliConfigError("artifacts apply requires at least one --paths pattern", nil)
	}
	if options.ContentSHA256 != "" {
		if err := preflightapi.ValidateSHA256Hex(options.ContentSHA256); err != nil {
			return artifactApplyOptions{}, cliConfigError("invalid --content-sha256: "+err.Error(), err)
		}
	}
	return options, nil
}

func applyArtifactsFromRun(options artifactApplyOptions) error {
	result, err := buildArtifactApplyResult(options)
	if err != nil {
		return err
	}
	if options.JSONOutput {
		return writeJSONOutput(result)
	}

	action := "applied"
	if options.DryRun {
		action = "would apply"
	}
	if result.MatchedCount == 0 {
		fmt.Printf("%s 0 artifact files from run %s\n", action, options.RunID)
		return nil
	}
	fmt.Printf("%s %d artifact file(s) from run %s\n", action, result.MatchedCount, options.RunID)
	for _, file := range result.Files {
		fmt.Printf("%s size_bytes=%d\n", file.Path, file.SizeBytes)
	}
	return nil
}

func buildArtifactApplyResult(options artifactApplyOptions) (artifactApplyResult, error) {
	tmp, err := os.CreateTemp("", ".readyci-artifacts-*.tar.gz")
	if err != nil {
		return artifactApplyResult{}, err
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpPath)
	}()

	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	token, err := effectiveAPIToken(options.APIURL, options.APIToken)
	if err != nil {
		return artifactApplyResult{}, err
	}
	if err := api.NewClient(options.APIURL, api.WithToken(token)).DownloadArtifactArchive(ctx, options.RunID, tmp); err != nil {
		return artifactApplyResult{}, err
	}
	if _, err := tmp.Seek(0, 0); err != nil {
		return artifactApplyResult{}, err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, tmp); err != nil {
		return artifactApplyResult{}, fmt.Errorf("hash downloaded artifact archive: %w", err)
	}
	contentSHA256 := fmt.Sprintf("%x", hash.Sum(nil))
	if options.ContentSHA256 != "" {
		if err := preflightapi.ValidateSHA256Hex(options.ContentSHA256); err != nil {
			return artifactApplyResult{}, fmt.Errorf("invalid expected artifact content checksum: %w", err)
		}
		if contentSHA256 != options.ContentSHA256 {
			return artifactApplyResult{}, fmt.Errorf(
				"downloaded artifact content checksum mismatch: expected=%s actual=%s",
				options.ContentSHA256,
				contentSHA256,
			)
		}
	}
	if _, err := tmp.Seek(0, 0); err != nil {
		return artifactApplyResult{}, err
	}
	workspaceRoot, err := gitOutput(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return artifactApplyResult{}, fmt.Errorf("artifacts apply must be run inside a git worktree: %w", err)
	}
	manifest, err := artifactapply.Apply(ctx, workspaceRoot, options.Paths, tmp, artifactapply.ApplyOptions{DryRun: options.DryRun})
	if err != nil {
		return artifactApplyResult{}, err
	}
	if err := tmp.Close(); err != nil {
		return artifactApplyResult{}, err
	}
	closed = true

	result := artifactApplyResult{
		RunID:         options.RunID,
		ContentSHA256: contentSHA256,
		DryRun:        options.DryRun,
		AllowEmpty:    options.AllowEmpty,
		MatchedCount:  len(manifest.Files),
		Files:         append([]artifactbundle.File{}, manifest.Files...),
	}
	if !options.DryRun {
		result.AppliedCount = len(manifest.Files)
	}
	if len(manifest.Files) == 0 && !options.AllowEmpty {
		return result, fmt.Errorf("no artifact files matched --paths for run %s; use --allow-empty to permit zero matches", options.RunID)
	}
	return result, nil
}
