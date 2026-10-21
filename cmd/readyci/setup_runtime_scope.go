package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type setupRuntimeDeclarationScope struct {
	directory string
	runtimes  map[string]bool
}

// Associate declarations with the invocation that uses them. Shell wrappers,
// log messages and another language's preparation do not select a root package.
func setupRuntimeDeclarationScopes(root string, commands []string, active map[string]bool) ([]setupRuntimeDeclarationScope, []string) {
	var scopes []setupRuntimeDeclarationScope
	pending, warnings := setupRuntimeCommands(root, commands)
	var opaqueDirectories []string
	for _, entry := range pending {
		if entry.InferRuntimes && entry.Command != setupPythonVenvPreparation {
			for _, runtime := range setupDirectCommandRuntimes(entry.Command) {
				active[runtime] = true
			}
		}
	}
	add := func(directory string, runtimes []string) {
		directory = filepath.Clean(defaultString(directory, "."))
		if !filepath.IsLocal(directory) {
			warnings = append(warnings, "runtime working directory must stay inside the repository; configure its scope explicitly")
			return
		}
		index := 0
		for index < len(scopes) && scopes[index].directory != directory {
			index++
		}
		if index == len(scopes) {
			scopes = append(scopes, setupRuntimeDeclarationScope{directory: directory, runtimes: map[string]bool{}})
		}
		for _, runtime := range runtimes {
			scopes[index].runtimes[runtime] = true
		}
	}
	for _, entry := range pending {
		command := entry.Command
		if command == setupPythonVenvPreparation {
			// This generated bootstrap uses the already selected Python; it does
			// not make the repository root an additional Python project.
			continue
		}
		for _, selected := range setupMakeSelections(root, command) {
			recipes := setupMakeSelectionCommands(root, selected)
			if len(recipes) == 0 {
				opaqueDirectories = appendUniqueString(opaqueDirectories, defaultString(selected.Directory, "."))
			}
		}
		for _, call := range setupExpandedShellCommands(command) {
			if _, wrapper := setupLiteralBashPayload(call); wrapper {
				continue // Its expanded payload carries the actual working directory.
			}
			invocation := executableShellCommandFromCommand(call)
			args := invocation.Args
			if len(args) == 0 || setupRuntimeProjectIndependentInvocation(args) {
				continue
			}
			if invocation.DirDynamic {
				warnings = append(warnings, "runtime working directory is dynamic; configure its scope explicitly")
				continue
			}
			directory := defaultString(invocation.Dir, ".")
			if args[0].Dynamic {
				opaqueDirectories = appendUniqueString(opaqueDirectories, directory)
				continue
			}
			name := filepath.Base(normalizeCommandInvocationToken(args[0].Text))
			switch name {
			case "npm", "npx", "pnpm", "yarn", "bun", "bunx":
				selection, ok := setupRuntimePackageSelection(name, directory, args[1:])
				if !ok || setupWorkflowPackageSelectionUnknown(root, selection.Directory, name, command, nil, selection.ExplicitDirectory) {
					warnings = append(warnings, "package-manager runtime directory cannot be determined inside the repository; configure its scope explicitly")
					continue
				}
				if setupPackageExecKeepsWorkingDirectory(name, args[1:]) {
					add(directory, setupExecutableRuntimes(name))
					opaqueDirectories = appendUniqueString(opaqueDirectories, directory)
				}
				directory = selection.Directory
			}
			if runtimes := setupExecutableRuntimes(args[0].Text); len(runtimes) > 0 {
				if !entry.InferRuntimes {
					var selected []string
					for _, runtime := range runtimes {
						if active[runtime] {
							selected = append(selected, runtime)
						}
					}
					runtimes = selected
				}
				add(directory, runtimes)
			} else {
				opaqueDirectories = appendUniqueString(opaqueDirectories, directory)
			}
		}
	}
	// Each opaque harness can use the active runtimes even when another
	// invocation of the same runtime was located in a different directory.
	var runtimes []string
	for runtime := range active {
		runtimes = append(runtimes, runtime)
	}
	sort.Strings(runtimes)
	if len(runtimes) > 0 {
		for _, directory := range opaqueDirectories {
			add(directory, runtimes)
		}
	}
	return scopes, uniqueStrings(warnings)
}

// Preserve package-selection environment for static inspection only. Other
// values cannot affect package directory selection and need no projection.
func setupRuntimeCommandWithEnvironment(command string, environments ...map[string]string) string {
	if command == "" || command == setupPythonVenvPreparation {
		return command
	}
	values := map[string]string{}
	for _, environment := range environments {
		for key, value := range environment {
			if setupPackageSelectionEnvironmentKey(key) {
				values[key] = value
			}
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return command
	}
	sort.Strings(keys)
	var assignments []string
	for _, key := range keys {
		assignments = append(assignments, key+"="+shellQuoteArg(values[key]))
	}
	return "export " + strings.Join(assignments, " ") + "\n" + command
}

func setupRuntimeProjectIndependentInvocation(args []shellWord) bool {
	if args[0].Dynamic {
		return false
	}
	name := normalizeCommandInvocationToken(args[0].Text)
	switch name {
	case "make", "echo", "printf", "exit", "true", "false", ":",
		"mkdir", "rmdir", "rm", "cp", "mv", "ln", "chmod", "chown", "touch", "pwd", "ls", "grep":
		return true
	}
	switch filepath.Base(name) {
	case "npm", "npx", "pnpm", "yarn", "bun":
		return len(args) == 2 && !args[1].Dynamic && (args[1].Text == "--version" || args[1].Text == "-v")
	}
	return false
}

// Use the nearest declaration for each filename, retaining ancestor defaults
// when absent locally without merging an independent root package's manifest.
// The caller still requires ordinary files and rejects symlinked parents.
func setupRuntimeDeclarationPath(workspace *os.Root, directory, name string) (string, error) {
	for directory = filepath.Clean(directory); ; directory = filepath.Dir(directory) {
		path := filepath.Join(directory, name)
		if _, err := workspace.Lstat(path); err == nil || !os.IsNotExist(err) {
			return path, err
		}
		if directory == "." {
			return path, os.ErrNotExist
		}
	}
}
