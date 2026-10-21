package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

// GitHub evaluates environment scopes separately from the run shell. Preserve
// literal values verbatim, including empty strings, rather than expanding shell
// syntax or resolving provider expressions while importing the workflow.
func effectiveWorkflowEnvironment(workflow repoWorkflowYAML, job repoWorkflowJobYAML, step repoWorkflowStepYAML) (map[string]string, []string) {
	environment := map[string]string{"CI": "true"}
	var problems []string
	for _, scope := range []struct {
		name   string
		values map[string]any
	}{{"workflow", workflow.Env}, {"job", job.Env}, {"step", step.Env}} {
		keys := make([]string, 0, len(scope.values))
		for key := range scope.values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value, ok := literalWorkflowEnvironmentValue(scope.values[key])
			if !ok {
				problems = append(problems, scope.name+" environment variable "+key+" requires a literal value; provider expressions, secrets, and structured values need manual translation")
				continue
			}
			if strings.HasPrefix(key, "GITHUB_") || strings.HasPrefix(key, "RUNNER_") {
				problems = append(problems, scope.name+" environment variable "+key+" is reserved by GitHub Actions and requires manual translation")
				continue
			}
			if err := preflightapi.ValidateRunSteps([]preflightapi.RunStepSpec{{Name: "environment", Command: "true", Env: map[string]string{key: value}}}); err != nil {
				problems = append(problems, scope.name+" environment requires explicit configuration: "+err.Error())
				continue
			}
			environment[key] = value
		}
	}
	if len(environment) > preflightapi.MaxRunStepEnvVars {
		problems = append(problems, fmt.Sprintf("effective step environment exceeds Ready CI's limit of %d variables; configure it explicitly", preflightapi.MaxRunStepEnvVars))
	}
	return environment, uniqueStrings(problems)
}

func literalWorkflowEnvironmentValue(value any) (string, bool) {
	var literal string
	switch value := value.(type) {
	case nil:
		// A YAML null environment value is an explicitly empty string, not
		// a missing provider value or a request to inherit the host variable.
		literal = ""
	case string:
		literal = value
	case bool:
		literal = strconv.FormatBool(value)
	case int:
		literal = strconv.Itoa(value)
	case int64:
		literal = strconv.FormatInt(value, 10)
	case uint64:
		literal = strconv.FormatUint(value, 10)
	case float64:
		if math.IsInf(value, 0) || math.IsNaN(value) {
			return "", false
		}
		literal = strconv.FormatFloat(value, 'g', -1, 64)
	default:
		return "", false
	}
	return literal, !strings.Contains(literal, "${{")
}

func workflowLiteralEnvironmentProjection(values map[string]any) map[string]string {
	var environment map[string]string
	for key, value := range values {
		literal, ok := literalWorkflowEnvironmentValue(value)
		if !ok {
			continue
		}
		if environment == nil {
			environment = make(map[string]string, len(values))
		}
		environment[key] = literal
	}
	return environment
}

func staticWorkflowTimeout(value any, scope string) (string, []string) {
	if value == nil {
		return "", nil
	}
	var minutes int64
	valid := true
	switch value := value.(type) {
	case int:
		minutes = int64(value)
	case int64:
		minutes = value
	case uint64:
		valid = value <= uint64(preflightapi.MaxTimeoutSeconds/60)
		if valid {
			minutes = int64(value)
		}
	case string:
		valid = value != ""
		for _, character := range value {
			valid = valid && character >= '0' && character <= '9'
		}
		var err error
		minutes, err = strconv.ParseInt(value, 10, 64)
		valid = valid && err == nil
	default:
		valid = false
	}
	if !valid || minutes <= 0 || minutes > preflightapi.MaxTimeoutSeconds/60 {
		return "", []string{scope + " timeout-minutes must be a positive static integer within Ready CI's duration limit; configure the timeout explicitly"}
	}
	return strconv.FormatInt(minutes, 10) + "m", nil
}

func importedWorkflowStepCommand(step repoWorkflowRunStep, withEnvironment bool) string {
	// Its unspecified shell omits pipefail; an explicit bash enables it.
	shell := "bash -e -c "
	if step.Shell == "bash" {
		shell = "bash --noprofile --norc -eo pipefail -c "
	}
	command := commandWithWorkingDirectory(step.Command, step.WorkingDirectory)
	if !withEnvironment {
		return shell + shellQuoteArg(command)
	}
	environment := map[string]string{"CI": "true"}
	for key, value := range step.Env {
		environment[key] = value
	}
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var assignments []string
	for _, key := range keys {
		assignments = append(assignments, key+"="+shellQuoteArg(environment[key]))
	}
	return strings.Join(assignments, " ") + " " + shell + shellQuoteArg(command)
}

func importedWorkflowSteps(runSteps []repoWorkflowRunStep) []config.StepConfig {
	steps := make([]config.StepConfig, 0, len(runSteps))
	for index, step := range runSteps {
		environment := map[string]string{"CI": "true"}
		for key, value := range step.Env {
			environment[key] = value
		}
		steps = append(steps, config.StepConfig{
			Name: fmt.Sprintf("step-%d", index+1), Command: importedWorkflowStepCommand(step, false),
			Env: environment, Timeout: step.Timeout,
		})
	}
	return steps
}
