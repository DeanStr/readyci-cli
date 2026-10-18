package main

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func joinImportedSteps(runSteps []repoWorkflowRunStep) string {
	steps := make([]string, 0, len(runSteps))
	for _, step := range runSteps {
		// GitHub Actions provides CI=true for each independent run-step shell.
		// Its unspecified shell omits pipefail; explicit bash enables it.
		shell := "CI=true bash -e -c "
		if step.Shell == "bash" {
			shell = "CI=true bash --noprofile --norc -eo pipefail -c "
		}
		command := commandWithWorkingDirectory(step.Command, step.WorkingDirectory)
		steps = append(steps, shell+shellQuoteArg(command))
	}
	return strings.Join(steps, " && ")
}

// These are command imports, not an emulation of GitHub Actions. Requirements
// needing provider state remain visible and block automatic configuration.
func workflowImportRequirements(workflow repoWorkflowYAML, job repoWorkflowJobYAML) (requirements, unsupported []string) {
	for _, runner := range yamlStringList(job.RunsOn) {
		if !strings.HasPrefix(runner, "ubuntu-") || strings.Contains(runner, "${{") {
			unsupported = append(unsupported, "non-Ubuntu or dynamic runner requirements need explicit configuration")
		}
	}
	if len(workflow.Env) > 0 || len(job.Env) > 0 {
		unsupported = append(unsupported, "workflow/job environment variables require explicit configuration")
	}
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
	for _, step := range job.Steps {
		if len(step.Env) > 0 || step.If != "" || step.ContinueOnError {
			unsupported = append(unsupported, "step environment, conditions, or continue-on-error require manual translation")
		}
		if strings.TrimSpace(step.Run) != "" {
			defaults := effectiveWorkflowRunDefaults(workflow, job, step)
			if defaults.Shell != "" && defaults.Shell != "bash" {
				unsupported = append(unsupported, "custom shells require manual translation")
			}
			if strings.Contains(defaults.WorkingDirectory, "${{") {
				unsupported = append(unsupported, "dynamic working directories require manual translation")
			}
		}
		if strings.Contains(step.Run, "${{") || strings.Contains(step.Run, "GITHUB_") || strings.Contains(step.Run, "RUNNER_") {
			unsupported = append(unsupported, "provider expressions and runtime variables require manual translation")
		}
		if step.Uses == "" {
			continue
		}
		action, _, _ := strings.Cut(step.Uses, "@")
		switch action {
		case "actions/checkout":
			requirements = append(requirements, "Ready CI reconstructs Git metadata from a synthetic baseline; original commit history and tags are not available.")
			if depth, exists := step.With["fetch-depth"]; exists && !checkoutSingleCommitDepth(depth) {
				unsupported = append(unsupported, "checkout fetch-depth requires Git history or dynamic configuration; translate history-dependent commands manually")
			}
			for key := range step.With {
				if key != "fetch-depth" {
					unsupported = append(unsupported, "custom checkout options require manual review; Ready CI uploads the current workspace")
					break
				}
			}
		case "actions/setup-go", "actions/setup-node", "actions/cache", "pnpm/action-setup":
			requirements = append(requirements, action+" is not executed; review its toolchain/package-manager requirements against the chosen runner image")
			if action == "pnpm/action-setup" && !pnpmInstallDisabled(step.With["run_install"]) {
				unsupported = append(unsupported, "pnpm/action-setup dependency installation (run_install) requires manual translation")
			}
			for _, key := range []string{"go-version", "go-version-file", "node-version", "node-version-file", "version"} {
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

func setupImportCandidates(cwd, source string) []repoImportCandidate {
	repo := repoMapReport{Make: scanRepoMake(cwd), GitHubActions: scanRepoGitHubActions(cwd)}
	var out []repoImportCandidate
	for _, candidate := range inferRepoImportCandidates(repo) {
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
	for i, candidate := range candidates {
		fmt.Fprintf(output, "%d. %s [%s]\n   %s\n", i+1, candidate.Name, candidate.ID, candidate.Command)
		for _, requirement := range candidate.Requirements {
			fmt.Fprintf(output, "   Review: %s\n", requirement)
		}
		for _, unsupported := range candidate.Unsupported {
			fmt.Fprintf(output, "   Requires manual configuration: %s\n", unsupported)
		}
	}
	if options.ImportCandidate != "" {
		for _, candidate := range candidates {
			if candidate.ID == options.ImportCandidate {
				return validateSetupImport(candidate)
			}
		}
		return nil, cliConfigError("unknown import candidate; use setup --preview --json to list candidate IDs", nil)
	}
	if yes {
		if options.ImportSource != "" {
			return nil, cliConfigError("--yes with --import requires --candidate ID; inspect setup --preview --json first", nil)
		}
		return nil, nil
	}
	for {
		fmt.Fprint(output, "Choose an existing command by number, or press Enter for detected defaults: ")
		answer, err := reader.ReadString('\n')
		if err != nil {
			return nil, cliConfigError("setup input ended before command selection", err)
		}
		answer = strings.TrimSpace(answer)
		if answer == "" {
			return nil, nil
		}
		index, err := strconv.Atoi(answer)
		if err == nil && index > 0 && index <= len(candidates) {
			return validateSetupImport(candidates[index-1])
		}
		fmt.Fprintln(output, "Enter a candidate number or press Enter.")
	}
}

func validateSetupImport(candidate repoImportCandidate) (*repoImportCandidate, error) {
	if len(candidate.Unsupported) > 0 {
		return nil, cliConfigError("this candidate requires manual configuration: "+strings.Join(candidate.Unsupported, "; "), nil)
	}
	return &candidate, nil
}

func setupImportedTemplate(options setupOptions, projectID string, candidate repoImportCandidate) string {
	name := candidate.Check
	if name == "" {
		name = sanitizeCheckName(candidate.Name)
	}
	if name == "" {
		name = "test"
	}
	runtimes := setupImportRuntimes(".", candidate)
	return renderDetectedProjectTemplate(projectID, options.Image, options.Profile, []detectedCheck{{Name: name, Command: candidate.Command, Timeout: "20m", Runtimes: runtimes, RustTargetPaths: setupImportRustTargetPaths(".", candidate)}}, nil, []string{"Imported command from " + candidate.ID + "; review runtime requirements and the proposed dependency network access."}, config.CommandList(nil))
}
