package main

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func joinImportedSteps(runSteps []repoWorkflowRunStep) string {
	steps := make([]string, 0, len(runSteps))
	for _, step := range runSteps {
		steps = append(steps, importedWorkflowStepCommand(step, true))
	}
	return strings.Join(steps, " && ")
}

// These are command imports, not an emulation of GitHub Actions. Requirements
// needing provider state remain visible and block automatic configuration.
func workflowImportRequirements(workflow repoWorkflowYAML, job repoWorkflowJobYAML) (requirements, unsupported []string) {
	if len(yamlStringList(job.RunsOn)) == 0 {
		unsupported = append(unsupported, "runner requirements are missing; configure a supported Linux runner explicitly")
	}
	for _, runner := range yamlStringList(job.RunsOn) {
		if !setupWorkflowSupportedUbuntuRunner(runner) {
			unsupported = append(unsupported, "non-Ubuntu or dynamic runner requirements need explicit configuration; only reviewed x64 ubuntu-latest, ubuntu-22.04, and ubuntu-24.04 labels are imported")
		}
	}
	_, environmentProblems := effectiveWorkflowEnvironment(workflow, job, repoWorkflowStepYAML{})
	unsupported = append(unsupported, environmentProblems...)
	if len(job.Services) > 0 {
		unsupported = append(unsupported, "service topology requires explicit Ready CI services configuration")
	}
	if job.Container != nil {
		unsupported = append(unsupported, "job containers require an explicit runner configuration")
	}
	if job.Strategy != nil {
		unsupported = append(unsupported, "matrix strategy requires explicit Ready CI matrix configuration")
	}
	if len(yamlStringList(job.Needs)) > 0 || len(job.Outputs) > 0 {
		unsupported = append(unsupported, "job dependencies and outputs require explicit configuration")
	}
	if job.Uses != "" {
		unsupported = append(unsupported, "reusable workflows cannot be translated into shell commands")
	}
	if job.If != "" {
		unsupported = append(unsupported, "job conditions require explicit selection rules")
	}
	if job.ContinueOnError != nil && job.ContinueOnError != false {
		unsupported = append(unsupported, "job continue-on-error requires explicit failure handling")
	}
	_, timeoutProblems := staticWorkflowTimeout(job.TimeoutMinutes, "job")
	unsupported = append(unsupported, timeoutProblems...)
	if job.Environment != nil {
		unsupported = append(unsupported, "job environment approvals and secrets require explicit configuration")
	}
	runStepCount := 0
	for _, step := range job.Steps {
		_, environmentProblems := effectiveWorkflowEnvironment(workflow, job, step)
		unsupported = append(unsupported, environmentProblems...)
		if step.If != "" || step.ContinueOnError {
			unsupported = append(unsupported, "step conditions or continue-on-error require manual translation")
		}
		_, timeoutProblems := staticWorkflowTimeout(step.TimeoutMinutes, "step")
		unsupported = append(unsupported, timeoutProblems...)
		if strings.TrimSpace(step.Run) != "" {
			runStepCount++
			defaults := effectiveWorkflowRunDefaults(workflow, job, step)
			if defaults.Shell != "" && defaults.Shell != "bash" {
				unsupported = append(unsupported, "custom shells require manual translation")
			}
			if strings.Contains(defaults.WorkingDirectory, "${{") {
				unsupported = append(unsupported, "dynamic working directories require manual translation")
			} else if dir := strings.TrimSpace(defaults.WorkingDirectory); dir != "" && !filepath.IsLocal(dir) {
				unsupported = append(unsupported, "working directories must stay inside the uploaded repository")
			}
		}
		if strings.Contains(step.Run, "${{") || strings.Contains(step.Run, "GITHUB_") || strings.Contains(step.Run, "RUNNER_") {
			unsupported = append(unsupported, "provider expressions and runtime variables require manual translation")
		}
		if step.Uses == "" {
			continue
		}
		if len(step.Env) > 0 {
			unsupported = append(unsupported, "action step environment requires manual translation; the action itself is not executed")
		}
		if step.TimeoutMinutes != nil {
			unsupported = append(unsupported, "action step timeout-minutes requires manual translation; the action itself is not executed")
		}
		action, _, _ := strings.Cut(step.Uses, "@")
		switch action {
		case "actions/checkout":
			requirements = append(requirements, "Ready CI reconstructs Git metadata from a synthetic baseline; original commit history and tags are not available.")
			if depth, exists := step.With["fetch-depth"]; exists && !checkoutSingleCommitDepth(depth) {
				unsupported = append(unsupported, "checkout fetch-depth requires Git history or dynamic configuration; translate history-dependent commands manually")
			}
			for key := range step.With {
				if key == "persist-credentials" {
					if !checkoutCredentialsDisabled(step.With[key]) {
						unsupported = append(unsupported, "checkout persist-credentials must be false; Ready CI does not import checkout authentication")
					}
					continue
				}
				if key != "fetch-depth" {
					unsupported = append(unsupported, "custom checkout options require manual review; Ready CI uploads the current workspace")
					break
				}
			}
		case "actions/setup-go", "actions/setup-node", "actions/setup-python", "actions/cache", "pnpm/action-setup":
			allowed := map[string]map[string]bool{
				"actions/setup-go":     {"go-version": true, "go-version-file": true, "cache": true, "cache-dependency-path": true, "check-latest": true},
				"actions/setup-node":   {"node-version": true, "node-version-file": true, "cache": true, "cache-dependency-path": true, "check-latest": true},
				"actions/setup-python": {"python-version": true, "python-version-file": true, "cache": true, "cache-dependency-path": true, "check-latest": true},
				"actions/cache":        {"path": true, "key": true, "restore-keys": true},
				"pnpm/action-setup":    {"version": true, "run_install": true},
			}
			for key := range step.With {
				if !allowed[action][key] {
					unsupported = append(unsupported, action+" input "+key+" requires manual translation")
				}
			}
			if action == "actions/cache" {
				requirements = append(requirements, action+" is not executed; configure Ready CI cache paths explicitly")
			} else {
				requirements = append(requirements, action+" is translated into a required installed runtime; the action itself is not executed and its optional provider cache is not imported")
			}
			if action == "pnpm/action-setup" && !pnpmInstallDisabled(step.With["run_install"]) {
				unsupported = append(unsupported, "pnpm/action-setup dependency installation (run_install) requires manual translation")
			}
			for _, key := range []string{"go-version", "go-version-file", "node-version", "node-version-file", "python-version", "python-version-file", "version"} {
				if value, ok := step.With[key]; ok {
					text := fmt.Sprint(value)
					if strings.Contains(text, "${{") {
						unsupported = append(unsupported, "dynamic toolchain requirements need manual translation")
					} else {
						requirements = append(requirements, action+" requests "+key+"="+text)
					}
				}
			}
		default:
			unsupported = append(unsupported, "action "+action+" requires manual translation")
		}
	}
	if runStepCount > preflightapi.MaxRunSteps {
		unsupported = append(unsupported, fmt.Sprintf("workflow job exceeds Ready CI's limit of %d run steps; configure its scope explicitly", preflightapi.MaxRunSteps))
	}
	return uniqueStrings(requirements), uniqueStrings(unsupported)
}

func checkoutSingleCommitDepth(value any) bool {
	switch depth := value.(type) {
	case int:
		return depth == 1
	case string:
		return strings.TrimSpace(depth) == "1"
	default:
		return false
	}
}

func checkoutCredentialsDisabled(value any) bool {
	switch value := value.(type) {
	case bool:
		return !value
	case string:
		return value == "false"
	default:
		return false
	}
}

func pnpmInstallDisabled(input any) bool {
	switch value := input.(type) {
	case nil:
		return true
	case bool:
		return !value
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "", "false", "null", "~":
			return true
		}
	}
	return false
}

func effectiveWorkflowRunDefaults(workflow repoWorkflowYAML, job repoWorkflowJobYAML, step repoWorkflowStepYAML) repoWorkflowRunDefaultsYAML {
	return repoWorkflowRunDefaultsYAML{
		Shell:            defaultString(step.Shell, defaultString(job.Defaults.Run.Shell, workflow.Defaults.Run.Shell)),
		WorkingDirectory: defaultString(step.WorkingDirectory, defaultString(job.Defaults.Run.WorkingDirectory, workflow.Defaults.Run.WorkingDirectory)),
	}
}

func setupImportCandidates(cwd, source string, runtimePins ...map[string]string) []repoImportCandidate {
	repo := repoMapReport{Make: scanRepoMake(cwd), GitHubActions: scanRepoGitHubActions(cwd, runtimePins...)}
	var out []repoImportCandidate
	for _, candidate := range inferRepoImportCandidates(repo) {
		if len(runtimePins) > 0 {
			for _, runtime := range sortedStringMapKeys(runtimePins[0]) {
				if candidate.RuntimePins[runtime] == "" {
					candidate.Unsupported = append(candidate.Unsupported, fmt.Sprintf("--runtime %s=%s requires a matching supported workflow runtime declaration", runtime, runtimePins[0][runtime]))
				}
			}
		}
		if candidate.Source == "github-actions" {
			declaredTests := candidate.TestEntrypoint
			candidate.TestEntrypoint = false
			for _, step := range candidate.RunSteps {
				runsTests, scopeUnknown := setupWorkflowStepRunsTests(cwd, step)
				candidate.TestEntrypoint = candidate.TestEntrypoint || runsTests
				if scopeUnknown {
					candidate.ScopeUnknown = true
					candidate.AutomaticUnsupported = appendUniqueString(candidate.AutomaticUnsupported, "package-script test scope could not be inspected; review package selectors, directories, and manifests explicitly")
				}
			}
			if declaredTests && !candidate.TestEntrypoint {
				// Static script inspection must not silently prune a declared test
				// job. Runtime configuration and earlier steps may alter scripts;
				// choosing another job instead requires explicit scope review.
				candidate.ScopeUnknown = true
			}
		}
		if candidate.ID != "" && (source == "" || candidate.Source == source) {
			out = append(out, candidate)
		}
	}
	return out
}

func selectSetupImport(options setupOptions, candidates []repoImportCandidate, reader *bufio.Reader, output io.Writer, yes bool) (*repoImportCandidate, error) {
	if len(candidates) == 0 {
		if options.ImportSource != "" || options.ImportCandidate != "" {
			return nil, cliConfigError("no matching import candidates found", nil)
		}
		return nil, nil
	}
	parents := make(map[string]int)
	for _, candidate := range candidates {
		if candidate.ParentJobID != "" {
			parents[candidate.ParentJobID]++
		}
	}
	var choices []repoImportCandidate
	for _, candidate := range candidates {
		if variants := parents[candidate.ID]; variants > 0 {
			fmt.Fprintf(output, "Upstream matrix job: %s [%s]; %d concrete variants listed.\n", candidate.Name, candidate.ID, variants)
		} else {
			choices = append(choices, candidate)
			fmt.Fprintf(output, "%d. %s [%s]\n", len(choices), candidate.Name, candidate.ID)
		}
		if candidate.ParentJobID != "" {
			fmt.Fprintf(output, "   Upstream job: %s\n", candidate.ParentJobID)
		}
		if matrix := setupImportMatrixDescription(candidate.Matrix); matrix != "" {
			fmt.Fprintf(output, "   Matrix: %s\n", matrix)
		}
		if candidate.Command != "" {
			fmt.Fprintf(output, "   %s\n", candidate.Command)
		}
		for _, requirement := range candidate.Requirements {
			fmt.Fprintf(output, "   Review: %s\n", requirement)
		}
		for _, unsupported := range candidate.Unsupported {
			fmt.Fprintf(output, "   Requires manual configuration: %s\n", unsupported)
		}
		for _, unsupported := range candidate.AutomaticUnsupported {
			fmt.Fprintf(output, "   Explicit selection required: %s\n", unsupported)
		}
	}
	if options.ImportCandidate != "" {
		for _, candidate := range candidates {
			if candidate.ID == options.ImportCandidate {
				return selectConcreteSetupImport(candidate, candidates, parents, output)
			}
		}
		return nil, cliConfigError("unknown import candidate; use setup --preview --json to list candidate IDs", nil)
	}
	if yes {
		if options.ImportSource != "" {
			return nil, cliConfigError("--yes with --import requires --candidate ID; inspect setup --preview --json first", nil)
		}
		return automaticSetupImport(candidates)
	}
	if reader == nil {
		return nil, cliConfigError("setup input ended before command selection", io.EOF)
	}
	for {
		fmt.Fprint(output, "Choose an existing command by number or candidate ID, or press Enter for automatic selection: ")
		answer, err := reader.ReadString('\n')
		if err != nil {
			return nil, cliConfigError("setup input ended before command selection", err)
		}
		answer = strings.TrimSpace(answer)
		if answer == "" {
			selected, err := automaticSetupImport(candidates)
			if err != nil {
				fmt.Fprintln(output, err)
				continue
			}
			if selected != nil {
				printSetupImportSelection(output, *selected, candidates)
			}
			return selected, nil
		}
		index, err := strconv.Atoi(answer)
		var candidate *repoImportCandidate
		if err == nil && index > 0 && index <= len(choices) {
			candidate = &choices[index-1]
		} else {
			for i := range candidates {
				if candidates[i].ID == answer {
					candidate = &candidates[i]
					break
				}
			}
		}
		if candidate != nil {
			selected, err := selectConcreteSetupImport(*candidate, candidates, parents, output)
			if err == nil {
				return selected, nil
			}
			fmt.Fprintln(output, err)
			continue
		}
		fmt.Fprintln(output, "Enter a candidate number, an exact candidate ID, or press Enter.")
	}
}

func selectConcreteSetupImport(candidate repoImportCandidate, candidates []repoImportCandidate, parents map[string]int, output io.Writer) (*repoImportCandidate, error) {
	selected, err := validateSetupImport(candidate)
	if err != nil {
		return nil, err
	}
	if parents[candidate.ID] > 0 {
		return nil, cliConfigError("this candidate represents an upstream matrix job; select one of its concrete variants", nil)
	}
	printSetupImportSelection(output, candidate, candidates)
	return selected, nil
}

func setupImportMatrixDescription(matrix map[string]string) string {
	keys := make([]string, 0, len(matrix))
	for key := range matrix {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, key+"="+strconv.Quote(matrix[key]))
	}
	return strings.Join(values, ", ")
}

func printSetupImportSelection(output io.Writer, selected repoImportCandidate, candidates []repoImportCandidate) {
	scope := selectedSetupImportScope(selected, candidates)
	fmt.Fprintf(output, "\nSelected scope: %s\n", selected.ID)
	if matrix := setupImportMatrixDescription(selected.Matrix); matrix != "" {
		fmt.Fprintf(output, "   Matrix: %s\n", matrix)
	}
	fmt.Fprintln(output, scope.Coverage)
	for _, omitted := range scope.Omitted {
		fmt.Fprintf(output, "   Not imported: %s", omitted.ID)
		if matrix := setupImportMatrixDescription(omitted.Matrix); matrix != "" {
			fmt.Fprintf(output, " (matrix: %s)", matrix)
		}
		fmt.Fprintln(output)
	}
}

func validateSetupImport(candidate repoImportCandidate) (*repoImportCandidate, error) {
	if len(candidate.Unsupported) > 0 {
		return nil, cliConfigError("this candidate requires manual configuration: "+strings.Join(candidate.Unsupported, "; "), nil)
	}
	return &candidate, nil
}

func setupImportedTemplate(options setupOptions, projectID string, candidate repoImportCandidate) string {
	return setupImportedTemplateForRoot(".", options, projectID, candidate)
}

func setupImportedTemplateForRoot(root string, options setupOptions, projectID string, candidate repoImportCandidate) string {
	name := candidate.Check
	if name == "" {
		name = sanitizeCheckName(candidate.Name)
	}
	if name == "" {
		name = "test"
	}
	runtimes := setupImportRuntimes(root, candidate)
	// Moving selectors are resolved once during import. Their catalog records
	// remain in the import report; the saved check uses ordinary numeric policy.
	check := setupPrepareDetectedCheck(root, detectedCheck{Name: name, Command: candidate.Command, Steps: importedWorkflowSteps(candidate.RunSteps), Timeout: defaultString(candidate.Timeout, "20m"), Runtimes: runtimes, RuntimeRequirements: candidate.RuntimeRequirements, RustTargetPaths: setupImportRustTargetPaths(root, candidate)})
	signals := []string{"Imported command from " + candidate.ID + "; review runtime requirements and the proposed dependency network access."}
	signals = append(signals, setupRuntimePinNotices(candidate.RuntimeResolutions)...)
	signals = append(signals, setupUserRuntimePinNotices(candidate.RuntimePins)...)
	for _, requirement := range setupDependencyReviewRequirements(root, []string{candidate.Command}) {
		signals = append(signals, "Preparation review required: "+requirement)
	}
	return renderDetectedProjectTemplate(projectID, options.Image, options.Profile, []detectedCheck{check}, nil, signals, config.CommandList(nil))
}
