package main

import (
	"fmt"
	"strings"
)

// Resolve only values available before the selected command runs. No GitHub
// event, needs/steps output, status function, secret, or shell expansion is
// synthesized. Each environment mapping sees only its enclosing scope.
func setupWorkflowStaticJob(workflow repoWorkflowYAML, job repoWorkflowJobYAML, matrix map[string]any) (repoWorkflowYAML, repoWorkflowJobYAML) {
	job.Strategy, job.StrategyNode, job.StaticResolved = nil, nil, true
	job.Problems = append([]string(nil), job.Problems...)
	job.StaticConditions = append([]repoWorkflowCondition(nil), job.StaticConditions...)
	job.StaticRequirements = append([]string(nil), job.StaticRequirements...)
	context := map[string]map[string]any{"env": {}}
	matrixContext := map[string]map[string]any{}
	if matrix != nil {
		context["matrix"] = matrix
		matrixContext["matrix"] = matrix
	}
	resolve := func(value string, context map[string]map[string]any) string {
		out, err := setupInterpolateWorkflowExpressions(value, context)
		if err != nil {
			job.Problems = append(job.Problems, "untranslated provider or matrix expression requires explicit configuration: "+err.Error())
			return value
		}
		return out
	}
	var resolveAny func(any, map[string]map[string]any) any
	resolveAny = func(value any, context map[string]map[string]any) any {
		switch value := value.(type) {
		case string:
			return resolve(value, context)
		case []any:
			out := make([]any, len(value))
			for i, entry := range value {
				out[i] = resolveAny(entry, context)
			}
			return out
		case map[string]any:
			out := make(map[string]any, len(value))
			for key, entry := range value {
				out[key] = resolveAny(entry, context)
			}
			return out
		default:
			return value
		}
	}
	resolveMap := func(value map[string]any, context map[string]map[string]any) map[string]any {
		if value == nil {
			return nil
		}
		return resolveAny(value, context).(map[string]any)
	}
	// Workflow env and defaults cannot refer to a job's matrix or runner.
	workflow.Env = resolveMap(workflow.Env, map[string]map[string]any{})
	if strings.Contains(workflow.Defaults.Run.Shell, "${{") || strings.Contains(workflow.Defaults.Run.WorkingDirectory, "${{") {
		job.Problems = append(job.Problems, "workflow defaults.run does not allow expressions; configure literal defaults explicitly")
	}
	context["env"] = setupWorkflowEnvironmentContext(workflow.Env)
	// GitHub's context availability table excludes env and runner from
	// runs-on, job env and the job timeout. Do not make invalid upstream
	// expressions runnable merely because a value is locally known.
	job.RunsOn = resolveAny(job.RunsOn, matrixContext)
	// Only the OS is determined by the literal label. Do not infer an
	// architecture, runner path, or any other provider-specific value.
	if runners := yamlStringList(job.RunsOn); len(runners) == 1 {
		os := ""
		switch runners[0] {
		case "ubuntu-latest", "ubuntu-22.04", "ubuntu-24.04":
			os = "Linux"
		case "windows-latest", "windows-2022", "windows-2025":
			os = "Windows"
		case "macos-latest", "macos-13", "macos-14", "macos-15":
			os = "macOS"
		}
		if os != "" {
			context["runner"] = map[string]any{"os": os}
		}
	}
	job.Env = resolveMap(job.Env, matrixContext)
	for key, value := range setupWorkflowEnvironmentContext(job.Env) {
		context["env"][key] = value
	}
	job.TimeoutMinutes = resolveAny(job.TimeoutMinutes, matrixContext)
	defaultsContext := map[string]map[string]any{"env": context["env"]}
	if matrix != nil {
		defaultsContext["matrix"] = matrix
	}
	// The upstream runner evaluates job defaults before initializing steps,
	// including defaults that every individual step overrides.
	job.Defaults.Run.Shell = resolve(job.Defaults.Run.Shell, defaultsContext)
	job.Defaults.Run.WorkingDirectory = resolve(job.Defaults.Run.WorkingDirectory, defaultsContext)
	// jobs.<job>.if runs before matrix expansion and cannot use runner/env.
	if expression := strings.TrimSpace(job.If); expression != "" {
		if result, err := setupWorkflowStaticCondition(expression, map[string]map[string]any{}); err == nil {
			job.StaticConditions = append(job.StaticConditions, repoWorkflowCondition{Scope: "job", Expression: expression, Line: job.IfLine, Supported: true, Translation: fmt.Sprintf("static condition is %t", result)})
			if result {
				job.If = ""
			} else {
				job.Problems = append(job.Problems, "job condition is statically false; this skipped upstream job requires explicit selection of a runnable scope")
			}
		}
	}
	steps := make([]repoWorkflowStepYAML, 0, len(job.Steps))
	for index, step := range job.Steps {
		// Invalid syntax is a workflow error even when the condition is false.
		// Conditional omission only removes a well-formed upstream step.
		job.Problems = append(job.Problems, step.Problems...)
		step.Env = resolveMap(step.Env, context)
		stepContext := make(map[string]map[string]any, len(context))
		for key, value := range context {
			stepContext[key] = value
		}
		stepContext["env"] = make(map[string]any, len(context["env"])+len(step.Env))
		for key, value := range context["env"] {
			stepContext["env"][key] = value
		}
		for key, value := range setupWorkflowEnvironmentContext(step.Env) {
			stepContext["env"][key] = value
		}
		if expression := strings.TrimSpace(step.If); expression != "" {
			if result, err := setupWorkflowStaticCondition(expression, stepContext); err == nil {
				translation := fmt.Sprintf("static condition is %t", result)
				if !result {
					translation += "; step is omitted from this selected job variant"
				}
				job.StaticConditions = append(job.StaticConditions, repoWorkflowCondition{Scope: "step", Expression: expression, Line: step.IfLine, Supported: true, Translation: translation})
				job.StaticRequirements = append(job.StaticRequirements, fmt.Sprintf("Upstream step %d condition %q is statically %t for this selected job variant.", index+1, expression, result))
				if !result {
					continue
				}
				step.If = ""
			}
		}
		step.Run, step.Shell, step.WorkingDirectory = resolve(step.Run, stepContext), resolve(step.Shell, stepContext), resolve(step.WorkingDirectory, stepContext)
		step.With = resolveMap(step.With, stepContext)
		step.TimeoutMinutes = resolveAny(step.TimeoutMinutes, stepContext)
		steps = append(steps, step)
		if step.Run != "" || step.Uses != "" {
			// Both actions and opaque repository scripts can export variables
			// for later steps, including overrides of workflow/job env. Do not
			// guess their values or their absence. A later step may establish
			// its own known values through its explicit env mapping.
			context["env"] = map[string]any{}
		}
	}
	job.Steps = steps
	if len(steps) == 0 {
		job.Problems = append(job.Problems, "static conditions leave no runnable upstream steps; configure a test scope explicitly")
	}
	job.Problems = uniqueStrings(job.Problems)
	return workflow, job
}

func setupWorkflowEnvironmentContext(environment map[string]any) map[string]any {
	context := map[string]any{}
	for key, value := range environment {
		if literal, ok := literalWorkflowEnvironmentValue(value); ok {
			// GitHub environment context values are strings, even when YAML
			// used a boolean or number. Matrix context values retain their type.
			context[key] = literal
		}
	}
	return context
}

func setupWorkflowSupportedUbuntuRunner(label string) bool {
	switch label {
	case "ubuntu-latest", "ubuntu-22.04", "ubuntu-24.04":
		return true
	default:
		return false
	}
}

func setupWorkflowStaticCondition(expression string, context map[string]map[string]any) (bool, error) {
	expression = strings.TrimSpace(expression)
	if strings.HasPrefix(expression, "${{") && strings.HasSuffix(expression, "}}") {
		expression = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(expression, "${{"), "}}"))
	}
	// Express truthiness through the same evaluator as operators, avoiding a
	// second set of coercion rules in condition handling.
	value, err := setupEvaluateWorkflowExpression("!!("+expression+")", context)
	if err != nil {
		return false, err
	}
	result, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("condition did not produce a static boolean")
	}
	return result, nil
}
