package verificationplan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

type commandPhases struct {
	CacheDependencyCheck     string
	CacheRuntimeRequirements map[string]string
	Env                      map[string]string
	Prepare                  []string
	Command                  string
	Steps                    []preflightapi.RunStepSpec
	Post                     []string
	Always                   []string
}

// CommandForRunSpec assembles the single shell command a run executes,
// wrapping prepare/post/always phases and steps as needed.
func CommandForRunSpec(options RunSpec) (string, error) {
	if strings.TrimSpace(options.Command) != "" && len(options.Steps) > 0 {
		return strings.TrimSpace(options.Command), nil
	}
	cacheRequirements := options.CacheRuntimeRequirements
	if cacheRequirements == nil {
		cacheRequirements = options.RuntimeRequirements
	}
	return buildCommand(commandPhases{
		CacheRuntimeRequirements: cacheRequirements,
		CacheDependencyCheck:     options.CacheDependencyCheck,
		Env:                      options.Env,
		Prepare:                  options.PrepareCommands,
		Command:                  options.Command,
		Steps:                    options.Steps,
		Post:                     options.PostCommands,
		Always:                   options.AlwaysCommands,
	}, options.CachePaths, options.CacheKeyFiles, options.KeepFailed)
}

// CommandAndSteps decides whether a run carries structured steps, a single
// wrapped command, or both.
func CommandAndSteps(options RunSpec, command string) (string, []preflightapi.RunStepSpec) {
	steps := CloneRunSteps(options.Steps)
	if len(steps) == 0 {
		return command, nil
	}
	if runRequiresCommandWrapper(options) {
		return command, nil
	}
	if strings.TrimSpace(options.Command) != "" {
		if canonicalStepsOnlyCommand(command, steps) {
			return command, steps
		}
		return command, nil
	}
	return "", steps
}

func runRequiresCommandWrapper(options RunSpec) bool {
	return len(options.Env) > 0 ||
		len(trimCommandList(options.PrepareCommands)) > 0 ||
		len(trimCommandList(options.PostCommands)) > 0 ||
		len(trimCommandList(options.AlwaysCommands)) > 0 ||
		len(options.CachePaths) > 0 ||
		options.KeepFailed
}

func canonicalStepsOnlyCommand(command string, steps []preflightapi.RunStepSpec) bool {
	canonical, err := buildCommand(commandPhases{Steps: steps}, nil, nil, false)
	return err == nil && command == canonical
}

func buildCommand(phases commandPhases, cachePaths, cacheKeyFiles []string, keepFailed bool) (string, error) {
	phases.Prepare = trimCommandList(phases.Prepare)
	phases.Command = strings.TrimSpace(phases.Command)
	phases.Steps = normalizeRunSteps(phases.Steps)
	phases.Post = trimCommandList(phases.Post)
	phases.Always = trimCommandList(phases.Always)
	envScript := matrixCommandPrefix(phases.Env)
	cacheScript, err := cachePathSetupScript(cachePaths, cacheKeyFiles, phases.CacheRuntimeRequirements, phases.CacheDependencyCheck)
	if err != nil {
		return "", err
	}
	if cacheScript == "" && envScript == "" && !keepFailed && len(phases.Prepare) == 0 && len(phases.Steps) == 0 && len(phases.Post) == 0 && len(phases.Always) == 0 {
		return phases.Command, nil
	}

	var script strings.Builder
	writeRemoteLogPhaseMarkerFunction(&script)
	if cacheScript != "" {
		script.WriteString(cacheScript)
		script.WriteString("\n__preflight_setup_cache_paths\n")
		script.WriteString("__preflight_setup_status=$?\n")
		script.WriteString("if [ $__preflight_setup_status -ne 0 ]; then exit $__preflight_setup_status; fi\n")
	}
	if envScript != "" {
		script.WriteString(envScript)
	}
	if len(phases.Prepare) > 0 {
		writeCommandFunction(&script, "__preflight_prepare", phases.Prepare)
	}
	if len(phases.Steps) > 0 {
		for index, step := range phases.Steps {
			writeStepFunction(&script, fmt.Sprintf("__preflight_step_%d", index), step)
		}
	} else {
		writeCommandFunction(&script, "__preflight_command", []string{phases.Command})
	}
	if len(phases.Post) > 0 {
		writeCommandFunction(&script, "__preflight_post", phases.Post)
	}
	if len(phases.Always) > 0 {
		writeCommandFunction(&script, "__preflight_always", phases.Always)
	}
	script.WriteString("__preflight_run_phases() {\n")
	if len(phases.Prepare) > 0 {
		script.WriteString("  __readyci_log_phase prepare\n")
		script.WriteString("  __preflight_prepare\n")
		script.WriteString("  __preflight_status=$?\n")
		script.WriteString("  if [ $__preflight_status -ne 0 ]; then return $__preflight_status; fi\n")
	}
	script.WriteString("  __readyci_log_phase command\n")
	if len(phases.Steps) > 0 {
		for index, step := range phases.Steps {
			if step.Always {
				continue
			}
			writeStepInvocation(&script, fmt.Sprintf("__preflight_step_%d", index), step)
			script.WriteString("  __preflight_status=$?\n")
			script.WriteString("  if [ $__preflight_status -ne 0 ]; then return $__preflight_status; fi\n")
		}
	} else {
		script.WriteString("  __preflight_command\n")
		script.WriteString("  __preflight_status=$?\n")
		script.WriteString("  if [ $__preflight_status -ne 0 ]; then return $__preflight_status; fi\n")
	}
	if len(phases.Post) > 0 {
		script.WriteString("  __readyci_log_phase post\n")
		script.WriteString("  __preflight_post\n")
		script.WriteString("  __preflight_status=$?\n")
		script.WriteString("  if [ $__preflight_status -ne 0 ]; then return $__preflight_status; fi\n")
	}
	script.WriteString("  return 0\n")
	script.WriteString("}\n")
	writeCommandInvocation(&script, "__preflight_run_phases", "command")
	script.WriteString("__preflight_status=$?\n")
	if len(phases.Steps) > 0 {
		for index, step := range phases.Steps {
			if !step.Always {
				continue
			}
			script.WriteString("(\n")
			script.WriteString("__readyci_log_phase command\n")
			writeStepInvocation(&script, fmt.Sprintf("__preflight_step_%d", index), step)
			script.WriteString(")\n")
			script.WriteString("__preflight_always_step_status=$?\n")
			script.WriteString("if [ $__preflight_status -eq 0 ] && [ $__preflight_always_step_status -ne 0 ]; then __preflight_status=$__preflight_always_step_status; fi\n")
		}
	}
	if len(phases.Always) > 0 {
		writeCommandInvocation(&script, "__preflight_always", "always")
		script.WriteString("__preflight_always_status=$?\n")
		script.WriteString("if [ $__preflight_status -eq 0 ] && [ $__preflight_always_status -ne 0 ]; then __preflight_status=$__preflight_always_status; fi\n")
	}
	if keepFailed {
		script.WriteString(failedWorkspaceRetentionScript())
	}
	script.WriteString("exit $__preflight_status\n")
	return script.String(), nil
}

func normalizeRunSteps(steps []preflightapi.RunStepSpec) []preflightapi.RunStepSpec {
	out := make([]preflightapi.RunStepSpec, 0, len(steps))
	for _, step := range steps {
		step.Name = strings.TrimSpace(step.Name)
		step.Command = strings.TrimSpace(step.Command)
		if step.Name == "" || step.Command == "" {
			continue
		}
		step.Env = copyStringMap(step.Env)
		out = append(out, step)
	}
	return out
}

func trimCommandList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func writeRemoteLogPhaseMarkerFunction(script *strings.Builder) {
	script.WriteString("__readyci_log_phase() {\n")
	script.WriteString("  printf '\\036READYCI_PHASE:%s\\036\\n' \"$1\"\n")
	script.WriteString("  printf '\\036READYCI_PHASE:%s\\036\\n' \"$1\" >&2\n")
	script.WriteString("  __readyci_log_step ''\n")
	script.WriteString("}\n")
	script.WriteString("__readyci_log_step() {\n")
	script.WriteString("  printf '\\036READYCI_STEP:%s\\036\\n' \"$1\"\n")
	script.WriteString("  printf '\\036READYCI_STEP:%s\\036\\n' \"$1\" >&2\n")
	script.WriteString("}\n")
}

func writeCommandFunction(script *strings.Builder, label string, commands []string) {
	script.WriteString(label)
	script.WriteString("() {\n")
	for _, command := range commands {
		script.WriteString(command)
		script.WriteString("\n")
		script.WriteString("  __preflight_phase_status=$?\n")
		script.WriteString("  if [ $__preflight_phase_status -ne 0 ]; then return $__preflight_phase_status; fi\n")
	}
	script.WriteString("}\n")
}

func writeCommandInvocation(script *strings.Builder, label, phase string) {
	script.WriteString("(\n")
	script.WriteString("__readyci_log_phase ")
	script.WriteString(ShellQuoteArg(phase))
	script.WriteString("\n")
	script.WriteString(label)
	script.WriteString("\n)\n")
}

func writeStepFunction(script *strings.Builder, label string, step preflightapi.RunStepSpec) {
	script.WriteString(label)
	script.WriteString("() {\n")
	script.WriteString("  (\n")
	for _, key := range sortedStringMapKeys(step.Env) {
		script.WriteString("    ")
		script.WriteString(key)
		script.WriteString("=")
		script.WriteString(ShellQuoteArg(step.Env[key]))
		script.WriteString("\n")
		script.WriteString("    export ")
		script.WriteString(key)
		script.WriteString("\n")
	}
	script.WriteString(StepCommand(step.Command, step.TimeoutSeconds))
	script.WriteString("  )\n")
	script.WriteString("}\n")
}

func writeStepInvocation(script *strings.Builder, label string, step preflightapi.RunStepSpec) {
	script.WriteString("  __readyci_log_step ")
	script.WriteString(ShellQuoteArg(step.Name))
	script.WriteString("\n")
	script.WriteString("  ")
	script.WriteString(label)
	script.WriteString("\n")
}

func matrixCommandPrefix(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+ShellQuoteArg(env[key]), "export "+key)
	}
	return strings.Join(parts, "\n") + "\n"
}

func sortedStringMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
