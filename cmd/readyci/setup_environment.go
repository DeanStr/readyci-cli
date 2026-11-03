package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

// setupEnvironmentReport is the selected check's environment preview.
// Status is ready only when the runner is compatible and nothing is
// unresolved; RuntimeStatus describes runner compatibility alone. Unresolved
// lists what static inspection could not confirm, which only a run that
// produces test feedback establishes. It is the only JSON home of those
// review notes; Warnings holds the remaining review text.
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
	Unresolved             []setupUnresolvedItem         `json:"unresolved,omitempty"`
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
	commands, tests := []string{}, []string{}
	if plan != nil {
		tests = append(tests, setupRuntimeCommandWithEnvironment(plan.Options.Command, plan.Options.Env))
		commands = append(commands, tests[0])
		for _, command := range plan.Options.PrepareCommands {
			commands = append(commands, setupRuntimeCommandWithEnvironment(command, plan.Options.Env))
		}
		for _, step := range plan.Options.Steps {
			command := setupRuntimeCommandWithEnvironment(step.Command, plan.Options.Env, step.Env)
			commands, tests = append(commands, command), append(tests, command)
		}
	}
	environment.Requirements, _ = setupRuntimeRequirementsForSelection(".", commands, environment.RuntimeRequirements)
	// Repository runtime declarations are review material. A configuration
	// that readyci check accepts is never blocked here; only the requirements
	// it retains authorize the run, and setup must not claim missing ones are
	// sealed.
	declared, declaredItems := setupDeclaredRuntimeRequirementsForSelection(".", commands, environment.RuntimeRequirements)
	var unsealed []setupUnresolvedItem
	if plan != nil {
		for _, runtime := range sortedStringMapKeys(declared) {
			required, configured := declared[runtime], plan.Options.RuntimeRequirements[runtime]
			if required == "installed" || preflightapi.RuntimeRequirementImplies(configured, required) {
				continue
			}
			sealed := "does not require " + runtime
			if configured != "" {
				sealed = "only requires " + strconv.Quote(configured)
			}
			var sources []string
			for _, requirement := range environment.Requirements {
				if requirement.Runtime == runtime {
					sources = appendUniqueString(sources, requirement.Source)
				}
			}
			unsealed = append(unsealed, setupUnresolvedItem{
				Kind: setupUnresolvedRuntimeDeclarationNotApplied, Subject: runtime, Source: defaultString(strings.Join(sources, ", "), "repository runtime declarations"), Declared: required,
				Detail: "repository runtime declarations narrow " + runtime + " to " + strconv.Quote(required) + ", but checks." + report.Check + ".runtime_requirements " + sealed + "; add the declaration there or regenerate setup to seal it.",
			})
		}
		dependencyCheck, _ := config.DependencyCheckFor(cfg, report.Check)
		environment.Unresolved = mergeSetupUnresolved(
			setupImportedWorkflowUnresolved(".", cfg.Checks[report.Check], report, plan.Options.RuntimeRequirements),
			setupUnresolvedForCheck(mergeSetupUnresolved(
				setupPythonDependencyReview(".", tests, plan.Options.PrepareCommands, dependencyCheck != ""),
				declaredItems,
				unsealed,
				setupOpaqueEntrypoints(".", tests),
			), report.Check),
		)
	}
	environment.NetworkReview = describeSetupNetwork(options, cfg, report, commands)
	if plan != nil {
		environment.Warnings = append(environment.Warnings, setupNetworkWarning())
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
	// runtime_status describes runner compatibility only; unresolved items keep
	// the environment under review without blocking the first run.
	if capability.Policy.Allowed && capability.Claimable {
		if len(environment.Unresolved) == 0 {
			environment.Status = "ready"
		}
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

// setupImportedWorkflowUnresolved returns what the upstream job a check was
// imported from declares but the check does not apply. Everything else is
// derived from the check itself. The job is re-read on every preview instead
// of being saved, so a proposal and its saved configuration report the same
// items and a changed workflow reports its current ones.
func setupImportedWorkflowUnresolved(root string, check config.CheckConfig, report *setupReport, requirements map[string]string) []setupUnresolvedItem {
	if report.ImportScope != nil {
		// The configuration is this run's import proposal; its candidate was
		// just analyzed with the same runtime pins.
		selected := report.ImportScope.Selected
		return setupImportedJobUnresolved(selected, selected.RuntimePins, requirements, report.Check)
	}
	imported := check.ImportedFrom
	if imported == nil {
		return nil // A hand-written check names no upstream job.
	}
	// Re-read without the recorded pins: each pin is checked against the
	// check below instead of being trusted.
	for _, candidate := range setupImportCandidates(root, "", nil) {
		if candidate.ID == imported.Candidate {
			return setupImportedJobUnresolved(candidate, imported.RuntimePins, requirements, report.Check)
		}
	}
	// The workflow or job is gone, was renamed, or no longer looks like a
	// verification job. The saved check still runs exactly as written and the
	// rest of its review still applies, so there are no upstream declarations
	// to report and no reason to fail the preview.
	return nil
}

// setupImportedJobUnresolved compares an import candidate's runtime
// declarations with the runtime requirements the check actually carries.
//
// A selector setup cannot translate, such as setup-node lts/*, stays
// unresolved unless the user pinned that runtime and the check's requirement
// still applies the pin. The pin is needed because a repository declaration
// such as package.json engines can give a pinned and an unpinned import the
// same requirement. A declaration setup does translate is unresolved when the
// check's requirement does not imply it, for example after the workflow moves
// to a new version once the configuration is saved.
func setupImportedJobUnresolved(candidate repoImportCandidate, pins, requirements map[string]string, check string) []setupUnresolvedItem {
	var items []setupUnresolvedItem
	for _, item := range candidate.WorkflowUnresolved {
		if pin := pins[item.Subject]; item.Kind == setupUnresolvedRuntimeDeclarationNotApplied && pin != "" &&
			preflightapi.RuntimeRequirementImplies(requirements[item.Subject], pin) {
			continue
		}
		items = append(items, item)
	}
	for _, runtime := range sortedStringMapKeys(candidate.RuntimeRequirements) {
		required, configured := candidate.RuntimeRequirements[runtime], requirements[runtime]
		if required == "installed" || preflightapi.RuntimeRequirementImplies(configured, required) {
			continue
		}
		sealed := "does not require " + runtime
		if configured != "" {
			sealed = "only requires " + strconv.Quote(configured)
		}
		items = append(items, setupUnresolvedItem{
			Kind: setupUnresolvedRuntimeDeclarationNotApplied, Subject: runtime, Source: candidate.ID, Declared: required,
			Detail: "the imported workflow job declares " + runtime + " " + strconv.Quote(required) + ", but checks." + check + ".runtime_requirements " + sealed + "; add the declaration there or regenerate setup to seal it.",
		})
	}
	return setupUnresolvedForCheck(items, check)
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
	for _, item := range environment.Unresolved {
		fmt.Printf("  unresolved (%s): %s\n", item.Kind, item.Detail)
	}
	for _, warning := range environment.Warnings {
		fmt.Printf("  review: %s\n", warning)
	}
	for _, blocker := range environment.Blockers {
		fmt.Printf("  blocked: %s\n", blocker)
	}
}
