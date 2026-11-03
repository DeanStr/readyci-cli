package main

import (
	"io"
	"os"
	"path/filepath"
)

const setupPythonVenvPreparation = `__readyci_venv=$(mktemp -d "${TMPDIR:-/tmp}/readyci-venv.XXXXXXXX") && python3 -m venv "$__readyci_venv" && export VIRTUAL_ENV="$__readyci_venv" && export PATH="$VIRTUAL_ENV/bin:$PATH"`

func setupPrepareDetectedCheck(root string, check detectedCheck) detectedCheck {
	for _, runtime := range setupCommandRuntimes(root, check.Command) {
		check.Runtimes = appendUniqueString(check.Runtimes, runtime)
	}
	// A venv supplies both the python and pip entrypoints without changing the
	// selected upstream command or installing into the runner's system Python.
	if containsString(check.Runtimes, "python") {
		check.Prepare = append(check.Prepare, setupPythonVenvPreparation)
	}
	tests := []string{check.Command}
	for _, step := range check.Steps {
		tests = append(tests, step.Command)
	}
	var requiredRuntimes []string
	for runtime := range check.RuntimeRequirements {
		requiredRuntimes = append(requiredRuntimes, runtime)
	}
	var declared []setupUnresolvedItem
	check.RuntimeRequirements, declared = setupDeclaredRuntimeRequirementsForSelection(root, append([]string{check.Command}, check.Prepare...), check.RuntimeRequirements, requiredRuntimes...)
	check.Unresolved = setupUnresolvedForCheck(mergeSetupUnresolved(
		setupPythonDependencyReview(root, tests, check.Prepare, false),
		declared,
		setupOpaqueEntrypoints(root, tests),
	), check.Name)
	return check
}

// Python tests run in a fresh virtual environment. Setup does not guess which
// repository target or manifest installs their dependencies; when nothing in
// the check installs them, the repository's own manifests become an unresolved
// item. Other explicit preparation or a dependency check is the repository's
// own choice and is not second-guessed.
func setupPythonDependencyReview(root string, tests, preparation []string, dependencyCheck bool) []setupUnresolvedItem {
	if dependencyCheck || !containsString(preparation, setupPythonVenvPreparation) {
		return nil
	}
	for _, command := range preparation {
		if command != setupPythonVenvPreparation && command != "" {
			return nil
		}
	}
	for _, command := range tests {
		if setupCommandHasPythonInstall(command) {
			return nil
		}
	}
	for _, manifest := range []string{"requirements-dev.txt", "requirements-test.txt", "requirements/tests.txt", "requirements.txt", "pyproject.toml", "setup.py", "setup.cfg"} {
		if importManifestExists(root, ".", manifest) {
			return []setupUnresolvedItem{{
				Kind: setupUnresolvedDependencyPreparationUnknown, Subject: "python", Source: manifest,
				Detail: "Python tests run in a fresh virtual environment and the selected command installs no dependencies; add the repository's test dependency installation (for example python -m pip install -r requirements-dev.txt) under prepare, or name a dependency_check. " + manifest + " is present.",
			}}
		}
	}
	return nil
}

// setupCommandRuntimes inspects literal commands and invoked package scripts.
// Make recipes are opaque: setup does not evaluate Makefiles to infer runtimes.
// It never executes repository code.
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

// Make runs its recipes in its own working directory. Setup treats a Make
// invocation as an opaque harness there, resolving only a static -C option.
func setupMakeWorkingDirectory(directory string, args []shellWord) (string, bool) {
	for i := 0; i < len(args); i++ {
		if args[i].Dynamic {
			if makeDynamicDirectoryOption(args[i].Text) {
				return "", false
			}
			continue
		}
		value, ok := makeInlineDirectoryValue(args[i].Text)
		if !ok && makeOptionValueKind(args[i].Text) == "directory" {
			if i+1 >= len(args) || args[i+1].Dynamic {
				return "", false
			}
			i++
			value, ok = args[i].Text, true
		}
		if ok {
			if directory, ok = resolveMakeDir(directory, value); !ok {
				return "", false
			}
		}
	}
	return defaultString(directory, "."), true
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
