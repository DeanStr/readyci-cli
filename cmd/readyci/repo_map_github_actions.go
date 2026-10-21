package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func scanRepoGitHubActions(cwd string, runtimePins ...map[string]string) repoGitHubActionsMap {
	var out repoGitHubActionsMap
	root := filepath.Join(cwd, ".github", "workflows")
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			// GitHub supports workflow files directly in this directory;
			// nested YAML may be support data rather than execution scope.
			if path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".yml") && !strings.HasSuffix(entry.Name(), ".yaml") {
			return nil
		}
		workflow := scanWorkflowFile(cwd, path, runtimePins...)
		out.Workflows = append(out.Workflows, workflow)
		return nil
	})
	sort.Slice(out.Workflows, func(i, j int) bool { return out.Workflows[i].Path < out.Workflows[j].Path })
	return out
}

func scanWorkflowFile(cwd, path string, runtimePins ...map[string]string) repoWorkflowMap {
	rel := relPath(cwd, path)
	data, err := os.ReadFile(path)
	if err != nil {
		return repoWorkflowMap{Path: rel, Problems: []string{"workflow cannot be read; its test scope requires explicit configuration"}}
	}
	var parsed repoWorkflowYAML
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return repoWorkflowMap{Path: rel, Problems: []string{"workflow YAML cannot be parsed; its test scope requires explicit configuration: " + err.Error()}}
	}
	jobNames := make([]string, 0, len(parsed.Jobs))
	for name := range parsed.Jobs {
		if !setupWorkflowMatrixKey.MatchString(name) {
			parsed.Problems = append(parsed.Problems, "workflow job identifiers must use letters, digits, underscores, or hyphens and begin with a letter or underscore")
		}
		jobNames = append(jobNames, name)
	}
	workflow := repoWorkflowMap{Path: rel, Env: workflowLiteralEnvironmentProjection(parsed.Env), Problems: uniqueStrings(append([]string(nil), parsed.Problems...))}
	if len(parsed.Jobs) == 0 {
		workflow.Problems = append(workflow.Problems, "workflow defines no inspectable jobs; its test scope requires explicit configuration")
	}
	sort.Strings(jobNames)
	for _, name := range jobNames {
		parsedJob := parsed.Jobs[name]
		job := scanWorkflowJob(cwd, parsed, name, parsedJob, runtimePins...)
		job.ID = "github-actions:" + rel + ":" + name
		workflow.Jobs = append(workflow.Jobs, job)
		if parsedJob.Strategy != nil {
			variants, problems := setupWorkflowMatrixVariants(parsed, parsedJob)
			workflow.Jobs[len(workflow.Jobs)-1].Unsupported = uniqueStrings(append(workflow.Jobs[len(workflow.Jobs)-1].Unsupported, problems...))
			workflow.Jobs[len(workflow.Jobs)-1].ScopeUnknown = job.ScopeUnknown || len(problems) > 0
			for _, variant := range variants {
				child := scanWorkflowJob(cwd, variant.Workflow, name, variant.Job, runtimePins...)
				child.ID = job.ID + ":matrix:" + variant.Digest
				child.ParentJobID, child.Matrix = job.ID, variant.Values
				child.Requirements = append(child.Requirements, "This selects one explicit upstream matrix variant; sibling variants and upstream matrix scheduling are not imported.")
				workflow.Jobs = append(workflow.Jobs, child)
			}
		}
	}
	if len(workflow.Env) == 0 {
		workflow.Env = nil
	}
	return workflow
}

func scanWorkflowJob(cwd string, workflow repoWorkflowYAML, name string, parsedJob repoWorkflowJobYAML, runtimePins ...map[string]string) repoWorkflowJob {
	if parsedJob.Strategy == nil && !parsedJob.StaticResolved {
		workflow, parsedJob = setupWorkflowStaticJob(workflow, parsedJob, nil)
	}
	job := repoWorkflowJob{Name: name, RunsOn: yamlStringList(parsedJob.RunsOn), Needs: yamlStringList(parsedJob.Needs), ScopeUnknown: len(workflow.Problems) > 0 || len(parsedJob.Problems) > 0 || parsedJob.Uses != ""}
	job.Requirements, job.Unsupported = workflowImportRequirements(workflow, parsedJob)
	job.Unsupported = append(job.Unsupported, workflow.Problems...)
	job.Unsupported = append(job.Unsupported, parsedJob.Problems...)
	var problems []string
	job.Timeout, problems = staticWorkflowTimeout(parsedJob.TimeoutMinutes, "job")
	job.Unsupported = append(job.Unsupported, problems...)
	job.RuntimeRequirements, job.RuntimeResolutions, problems = setupWorkflowRuntimeRequirements(cwd, parsedJob, runtimePins...)
	job.Unsupported = append(job.Unsupported, problems...)
	job.Requirements = append(job.Requirements, setupRuntimePinNotices(job.RuntimeResolutions)...)
	if len(runtimePins) > 0 {
		for runtime, pin := range runtimePins[0] {
			if job.RuntimeRequirements[runtime] != "" {
				if job.RuntimePins == nil {
					job.RuntimePins = map[string]string{}
				}
				job.RuntimePins[runtime] = pin
			}
		}
		job.Requirements = append(job.Requirements, setupUserRuntimePinNotices(job.RuntimePins)...)
	}
	job.AutomaticUnsupported = workflowAutomaticImportRequirements(parsedJob)
	job.Requirements = append(job.Requirements, parsedJob.StaticRequirements...)
	job.Conditions = append(job.Conditions, parsedJob.StaticConditions...)
	if condition := githubActionsConditionReport("job", parsedJob.If, parsedJob.IfLine); condition.Expression != "" {
		job.Conditions = append(job.Conditions, condition)
	}
	for _, step := range parsedJob.Steps {
		job.ScopeUnknown = job.ScopeUnknown || len(step.Problems) > 0
		// Hidden action implementations and provider substitutions can contain
		// test commands even when the outer job has a non-test name.
		if strings.Contains(step.Run, "${{") || strings.Contains(step.Run, "GITHUB_") || strings.Contains(step.Run, "RUNNER_") {
			job.ScopeUnknown = true
		}
		job.Unsupported = append(job.Unsupported, step.Problems...)
		switch action, _, _ := strings.Cut(step.Uses, "@"); action {
		case "actions/setup-go":
			job.Runtimes = appendUniqueString(job.Runtimes, "go")
		case "actions/setup-node", "pnpm/action-setup":
			job.Runtimes = appendUniqueString(job.Runtimes, "node")
		case "actions/setup-python":
			job.Runtimes = appendUniqueString(job.Runtimes, "python")
		case "", "actions/checkout", "actions/cache":
		default:
			job.ScopeUnknown = true
		}
		if condition := githubActionsConditionReport("step", step.If, step.IfLine); condition.Expression != "" {
			job.Conditions = append(job.Conditions, condition)
		}
		command := strings.TrimSpace(step.Run)
		if command == "" {
			continue
		}
		defaults := effectiveWorkflowRunDefaults(workflow, parsedJob, step)
		env, envProblems := effectiveWorkflowEnvironment(workflow, parsedJob, step)
		timeout, timeoutProblems := staticWorkflowTimeout(step.TimeoutMinutes, "step")
		job.Unsupported = append(job.Unsupported, envProblems...)
		job.Unsupported = append(job.Unsupported, timeoutProblems...)
		job.Commands = append(job.Commands, commandWithWorkingDirectory(command, defaults.WorkingDirectory))
		job.CommandLines = append(job.CommandLines, step.RunLine)
		job.RunSteps = append(job.RunSteps, repoWorkflowRunStep{Command: step.Run, Line: step.RunLine, Shell: defaults.Shell, WorkingDirectory: defaults.WorkingDirectory, Env: env, Timeout: timeout})
	}
	job.Unsupported = uniqueStrings(job.Unsupported)
	return job
}

type repoWorkflowYAML struct {
	Env      map[string]any                 `yaml:"env"`
	Defaults repoWorkflowDefaultsYAML       `yaml:"defaults"`
	Jobs     map[string]repoWorkflowJobYAML `yaml:"jobs"`
	Problems []string                       `yaml:"-"`
}

func (workflow *repoWorkflowYAML) UnmarshalYAML(value *yaml.Node) error {
	type plain repoWorkflowYAML
	var decoded plain
	if err := value.Decode(&decoded); err != nil {
		return err
	}
	*workflow = repoWorkflowYAML(decoded)
	workflow.Problems = setupWorkflowUnknownFields(value, "workflow", "name", "run-name", "on", "permissions", "env", "defaults", "concurrency", "jobs")
	workflow.Problems = append(workflow.Problems, setupWorkflowDefaultProblems(value)...)
	return nil
}

type repoWorkflowJobYAML struct {
	RunsOn             any                      `yaml:"runs-on"`
	Needs              any                      `yaml:"needs"`
	If                 string                   `yaml:"if"`
	IfLine             int                      `yaml:"-"`
	Defaults           repoWorkflowDefaultsYAML `yaml:"defaults"`
	Steps              []repoWorkflowStepYAML   `yaml:"steps"`
	Env                map[string]any           `yaml:"env"`
	Services           map[string]any           `yaml:"services"`
	Container          any                      `yaml:"container"`
	Strategy           any                      `yaml:"strategy"`
	Uses               string                   `yaml:"uses"`
	Outputs            map[string]any           `yaml:"outputs"`
	ContinueOnError    any                      `yaml:"continue-on-error"`
	TimeoutMinutes     any                      `yaml:"timeout-minutes"`
	Environment        any                      `yaml:"environment"`
	StrategyNode       *yaml.Node               `yaml:"-"`
	Problems           []string                 `yaml:"-"`
	StaticResolved     bool                     `yaml:"-"`
	StaticRequirements []string                 `yaml:"-"`
	StaticConditions   []repoWorkflowCondition  `yaml:"-"`
}

func (job *repoWorkflowJobYAML) UnmarshalYAML(value *yaml.Node) error {
	type plain repoWorkflowJobYAML
	var decoded plain
	if err := value.Decode(&decoded); err != nil {
		return err
	}
	*job = repoWorkflowJobYAML(decoded)
	job.Problems = setupWorkflowUnknownFields(value, "job", "name", "runs-on", "needs", "if", "defaults", "steps", "env", "services", "container", "strategy", "uses", "with", "secrets", "outputs", "continue-on-error", "timeout-minutes", "environment", "permissions", "concurrency")
	job.Problems = append(job.Problems, setupWorkflowDefaultProblems(value)...)
	if len(job.Steps) == 0 && job.Uses == "" {
		job.Problems = append(job.Problems, "workflow job has no inspectable steps or reusable workflow; its test scope requires explicit configuration")
	}
	if value.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(value.Content); i += 2 {
		key := value.Content[i]
		val := value.Content[i+1]
		if key.Value == "strategy" {
			job.StrategyNode = val
		}
		if key.Value == "if" && val.Kind == yaml.ScalarNode {
			job.IfLine = val.Line
		}
	}
	return nil
}

type repoWorkflowDefaultsYAML struct {
	Run repoWorkflowRunDefaultsYAML `yaml:"run"`
}

type repoWorkflowRunDefaultsYAML struct {
	WorkingDirectory string `yaml:"working-directory"`
	Shell            string `yaml:"shell"`
}

type repoWorkflowStepYAML struct {
	Run              string
	RunLine          int
	If               string
	IfLine           int
	WorkingDirectory string `yaml:"working-directory"`
	Uses             string
	With             map[string]any
	Env              map[string]any
	Shell            string
	ContinueOnError  bool
	TimeoutMinutes   any
	Problems         []string
}

func (step *repoWorkflowStepYAML) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("workflow step must be a mapping")
	}
	var metadata struct {
		Uses            string         `yaml:"uses"`
		With            map[string]any `yaml:"with"`
		Env             map[string]any `yaml:"env"`
		Shell           string         `yaml:"shell"`
		ContinueOnError any            `yaml:"continue-on-error"`
		TimeoutMinutes  any            `yaml:"timeout-minutes"`
	}
	if err := value.Decode(&metadata); err != nil {
		return err
	}
	step.Uses, step.With, step.Env, step.Shell = metadata.Uses, metadata.With, metadata.Env, metadata.Shell
	step.ContinueOnError = metadata.ContinueOnError != nil && metadata.ContinueOnError != false
	step.TimeoutMinutes = metadata.TimeoutMinutes
	step.Problems = setupWorkflowUnknownFields(value, "step", "id", "name", "run", "if", "working-directory", "uses", "with", "env", "shell", "continue-on-error", "timeout-minutes")
	for i := 0; i+1 < len(value.Content); i += 2 {
		key := value.Content[i]
		val := value.Content[i+1]
		switch key.Value {
		case "run":
			if val.Kind == yaml.ScalarNode && val.Tag == "!!str" {
				step.Run = val.Value
				step.RunLine = val.Line
				if val.Style == yaml.LiteralStyle || val.Style == yaml.FoldedStyle {
					step.RunLine++
				}
			} else {
				step.Problems = append(step.Problems, "step run must be a literal command string")
			}
		case "if":
			if val.Kind == yaml.ScalarNode {
				step.If = val.Value
				step.IfLine = val.Line
			} else {
				step.Problems = append(step.Problems, "step conditions require a scalar expression and explicit selection rules")
			}
		case "working-directory":
			if val.Kind == yaml.ScalarNode && val.Tag == "!!str" {
				step.WorkingDirectory = val.Value
			} else {
				step.Problems = append(step.Problems, "step working-directory must be a literal repository-relative string")
			}
		}
	}
	if (step.Uses == "") == (strings.TrimSpace(step.Run) == "") {
		step.Problems = append(step.Problems, "each workflow step must declare exactly one run command or action")
	}
	if step.Run != "" && len(step.With) > 0 {
		step.Problems = append(step.Problems, "run steps cannot declare action inputs; configure the prerequisite explicitly")
	}
	if step.Uses != "" && (step.Shell != "" || step.WorkingDirectory != "") {
		step.Problems = append(step.Problems, "action steps cannot declare a run shell or working directory; configure the prerequisite explicitly")
	}
	return nil
}

func commandWithWorkingDirectory(command, workingDir string) string {
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" || workingDir == "." {
		return command
	}
	if strings.HasPrefix(workingDir, "-") {
		workingDir = "./" + workingDir
	}
	return "cd " + shellQuoteArg(workingDir) + " || exit\n" + command
}

func yamlStringList(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return parseInlineList(typed)
	case []any:
		var out []string
		for _, item := range typed {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" {
				out = append(out, text)
			}
		}
		return uniqueStrings(out)
	case map[string]any:
		return yamlStringListMap(typed)
	default:
		text := strings.TrimSpace(fmt.Sprint(typed))
		if text == "" {
			return nil
		}
		return []string{text}
	}
}

func yamlStringListMap(values map[string]any) []string {
	if len(values) == 0 {
		return nil
	}
	var out []string
	if group := strings.TrimSpace(fmt.Sprint(values["group"])); group != "" && group != "<nil>" {
		out = append(out, "group:"+group)
	}
	for _, label := range yamlStringList(values["labels"]) {
		out = append(out, "label:"+label)
	}
	if len(out) > 0 {
		return uniqueStrings(out)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		text := strings.TrimSpace(fmt.Sprint(values[key]))
		if text != "" && text != "<nil>" {
			out = append(out, key+":"+text)
		}
	}
	return uniqueStrings(out)
}

func checkReferencesFromGitHubActions(actions repoGitHubActionsMap) []repoCheckReference {
	var refs []repoCheckReference
	for _, workflow := range actions.Workflows {
		for _, job := range workflow.Jobs {
			for _, step := range job.RunSteps {
				commandRefs := scanCheckReferencesInText(step.Command, "github-actions", workflow.Path, false)
				if step.Line > 0 {
					for refIndex := range commandRefs {
						if commandRefs[refIndex].Line > 0 {
							commandRefs[refIndex].Line = step.Line + commandRefs[refIndex].Line - 1
						}
					}
				} else {
					for refIndex := range commandRefs {
						commandRefs[refIndex].Line = 0
					}
				}
				refs = append(refs, commandRefs...)
			}
		}
	}
	return cleanCheckReferences(refs)
}

func makeReferencesFromGitHubActions(actions repoGitHubActionsMap) []repoMakeReference {
	var refs []repoMakeReference
	for _, workflow := range actions.Workflows {
		for _, job := range workflow.Jobs {
			for _, command := range job.Commands {
				for _, invocation := range makeInvocationsFromCommand(command) {
					refs = append(refs, repoMakeReference{
						Target:  invocation.Target,
						Dir:     invocation.Dir,
						Source:  "github-actions",
						Path:    workflow.Path,
						Context: "job " + job.Name + " runs make " + invocation.Display(),
					})
				}
			}
		}
	}
	return cleanMakeReferences(refs)
}

func githubActionsConditionReport(scope, expression string, line int) repoWorkflowCondition {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return repoWorkflowCondition{}
	}
	supported, translation := classifyGitHubActionsCondition(expression)
	return repoWorkflowCondition{
		Scope:       scope,
		Expression:  expression,
		Line:        line,
		Supported:   supported,
		Translation: translation,
	}
}

func classifyGitHubActionsCondition(expression string) (bool, string) {
	expr := unwrapGitHubActionsExpression(expression)
	if expr == "success()" {
		return true, "when.status: success"
	}
	if lhs, rhs, ok := splitGitHubActionsEquality(expr); ok {
		switch lhs {
		case "github.event_name":
			return true, "when.event: " + rhs
		case "github.ref":
			if branch, ok := strings.CutPrefix(rhs, "refs/heads/"); ok {
				return true, "when.branch: " + branch
			}
			if tag, ok := strings.CutPrefix(rhs, "refs/tags/"); ok {
				return true, "when.tag: " + tag
			}
		default:
			if envName, ok := strings.CutPrefix(lhs, "env."); ok && validServiceEnvKey(envName) {
				return true, "when.env." + envName + ": " + rhs
			}
		}
	}
	if arg, ok := gitHubActionsFunctionArgument(expr, "startsWith", "github.ref"); ok {
		if branch, ok := strings.CutPrefix(arg, "refs/heads/"); ok {
			return true, "when.branch: " + branch + "*"
		}
		if tag, ok := strings.CutPrefix(arg, "refs/tags/"); ok {
			return true, "when.tag: " + tag + "*"
		}
	}
	return false, "manual translation required"
}

func unwrapGitHubActionsExpression(expression string) string {
	expr := strings.TrimSpace(expression)
	if strings.HasPrefix(expr, "${{") && strings.HasSuffix(expr, "}}") {
		expr = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(expr, "${{"), "}}"))
	}
	return expr
}

func splitGitHubActionsEquality(expression string) (string, string, bool) {
	parts := strings.Split(expression, "==")
	if len(parts) != 2 {
		return "", "", false
	}
	lhs := strings.TrimSpace(parts[0])
	rhs := trimGitHubActionsStringLiteral(parts[1])
	if lhs == "" || rhs == "" {
		return "", "", false
	}
	return lhs, rhs, true
}

func gitHubActionsFunctionArgument(expression, functionName, firstArg string) (string, bool) {
	prefix := functionName + "("
	if !strings.HasPrefix(expression, prefix) || !strings.HasSuffix(expression, ")") {
		return "", false
	}
	args := strings.TrimSuffix(strings.TrimPrefix(expression, prefix), ")")
	left, right, ok := strings.Cut(args, ",")
	if !ok || strings.TrimSpace(left) != firstArg {
		return "", false
	}
	right = trimGitHubActionsStringLiteral(right)
	return right, right != ""
}

func trimGitHubActionsStringLiteral(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		quote := value[0]
		if (quote == '\'' || quote == '"') && value[len(value)-1] == quote {
			return strings.TrimSpace(value[1 : len(value)-1])
		}
	}
	return ""
}
