package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"mvdan.cc/sh/v3/syntax"
)

const setupPythonVenvPreparation = `__readyci_venv=$(mktemp -d "${TMPDIR:-/tmp}/readyci-venv.XXXXXXXX") && python3 -m venv "$__readyci_venv" && export VIRTUAL_ENV="$__readyci_venv" && export PATH="$VIRTUAL_ENV/bin:$PATH"`

type setupMakeSelection struct {
	File, Directory, Target string
}

type setupMakeRule struct {
	Recipes       []string
	Prerequisites []string
	Unsupported   bool
}

func setupPrepareDetectedCheck(root string, check detectedCheck) detectedCheck {
	for _, runtime := range setupCommandRuntimes(root, check.Command) {
		check.Runtimes = appendUniqueString(check.Runtimes, runtime)
	}
	// A venv supplies both the python and pip entrypoints without changing the
	// selected upstream command or installing into the runner's system Python.
	if containsString(check.Runtimes, "python") {
		check.Prepare = append(check.Prepare, setupPythonVenvPreparation)
		if !setupCommandHasPythonInstall(check.Command) {
			for _, selected := range setupMakeSelections(root, check.Command) {
				commands, _ := setupMakeDependencyPreparation(root, selected)
				for _, command := range commands {
					check.Prepare = appendUniqueString(check.Prepare, command)
				}
			}
		}
	}
	commands := append([]string{check.Command}, check.Prepare...)
	var requiredRuntimes []string
	for runtime := range check.RuntimeRequirements {
		requiredRuntimes = append(requiredRuntimes, runtime)
	}
	declared, err := setupDeclaredRuntimeRequirementsForSelection(root, commands, check.RuntimeRequirements, requiredRuntimes...)
	if err == nil {
		var merged map[string]string
		merged, err = preflightapi.MergeRuntimeRequirements(check.RuntimeRequirements, declared)
		if err == nil {
			check.RuntimeRequirements = merged
		}
	}
	if err != nil {
		check.PreparationReview = append(check.PreparationReview, "Runtime configuration required: "+err.Error())
	}
	return check
}

// setupCommandRuntimes inspects literal commands, selected Make recipes and
// invoked package scripts. It never executes repository code.
func setupCommandRuntimes(root, command string) []string {
	var runtimes []string
	commands, _ := setupRuntimeCommands(root, []string{command})
	for _, entry := range commands {
		if !entry.InferRuntimes {
			continue
		}
		for _, runtime := range setupDirectCommandRuntimes(entry.Command) {
			runtimes = appendUniqueString(runtimes, runtime)
		}
	}
	return runtimes
}

func setupMakeSelectionCommands(root string, selected setupMakeSelection) []string {
	rules := setupMakeRules(root, selected.File)
	seen := map[string]bool{}
	var commands []string
	var inspect func(string)
	inspect = func(target string) {
		if seen[target] || len(seen) >= 64 {
			return
		}
		seen[target] = true
		rule, ok := rules[target]
		if !ok {
			return
		}
		for _, recipe := range rule.Recipes {
			commands = append(commands, commandWithWorkingDirectory(recipeWithoutMakePrefix(recipe), selected.Directory))
		}
		for _, prerequisite := range rule.Prerequisites {
			inspect(prerequisite)
		}
	}
	inspect(selected.Target)
	return commands
}

func setupDirectCommandRuntimes(command string) []string {
	var runtimes []string
	for _, call := range setupExpandedShellCommands(command) {
		args := executableShellCommandFromCommand(call).Args
		if len(args) == 0 || args[0].Dynamic {
			continue
		}
		for _, runtime := range setupExecutableRuntimes(args[0].Text) {
			runtimes = appendUniqueString(runtimes, runtime)
		}
	}
	return runtimes
}

func setupExecutableRuntimes(executable string) []string {
	name := filepath.Base(normalizeCommandInvocationToken(executable))
	switch name {
	case "go", "node", "dotnet":
		return []string{name}
	case "vitest", "jest", "mocha", "ava", "playwright":
		return []string{"node"}
	case "npm", "npx":
		return []string{"node", "npm"}
	case "pnpm", "yarn":
		return []string{"node", name}
	case "bun", "bunx":
		return []string{"bun"}
	case "cargo":
		return []string{"rust", "cargo"}
	case "rustc":
		return []string{"rust"}
	case "python", "python3", "pip", "pip3", "pytest", "py.test", "tox", "nox":
		return []string{"python"}
	case "ruby", "bundle", "bundler", "rake":
		return []string{"ruby"}
	case "java", "javac", "mvn", "mvnw", "gradle", "gradlew":
		return []string{"java"}
	default:
		return nil
	}
}

// A declared preparation target is evidence of dependencies, not permission to
// run arbitrary setup code. Unsupported or ambiguous declarations stay visible
// as blockers in setup's environment preview.
func setupDependencyReviewRequirements(root string, commands []string, preparation ...string) []string {
	// A second execution step is still a test command, not preparation. Only
	// explicitly declared preparation or a recognized install suppresses Make
	// dependency inference; neither suppresses validation of Make arguments.
	explicitPreparation := false
	for _, command := range preparation {
		explicitPreparation = explicitPreparation || strings.TrimSpace(command) != "" && command != setupPythonVenvPreparation
	}
	for _, command := range commands {
		explicitPreparation = explicitPreparation || setupCommandHasPythonInstall(command)
	}
	var requirements []string
	for _, command := range commands {
		selections, problems := setupInspectMakeSelections(root, command)
		for _, problem := range problems {
			requirements = appendUniqueString(requirements, problem)
		}
		if explicitPreparation {
			continue
		}
		if !containsString(setupCommandRuntimes(root, command), "python") {
			continue
		}
		for _, selected := range selections {
			_, problems := setupMakeDependencyPreparation(root, selected)
			for _, problem := range problems {
				requirements = appendUniqueString(requirements, problem)
			}
		}
	}
	return requirements
}

func setupCommandHasPythonInstall(command string) bool {
	for _, call := range setupExpandedShellCommands(command) {
		args := executableShellCommandFromCommand(call).Args
		if len(args) < 2 || args[0].Dynamic {
			continue
		}
		if (args[0].Text == "python" || args[0].Text == "python3") && len(args) >= 4 && !args[1].Dynamic && args[1].Text == "-m" && !args[2].Dynamic && args[2].Text == "pip" {
			args = args[2:]
		}
		if (args[0].Text == "pip" || args[0].Text == "pip3") && !args[1].Dynamic && args[1].Text == "install" {
			return true
		}
	}
	return false
}

func setupMakeDependencyPreparation(root string, selected setupMakeSelection) ([]string, []string) {
	rules := setupMakeRules(root, selected.File)
	seen := map[string]bool{}
	var inspect func(string) string
	inspect = func(target string) string {
		if seen[target] {
			return ""
		}
		if len(seen) >= 64 {
			return selected.File + " prerequisite graph exceeds the static inspection limit; configure preparation explicitly"
		}
		seen[target] = true
		if rule, ok := rules[target]; ok {
			if rule.Unsupported {
				return selected.File + " target " + target + " has dynamic, conditional, or repeated rules; configure its preparation explicitly"
			}
			for _, prerequisite := range rule.Prerequisites {
				if problem := inspect(prerequisite); problem != "" {
					return problem
				}
			}
		}
		return ""
	}
	if problem := inspect(selected.Target); problem != "" {
		return nil, []string{problem}
	}
	var declarations [][]string
	var problems []string
	for _, target := range []string{"init", "bootstrap", "install-deps"} {
		rule, ok := rules[target]
		if !ok {
			continue
		}
		var installs []string
		valid := !rule.Unsupported && len(rule.Prerequisites) == 0 && len(rule.Recipes) > 0
		for _, recipe := range rule.Recipes {
			install, ok := setupLiteralPipRequirementsInstall(root, selected.Directory, recipeWithoutMakePrefix(recipe))
			valid = valid && ok
			installs = append(installs, install)
		}
		if !valid {
			problems = append(problems, fmt.Sprintf("%s target %q requires explicit dependency preparation; only unconditional pip requirements installs with no prerequisite or other setup actions can be translated automatically", selected.File, target))
			continue
		}
		declarations = append(declarations, installs)
	}
	if len(declarations) > 1 {
		problems = append(problems, selected.File+" declares multiple dependency setup targets; choose their preparation explicitly in readyci.yml")
	}
	if len(problems) > 0 {
		return nil, problems
	}
	if len(declarations) == 1 {
		return declarations[0], nil
	}
	// An existing Python dependency manifest still needs a declared test setup;
	// installing a guessed package or choosing an arbitrary target hides gaps.
	for _, manifest := range []string{"requirements-dev.txt", "requirements-test.txt", "requirements/tests.txt", "pyproject.toml", "setup.py", "setup.cfg"} {
		if importManifestExists(root, defaultString(selected.Directory, "."), manifest) {
			return nil, []string{selected.File + " selects Python tests but no supported dependency preparation is declared; configure prepare with the repository's test requirements"}
		}
	}
	return nil, nil
}

func setupLiteralPipRequirementsInstall(root, directory, command string) (string, bool) {
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil || len(file.Stmts) != 1 {
		return "", false
	}
	stmt := file.Stmts[0]
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || stmt.Negated || stmt.Background || len(stmt.Redirs) != 0 || len(call.Assigns) != 0 {
		return "", false
	}
	var args []string
	for _, word := range shellWordsFromSyntax(call.Args) {
		if word.Dynamic {
			return "", false
		}
		args = append(args, word.Text)
	}
	if len(args) >= 3 && (args[0] == "python" || args[0] == "python3") && args[1] == "-m" && args[2] == "pip" {
		args = args[3:]
	} else if len(args) > 0 && (args[0] == "pip" || args[0] == "pip3") {
		args = args[1:]
	} else {
		return "", false
	}
	if len(args) < 3 || args[0] != "install" {
		return "", false
	}
	install := []string{"python -m pip install"}
	for i := 1; i < len(args); i += 2 {
		if i+1 >= len(args) || args[i] != "-r" && args[i] != "--requirement" {
			return "", false
		}
		manifest := args[i+1]
		if !filepath.IsLocal(manifest) || !importManifestExists(root, defaultString(directory, "."), manifest) {
			return "", false
		}
		install = append(install, "-r "+shellQuoteArg(manifest))
	}
	command = strings.Join(install, " ")
	if directory != "" && directory != "." {
		// Keep preparation's cwd from changing the selected command's cwd.
		command = "(" + withWorkingDir(directory, command) + ")"
	}
	return command, true
}

func recipeWithoutMakePrefix(recipe string) string {
	return strings.TrimPrefix(strings.TrimSpace(recipe), "@")
}

func setupMakeRules(root, path string) map[string]setupMakeRule {
	data, ok := setupReadMakeSource(root, path)
	if !ok {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	rules := map[string]setupMakeRule{}
	for _, target := range repoMakeTargetsFromSource(data, path) {
		rule := setupMakeRule{Unsupported: target.Conditional}
		header := strings.SplitN(lines[target.Line-1], ":", 2)
		if len(header) != 2 {
			continue
		}
		prerequisites, _, _ := strings.Cut(header[1], "#")
		for _, prerequisite := range strings.Fields(prerequisites) {
			if !isMakeRuleTargetToken(prerequisite) || strings.ContainsAny(prerequisite, "%|:=;") {
				rule.Unsupported = true
			}
			rule.Prerequisites = append(rule.Prerequisites, prerequisite)
		}
		for _, line := range lines[target.Line:] {
			if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if !strings.HasPrefix(line, "\t") {
				break
			}
			rule.Recipes = append(rule.Recipes, strings.TrimPrefix(line, "\t"))
		}
		if previous, duplicate := rules[target.Name]; duplicate {
			rule.Unsupported = true
			rule.Recipes = append(previous.Recipes, rule.Recipes...)
		}
		rules[target.Name] = rule
	}
	return rules
}

// Makefiles can be internal source symlinks. Inspect their confined contents
// without changing the logical path used for recipes and runtime declarations.
// Other manifests retain the ordinary-file requirement below.
func setupReadMakeSource(root, path string) ([]byte, bool) {
	workspace, err := os.OpenRoot(root)
	if err != nil {
		return nil, false
	}
	defer workspace.Close()
	resolved, err := resolveRepoMakeSource(workspace, path)
	if err != nil {
		return nil, false
	}
	file, err := workspace.Open(resolved)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(file, setupRuntimeDeclarationLimit+1))
	return data, err == nil && len(data) <= setupRuntimeDeclarationLimit
}

func setupReadRepositoryFile(root, path string) ([]byte, bool) {
	if !filepath.IsLocal(path) || !importManifestExists(root, filepath.Dir(path), filepath.Base(path)) {
		return nil, false
	}
	workspace, err := os.OpenRoot(root)
	if err != nil {
		return nil, false
	}
	defer workspace.Close()
	file, err := workspace.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, setupRuntimeDeclarationLimit+1))
	return data, err == nil && len(data) <= setupRuntimeDeclarationLimit
}

// Only exact static Make selections are usable evidence for source-based
// preparation. Unknown flags, assignments, dynamic paths and extra Makefiles
// require explicit configuration rather than guessed evaluation.
func setupMakeSelections(root, command string) []setupMakeSelection {
	selections, _ := setupInspectMakeSelections(root, command)
	return selections
}

func setupInspectMakeSelections(root, command string) ([]setupMakeSelection, []string) {
	var selections []setupMakeSelection
	var problems []string
	if setupMakeEnvironmentIsDynamic(command, false, 0) {
		return nil, []string{"Make environment assignments or shell environment changes require explicit runtime and dependency preparation"}
	}
	for _, call := range setupExpandedShellCommands(command) {
		executable := executableShellCommandFromCommand(call)
		args := executable.Args
		if len(args) == 0 || args[0].Dynamic || filepath.Base(args[0].Text) != "make" {
			continue
		}
		if executable.DirDynamic || args[0].Text != "make" {
			problems = append(problems, "Make executable or working directory requires explicit configuration before runtime and dependency preparation can be inspected")
			continue
		}
		directory, file := executable.Dir, ""
		var targets []string
		valid, options := true, true
		for i := 1; i < len(args); i++ {
			if args[i].Dynamic {
				valid = false
				break
			}
			value := args[i].Text
			if options && value == "--" {
				options = false
				continue
			}
			if options && (value == "-j" || value == "--jobs") {
				if i+1 < len(args) && !args[i+1].Dynamic && makeStaticJobCount(args[i+1].Text) {
					i++
				}
				continue
			}
			if options && (strings.HasPrefix(value, "-j") || strings.HasPrefix(value, "--jobs=")) {
				count := strings.TrimPrefix(strings.TrimPrefix(value, "--jobs="), "-j")
				if !makeStaticJobCount(count) {
					valid = false
					break
				}
				continue
			}
			if options && (value == "-s" || value == "--silent" || value == "--quiet" || value == "--no-print-directory" || value == "--print-directory") {
				continue
			}
			if options && (value == "-f" || value == "--file" || value == "-C" || value == "--directory") {
				if i+1 == len(args) || args[i+1].Dynamic {
					valid = false
					break
				}
				i++
				if value == "-f" || value == "--file" {
					valid = valid && file == ""
					file = args[i].Text
				} else {
					var ok bool
					directory, ok = resolveMakeDir(directory, args[i].Text)
					valid = valid && ok
				}
				continue
			}
			if strings.HasPrefix(value, "-") || strings.Contains(value, "=") || !isMakeRuleTargetToken(value) {
				valid = false
				break
			}
			targets = append(targets, value)
		}
		if !valid {
			problems = append(problems, "Make invocation has unsupported flags, assignments, or dynamic arguments; configure its runtime and dependency preparation explicitly")
			continue
		}
		if file == "" {
			file = setupDefaultMakeFile(root, directory)
		}
		path := filepath.Join(directory, file)
		if file == "" || !filepath.IsLocal(path) {
			problems = append(problems, "Make invocation needs a statically inspectable repository Makefile for runtime and dependency preparation")
			continue
		}
		if len(targets) == 0 {
			problems = append(problems, "Make invocation has no explicit target; configure its default target and preparation explicitly")
			continue
		}
		if problem := setupMakeFileAmbiguity(root, path); problem != "" {
			problems = append(problems, problem)
			continue
		}
		for _, target := range targets {
			selections = append(selections, setupMakeSelection{File: filepath.ToSlash(path), Directory: directory, Target: target})
		}
	}
	return selections, uniqueStrings(problems)
}

func setupDefaultMakeFile(root, directory string) string {
	workspace, err := os.OpenRoot(root)
	if err != nil {
		return ""
	}
	defer workspace.Close()
	for _, name := range []string{"GNUmakefile", "makefile", "Makefile"} {
		// Select by presence, then validate the selected source. Skipping an
		// uninspectable file could inspect different rules than Make executes.
		if _, err := workspace.Lstat(filepath.Join(directory, name)); !os.IsNotExist(err) {
			return name
		}
	}
	return ""
}

var setupMakeEvalPattern = regexp.MustCompile(`\$[({]\s*eval(?:\s|[,)\}])`)
var setupMakeRecipePrefixPattern = regexp.MustCompile(`^(?:(?:override|export|private|unexport)\s+)*(?:\.RECIPEPREFIX\s*[:+?!]*=|define\s+\.RECIPEPREFIX(?:\s|$))`)

func setupMakeFileAmbiguity(root, path string) string {
	data, ok := setupReadMakeSource(root, path)
	if !ok {
		return path + " cannot be inspected within the repository; configure preparation explicitly"
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Fields(trimmed)
		if !strings.HasPrefix(line, "\t") && setupMakeRecipePrefixPattern.MatchString(trimmed) {
			return path + " changes Make recipe syntax; configure runtime and dependency preparation explicitly"
		}
		if len(fields) > 0 && !strings.HasPrefix(line, "\t") && (fields[0] == "include" || fields[0] == "-include" || fields[0] == "sinclude") || setupMakeEvalPattern.MatchString(line) {
			return path + " includes or evaluates additional Make rules; configure runtime and dependency preparation explicitly"
		}
	}
	return ""
}

// Imported workflow steps use literal bash -c wrappers to preserve their shell
// semantics. Inspect their literal payloads as source, never execute them.
func setupExpandedShellCommands(command string) []shellCommand {
	var expanded []shellCommand
	var inspect func(string, int)
	inspect = func(command string, depth int) {
		if depth >= 8 {
			return
		}
		for _, call := range shellCommandsFromCommand(command) {
			expanded = append(expanded, call)
			if payload, ok := setupLiteralBashPayload(call); ok {
				inspect(payload, depth+1)
			}
		}
	}
	inspect(command, 0)
	return expanded
}

func setupLiteralBashPayload(call shellCommand) (string, bool) {
	executable := executableShellCommandFromCommand(call)
	args := executable.Args
	if len(args) == 0 || args[0].Dynamic || args[0].Text != "bash" || executable.DirDynamic {
		return "", false
	}
	for i := 1; i < len(args); i++ {
		if args[i].Dynamic {
			return "", false
		}
		switch args[i].Text {
		case "-c", "-lc", "-ec":
			if i+1 < len(args) && !args[i+1].Dynamic {
				return commandWithWorkingDirectory(args[i+1].Text, executable.Dir), true
			}
			return "", false
		case "-e", "--noprofile", "--norc":
		case "-eo":
			if i+1 >= len(args) || args[i+1].Dynamic || args[i+1].Text != "pipefail" {
				return "", false
			}
			i++
		default:
			return "", false
		}
	}
	return "", false
}

func setupMakeEnvironmentIsDynamic(command string, inherited bool, depth int) bool {
	if depth >= 8 {
		return inherited
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return false
	}
	dynamic, hasMake := inherited, false
	syntax.Walk(file, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.DeclClause:
			dynamic = true
		case *syntax.CallExpr:
			for _, assignment := range node.Assigns {
				// Imported GitHub steps already run under CI=true. Keep that
				// precise, generated wrapper while rejecting source-changing env.
				if assignment.Name == nil || assignment.Name.Value != "CI" || assignment.Value == nil || assignment.Append || assignment.Index != nil {
					dynamic = true
					continue
				}
				value := shellWordFromSyntax(assignment.Value)
				if value.Dynamic || value.Text != "true" {
					dynamic = true
				}
			}
			raw := shellWordsFromSyntax(node.Args)
			args := executableShellArgs(raw)
			if len(args) > 0 && !args[0].Dynamic && filepath.Base(args[0].Text) == "make" {
				hasMake = true
			}
			if len(raw) > 0 && !raw[0].Dynamic {
				switch raw[0].Text {
				case "env", "export", "unset", "source", ".", "eval", "read", "declare", "typeset":
					dynamic = true
				}
			}
		}
		return true
	})
	if dynamic && hasMake {
		return true
	}
	for _, call := range shellCommandsFromCommand(command) {
		if payload, ok := setupLiteralBashPayload(call); ok && setupMakeEnvironmentIsDynamic(payload, dynamic, depth+1) {
			return true
		}
	}
	return false
}
