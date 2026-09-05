package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

var (
	version              = "0.1.0-dev"
	releaseDefaultAPIURL = ""
)

const (
	localDefaultAPIURL       = "http://127.0.0.1:8080"
	publicDefaultAPIURL      = "https://api.readyci.com"
	defaultRunTimeoutSeconds = 20 * 60
)

func defaultAPIURL() string {
	if value := strings.TrimSpace(releaseDefaultAPIURL); value != "" {
		return value
	}
	if strings.Contains(strings.ToLower(version), "dev") {
		return localDefaultAPIURL
	}
	return publicDefaultAPIURL
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		var exitErr exitCodeError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.code)
		}
		os.Exit(renderCLIError(os.Args[1:], err))
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printUsage()
		return nil
	}

	switch args[0] {
	case "start":
		return startCommand(args[1:])
	case "init":
		return initCommand(args[1:])
	case "setup":
		return setupCommand(args[1:])
	case "login":
		return loginCommand(args[1:])
	case "auth":
		return authCommand(args[1:])
	case "config":
		return configCommand(args[1:])
	case "plan":
		return planCommand(args[1:])
	case "import":
		return importCommand(args[1:])
	case "sync":
		return syncCommand(args[1:])
	case "doctor":
		return doctorCommand(args[1:])
	case "usage":
		return usageCommand(args[1:])
	case "feedback":
		return feedbackCommand(args[1:])
	case "project":
		return projectCommand(args[1:])
	case "secrets":
		return secretsCommand(args[1:])
	case "run":
		return runCommand(args[1:])
	case "check":
		return checkCommand(args[1:])
	case "affected":
		return affectedCommand(args[1:])
	case "touched":
		return touchedCommand(args[1:])
	case "warm":
		return warmCommand(args[1:])
	case "runs":
		return runsCommand(args[1:])
	case "groups":
		return groupsCommand(args[1:])
	case "verify":
		return verificationCommand(args[1:])
	case "sessions":
		return sessionsCommand(args[1:])
	case "agent":
		return agentCommand(args[1:])
	case "artifacts":
		return artifactsCommand(args[1:])
	case "fixtures":
		return fixturesCommand(args[1:])
	case "cache":
		return cacheCommand(args[1:])
	case "admin":
		return adminCommand(args[1:])
	case "version":
		fmt.Println(version)
		return nil
	default:
		printUsage()
		return cliConfigError(fmt.Sprintf("unknown command %q", args[0]), nil)
	}
}

func runCommand(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	region := fs.String("region", envDefault("READYCI_REGION", ""), "runner region/location")
	runnerSizeOverride := fs.String("runner-size", envDefault("READYCI_RUNNER_SIZE", ""), "runner size: small, standard, large, or xlarge")
	timeoutOverride := fs.String("timeout", envDefault("READYCI_TIMEOUT", ""), "run timeout, such as 20m, 1h, or seconds")
	changes := fs.String("changes", "working", "changed-file mode: working, staged, or all")
	networkMode := fs.String("network-mode", envDefault("READYCI_NETWORK_MODE", ""), "network mode: none, default, or allowlist")
	networkAllowlist := fs.String("network-allowlist", envDefault("READYCI_NETWORK_ALLOWLIST", ""), "comma-separated hosts/IPs for --network-mode allowlist")
	docker := fs.Bool("docker", false, "request Docker/Testcontainers support inside the runner")
	keepFailed := fs.Bool("keep-failed", false, "retain failed workspace archive in the persistent cache")
	concurrencyGroup := fs.String("concurrency-group", "", "serialize runs in this per-project concurrency group")
	concurrencyLimit := fs.Int("concurrency-limit", 0, "max active runs in --concurrency-group; defaults to 1 when group is set")
	retryCount := fs.Int("retry", 0, "retry failed commands this many times")
	detach := fs.Bool("detach", false, "create and upload the run without waiting for completion")
	openRun := fs.Bool("open", false, "open the dashboard run detail after creation")
	dashboardURL := fs.String("dashboard-url", envDefault("READYCI_DASHBOARD_URL", ""), "Ready CI dashboard URL for --open")
	var cachePaths stringSliceFlag
	fs.Var(&cachePaths, "cache-path", "workspace or home-relative path to back with the persistent cache; repeatable")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() == 0 {
		return cliConfigError("usage: readyci run [--api-url URL] [--runner-size SIZE] [--timeout DURATION] [--detach] <command>", nil)
	}
	if *detach && *retryCount != 0 {
		return cliConfigError("--detach cannot be combined with --retry", nil)
	}
	cfg, _, err := config.LoadProjectConfig(".")
	if err != nil {
		return err
	}
	if *projectID == "" {
		*projectID = cfg.ProjectID
	}
	effectiveRegion := *region
	if effectiveRegion == "" {
		effectiveRegion = cfg.Region
	}
	if err := preflightapi.ValidateRegion(effectiveRegion); err != nil {
		return cliConfigError(err.Error(), err)
	}
	effectiveNetworkMode := *networkMode
	if effectiveNetworkMode == "" {
		effectiveNetworkMode = cfg.Network.Mode
	}
	if effectiveNetworkMode == "" {
		effectiveNetworkMode = preflightapi.DefaultNetworkMode
	}
	if err := validateNetworkMode(effectiveNetworkMode); err != nil {
		return cliConfigError(err.Error(), err)
	}
	effectiveDocker := boolValue(cfg.Resources.Default.Docker)
	if flagProvided(fs, "docker") {
		effectiveDocker = *docker
	}
	networkModeExplicit := flagProvided(fs, "network-mode") || *networkMode != "" || cfg.Network.Mode != ""
	effectiveNetworkMode, err = effectiveRunNetworkMode(effectiveNetworkMode, effectiveDocker, networkModeExplicit)
	if err != nil {
		return cliConfigError(err.Error(), err)
	}
	allowlist := splitCSV(*networkAllowlist)
	if effectiveNetworkMode == "allowlist" && len(allowlist) == 0 && len(cfg.Network.Allowlist) > 0 && !flagProvided(fs, "network-allowlist") {
		allowlist = append([]string(nil), cfg.Network.Allowlist...)
	}
	if err := validateNetworkAllowlistUse(effectiveNetworkMode, allowlist); err != nil {
		return cliConfigError(err.Error(), err)
	}

	command := commandFromArgs(fs.Args())
	runnerSize := cfg.RunnerSize
	if cfg.Resources.Default.Size != "" {
		runnerSize = cfg.Resources.Default.Size
	}
	if *runnerSizeOverride != "" {
		runnerSize = *runnerSizeOverride
	}
	if err := validateRunnerSize(runnerSize); err != nil {
		return cliConfigError(err.Error(), err)
	}
	timeoutText := defaultString(cfg.Resources.Default.Timeout, cfg.Timeout)
	if *timeoutOverride != "" {
		timeoutText = *timeoutOverride
	}
	timeoutSeconds, err := parseTimeoutSeconds(cfg.TimeoutSeconds, timeoutText)
	if err != nil {
		return cliConfigError(err.Error(), err)
	}
	cachePathValues := append([]string(nil), cfg.Cache.Paths...)
	cachePathValues = append(cachePathValues, []string(cachePaths)...)
	cacheKeyFiles := append([]string(nil), cfg.Cache.KeyFiles...)
	cacheLaneSizeBytes, err := parseByteSize(cfg.Cache.Size)
	if err != nil {
		return cliConfigError(fmt.Sprintf("cache.size: %v", err), err)
	}
	cacheMode := projectCacheMode(cfg)
	cacheLaneSizeBytes = cacheLaneSizeForMode(cacheMode, cacheLaneSizeBytes)
	effectiveConcurrencyGroup, effectiveConcurrencyLimit, err := effectiveProjectConcurrency(cfg)
	if err != nil {
		return cliConfigError(err.Error(), err)
	}
	if flagProvided(fs, "concurrency-group") {
		effectiveConcurrencyGroup = strings.TrimSpace(*concurrencyGroup)
		if !flagProvided(fs, "concurrency-limit") {
			effectiveConcurrencyLimit = 0
		}
	}
	if flagProvided(fs, "concurrency-limit") {
		effectiveConcurrencyLimit = *concurrencyLimit
		if effectiveConcurrencyLimit == 0 && !flagProvided(fs, "concurrency-group") {
			effectiveConcurrencyGroup = ""
		}
		if effectiveConcurrencyLimit > 0 && effectiveConcurrencyGroup == "" {
			effectiveConcurrencyGroup = "default"
		}
	}
	effectiveConcurrencyGroup, effectiveConcurrencyLimit, err = preflightapi.NormalizeConcurrency(effectiveConcurrencyGroup, effectiveConcurrencyLimit)
	if err != nil {
		return cliConfigError(err.Error(), err)
	}
	token, err := effectiveAPIToken(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	options := remoteRunOptions{
		APIURL:             *apiURL,
		APIToken:           token,
		ProjectID:          *projectID,
		Region:             effectiveRegion,
		Command:            command,
		Image:              cfg.Image,
		ConcurrencyGroup:   effectiveConcurrencyGroup,
		ConcurrencyLimit:   effectiveConcurrencyLimit,
		RunnerSize:         runnerSize,
		TimeoutSeconds:     timeoutSeconds,
		CacheMode:          cacheMode,
		CacheLaneSizeBytes: cacheLaneSizeBytes,
		ArtifactPaths:      append([]string(nil), cfg.Artifacts.Paths...),
		NetworkMode:        effectiveNetworkMode,
		NetworkAllowlist:   allowlist,
		Docker:             effectiveDocker,
		ChangeMode:         *changes,
		SyncExclude:        append([]string(nil), cfg.Sync.Exclude...),
		IncludeUntracked:   cfg.Sync.IncludeUntracked,
		CachePaths:         cachePathValues,
		CacheKeyFiles:      cacheKeyFiles,
		KeepFailed:         *keepFailed,
		Open:               *openRun,
		DashboardURL:       *dashboardURL,
		Detach:             *detach,
	}
	return executeRemoteRunWithRetries(options, *retryCount)
}
