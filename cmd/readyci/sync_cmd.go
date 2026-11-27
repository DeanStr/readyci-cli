package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

type syncDryRunOptions struct {
	CheckName        string
	ChangeMode       string
	RegisteredClient *api.Client
	ProjectID        string
}

type syncDryRunReport struct {
	SnapshotDigest        string                       `json:"snapshot_digest"`
	Root                  string                       `json:"root"`
	ConfigPath            string                       `json:"config_path,omitempty"`
	CheckName             string                       `json:"check,omitempty"`
	ChangeMode            string                       `json:"change_mode"`
	IncludeUntracked      bool                         `json:"include_untracked"`
	ExcludePatterns       []string                     `json:"exclude_patterns,omitempty"`
	ReviewedFixtures      []syncbundle.ReviewedFixture `json:"reviewed_fixtures,omitempty"`
	Git                   syncbundle.GitMetadata       `json:"git"`
	CompressedBytes       int64                        `json:"compressed_bytes"`
	ExpandedBytes         int64                        `json:"expanded_bytes"`
	ManifestBytes         int64                        `json:"manifest_bytes"`
	EntryCount            int                          `json:"entry_count"`
	AdmissionLimits       syncbundle.AdmissionLimits   `json:"admission_limits"`
	BaselineCount         int                          `json:"baseline_count"`
	WorkspaceCount        int                          `json:"workspace_count"`
	DeletedCount          int                          `json:"deleted_count"`
	ChangedFilesCount     int                          `json:"changed_files_count"`
	OmittedUntrackedFiles []string                     `json:"omitted_untracked_files,omitempty"`
	OmittedTrackedFiles   []string                     `json:"omitted_tracked_files,omitempty"`
	UntrackedCount        int                          `json:"untracked_count"`
	BaselineFiles         []string                     `json:"baseline_files,omitempty"`
	WorkspaceFiles        []string                     `json:"workspace_files,omitempty"`
	DeletedFiles          []string                     `json:"deleted_files,omitempty"`
	ChangedFiles          []string                     `json:"changed_files,omitempty"`
	UntrackedFiles        []string                     `json:"untracked_files,omitempty"`
	Warnings              []string                     `json:"warnings,omitempty"`
}

func syncCommand(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id for the registered policy")
	registered := fs.Bool("registered", false, "preview the named check using its active registered source policy")
	dryRun := fs.Bool("dry-run", false, "preview the workspace bundle without creating a run")
	checkName := fs.String("check", "", "named check whose sync settings should be previewed")
	changes := fs.String("changes", "", "changed-file mode override: working, staged, or all")
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 || !*dryRun {
		return cliConfigError("usage: readyci sync --dry-run [--check NAME] [--registered] [--project PROJECT] [--changes MODE] [--json]", nil)
	}

	cfg, path, err := config.LoadProjectConfig(".")
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	options := syncDryRunOptions{CheckName: *checkName, ChangeMode: *changes}
	if *registered {
		if strings.TrimSpace(*checkName) == "" {
			return cliConfigError("--registered requires --check NAME", nil)
		}
		options.ProjectID, err = effectiveProjectID(*apiURL, *projectID)
		if err != nil {
			return err
		}
		options.RegisteredClient, err = authenticatedClient(*apiURL, *apiToken)
		if err != nil {
			return err
		}
	}
	report, err := buildSyncDryRunReport(ctx, ".", cfg, path, options)
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	printSyncDryRunReport(report)
	return nil
}

func buildSyncDryRunReport(ctx context.Context, cwd string, cfg config.ProjectConfig, configPath string, options syncDryRunOptions) (syncDryRunReport, error) {
	changeMode := strings.TrimSpace(options.ChangeMode)
	checkName := strings.TrimSpace(options.CheckName)
	syncConfig := cfg.Sync
	if checkName != "" {
		if configPath == "" && options.RegisteredClient == nil {
			return syncDryRunReport{}, cliConfigError("--check requires .readyci.yml", nil)
		}
		check, ok := cfg.Checks[checkName]
		if !ok && options.RegisteredClient == nil {
			return syncDryRunReport{}, cliConfigError(fmt.Sprintf("unknown check %q (available: %s)", checkName, strings.Join(sortedCheckNames(cfg.Checks), ", ")), nil)
		}
		if changeMode == "" {
			changeMode = check.Changes
		}
		syncConfig = effectiveCheckSyncConfig(cfg.Sync, check.Sync)
	}
	normalizedChangeMode, err := syncbundle.NormalizeChangeMode(changeMode)
	if err != nil {
		return syncDryRunReport{}, cliConfigError(err.Error(), err)
	}
	buildOptions := syncbundlebuild.BuildOptions{
		ChangeMode:       normalizedChangeMode,
		ExcludePatterns:  syncConfig.Exclude,
		IncludeUntracked: syncConfig.IncludeUntracked,
		ReviewedFixtures: syncConfig.ReviewedFixtures,
	}
	sessionViewDigest := ""
	if options.RegisteredClient != nil {
		policy, err := options.RegisteredClient.ProjectVerificationSourcePolicy(ctx, options.ProjectID, normalizedChangeMode, checkName)
		if err != nil {
			return syncDryRunReport{}, err
		}
		if policy.Active == nil || policy.CheckSource == nil {
			return syncDryRunReport{}, cliConfigError("active registered check source policy is required", nil)
		}
		if sessionViewDigest, err = registeredSessionViewDigest(ctx, cwd, buildOptions, *policy.Active, *policy.CheckSource); err != nil {
			return syncDryRunReport{}, err
		}
		buildOptions, err = registeredPolicyBuildOptions(buildOptions, policy.CheckSource)
		if err != nil {
			return syncDryRunReport{}, err
		}
	}
	includeUntracked := normalizedChangeMode != syncbundle.ChangeModeStaged && (buildOptions.IncludeUntracked == nil || *buildOptions.IncludeUntracked)
	bundlePath, result, err := syncbundlebuild.BuildToTempWithOptions(ctx, cwd, buildOptions)
	if err != nil {
		return syncDryRunReport{}, syncBuildError(err)
	}
	defer os.Remove(bundlePath)
	if sessionViewDigest != "" && result.Manifest.SnapshotDigest != sessionViewDigest {
		return syncDryRunReport{}, fmt.Errorf("check preview digest %s differs from the session's view %s of the same workspace", result.Manifest.SnapshotDigest, sessionViewDigest)
	}
	expandedBytes, err := decompressedBundleBytes(bundlePath)
	if err != nil {
		return syncDryRunReport{}, err
	}
	usage, err := syncbundle.ManifestAdmission(result.Manifest)
	if err != nil {
		return syncDryRunReport{}, cliConfigError(err.Error(), err)
	}
	report := syncDryRunReport{
		SnapshotDigest:        result.Manifest.SnapshotDigest,
		Root:                  result.Root,
		ConfigPath:            configPath,
		CheckName:             checkName,
		ChangeMode:            result.Manifest.ChangeMode,
		IncludeUntracked:      includeUntracked,
		ExcludePatterns:       buildOptions.ExcludePatterns,
		ReviewedFixtures:      append([]syncbundle.ReviewedFixture(nil), result.Manifest.ReviewedFixtures...),
		Git:                   result.Manifest.Git,
		CompressedBytes:       result.Size,
		ExpandedBytes:         expandedBytes,
		ManifestBytes:         usage.ManifestBytes,
		EntryCount:            usage.Entries,
		AdmissionLimits:       syncbundle.SourceAdmissionLimits(),
		BaselineCount:         len(result.Manifest.Baseline),
		WorkspaceCount:        len(result.Manifest.Workspace),
		DeletedCount:          len(result.Manifest.Deleted),
		ChangedFilesCount:     len(result.Manifest.ChangedFiles),
		UntrackedCount:        len(result.UntrackedFiles),
		OmittedUntrackedFiles: result.OmittedUntrackedFiles,
		OmittedTrackedFiles:   result.OmittedTrackedFiles,
		BaselineFiles:         entryPaths(result.Manifest.Baseline),
		WorkspaceFiles:        entryPaths(result.Manifest.Workspace),
		DeletedFiles:          append([]string(nil), result.Manifest.Deleted...),
		ChangedFiles:          append([]string(nil), result.Manifest.ChangedFiles...),
		UntrackedFiles:        append([]string(nil), result.UntrackedFiles...),
	}
	report.Warnings = syncDryRunWarnings(report, result.Manifest)
	return report, nil
}

func decompressedBundleBytes(bundlePath string) (int64, error) {
	file, err := os.Open(bundlePath)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return 0, err
	}
	defer reader.Close()
	return io.Copy(io.Discard, reader)
}

// registeredSessionViewDigest builds the combined source a session uploads
// under the active policy and returns the digest of the check's view of it, so
// a preview fails wherever that upload would.
func registeredSessionViewDigest(ctx context.Context, cwd string, base syncbundlebuild.BuildOptions, active, check syncbundle.SourcePolicyBinding) (string, error) {
	options, err := registeredPolicyBuildOptions(base, &active)
	if err != nil {
		return "", err
	}
	options.SealSourceViews = true
	built, err := syncbundlebuild.BuildWithOptions(ctx, cwd, io.Discard, options)
	if err != nil {
		return "", syncBuildError(err)
	}
	if len(options.SourceScopes) == 0 {
		return built.Manifest.SnapshotDigest, nil
	}
	check, err = syncbundle.NormalizeSourcePolicyBinding(check)
	if err != nil {
		return "", fmt.Errorf("invalid registered check source policy: %w", err)
	}
	view, ok := built.SourceViews[syncbundle.SourcePolicyScopeKey(check)]
	if !ok {
		return "", errors.New("registered check source is not a scope of the active policy")
	}
	return view.SnapshotDigest, nil
}

func syncBuildError(err error) error {
	var admission *syncbundle.AdmissionError
	if errors.As(err, &admission) {
		return cliConfigError(err.Error(), err)
	}
	return err
}

func printSyncDryRunReport(report syncDryRunReport) {
	fmt.Println("sync: dry-run")
	fmt.Printf("root: %s\n", report.Root)
	if report.ConfigPath != "" {
		fmt.Printf("config: %s\n", report.ConfigPath)
	}
	if report.CheckName != "" {
		fmt.Printf("check: %s\n", report.CheckName)
	}
	fmt.Printf("change_mode: %s\n", report.ChangeMode)
	fmt.Printf("snapshot_digest: %s\n", report.SnapshotDigest)
	fmt.Printf("include_untracked: %t\n", report.IncludeUntracked)
	fmt.Printf("compressed_bytes: %d\n", report.CompressedBytes)
	fmt.Printf("expanded_bytes: %d\n", report.ExpandedBytes)
	fmt.Printf("manifest_bytes: %d\n", report.ManifestBytes)
	fmt.Printf("bundle_entries: %d\n", report.EntryCount)
	fmt.Printf("baseline_entries: %d\n", report.BaselineCount)
	fmt.Printf("workspace_entries: %d\n", report.WorkspaceCount)
	fmt.Printf("deleted_entries: %d\n", report.DeletedCount)
	fmt.Printf("changed_files: %d\n", report.ChangedFilesCount)
	fmt.Printf("untracked_files: %d\n", report.UntrackedCount)
	printPathSample("changed_file", report.ChangedFiles, 20)
	printPathSample("untracked_file", report.UntrackedFiles, 20)
	printPathSample("omitted_untracked_file", report.OmittedUntrackedFiles, 20)
	printPathSample("omitted_tracked_file", report.OmittedTrackedFiles, 20)
	printPathSample("workspace_file", report.WorkspaceFiles, 20)
	if len(report.Warnings) > 0 {
		fmt.Println("warnings:")
		for _, warning := range report.Warnings {
			fmt.Printf("  %s\n", warning)
		}
	}
}

func printPathSample(label string, paths []string, limit int) {
	if len(paths) == 0 {
		return
	}
	for i, path := range paths {
		if i >= limit {
			fmt.Printf("%s: ... %d more\n", label, len(paths)-limit)
			return
		}
		fmt.Printf("%s: %s\n", label, path)
	}
}

func entryPaths(entries []syncbundle.Entry) []string {
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.Path)
	}
	sort.Strings(paths)
	return paths
}

func syncDryRunWarnings(report syncDryRunReport, manifest syncbundle.Manifest) []string {
	var warnings []string
	if report.CompressedBytes > syncbundle.MaxCompressedBundleBytes {
		warnings = append(warnings, fmt.Sprintf("compressed bundle exceeds API limit (%d > %d bytes)", report.CompressedBytes, syncbundle.MaxCompressedBundleBytes))
	}
	if report.ExpandedBytes > syncbundle.MaxExpandedBundleBytes {
		warnings = append(warnings, fmt.Sprintf("expanded bundle exceeds API limit (%d > %d bytes)", report.ExpandedBytes, syncbundle.MaxExpandedBundleBytes))
	}
	if report.UntrackedCount > 0 {
		warnings = append(warnings, formatUntrackedUploadWarning(report.UntrackedFiles))
	}
	// An untracked secret-like file is new to the upload and gets its own line.
	// Committed ones are already part of the repository: naming each on every
	// upload buries the lines that matter, so they share one.
	untracked := make(map[string]struct{}, len(report.UntrackedFiles))
	for _, path := range report.UntrackedFiles {
		untracked[path] = struct{}{}
	}
	const untrackedSecretLimit = 10
	untrackedSecretTotal := 0
	var trackedSecrets []string
	seen := make(map[string]struct{})
	for _, entry := range append(append([]syncbundle.Entry{}, manifest.Baseline...), manifest.Workspace...) {
		if _, duplicate := seen[entry.Path]; !duplicate && secretLikeSyncPath(entry.Path) {
			seen[entry.Path] = struct{}{}
			if _, ok := untracked[entry.Path]; !ok {
				trackedSecrets = append(trackedSecrets, entry.Path)
			} else {
				untrackedSecretTotal++
				if untrackedSecretTotal <= untrackedSecretLimit {
					warnings = append(warnings, "secret-like untracked path included: "+entry.Path)
				}
			}
		}
		if entry.Size > 25<<20 {
			warnings = append(warnings, fmt.Sprintf("large file included: %s (%d bytes)", entry.Path, entry.Size))
		}
	}
	if untrackedSecretTotal > untrackedSecretLimit {
		warnings = append(warnings, fmt.Sprintf("%d additional secret-like untracked path(s) omitted from warnings", untrackedSecretTotal-untrackedSecretLimit))
	}
	if warning := formatTrackedSecretWarning(trackedSecrets); warning != "" {
		warnings = append(warnings, warning)
	}
	return warnings
}

func formatTrackedSecretWarning(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	sort.Strings(paths)
	const sampleLimit = 5
	if len(paths) > sampleLimit {
		return fmt.Sprintf("%d tracked secret-like paths included (first %d: %s)", len(paths), sampleLimit, strings.Join(paths[:sampleLimit], ", "))
	}
	if len(paths) == 1 {
		return "1 tracked secret-like path included (" + paths[0] + ")"
	}
	return fmt.Sprintf("%d tracked secret-like paths included (%s)", len(paths), strings.Join(paths, ", "))
}

func secretLikeSyncPath(value string) bool {
	base := strings.ToLower(filepath.Base(value))
	if base == ".env.example" || strings.HasSuffix(base, ".env.example") || base == ".npmrc.example" || strings.HasSuffix(base, ".npmrc.example") {
		return false
	}
	switch base {
	case ".env", ".envrc", ".npmrc", ".npmrc.local", ".pypirc", ".netrc", "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", "credentials.json", "service-account.json", "application_default_credentials.json", ".git-credentials", "kubeconfig":
		return true
	default:
		return strings.HasPrefix(base, ".env") || strings.HasSuffix(base, ".pem") || strings.Contains("/"+filepath.ToSlash(value), "/.ssh/") || strings.HasSuffix("/"+filepath.ToSlash(value), "/.aws/credentials") || strings.HasSuffix("/"+filepath.ToSlash(value), "/.docker/config.json")
	}
}

func formatUntrackedUploadWarning(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	const sampleLimit = 5
	sampleSize := len(paths)
	if sampleSize > sampleLimit {
		sampleSize = sampleLimit
	}
	sample := strings.Join(paths[:sampleSize], ", ")
	if len(paths) > sampleSize {
		return fmt.Sprintf("untracked files included in upload (%d total; first %d: %s); run `readyci sync --dry-run` to preview or set sync.include_untracked: false", len(paths), sampleSize, sample)
	}
	return fmt.Sprintf("untracked files included in upload (%d total: %s); run `readyci sync --dry-run` to preview or set sync.include_untracked: false", len(paths), sample)
}
