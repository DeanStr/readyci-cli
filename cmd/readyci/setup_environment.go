package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

type setupEnvironmentReport struct {
	RuntimePreparation     []preflightapi.RuntimeArchive `json:"runtime_preparation,omitempty"`
	RuntimeRequirements    map[string]string             `json:"runtime_requirements,omitempty"`
	Status                 string                        `json:"status"`
	Image                  string                        `json:"image"`
	RunnerSize             string                        `json:"runner_size,omitempty"`
	NetworkMode            string                        `json:"network_mode,omitempty"`
	NetworkAllowlist       []string                      `json:"network_allowlist,omitempty"`
	NetworkReview          *setupNetworkReview           `json:"network_review,omitempty"`
	Docker                 bool                          `json:"docker"`
	CacheMode              string                        `json:"cache_mode,omitempty"`
	ScratchSizeBytes       int64                         `json:"scratch_size_bytes,omitempty"`
	CacheLaneSizeBytes     int64                         `json:"cache_lane_size_bytes,omitempty"`
	RuntimeStatus          string                        `json:"runtime_status"`
	TemporaryStorageStatus string                        `json:"temporary_storage_status"`
	Requirements           []setupRuntimeRequirement     `json:"requirements,omitempty"`
	Warnings               []string                      `json:"warnings,omitempty"`
	Blockers               []string                      `json:"blockers,omitempty"`
	WorkerCapability       *workerCapabilityReport       `json:"worker_capability,omitempty"`
	preflightError         error
}

type setupRuntimeRequirement struct {
	Runtime string `json:"runtime"`
	Value   string `json:"value"`
	Source  string `json:"source"`
}

func reconcileSetupEnvironment(options setupOptions, cfg config.ProjectConfig, report *setupReport) {
	environment := &setupEnvironmentReport{
		Status: "review_required", Image: defaultString(options.Image, defaultString(cfg.Image, preflightapi.DefaultImage)),
		RuntimeStatus: "not_checked", TemporaryStorageStatus: "not_advertised",
	}
	report.Environment = environment
	var plan *checkRunPlan
	if (report.ConfigStatus == "ok" || report.ProposedConfig != "") && report.Check != "" {
		resolved, err := resolveCheckRunPlan(report.Check, cfg, checkResolveOptions{ProjectID: report.ProjectID, RetryCount: -1})
		if err != nil {
			environment.Blockers = append(environment.Blockers, err.Error())
			environment.Status = "blocked"
			return
		}
		plan = &resolved
		environment.Image = defaultString(plan.Options.Image, preflightapi.DefaultImage)
		environment.RunnerSize, environment.NetworkMode = plan.Options.RunnerSize, plan.Options.NetworkMode
		environment.NetworkAllowlist = append([]string(nil), plan.Options.NetworkAllowlist...)
		environment.Docker, environment.CacheMode = remoteRunRequiresDocker(plan.Options), plan.Options.CacheMode
		environment.CacheLaneSizeBytes = plan.Options.CacheLaneSizeBytes
		environment.RuntimeRequirements = preflightapi.CloneRuntimeRequirements(plan.Options.RuntimeRequirements)
	}
	commands := []string{}
	var preparation []string
	if plan != nil {
		commands = append(commands, setupRuntimeCommandWithEnvironment(plan.Options.Command, plan.Options.Env))
		for _, command := range plan.Options.PrepareCommands {
			commands = append(commands, setupRuntimeCommandWithEnvironment(command, plan.Options.Env))
		}
		preparation = plan.Options.PrepareCommands
		for _, step := range plan.Options.Steps {
			commands = append(commands, setupRuntimeCommandWithEnvironment(step.Command, plan.Options.Env, step.Env))
		}
	}
	environment.Requirements, environment.Warnings = setupRuntimeRequirementsForSelection(".", commands, environment.RuntimeRequirements)
	environment.NetworkReview = describeSetupNetwork(options, cfg, report, commands)
	if plan != nil {
		environment.Warnings = append(environment.Warnings, setupNetworkWarning())
	}
	if len(environment.Requirements) > 0 {
		environment.RuntimeStatus = "not_checked"
	}

	var explicitRuntimes []string
	if plan != nil {
		for runtime := range plan.Options.RuntimeRequirements {
			explicitRuntimes = append(explicitRuntimes, runtime)
		}
	}
	declared, declarationErr := setupDeclaredRuntimeRequirementsForSelection(".", commands, environment.RuntimeRequirements, explicitRuntimes...)
	if declarationErr != nil {
		environment.Status, environment.RuntimeStatus = "blocked", "unsupported"
		environment.Blockers = append(environment.Blockers, declarationErr.Error())
		environment.preflightError = cliConfigError(declarationErr.Error(), nil)
		return
	}
	if plan != nil {
		// Only the requirements retained in the configuration authorize the run.
		// A setup preview may diagnose an older hand-written configuration, but
		// it must not claim those missing requirements are sealed for execution.
		for runtime, required := range declared {
			if !preflightapi.RuntimeRequirementImplies(plan.Options.RuntimeRequirements[runtime], required) {
				environment.RuntimeStatus = "requires_configuration"
				environment.Blockers = append(environment.Blockers, "declare "+runtime+" "+required+" under checks."+report.Check+".runtime_requirements; regenerate setup to seal repository runtime declarations")
			}
		}
	}
	if blockers := setupDependencyReviewRequirements(".", commands, preparation...); len(blockers) > 0 {
		environment.Blockers = append(environment.Blockers, blockers...)
	}
	if len(environment.Blockers) > 0 {
		environment.Status = "blocked"
		environment.preflightError = cliConfigError(strings.Join(environment.Blockers, "; "), nil)
		return
	}

	if check, ok := cfg.Checks[report.Check]; ok && check.Browser != "" {
		environment.Warnings = append(environment.Warnings, "browser preparation installs browser binaries only; the selected runner image must already provide its operating-system libraries")
	}
	if plan == nil || report.AuthStatus != "ok" {
		return
	}
	token, err := effectiveAPIToken(options.APIURL, options.APIToken)
	if err != nil {
		environment.Status = "blocked"
		environment.Blockers = append(environment.Blockers, "environment capability preflight unavailable: "+err.Error())
		environment.preflightError = err
		return
	}
	ctx, stop := signalContext()
	defer stop()
	capability, err := publicWorkerCapability(ctx, options.APIURL, token, *plan)
	if err != nil {
		environment.Status = "blocked"
		environment.Blockers = append(environment.Blockers, "environment capability preflight unavailable: "+err.Error())
		environment.preflightError = err
		return
	}
	environment.WorkerCapability = &capability
	environment.RuntimePreparation = append([]preflightapi.RuntimeArchive(nil), capability.RuntimePreparation...)
	environment.ScratchSizeBytes = capability.ScratchSizeBytes
	if capability.ScratchSizeBytes > 0 {
		environment.TemporaryStorageStatus = "advertised"
	}
	if capability.Policy == nil {
		environment.Status = "blocked"
		environment.Blockers = append(environment.Blockers, "capability response omitted effective admission policy; update the CLI and API together before running setup")
		environment.preflightError = cliConfigError(environment.Blockers[len(environment.Blockers)-1], nil)
		return
	}
	// Session sources use the service's object-store upload contract, not a
	// worker-local bundle upload URL. Session creation validates that store.
	if capability.Policy.Allowed && capability.Claimable {
		environment.Status = "ready"
		if len(plan.Options.RuntimeRequirements) > 0 {
			environment.RuntimeStatus = "compatible"
			if len(environment.RuntimePreparation) > 0 {
				environment.RuntimeStatus = "preparation_required"
			}
		} else {
			environment.RuntimeStatus = "not_required"
		}
	}
	if !capability.Policy.Allowed || !capability.Claimable {
		environment.Status = "blocked"
		if !capability.Policy.Allowed {
			environment.Blockers = append(environment.Blockers, defaultString(capability.Policy.Message, "effective admission policy denies the selected run"))
		}
		if !capability.Claimable {
			environment.Blockers = append(environment.Blockers, capability.Reasons...)
		}
		environment.Blockers = uniqueStrings(environment.Blockers)
		if len(environment.Blockers) == 0 {
			environment.Blockers = []string{"no fresh worker currently supports the selected image, region, size, network, and Docker requirements"}
		}
	}
}

func validateSetupEnvironment(environment *setupEnvironmentReport) error {
	if environment == nil || len(environment.Blockers) == 0 {
		return nil
	}
	if environment.preflightError != nil {
		return environment.preflightError
	}
	message := "setup environment preflight blocked: " + strings.Join(environment.Blockers, "; ")
	if capability := environment.WorkerCapability; capability != nil {
		if policy := capability.Policy; policy != nil && !policy.Allowed {
			return normalizeCLIError(api.ResponseError{
				Operation: "setup environment preflight", StatusCode: policy.StatusCode,
				Status: http.StatusText(policy.StatusCode), Code: policy.Code, Body: policy.Message,
			})
		}
		return cliError{Code: "worker_unavailable", Classification: preflightapi.ClassificationWorkerUnavailable,
			ExitCode: cliExitTemporaryUnavailable, Retryable: true, Message: message}
	}
	return cliConfigError(message, nil)
}

func printSetupEnvironment(environment *setupEnvironmentReport) {
	if environment == nil {
		return
	}
	fmt.Printf("environment: %s (image=%s network=%s docker=%t)\n", environment.Status, environment.Image, valueOrDash(environment.NetworkMode), environment.Docker)
	if len(environment.NetworkAllowlist) > 0 {
		fmt.Printf("  network_allowlist: %s\n", strings.Join(environment.NetworkAllowlist, ","))
	}
	if review := environment.NetworkReview; review != nil {
		fmt.Printf("  network_review: %s; test endpoints %s; access applies to the entire check\n", review.PolicySource, review.TestEndpointStatus)
	}
	for _, name := range sortedStringMapKeys(environment.RuntimeRequirements) {
		fmt.Printf("  runtime: %s %s\n", name, environment.RuntimeRequirements[name])
	}
	for _, artifact := range environment.RuntimePreparation {
		name := defaultString(artifact.Runtime, "Go")
		if name == "pnpm-native" {
			name = "pnpm native"
		}
		fmt.Printf("  runtime_preparation: %s %s from approved archive %s (%d bytes), using existing network policy\n", name, artifact.Version, artifact.SHA256, artifact.SizeBytes)
	}
	for _, requirement := range environment.Requirements {
		fmt.Printf("  runtime_requirement: %s %s (%s)\n", requirement.Runtime, requirement.Value, requirement.Source)
	}
	if environment.ScratchSizeBytes > 0 {
		fmt.Printf("  temporary_storage: %d bytes per-run device capacity (filesystem overhead reduces usable space)\n", environment.ScratchSizeBytes)
	} else {
		fmt.Println("  temporary_storage: not advertised")
	}
	for _, warning := range environment.Warnings {
		fmt.Printf("  review: %s\n", warning)
	}
	for _, blocker := range environment.Blockers {
		fmt.Printf("  blocked: %s\n", blocker)
	}
}
