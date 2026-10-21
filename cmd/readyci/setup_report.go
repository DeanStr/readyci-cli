package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/config"
)

func buildSetupReport(options setupOptions) (setupReport, error) {
	cfg, configPath, err := config.LoadProjectConfig(".")
	configLoadErr := err
	if err != nil {
		if configPath == "" {
			return setupReport{}, err
		}
		if options.WriteConfig && options.Force {
			cfg = config.DefaultProjectConfig()
			configPath = ""
		}
	}
	projectID := resolveSetupProjectID(options.ProjectID, cfg)
	if err := validateControlFreeValue("project_id", projectID); err != nil {
		return setupReport{}, err
	}
	var written []string
	configGenerated := false
	var candidates []repoImportCandidate
	var scopeCandidates []repoImportCandidate
	importScope := options.ImportScope
	if options.Detect && (configPath == "" || options.Force) {
		scopeCandidates = setupImportCandidates(".", "", options.RuntimePins)
		candidates = filterSetupImportCandidates(scopeCandidates, options.ImportSource)
	}
	if (options.ImportSource != "" || options.ImportCandidate != "") && configPath != "" && !options.Force {
		return setupReport{}, cliConfigError("import selection would replace existing configuration; use --force after reviewing it", nil)
	}
	if len(options.NetworkAllowlist) > 0 && configPath != "" && !options.Force {
		return setupReport{}, cliConfigError("--network-allowlist replaces generated configuration; use --force to review regeneration, or readyci check --network-mode allowlist --network-allowlist HOSTS for an existing check", nil)
	}
	if len(options.RuntimePins) > 0 && configPath != "" && !options.Force {
		return setupReport{}, cliConfigError("--runtime selects generated workflow runtimes; use --force to review regeneration of existing configuration", nil)
	}
	if options.ImportCandidate != "" && options.ConfigTemplate == nil {
		candidate, err := selectSetupImport(options, candidates, nil, io.Discard, true)
		if err != nil {
			return setupReport{}, err
		}
		importScope = selectedSetupImportScope(*candidate, scopeCandidates)
		template := setupImportedTemplateWithScope(".", options, projectID, *candidate, scopeCandidates)
		options.ConfigTemplate = &template
	}
	if options.ConfigTemplate == nil && options.ImportSource == "" && (configPath == "" || options.Force) {
		if options.Detect {
			// An unresolved import remains an inventory for guided selection.
			// Writing still requires a concrete template and rejects ambiguity.
			if detected, detectionErr := setupDetectedTemplate(".", projectID, options, candidates); detectionErr == nil {
				options.ConfigTemplate = &detected
				if imported, _ := automaticSetupImport(candidates); imported != nil {
					importScope = selectedSetupImportScope(*imported, scopeCandidates)
				}
			}
		} else if len(options.NetworkAllowlist) > 0 {
			template := remoteCIProjectTemplateWithProfile(projectID, options.Image, options.Profile)
			options.ConfigTemplate = &template
		}
	}
	// Preview resolves the same concrete configuration as writing it. Keep the
	// actual file status missing while exposing the selected checks and running
	// capability checks against the reviewed proposal.
	if options.ConfigTemplate != nil {
		template, err := applySetupNetworkPolicy(*options.ConfigTemplate, options)
		if err != nil {
			return setupReport{}, err
		}
		options.ConfigTemplate = &template
		proposed, err := config.ParseProjectConfig([]byte(*options.ConfigTemplate), "proposed imported configuration")
		if err != nil {
			return setupReport{}, err
		}
		if err := validateProjectConfig(proposed); err != nil {
			return setupReport{}, err
		}
		if err := validateSetupRuntimeTemplate(".", *options.ConfigTemplate); err != nil {
			return setupReport{}, err
		}
		if err := validateGuidedSetupCheck(options.CheckName, sortedCheckNames(proposed.Checks)); err != nil {
			return setupReport{}, err
		}
		cfg = proposed
		configLoadErr = nil
	}
	if options.WriteConfig && options.ImportSource != "" && options.ConfigTemplate == nil {
		return setupReport{}, cliConfigError("--write-config with --import requires --candidate ID", nil)
	}
	if options.WriteConfig && (configPath == "" || options.Force) {
		var template string
		if options.ConfigTemplate != nil {
			template = *options.ConfigTemplate
		} else {
			template = remoteCIProjectTemplateWithProfile(projectID, options.Image, options.Profile)
			if options.Detect {
				template, err = setupDetectedTemplate(".", projectID, options, candidates)
				if err != nil {
					return setupReport{}, err
				}
			} else if options.WriteHelpers {
				template = remoteCIProjectTemplateWithStandardHelpers(projectID, options.Image, options.Profile)
			}
		}
		writePath, err := initConfigPath(options.Force)
		if err != nil {
			return setupReport{}, err
		}
		template, err = applySetupNetworkPolicy(template, options)
		if err != nil {
			return setupReport{}, err
		}
		if options.Detect {
			if err := validateSetupRuntimeTemplate(".", template); err != nil {
				return setupReport{}, err
			}
		}
		if err := writeSetupConfig(writePath, template, options.Force); err != nil {
			return setupReport{}, err
		}
		written = append(written, writePath)
		configGenerated = true
		cfg, configPath, err = config.LoadProjectConfig(".")
		configLoadErr = err
		if err != nil {
			return setupReport{}, err
		}
		if cfg.ProjectID != "" {
			projectID = cfg.ProjectID
		}
	}

	report := setupReport{
		APIURL:           normalizeAPIURL(options.APIURL),
		ProjectID:        projectID,
		Check:            strings.TrimSpace(options.CheckName),
		Checks:           sortedCheckNames(cfg.Checks),
		Written:          append([]string(nil), written...),
		ImportCandidates: candidates,
		ImportScope:      importScope,
		RuntimePins:      copyStringMap(options.RuntimePins),
		scopeCandidates:  scopeCandidates,
		configGenerated:  configGenerated,
	}
	if options.ConfigTemplate != nil {
		report.ProposedConfig = *options.ConfigTemplate
		report.SelectionStatus = "selected"
		report.SelectionReason = "Generated configuration is proposed; review the selected checks and network access."
		if importScope != nil {
			report.SelectionReason = importScope.Coverage
		}
	}

	var configErr error
	if configLoadErr != nil {
		configErr = configLoadErr
	} else if configPath != "" {
		configErr = validateProjectConfigForConfigPath(cfg, configPath)
	}
	report.configError = configErr
	switch {
	case configPath == "":
		report.ConfigStatus = "missing"
		report.Warnings = append(report.Warnings, "no .readyci.yml found")
		if options.Detect && options.ConfigTemplate == nil {
			if _, err := setupDetectedTemplate(".", projectID, options, candidates); err != nil {
				report.SelectionStatus = "configuration_required"
				report.SelectionReason = err.Error()
				report.Warnings = append(report.Warnings, err.Error())
			} else {
				report.SelectionStatus = "available"
			}
		}
	case configErr != nil:
		report.ConfigPath = configPath
		report.ConfigStatus = "invalid"
		report.Warnings = append(report.Warnings, configErr.Error())
	default:
		report.ConfigPath = configPath
		report.ConfigStatus = "ok"
	}
	if report.Check == "" {
		report.Check = preferredSetupCheck(report.Checks)
	}
	report.warmProfileAvailable = report.ConfigStatus == "ok" && setupWarmProfileAvailable(cfg)
	report.affectedAuditAvailable = report.ConfigStatus == "ok" && setupAffectedAuditAvailable(cfg)
	if report.ConfigStatus == "ok" || report.ProposedConfig != "" {
		if warning := sharedSourceScopeWarning(cfg); warning != "" {
			report.Warnings = append(report.Warnings, warning)
		}
	}

	if options.WriteMakefile {
		makefilePath := "readyci.mk"
		if err := writeSetupGeneratedFile(makefilePath, setupMakefileScaffold(normalizeAPIURL(options.APIURL), projectID), 0o644, options.Force); err != nil {
			return setupReport{}, err
		}
		report.Written = append(report.Written, makefilePath)
	}
	if options.WriteHelpers {
		for _, name := range []string{"shell-syntax", "ops-config"} {
			path, content, err := setupHelperScript(name)
			if err != nil {
				return setupReport{}, err
			}
			if err := writeSetupHelperScript(path, content, options.Force); err != nil {
				return setupReport{}, err
			}
			report.Written = append(report.Written, path)
		}
	}

	token, err := effectiveAPIToken(options.APIURL, options.APIToken)
	if err != nil {
		report.authError = err
		report.AuthStatus = "error"
		report.Warnings = append(report.Warnings, err.Error())
	} else if token == "" {
		report.AuthStatus = "missing"
	} else {
		ctx, stop := signalContext()
		defer stop()
		usage, err := api.NewClient(options.APIURL, api.WithToken(token)).AccountUsage(ctx)
		var responseErr api.ResponseError
		switch {
		case err == nil:
			report.AuthStatus = "ok"
			report.TeamID = usage.TeamID
			report.PlanID = usage.Plan.ID
		case errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusForbidden && responseErr.Code == "auth_scope_required":
			report.AuthStatus = "ok"
			report.Warnings = append(report.Warnings, "account details are unavailable without account:read; run permissions will be checked when the check starts")
		default:
			report.authError = err
			report.AuthStatus = "error"
			report.Warnings = append(report.Warnings, "account check failed: "+err.Error())
		}
	}
	reconcileSetupEnvironment(options, cfg, &report)
	report.Next = setupNextCommands(report)
	return report, nil
}

func preferredSetupCheck(checks []string) string {
	for _, candidate := range []string{"affected", "touched", "ci", "test", "web", "go", "rust", "staged"} {
		if containsString(checks, candidate) {
			return candidate
		}
	}
	if len(checks) > 0 {
		return checks[0]
	}
	return ""
}

func setupNextCommands(report setupReport) []string {
	var commands []string
	apiFlag := apiURLFlag(report.APIURL)
	if report.ConfigStatus == "missing" {
		if report.ImportScope != nil {
			commands = append(commands, "readyci setup"+apiFlag+" --project "+shellQuoteArg(report.ProjectID)+" --candidate "+shellQuoteArg(report.ImportScope.Selected.ID)+setupRuntimePinNextFlags(report.RuntimePins)+setupNetworkNextFlags(report.Environment)+" --write-config")
		} else {
			commands = append(commands, "readyci setup"+apiFlag+" --project "+shellQuoteArg(report.ProjectID)+setupRuntimePinNextFlags(report.RuntimePins)+setupNetworkNextFlags(report.Environment))
			commands = append(commands, "readyci setup --print --detect=false")
		}
	}
	if report.AuthStatus != "ok" {
		commands = append(commands, "readyci login"+apiFlag)
		commands = append(commands, readyCIAPIEnvCommand(report.APIURL, "<token>"))
	}
	doctor := "readyci doctor" + apiFlag + " --project " + shellQuoteArg(report.ProjectID)
	if report.Check != "" {
		doctor += " --check " + shellQuoteArg(report.Check)
	}
	commands = append(commands, doctor)
	if report.affectedAuditAvailable {
		commands = append(commands, "readyci affected audit --strict --duplicates")
	}
	if len(report.Checks) > 0 && report.warmProfileAvailable {
		commands = append(commands, "readyci warm --profile dependencies --dry-run")
	}
	if report.Check != "" {
		if report.Check == "affected" {
			commands = append(commands,
				"readyci affected --dry-run",
				"readyci affected"+apiFlag+" --project "+shellQuoteArg(report.ProjectID)+" --open",
			)
		} else if report.Check == "touched" {
			commands = append(commands,
				"readyci affected --check touched --dry-run",
				"readyci affected"+apiFlag+" --project "+shellQuoteArg(report.ProjectID)+" --check touched --open",
			)
		} else {
			commands = append(commands,
				"readyci check --dry-run "+shellQuoteArg(report.Check),
				"readyci check"+apiFlag+" --project "+shellQuoteArg(report.ProjectID)+" --open "+shellQuoteArg(report.Check),
			)
		}
	}
	commands = append(commands, "readyci runs latest"+apiFlag+" --project "+shellQuoteArg(report.ProjectID))
	return commands
}

func setupWarmProfileAvailable(cfg config.ProjectConfig) bool {
	_, err := warmCheckNames(cfg, "", "dependencies")
	return err == nil
}

func setupAffectedAuditAvailable(cfg config.ProjectConfig) bool {
	return len(cfg.Affected.Rules) > 0 || len(cfg.Affected.Profiles) > 0
}

func apiURLFlag(apiURL string) string {
	normalized := normalizeAPIURL(apiURL)
	if normalized == "" {
		return ""
	}
	if normalized == normalizeAPIURL(defaultAPIURL()) &&
		normalized == normalizeAPIURL(envDefault("READYCI_API_URL", defaultAPIURL())) {
		return ""
	}
	return " --api-url " + shellQuoteArg(normalized)
}

func readyCIAPIEnvCommand(apiURL, token string) string {
	tokenExport := "READYCI_API_TOKEN=" + shellQuoteArg(token)
	normalized := normalizeAPIURL(apiURL)
	if normalized == normalizeAPIURL(defaultAPIURL()) &&
		normalized == normalizeAPIURL(envDefault("READYCI_API_URL", defaultAPIURL())) {
		return "export " + tokenExport
	}
	return "export READYCI_API_URL=" + shellQuoteArg(normalized) + " " + tokenExport
}

func printSetupReport(report setupReport) {
	fmt.Println("setup: readyci")
	fmt.Printf("api_url: %s\n", report.APIURL)
	fmt.Printf("project_id: %s\n", report.ProjectID)
	fmt.Printf("config: %s\n", defaultString(report.ConfigPath, report.ConfigStatus))
	fmt.Printf("auth: %s\n", report.AuthStatus)
	if report.TeamID != "" {
		fmt.Printf("team_id: %s\n", report.TeamID)
	}
	if report.PlanID != "" {
		fmt.Printf("plan: %s\n", report.PlanID)
	}
	if report.Check != "" {
		fmt.Printf("first_check: %s\n", report.Check)
	}
	if len(report.Checks) > 0 {
		fmt.Printf("checks: %s\n", strings.Join(report.Checks, ","))
	}
	if report.SelectionStatus != "" {
		fmt.Printf("selection: %s\n", report.SelectionStatus)
	}
	if report.ImportScope != nil {
		fmt.Printf("selected_candidate: %s\n", report.ImportScope.Selected.ID)
		if matrix := setupImportMatrixDescription(report.ImportScope.Selected.Matrix); matrix != "" {
			fmt.Printf("selected_matrix: %s\n", matrix)
		}
		fmt.Printf("scope: %s\n", report.ImportScope.Coverage)
		for _, notice := range setupRuntimePinNotices(report.ImportScope.Selected.RuntimeResolutions) {
			fmt.Printf("runtime_pin: %s\n", notice)
		}
		for _, notice := range setupUserRuntimePinNotices(report.ImportScope.Selected.RuntimePins) {
			fmt.Printf("runtime_pin: %s\n", notice)
		}
		for _, omitted := range report.ImportScope.Omitted {
			fmt.Printf("not_imported: %s\n", omitted.ID)
			if matrix := setupImportMatrixDescription(omitted.Matrix); matrix != "" {
				fmt.Printf("  matrix: %s\n", matrix)
			}
			for _, blocker := range omitted.Unsupported {
				fmt.Printf("  blocked: %s\n", blocker)
			}
		}
	}
	if report.ProposedConfig != "" && report.ConfigStatus == "missing" {
		fmt.Printf("proposed_configuration:\n%s\n", report.ProposedConfig)
	}
	printSetupEnvironment(report.Environment)
	for _, path := range report.Written {
		fmt.Printf("wrote: %s\n", path)
	}
	for _, warning := range report.Warnings {
		fmt.Printf("warning: %s\n", warning)
	}
	for _, candidate := range report.ImportCandidates {
		fmt.Printf("import_candidate: %s · %s\n", candidate.ID, candidate.Command)
		if matrix := setupImportMatrixDescription(candidate.Matrix); matrix != "" {
			fmt.Printf("  matrix: %s\n", matrix)
		}
		for _, requirement := range candidate.Requirements {
			fmt.Printf("  review: %s\n", requirement)
		}
		for _, unsupported := range candidate.Unsupported {
			fmt.Printf("  manual_configuration: %s\n", unsupported)
		}
		for _, unsupported := range candidate.AutomaticUnsupported {
			fmt.Printf("  explicit_selection: %s\n", unsupported)
		}
	}
	fmt.Println("next:")
	for _, command := range report.Next {
		fmt.Printf("  %s\n", command)
	}
}

func doctorHealthClient() *http.Client {
	return &http.Client{Timeout: doctorHealthTimeout}
}
