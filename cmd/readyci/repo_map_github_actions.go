package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func scanRepoGitHubActions(cwd string) repoGitHubActionsMap {
	var out repoGitHubActionsMap
	root := filepath.Join(cwd, ".github", "workflows")
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".yml") && !strings.HasSuffix(entry.Name(), ".yaml") {
			return nil
		}
		workflow := scanWorkflowFile(cwd, path)
		out.Workflows = append(out.Workflows, workflow)
		return nil
	})
	sort.Slice(out.Workflows, func(i, j int) bool { return out.Workflows[i].Path < out.Workflows[j].Path })
	return out
}

func scanWorkflowFile(cwd, path string) repoWorkflowMap {
	rel := relPath(cwd, path)
	data, err := os.ReadFile(path)
	if err != nil {
		return repoWorkflowMap{Path: rel}
	}
	var parsed repoWorkflowYAML
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return repoWorkflowMap{Path: rel}
	}
	workflow := repoWorkflowMap{Path: rel, Env: yamlStringMap(parsed.Env)}
	jobNames := make([]string, 0, len(parsed.Jobs))
	for name := range parsed.Jobs {
		jobNames = append(jobNames, name)
	}
	sort.Strings(jobNames)
	for _, name := range jobNames {
		parsedJob := parsed.Jobs[name]
		job := repoWorkflowJob{
			Name:   name,
			RunsOn: yamlStringList(parsedJob.RunsOn),
			Needs:  yamlStringList(parsedJob.Needs),
		}
		job.Requirements, job.Unsupported = workflowImportRequirements(parsed, parsedJob)
		if condition := githubActionsConditionReport("job", parsedJob.If, parsedJob.IfLine); condition.Expression != "" {
			job.Conditions = append(job.Conditions, condition)
		}
		for _, step := range parsedJob.Steps {
			switch action, _, _ := strings.Cut(step.Uses, "@"); action {
			case "actions/setup-go":
				job.Runtimes = appendUniqueString(job.Runtimes, "go")
			case "actions/setup-node", "pnpm/action-setup":
				job.Runtimes = appendUniqueString(job.Runtimes, "node")
			}
			if condition := githubActionsConditionReport("step", step.If, step.IfLine); condition.Expression != "" {
				job.Conditions = append(job.Conditions, condition)
			}
			command := step.Run
			command = strings.TrimSpace(command)
			if command != "" {
				defaults := effectiveWorkflowRunDefaults(parsed, parsedJob, step)
				job.Commands = append(job.Commands, commandWithWorkingDirectory(command, defaults.WorkingDirectory))
				job.CommandLines = append(job.CommandLines, step.RunLine)
				job.RunSteps = append(job.RunSteps, repoWorkflowRunStep{
					Command: step.Run, Line: step.RunLine, Shell: defaults.Shell, WorkingDirectory: defaults.WorkingDirectory,
				})
			}
		}
		workflow.Jobs = append(workflow.Jobs, job)
	}
	if len(workflow.Env) == 0 {
		workflow.Env = nil
	}
	return workflow
}

type repoWorkflowYAML struct {
	Env      map[string]any                 `yaml:"env"`
	Defaults repoWorkflowDefaultsYAML       `yaml:"defaults"`
	Jobs     map[string]repoWorkflowJobYAML `yaml:"jobs"`
}

type repoWorkflowJobYAML struct {
	RunsOn    any                      `yaml:"runs-on"`
	Needs     any                      `yaml:"needs"`
	If        string                   `yaml:"if"`
	IfLine    int                      `yaml:"-"`
	Defaults  repoWorkflowDefaultsYAML `yaml:"defaults"`
	Steps     []repoWorkflowStepYAML   `yaml:"steps"`
	Env       map[string]any           `yaml:"env"`
	Services  map[string]any           `yaml:"services"`
	Container any                      `yaml:"container"`
	Strategy  any                      `yaml:"strategy"`
	Uses      string                   `yaml:"uses"`
	Outputs   map[string]any           `yaml:"outputs"`
}

func (job *repoWorkflowJobYAML) UnmarshalYAML(value *yaml.Node) error {
	type plain repoWorkflowJobYAML
	var decoded plain
	if err := value.Decode(&decoded); err != nil {
		return err
	}
	*job = repoWorkflowJobYAML(decoded)
	if value.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(value.Content); i += 2 {
		key := value.Content[i]
		val := value.Content[i+1]
		if key.Value == "if" && val.Kind == yaml.ScalarNode {
			job.IfLine = val.Line
			break
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
}

func (step *repoWorkflowStepYAML) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return nil
	}
	var metadata struct {
		Uses            string         `yaml:"uses"`
		With            map[string]any `yaml:"with"`
		Env             map[string]any `yaml:"env"`
		Shell           string         `yaml:"shell"`
		ContinueOnError any            `yaml:"continue-on-error"`
	}
	if err := value.Decode(&metadata); err != nil {
		return err
	}
	step.Uses, step.With, step.Env, step.Shell = metadata.Uses, metadata.With, metadata.Env, metadata.Shell
	step.ContinueOnError = metadata.ContinueOnError != nil && metadata.ContinueOnError != false
	for i := 0; i+1 < len(value.Content); i += 2 {
		key := value.Content[i]
		val := value.Content[i+1]
		switch key.Value {
		case "run":
			if val.Kind == yaml.ScalarNode {
				step.Run = val.Value
				step.RunLine = val.Line
				if val.Style == yaml.LiteralStyle || val.Style == yaml.FoldedStyle {
					step.RunLine++
				}
			}
		case "if":
			if val.Kind == yaml.ScalarNode {
				step.If = val.Value
				step.IfLine = val.Line
			}
		case "working-directory":
			if val.Kind == yaml.ScalarNode {
				step.WorkingDirectory = val.Value
			}
		}
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

func yamlStringMap(values map[string]any) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range values {
		if key == "" {
			continue
		}
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" {
			out[key] = text
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
