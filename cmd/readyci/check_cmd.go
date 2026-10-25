package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func checkCommand(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	changes := fs.String("changes", "", "changed-file mode override: working, staged, or all")
	networkMode := fs.String("network-mode", envDefault("READYCI_NETWORK_MODE", ""), "network mode override: none, default, or allowlist")
	networkAllowlist := fs.String("network-allowlist", envDefault("READYCI_NETWORK_ALLOWLIST", ""), "comma-separated hosts/IPs for --network-mode allowlist")
	region := fs.String("region", envDefault("READYCI_REGION", ""), "runner region/location override")
	timeoutOverride := fs.String("timeout", envDefault("READYCI_TIMEOUT", ""), "run timeout override, such as 20m, 1h, or seconds")
	docker := fs.Bool("docker", false, "request Docker/Testcontainers support inside the runner")
	keepFailed := fs.Bool("keep-failed", false, "retain failed workspace archive in the persistent cache")
	concurrencyGroup := fs.String("concurrency-group", "", "serialize runs in this per-project concurrency group")
	concurrencyLimit := fs.Int("concurrency-limit", 0, "max active runs in --concurrency-group; defaults to 1 when group is set")
	requestSecrets := fs.Bool("request-secrets", false, "request authorized project secrets for selected tests")
	skipWarm := fs.Bool("skip-warm", false, "skip automatic dependency preparation, including watch iterations")
	retryCount := fs.Int("retry", -1, "retry failed commands this many times")
	watch := fs.Bool("watch", false, "rerun the check when the git workspace changes")
	watchInterval := fs.Duration("watch-interval", 2*time.Second, "poll interval for --watch")
	dryRun := fs.Bool("dry-run", false, "preview the resolved check without creating a run")
	jsonOutput := fs.Bool("json", false, "print machine-readable selection or execution results")
	summaryOutput := fs.Bool("summary", false, "print a compact dry-run summary")
	why := fs.Bool("why", false, "explain why Ready CI selected or skipped checks")
	listChecks := fs.Bool("list", false, "list configured checks")
	openRun := fs.Bool("open", false, "open the dashboard run detail after creation")
	dashboardURL := fs.String("dashboard-url", envDefault("READYCI_DASHBOARD_URL", ""), "Ready CI dashboard URL for --open")
	var cachePaths stringSliceFlag
	fs.Var(&cachePaths, "cache-path", "workspace or home-relative path to back with the persistent cache; repeatable")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if flagProvided(fs, "retry") && *retryCount < 0 {
		return cliConfigError("retry count must be non-negative", nil)
	}
	if *summaryOutput {
		*dryRun = true
	}
	if *listChecks && *dryRun {
		return cliConfigError("--list and --dry-run cannot be combined", nil)
	}
	if *summaryOutput && *jsonOutput {
		return cliConfigError("--summary and --json cannot be combined", nil)
	}
	if *jsonOutput && *watch && !*dryRun {
		return cliConfigError("--json is not valid with --watch", nil)
	}

	loaded, err := loadProjectConfigOrInfer(".")
	if err != nil {
		return err
	}
	cfg := loaded.Config
	path := loaded.Path
	resolveOptions := checkResolveOptions{
		APIURL:                   *apiURL,
		ProjectID:                *projectID,
		Region:                   *region,
		Changes:                  *changes,
		NetworkMode:              *networkMode,
		NetworkAllowlist:         *networkAllowlist,
		Timeout:                  *timeoutOverride,
		Docker:                   *docker,
		DockerProvided:           flagProvided(fs, "docker"),
		KeepFailed:               *keepFailed,
		ConcurrencyGroup:         *concurrencyGroup,
		ConcurrencyGroupProvided: flagProvided(fs, "concurrency-group"),
		ConcurrencyLimit:         *concurrencyLimit,
		ConcurrencyLimitProvided: flagProvided(fs, "concurrency-limit"),
		RequestSecrets:           *requestSecrets,
		SkipWarm:                 *skipWarm,
		RetryCount:               *retryCount,
		RetryProvided:            flagProvided(fs, "retry"),
		Open:                     *openRun,
		DashboardURL:             *dashboardURL,
		CachePaths:               []string(cachePaths),
	}
	if *listChecks {
		if fs.NArg() != 0 {
			return cliConfigError("usage: readyci check --list [--json]", nil)
		}
		report := buildCheckListReport(cfg, path)
		if *jsonOutput {
			return writeJSONOutput(report)
		}
		printCheckListReport(report)
		return nil
	}
	if fs.NArg() > 1 {
		return cliConfigError("usage: readyci check [--api-url URL] [--dry-run] [--why] [--json] [name]", nil)
	}
	if fs.NArg() == 0 {
		executionOptions := checkExecutionOptions{
			APIURL:        *apiURL,
			APIToken:      *apiToken,
			DryRun:        *dryRun,
			JSONOutput:    *jsonOutput,
			SummaryOutput: *summaryOutput,
			Why:           *why,
			Inferred:      loaded.Inferred,
			Warnings:      loaded.Warnings,
			Signals:       verificationSignalsWithRepoMap(".", cfg, path, loaded.Signals, *why),
			Watch:         *watch,
			WatchInterval: *watchInterval,
		}
		if loaded.Inferred {
			return executeDefaultInferredCheck(".", loaded, resolveOptions, executionOptions)
		}
		if shouldUseAffectedRules(cfg, "") {
			executionOptions.SelectionMode = "affected_rules"
			executionOptions.SelectedReason = "Configured affected rules decide which checks match the changed files."
			printInferredConfigNotice(loaded, "affected checks")
			return executeAffectedRules(".", cfg, path, resolveOptions, executionOptions)
		}
		recommendation, err := defaultCheckSelection(cfg, "")
		if err != nil {
			return err
		}
		executionOptions.SelectionMode = "check"
		executionOptions.SelectedReason = recommendation.Reason
		printInferredConfigNotice(loaded, recommendation.Check)
		return executeResolvedCheck(recommendation.Check, cfg, path, resolveOptions, executionOptions)
	}
	name := fs.Arg(0)
	printInferredConfigNotice(loaded, name)
	return executeResolvedCheck(name, cfg, path, resolveOptions, checkExecutionOptions{
		APIURL:         *apiURL,
		APIToken:       *apiToken,
		DryRun:         *dryRun,
		JSONOutput:     *jsonOutput,
		SummaryOutput:  *summaryOutput,
		Why:            *why,
		Inferred:       loaded.Inferred,
		Warnings:       loaded.Warnings,
		Signals:        verificationSignalsWithRepoMap(".", cfg, path, loaded.Signals, *why),
		SelectionMode:  "explicit_check",
		SelectedReason: "Explicit check requested.",
		Watch:          *watch,
		WatchInterval:  *watchInterval,
	})
}

func affectedCommand(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "audit":
			return affectedAuditCommand(args[1:])
		case "explain":
			return affectedExplainCommand(args[1:])
		case "cargo":
			return affectedCargoCommand(args[1:])
		case "suggest":
			return affectedSuggestCommand(args[1:])
		}
	}
	return affectedLikeCommand(
		args,
	)
}

func warmCommand(args []string) error {
	fs := flag.NewFlagSet("warm", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	checkName := fs.String("check", "", "configured check to warm")
	warmProfile := fs.String("profile", "", "warm profile: dependencies, docker, or all")
	changes := fs.String("changes", "", "changed-file mode override: working, staged, or all")
	region := fs.String("region", envDefault("READYCI_REGION", ""), "runner region/location override")
	skipConfirm := fs.Bool("skip-confirm", false, "skip the post-warm run under configured network policy")
	dryRun := fs.Bool("dry-run", false, "preview the warm and confirm runs without creating runs")
	openRun := fs.Bool("open", false, "open the dashboard run detail after creation")
	dashboardURL := fs.String("dashboard-url", envDefault("READYCI_DASHBOARD_URL", ""), "Ready CI dashboard URL for --open")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 || (strings.TrimSpace(*checkName) == "" && strings.TrimSpace(*warmProfile) == "") {
		return cliConfigError("usage: readyci warm --check NAME|--profile dependencies|docker|all [--skip-confirm] [--dry-run]", nil)
	}
	if strings.TrimSpace(*checkName) != "" && strings.TrimSpace(*warmProfile) != "" {
		return cliConfigError("use either --check or --profile, not both", nil)
	}
	cfg, path, err := config.LoadProjectConfig(".")
	if err != nil {
		return err
	}
	if path == "" {
		return cliConfigError("readyci warm requires .readyci.yml or .readyci.yaml", nil)
	}
	token := ""
	if !*dryRun {
		token, err = effectiveAPIToken(*apiURL, *apiToken)
		if err != nil {
			return err
		}
	}
	baseResolve := checkResolveOptions{
		APIURL:       *apiURL,
		ProjectID:    *projectID,
		Region:       *region,
		Changes:      *changes,
		RetryCount:   -1,
		Open:         *openRun,
		DashboardURL: *dashboardURL,
	}
	execOptions := checkExecutionOptions{
		APIURL:     *apiURL,
		APIToken:   token,
		DryRun:     *dryRun,
		JSONOutput: false,
	}
	checks, err := resolveWarmCheckNames(context.Background(), cfg, path, baseResolve, strings.TrimSpace(*checkName), strings.TrimSpace(*warmProfile))
	if err != nil {
		return cliConfigError(err.Error(), err)
	}
	return executeWarmChecks(checks, cfg, path, baseResolve, execOptions, *skipConfirm)
}

func executeWarmCheck(name string, cfg config.ProjectConfig, path string, base checkResolveOptions, options checkExecutionOptions, skipConfirm bool) error {
	return executeWarmChecks([]string{name}, cfg, path, base, options, skipConfirm)
}

func executeWarmChecks(names []string, cfg config.ProjectConfig, path string, base checkResolveOptions, options checkExecutionOptions, skipConfirm bool) error {
	options.SelectionMode = "warm"
	prepared, err := prepareWarmChecks(context.Background(), names, cfg, path, base, skipConfirm)
	if err != nil {
		return err
	}
	if options.DryRun {
		for _, plan := range append(prepared.WarmPlans, prepared.Plans...) {
			if err := executeCheckPlan(plan, cfg, path, options); err != nil {
				return err
			}
		}
		return nil
	}
	return executeSessionPlans(prepared.Plans, prepared.WarmPlans, options.APIToken, options.JSONOutput, verificationPlan{Mode: "warm"})
}

func warmCheckNames(cfg config.ProjectConfig, checkName string, profile string) ([]string, error) {
	if checkName != "" {
		return []string{checkName}, nil
	}
	profile = strings.TrimSpace(profile)
	if profile == "dependencies" {
		var names []string
		for _, name := range sortedCheckNames(cfg.Checks) {
			if config.IsDependencyCheck(cfg, name) {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			return nil, fmt.Errorf("warm profile %q requires a configured dependencies check or dependency_check reference", profile)
		}
		return names, nil
	}
	var names []string
	for _, name := range sortedCheckNames(cfg.Checks) {
		check := cfg.Checks[name]
		switch profile {
		case "all":
			names = append(names, name)
		case "docker":
			if effectiveCheckDocker(cfg, name, check, checkResolveOptions{}) {
				names = append(names, name)
			}
		default:
			return nil, fmt.Errorf("unknown warm profile %q (available: dependencies, docker, all)", profile)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("warm profile %q did not match any configured checks", profile)
	}
	return names, nil
}

func effectiveCheckDocker(cfg config.ProjectConfig, name string, check config.CheckConfig, options checkResolveOptions) bool {
	resource := cfg.Resources.Default
	if checkResource, ok := cfg.Resources.Checks[name]; ok {
		resource = mergeResourceDefaults(resource, checkResource)
	}
	effectiveDocker := boolValue(resource.Docker)
	if check.Docker != nil {
		effectiveDocker = *check.Docker
	}
	if options.DockerProvided {
		effectiveDocker = options.Docker
	}
	return effectiveDocker
}

func affectedLikeCommand(args []string) error {
	commandName := "affected"
	fs := flag.NewFlagSet(commandName, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	checkName := fs.String("check", "", "configured check to run instead of auto-detecting the affected check")
	changes := fs.String("changes", "", "changed-file mode override: working, staged, or all")
	staged := fs.Bool("staged", false, "run the staged affected check")
	networkMode := fs.String("network-mode", envDefault("READYCI_NETWORK_MODE", ""), "network mode override: none, default, or allowlist")
	networkAllowlist := fs.String("network-allowlist", envDefault("READYCI_NETWORK_ALLOWLIST", ""), "comma-separated hosts/IPs for --network-mode allowlist")
	region := fs.String("region", envDefault("READYCI_REGION", ""), "runner region/location override")
	timeoutOverride := fs.String("timeout", envDefault("READYCI_TIMEOUT", ""), "run timeout override, such as 20m, 1h, or seconds")
	docker := fs.Bool("docker", false, "request Docker/Testcontainers support inside the runner")
	keepFailed := fs.Bool("keep-failed", false, "retain failed workspace archive in the persistent cache")
	concurrencyGroup := fs.String("concurrency-group", "", "serialize runs in this per-project concurrency group")
	concurrencyLimit := fs.Int("concurrency-limit", 0, "max active runs in --concurrency-group; defaults to 1 when group is set")
	requestSecrets := fs.Bool("request-secrets", false, "request authorized project secrets for selected tests")
	skipWarm := fs.Bool("skip-warm", false, "skip automatic dependency preparation, including watch iterations")
	retryCount := fs.Int("retry", -1, "retry failed commands this many times")
	watch := fs.Bool("watch", false, "rerun the affected check when the git workspace changes")
	watchInterval := fs.Duration("watch-interval", 2*time.Second, "poll interval for --watch")
	dryRun := fs.Bool("dry-run", false, "preview the resolved affected check without creating a run")
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	summaryOutput := fs.Bool("summary", false, "print a compact dry-run summary")
	openRun := fs.Bool("open", false, "open the dashboard run detail after creation")
	dashboardURL := fs.String("dashboard-url", envDefault("READYCI_DASHBOARD_URL", ""), "Ready CI dashboard URL for --open")
	var cachePaths stringSliceFlag
	fs.Var(&cachePaths, "cache-path", "workspace or home-relative path to back with the persistent cache; repeatable")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError(fmt.Sprintf("usage: readyci %s [--check NAME] [--staged] [--dry-run] [--json] [--watch]", commandName), nil)
	}
	if flagProvided(fs, "retry") && *retryCount < 0 {
		return cliConfigError("retry count must be non-negative", nil)
	}
	if *summaryOutput {
		*dryRun = true
	}
	if *summaryOutput && *jsonOutput {
		return cliConfigError("--summary and --json cannot be combined", nil)
	}
	if *jsonOutput && *watch && !*dryRun {
		return cliConfigError("--json is not valid with --watch", nil)
	}
	changeMode := strings.TrimSpace(*changes)
	if *staged {
		if flagProvided(fs, "changes") {
			return cliConfigError("use either --staged or --changes, not both", nil)
		}
		changeMode = syncbundle.ChangeModeStaged
	}

	loaded, err := loadProjectConfigOrInfer(".")
	if err != nil {
		return err
	}
	cfg := loaded.Config
	path := loaded.Path

	resolveOptions := checkResolveOptions{
		APIURL:                   *apiURL,
		ProjectID:                *projectID,
		Region:                   *region,
		Changes:                  changeMode,
		NetworkMode:              *networkMode,
		NetworkAllowlist:         *networkAllowlist,
		Timeout:                  *timeoutOverride,
		Docker:                   *docker,
		DockerProvided:           flagProvided(fs, "docker"),
		KeepFailed:               *keepFailed,
		ConcurrencyGroup:         *concurrencyGroup,
		ConcurrencyGroupProvided: flagProvided(fs, "concurrency-group"),
		ConcurrencyLimit:         *concurrencyLimit,
		ConcurrencyLimitProvided: flagProvided(fs, "concurrency-limit"),
		RequestSecrets:           *requestSecrets,
		SkipWarm:                 *skipWarm,
		RetryCount:               *retryCount,
		RetryProvided:            flagProvided(fs, "retry"),
		Open:                     *openRun,
		DashboardURL:             *dashboardURL,
		CachePaths:               []string(cachePaths),
	}
	executionOptions := checkExecutionOptions{
		APIURL:        *apiURL,
		APIToken:      *apiToken,
		DryRun:        *dryRun,
		JSONOutput:    *jsonOutput,
		SummaryOutput: *summaryOutput,
		Watch:         *watch,
		WatchInterval: *watchInterval,
		Inferred:      loaded.Inferred,
		Signals:       loaded.Signals,
		Warnings:      loaded.Warnings,
	}
	if shouldUseAffectedRules(cfg, *checkName) {
		printInferredConfigNotice(loaded, "affected checks")
		return executeAffectedRules(".", cfg, path, resolveOptions, executionOptions)
	}

	if strings.TrimSpace(*checkName) == "" {
		return cliConfigError("readyci affected requires affected.rules, affected.profiles, or check when.changes; use readyci check NAME to run a configured check", nil)
	}
	selectedCheck := strings.TrimSpace(*checkName)
	printInferredConfigNotice(loaded, selectedCheck)
	return executeResolvedCheck(selectedCheck, cfg, path, resolveOptions, executionOptions)
}
