package verificationplan

import (
	"fmt"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

// RunSpec is what a resolved check will execute: image, command, resources,
// network, services, and cache policy. It deliberately excludes how a caller
// reaches the API (endpoint, credentials), how it reports progress, and the
// graph identity a planner assigns later, so that resolving a check produces the
// same specification for the CLI and for service-side planning.
type RunSpec struct {
	EnvironmentRequirements []string
	// Planner-owned cache identity includes preparation-only SDKs even when a
	// run omits preparation. It is rendered into the command, never a run API field.
	CacheDependencyCheck     string            `json:"-"`
	CacheRuntimeRequirements map[string]string `json:"-"`
	RuntimeRequirements      map[string]string
	ProjectID                string
	Region                   string
	Command                  string
	CommandAlias             string
	Steps                    []preflightapi.RunStepSpec
	Env                      map[string]string
	ConcurrencyGroup         string
	ConcurrencyLimit         int
	PrepareCommands          []string
	PostCommands             []string
	AlwaysCommands           []string
	Image                    string
	RunnerSize               string
	TimeoutSeconds           int
	CacheMode                string
	CacheLaneSizeBytes       int64
	ArtifactPaths            []string
	NetworkMode              string
	NetworkAllowlist         []string
	Docker                   bool
	Services                 []preflightapi.RunServiceSpec
	WorkerSelector           map[string]string
	SecretsRequested         *bool
	ChangeMode               string
	SyncExclude              []string
	SyncReviewedFixtures     []syncbundle.ReviewedFixture
	IncludeUntracked         *bool
	CachePaths               []string
	CacheKeyFiles            []string
	KeepFailed               bool
}

// ResolveOptions are the caller-supplied overrides applied while resolving a
// check. The *Provided flags distinguish "explicitly set to the zero value" from
// "not set", which the CLI needs for flags and a service needs for request
// fields.
type ResolveOptions struct {
	ProjectID                string
	Region                   string
	Changes                  string
	NetworkMode              string
	NetworkAllowlist         string
	Timeout                  string
	Docker                   bool
	DockerProvided           bool
	KeepFailed               bool
	ConcurrencyGroup         string
	ConcurrencyGroupProvided bool
	ConcurrencyLimit         int
	ConcurrencyLimitProvided bool
	RetryCount               int
	RetryProvided            bool
	CachePaths               []string
}

// CheckPlan is one resolved check: what it runs, how often to retry it, and the
// matrix it expands into.
type CheckPlan struct {
	Source *preflightapi.VerificationNodeSource
	Name   string
	Run    RunSpec
	// Identity a planner assigned to this node, for work it inserts rather than
	// the user configuring it — a dependency warm-up, for example. It is kept out
	// of RunSpec because it names the node in the graph rather than describing
	// what the node executes. Empty means derive the identity from the command
	// alias, which is the ordinary case.
	LogicalChildID   string
	LogicalChildName string
	// DependencyWarmTargets routes synthesized preparation to its consumers.
	// It is compiled into sealed success dependencies, never accepted from a run.
	DependencyWarmTargets []string
	Retries               int
	Matrix                config.MatrixConfig
	Variants              []Variant
	Services              []string
	ServiceSpecs          []preflightapi.RunServiceSpec
	When                  config.WhenConfig
	RegionDefaulted       bool
	DiagnosticRegion      string
}

type resolvedCheckIdentity struct {
	projectID       string
	region          string
	changes         string
	regionDefaulted bool
}

type resolvedCheckNetwork struct {
	mode      string
	allowlist []string
}

type resolvedCheckCache struct {
	mode          string
	laneSizeBytes int64
	paths         []string
	keyFiles      []string
}

type resolvedCheckResources struct {
	runnerSize       string
	timeoutSeconds   int
	concurrencyGroup string
	concurrencyLimit int
}

// ResolveCheck resolves one configured check into a run specification, layering
// check settings over project defaults and caller overrides.
func ResolveCheck(name string, cfg config.ProjectConfig, options ResolveOptions) (CheckPlan, error) {
	check, ok := cfg.Checks[name]
	if !ok {
		return CheckPlan{}, fmt.Errorf("unknown check %q (available: %s)", name, strings.Join(SortedCheckNames(cfg.Checks), ", "))
	}
	if strings.TrimSpace(check.Command) == "" && len(check.Steps) == 0 {
		return CheckPlan{}, fmt.Errorf("check %q has no command or steps", name)
	}
	if strings.TrimSpace(check.Command) != "" && len(check.Steps) > 0 {
		return CheckPlan{}, fmt.Errorf("check %q cannot set both command and steps", name)
	}

	identity, err := resolveCheckIdentity(cfg, check, options)
	if err != nil {
		return CheckPlan{}, err
	}
	resource, effectiveDocker := resolveCheckDocker(name, cfg, check, options)
	network, err := resolveCheckNetworkPolicy(cfg, check, options, effectiveDocker)
	if err != nil {
		return CheckPlan{}, err
	}
	cache, err := resolveCheckCachePolicy(name, cfg, check, options)
	if err != nil {
		return CheckPlan{}, err
	}
	resources, err := resolveCheckResourcePolicy(name, cfg, check, options, resource)
	if err != nil {
		return CheckPlan{}, err
	}
	artifactPaths := append([]string(nil), cfg.Artifacts.Paths...)
	artifactPaths = append(artifactPaths, check.ArtifactPaths...)
	artifactPaths = append(artifactPaths, check.Outputs.Reviewable...)
	syncConfig := EffectiveSyncConfig(cfg.Sync, check.Sync)
	syncConfig.ReviewedFixtures, err = syncbundle.NormalizeReviewedFixtures(syncConfig.ReviewedFixtures)
	if err != nil {
		return CheckPlan{}, fmt.Errorf("check %q sync.reviewed_fixtures: %w", name, err)
	}
	retries := EffectiveCheckRetries(cfg.Retries, check, -1)
	if options.RetryProvided {
		retries = EffectiveCheckRetries(cfg.Retries, check, options.RetryCount)
	}
	variants, err := ExpandMatrix(check.Matrix)
	if err != nil {
		return CheckPlan{}, err
	}
	serviceSpecs, workerSelector, err := resolveCheckServices(name, cfg, check, effectiveDocker)
	if err != nil {
		return CheckPlan{}, err
	}
	steps, err := resolvedRunSteps(name, check)
	if err != nil {
		return CheckPlan{}, err
	}
	browserPrepare, err := browserPrepareCommands(name, check.Browser)
	if err != nil {
		return CheckPlan{}, err
	}
	environmentRequirements, err := preflightapi.MergeEnvironmentRequirements(cfg.EnvironmentRequirements, check.EnvironmentRequirements)
	if err != nil {
		return CheckPlan{}, fmt.Errorf("check %q environment_requirements: %w", name, err)
	}
	runtimeRequirements, err := preflightapi.NormalizeRuntimeRequirements(check.RuntimeRequirements)
	if err != nil {
		return CheckPlan{}, fmt.Errorf("check %q runtime_requirements: %w", name, err)
	}
	var cacheRuntimeRequirements map[string]string
	dependencyCheck, err := config.DependencyCheckFor(cfg, name)
	if err != nil {
		return CheckPlan{}, err
	}
	if check.DependencyCheck != "" && strings.TrimSpace(cache.mode) == "ephemeral" {
		return CheckPlan{}, fmt.Errorf("check %q dependency_check requires a persistent cache lane", name)
	}
	if warm, exists := cfg.Checks[dependencyCheck]; exists && strings.TrimSpace(cache.mode) != "ephemeral" && len(cache.paths) > 0 {
		cacheRuntimeRequirements, err = mergeDependencyWarmRequirements(warm.RuntimeRequirements, runtimeRequirements)
		if err != nil {
			return CheckPlan{}, fmt.Errorf("check %q dependency preparation runtimes: %w", name, err)
		}
		// Only declared consumer runtimes execute again. The cache identity
		// additionally retains SDKs used exclusively during preparation.
		for runtime := range runtimeRequirements {
			runtimeRequirements[runtime] = cacheRuntimeRequirements[runtime]
		}
	}
	cacheDependencyCheck := dependencyCheck
	if config.IsDependencyCheck(cfg, name) {
		cacheDependencyCheck = name
	}
	if cacheDependencyCheck == config.DefaultDependencyCheck {
		cacheDependencyCheck = ""
	}
	prepareCommands := append([]string(nil), check.Prepare...)
	prepareCommands = append(prepareCommands, browserPrepare...)
	return CheckPlan{
		Name:             name,
		Retries:          retries,
		Matrix:           check.Matrix,
		Variants:         variants,
		Services:         append([]string(nil), check.Services...),
		ServiceSpecs:     CloneRunServices(serviceSpecs),
		When:             check.When,
		RegionDefaulted:  identity.regionDefaulted,
		DiagnosticRegion: preflightapi.NormalizeRegion(identity.region),
		Run: RunSpec{
			EnvironmentRequirements:  environmentRequirements,
			CacheRuntimeRequirements: cacheRuntimeRequirements,
			CacheDependencyCheck:     cacheDependencyCheck,
			RuntimeRequirements:      runtimeRequirements,
			ProjectID:                identity.projectID,
			Region:                   identity.region,
			Command:                  check.Command,
			CommandAlias:             name,
			Steps:                    CloneRunSteps(steps),
			ConcurrencyGroup:         resources.concurrencyGroup,
			ConcurrencyLimit:         resources.concurrencyLimit,
			PrepareCommands:          prepareCommands,
			PostCommands:             append([]string(nil), check.Post...),
			AlwaysCommands:           append([]string(nil), check.Always...),
			Image:                    defaultString(check.Image, cfg.Image),
			RunnerSize:               resources.runnerSize,
			TimeoutSeconds:           resources.timeoutSeconds,
			CacheMode:                cache.mode,
			CacheLaneSizeBytes:       cache.laneSizeBytes,
			ArtifactPaths:            artifactPaths,
			NetworkMode:              network.mode,
			NetworkAllowlist:         network.allowlist,
			Docker:                   effectiveDocker,
			Services:                 serviceSpecs,
			WorkerSelector:           copyStringMap(workerSelector),
			ChangeMode:               identity.changes,
			SyncExclude:              append([]string(nil), syncConfig.Exclude...),
			SyncReviewedFixtures:     append([]syncbundle.ReviewedFixture(nil), syncConfig.ReviewedFixtures...),
			IncludeUntracked:         syncConfig.IncludeUntracked,
			CachePaths:               cache.paths,
			CacheKeyFiles:            cache.keyFiles,
			KeepFailed:               check.KeepFailed || options.KeepFailed,
		},
	}, nil
}

func resolveCheckIdentity(cfg config.ProjectConfig, check config.CheckConfig, options ResolveOptions) (resolvedCheckIdentity, error) {
	identity := resolvedCheckIdentity{
		projectID: strings.TrimSpace(options.ProjectID),
		region:    strings.TrimSpace(options.Region),
		changes:   options.Changes,
	}
	if identity.projectID == "" {
		identity.projectID = cfg.ProjectID
	}
	if err := ValidateControlFreeValue("project_id", identity.projectID); err != nil {
		return resolvedCheckIdentity{}, err
	}
	if identity.region == "" {
		identity.region = check.Region
	}
	if identity.region == "" {
		identity.region = cfg.Region
	}
	identity.regionDefaulted = strings.TrimSpace(identity.region) == ""
	if err := preflightapi.ValidateRegion(identity.region); err != nil {
		return resolvedCheckIdentity{}, err
	}
	if identity.changes == "" {
		identity.changes = check.Changes
	}
	if identity.changes == "" {
		identity.changes = "working"
	}
	if _, err := syncbundle.NormalizeChangeMode(identity.changes); err != nil {
		return resolvedCheckIdentity{}, err
	}
	return identity, nil
}

func resolveCheckDocker(name string, cfg config.ProjectConfig, check config.CheckConfig, options ResolveOptions) (config.ResourceDefaults, bool) {
	resource := cfg.Resources.Default
	if checkResource, ok := cfg.Resources.Checks[name]; ok {
		resource = MergeResourceDefaults(resource, checkResource)
	}
	docker := boolValue(resource.Docker)
	if check.Docker != nil {
		docker = *check.Docker
	}
	if options.DockerProvided {
		docker = options.Docker
	}
	return resource, docker
}

func resolveCheckNetworkPolicy(cfg config.ProjectConfig, check config.CheckConfig, options ResolveOptions, docker bool) (resolvedCheckNetwork, error) {
	mode := options.NetworkMode
	if mode == "" {
		mode = check.NetworkMode
	}
	if mode == "" {
		mode = cfg.Network.Mode
	}
	if mode == "" {
		mode = preflightapi.DefaultNetworkMode
	}
	if err := ValidateNetworkMode(mode); err != nil {
		return resolvedCheckNetwork{}, err
	}
	modeExplicit := options.NetworkMode != "" || check.NetworkMode != "" || cfg.Network.Mode != ""
	mode, err := EffectiveRunNetworkMode(mode, docker, modeExplicit)
	if err != nil {
		return resolvedCheckNetwork{}, err
	}
	allowlist := splitCSV(options.NetworkAllowlist)
	if mode == "allowlist" && len(allowlist) == 0 && len(check.NetworkAllowlist) > 0 && options.NetworkAllowlist == "" {
		allowlist = append([]string(nil), check.NetworkAllowlist...)
	}
	if mode == "allowlist" && len(allowlist) == 0 && len(cfg.Network.Allowlist) > 0 && options.NetworkAllowlist == "" {
		allowlist = append([]string(nil), cfg.Network.Allowlist...)
	}
	if err := ValidateNetworkAllowlistUse(mode, allowlist); err != nil {
		return resolvedCheckNetwork{}, err
	}
	return resolvedCheckNetwork{mode: mode, allowlist: allowlist}, nil
}

func resolveCheckCachePolicy(name string, cfg config.ProjectConfig, check config.CheckConfig, options ResolveOptions) (resolvedCheckCache, error) {
	cache := resolvedCheckCache{mode: ProjectCacheMode(cfg)}
	if check.CacheMode != "" {
		cache.mode = check.CacheMode
	}
	if check.Cache.Mode != "" {
		cache.mode = check.Cache.Mode
	}
	if _, err := NormalizeCacheMode(cache.mode); err != nil {
		return resolvedCheckCache{}, err
	}
	size := strings.TrimSpace(cfg.Cache.Size)
	if check.Cache.Size != "" {
		size = check.Cache.Size
	}
	laneSizeBytes, err := ParseByteSize(size)
	if err != nil {
		return resolvedCheckCache{}, fmt.Errorf("check %q cache.size: %w", name, err)
	}
	cache.laneSizeBytes = CacheLaneSizeForMode(cache.mode, laneSizeBytes)
	cache.paths = append(cache.paths, cfg.Cache.Paths...)
	cache.paths = append(cache.paths, check.Cache.Paths...)
	cache.paths = append(cache.paths, options.CachePaths...)
	cache.keyFiles = append(cache.keyFiles, cfg.Cache.KeyFiles...)
	cache.keyFiles = append(cache.keyFiles, check.Cache.KeyFiles...)
	return cache, nil
}

func resolveCheckResourcePolicy(name string, cfg config.ProjectConfig, check config.CheckConfig, options ResolveOptions, resource config.ResourceDefaults) (resolvedCheckResources, error) {
	resources := resolvedCheckResources{runnerSize: cfg.RunnerSize}
	if resource.Size != "" {
		resources.runnerSize = resource.Size
	}
	if check.Size != "" {
		resources.runnerSize = check.Size
	}
	if check.RunnerSize != "" {
		resources.runnerSize = check.RunnerSize
	}
	if err := ValidateRunnerSize(resources.runnerSize); err != nil {
		return resolvedCheckResources{}, fmt.Errorf("check %q: %w", name, err)
	}
	resources.timeoutSeconds = cfg.TimeoutSeconds
	timeoutText := cfg.Timeout
	if resource.Timeout != "" {
		timeoutText = resource.Timeout
	}
	if check.TimeoutSeconds != 0 {
		resources.timeoutSeconds = check.TimeoutSeconds
		timeoutText = ""
	}
	if check.Timeout != "" {
		timeoutText = check.Timeout
	}
	if options.Timeout != "" {
		timeoutText = options.Timeout
	}
	var err error
	resources.timeoutSeconds, err = ParseTimeoutSeconds(resources.timeoutSeconds, timeoutText)
	if err != nil {
		return resolvedCheckResources{}, err
	}
	resources.concurrencyGroup, resources.concurrencyLimit, err = EffectiveCheckConcurrency(name, cfg, check, options)
	if err != nil {
		return resolvedCheckResources{}, fmt.Errorf("check %q: %w", name, err)
	}
	return resources, nil
}

func resolveCheckServices(name string, cfg config.ProjectConfig, check config.CheckConfig, docker bool) ([]preflightapi.RunServiceSpec, map[string]string, error) {
	if err := ValidateServiceConfigs(cfg.Services); err != nil {
		return nil, nil, err
	}
	if err := ValidateCheckServiceRefs(name, check.Services, cfg.Services, docker); err != nil {
		return nil, nil, err
	}
	workerSelector, err := preflightapi.NormalizeWorkerSelector(check.WorkerSelector)
	if err != nil {
		return nil, nil, fmt.Errorf("check %q: worker_selector %w", name, err)
	}
	serviceSpecs := ResolvedServiceSpecs(cfg.Services, check.Services)
	if err := preflightapi.ValidateRunServices(serviceSpecs, docker); err != nil {
		return nil, nil, fmt.Errorf("check %q: %w", name, err)
	}
	return serviceSpecs, workerSelector, nil
}

// EffectiveCheckRetries resolves the retry count for a check.
func EffectiveCheckRetries(projectRetries int, check config.CheckConfig, cliRetries int) int {
	retries := projectRetries
	if check.RetriesSet {
		retries = check.Retries
	}
	if cliRetries >= 0 {
		retries = cliRetries
	}
	return retries
}

// EffectiveProjectConcurrency resolves project-level concurrency defaults.
func EffectiveProjectConcurrency(cfg config.ProjectConfig) (string, int, error) {
	group := strings.TrimSpace(cfg.ConcurrencyGroup)
	limit := cfg.ConcurrencyLimit
	if group == "" && limit > 0 {
		group = "default"
	}
	return preflightapi.NormalizeConcurrency(group, limit)
}

// EffectiveSyncConfig merges project and check sync settings.
func EffectiveSyncConfig(projectSync, checkSync config.SyncConfig) config.SyncConfig {
	effective := config.SyncConfig{
		IncludeUntracked: projectSync.IncludeUntracked,
		RespectGitignore: projectSync.RespectGitignore,
		Exclude:          append([]string(nil), projectSync.Exclude...),
		ReviewedFixtures: append([]syncbundle.ReviewedFixture(nil), projectSync.ReviewedFixtures...),
	}
	if checkSync.IncludeUntracked != nil {
		effective.IncludeUntracked = checkSync.IncludeUntracked
	}
	if checkSync.RespectGitignore != nil {
		effective.RespectGitignore = checkSync.RespectGitignore
	}
	effective.Exclude = append(effective.Exclude, checkSync.Exclude...)
	effective.ReviewedFixtures = append(effective.ReviewedFixtures, checkSync.ReviewedFixtures...)
	return effective
}

// EffectiveCheckConcurrency resolves a check's concurrency group and limit.
func EffectiveCheckConcurrency(name string, cfg config.ProjectConfig, check config.CheckConfig, options ResolveOptions) (string, int, error) {
	defaultConcurrencyGroup, defaultConcurrencyLimit, err := EffectiveProjectConcurrency(cfg)
	if err != nil {
		return "", 0, err
	}
	concurrencyGroup := defaultConcurrencyGroup
	if check.ConcurrencyGroupSet {
		concurrencyGroup = strings.TrimSpace(check.ConcurrencyGroup)
		if !check.ConcurrencyLimitSet {
			defaultConcurrencyLimit = 0
		}
	}
	if !check.ConcurrencyGroupSet && strings.TrimSpace(check.ConcurrencyGroup) != "" {
		concurrencyGroup = strings.TrimSpace(check.ConcurrencyGroup)
	}
	concurrencyLimit := defaultConcurrencyLimit
	if check.ConcurrencyLimitSet {
		concurrencyLimit = check.ConcurrencyLimit
		if concurrencyLimit == 0 && !check.ConcurrencyGroupSet {
			concurrencyGroup = ""
		}
		if concurrencyLimit > 0 && concurrencyGroup == "" {
			concurrencyGroup = name
		}
	}
	if options.ConcurrencyGroupProvided {
		concurrencyGroup = strings.TrimSpace(options.ConcurrencyGroup)
		if !options.ConcurrencyLimitProvided {
			concurrencyLimit = 0
		}
	}
	if options.ConcurrencyLimitProvided {
		concurrencyLimit = options.ConcurrencyLimit
		if concurrencyLimit == 0 && !options.ConcurrencyGroupProvided {
			concurrencyGroup = ""
		}
		if concurrencyLimit > 0 && concurrencyGroup == "" {
			concurrencyGroup = name
		}
	}
	return preflightapi.NormalizeConcurrency(concurrencyGroup, concurrencyLimit)
}

func resolvedRunSteps(checkName string, check config.CheckConfig) ([]preflightapi.RunStepSpec, error) {
	if len(check.Steps) == 0 {
		return nil, nil
	}
	steps := make([]preflightapi.RunStepSpec, 0, len(check.Steps))
	for index, step := range check.Steps {
		timeoutSeconds, err := ParseTimeoutSeconds(0, step.Timeout)
		if err != nil {
			return nil, fmt.Errorf("check %q steps[%d].timeout: %w", checkName, index, err)
		}
		steps = append(steps, preflightapi.RunStepSpec{
			Name:           strings.TrimSpace(step.Name),
			Command:        strings.TrimSpace(step.Command),
			Always:         step.Always,
			Env:            copyStringMap(step.Env),
			TimeoutSeconds: timeoutSeconds,
		})
	}
	if err := preflightapi.ValidateRunSteps(steps); err != nil {
		return nil, fmt.Errorf("check %q: %w", checkName, err)
	}
	return steps, nil
}

func browserPrepareCommands(checkName string, browser string) ([]string, error) {
	browser = strings.ToLower(strings.TrimSpace(browser))
	if browser == "" {
		return nil, nil
	}
	switch browser {
	case "chromium", "firefox", "webkit":
		// Operating-system packages belong in the runner image. A normal check
		// must not implicitly invoke apt or require root through --with-deps.
		return []string{"if command -v pnpm >/dev/null 2>&1; then pnpm exec playwright install " + browser + "; else npx playwright install " + browser + "; fi"}, nil
	default:
		return nil, fmt.Errorf("check %q: browser must be chromium, firefox, or webkit", checkName)
	}
}

// CloneRunSteps deep-copies run steps.
func CloneRunSteps(steps []preflightapi.RunStepSpec) []preflightapi.RunStepSpec {
	if len(steps) == 0 {
		return nil
	}
	cloned := make([]preflightapi.RunStepSpec, len(steps))
	for index, step := range steps {
		cloned[index] = step
		cloned[index].Env = copyStringMap(step.Env)
	}
	return cloned
}

// CloneRunServices deep-copies run services.
func CloneRunServices(services []preflightapi.RunServiceSpec) []preflightapi.RunServiceSpec {
	if len(services) == 0 {
		return nil
	}
	cloned := make([]preflightapi.RunServiceSpec, len(services))
	for index, service := range services {
		cloned[index] = service
		cloned[index].Env = copyStringMap(service.Env)
		cloned[index].Ports = append([]string(nil), service.Ports...)
		cloned[index].Logs = append([]string(nil), service.Logs...)
	}
	return cloned
}
