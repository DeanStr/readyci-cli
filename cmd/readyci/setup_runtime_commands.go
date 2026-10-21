package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type setupRuntimeCommand struct {
	Command       string
	InferRuntimes bool
}

// Both runtime detection and declaration scoping use this static command graph.
// Lifecycle hooks constrain already selected runtimes conservatively: manager
// versions and configuration determine whether those hooks actually execute.
func setupRuntimeCommands(root string, commands []string) ([]setupRuntimeCommand, []string) {
	pending := make([]setupRuntimeCommand, 0, len(commands))
	for _, command := range commands {
		pending = append(pending, setupRuntimeCommand{Command: command, InferRuntimes: true})
	}
	workspace, err := os.OpenRoot(root)
	if err != nil {
		return pending, []string{"runtime command sources could not be inspected within the repository"}
	}
	defer workspace.Close()
	seenMake := map[setupMakeSelection]bool{}
	seenScripts := map[string]bool{}
	var warnings []string
	for index := 0; index < len(pending); index++ {
		entry := pending[index]
		if entry.Command == setupPythonVenvPreparation {
			continue
		}
		for _, selected := range setupMakeSelections(root, entry.Command) {
			inferred, seen := seenMake[selected]
			if seen && (inferred || !entry.InferRuntimes) {
				continue
			}
			if !seen && len(seenMake) >= 64 {
				warnings = append(warnings, "Make runtime scope exceeds the static inspection limit; configure its scope explicitly")
				continue
			}
			seenMake[selected] = entry.InferRuntimes
			for _, command := range setupMakeSelectionCommands(root, selected) {
				pending = append(pending, setupRuntimeCommand{Command: command, InferRuntimes: entry.InferRuntimes})
			}
		}
		for _, call := range setupExpandedShellCommands(entry.Command) {
			invocation := executableShellCommandFromCommand(call)
			args := invocation.Args
			if len(args) == 0 || args[0].Dynamic || invocation.DirDynamic {
				continue
			}
			manager := filepath.Base(normalizeCommandInvocationToken(args[0].Text))
			switch manager {
			case "npm", "pnpm", "yarn", "bun":
			default:
				continue
			}
			names := setupInvokedPackageScripts(manager, args[1:])
			if len(names) == 0 {
				continue
			}
			selection, ok := setupRuntimePackageSelection(manager, defaultString(invocation.Dir, "."), args[1:])
			if !ok || setupWorkflowPackageSelectionUnknown(root, selection.Directory, manager, entry.Command, nil, selection.ExplicitDirectory) {
				continue // The declaration inspector reports this unresolved scope.
			}
			manifest, err := setupRuntimeDeclarationPath(workspace, selection.Directory, "package.json")
			if err != nil {
				continue
			}
			data, readable := setupReadRepositoryFile(root, manifest)
			var pkg struct {
				Scripts map[string]string `json:"scripts"`
			}
			if !readable || json.Unmarshal(data, &pkg) != nil {
				warnings = append(warnings, "package scripts require explicit runtime configuration: "+filepath.ToSlash(manifest))
				continue
			}
			for _, name := range names {
				body := pkg.Scripts[name.Command]
				key := manifest + "\x00" + name.Command
				infer := entry.InferRuntimes && name.InferRuntimes
				inferred, seen := seenScripts[key]
				if body == "" || seen && (inferred || !infer) {
					continue
				}
				if !seen && len(seenScripts) >= 64 {
					warnings = append(warnings, "package-script runtime scope exceeds the static inspection limit; configure its scope explicitly")
					continue
				}
				seenScripts[key] = infer
				pending = append(pending, setupRuntimeCommand{Command: commandWithWorkingDirectory(body, filepath.Dir(manifest)), InferRuntimes: infer})
			}
		}
	}
	return pending, uniqueStrings(warnings)
}

func setupInvokedPackageScripts(manager string, args []shellWord) []setupRuntimeCommand {
	words := setupNodeInvocationArgs(manager, args)
	if len(words) == 0 || words[0].Dynamic {
		return nil
	}
	name := words[0].Text
	explicitRun := name == "run" || name == "run-script"
	if explicitRun {
		words = setupNodeInvocationArgs(manager, words[1:])
		if len(words) == 0 || words[0].Dynamic {
			return nil
		}
		name = words[0].Text
	} else {
		// Install lifecycle scripts may constrain selected runtimes, but are
		// not unconditional entrypoints (for example with ignore-scripts).
		install := name == "install" || name == "add" || name == "i" && manager != "yarn" || name == "ci" && manager == "npm"
		if install {
			var hooks []setupRuntimeCommand
			for _, hook := range []string{"preinstall", "install", "postinstall", "prepublish", "preprepare", "prepare", "postprepare", "pnpm:devPreinstall"} {
				hooks = append(hooks, setupRuntimeCommand{Command: hook})
			}
			return hooks
		}
		if manager == "npm" {
			switch name {
			case "test", "t", "tst":
				name = "test"
			case "start", "stop", "restart":
			default:
				return nil
			}
		} else if setupPackageManagerBuiltin(manager, name) {
			return nil
		}
	}
	return []setupRuntimeCommand{{Command: "pre" + name}, {Command: name, InferRuntimes: true}, {Command: "post" + name}}
}

func setupPackageManagerBuiltin(manager, name string) bool {
	switch manager {
	case "yarn":
		switch name {
		case "node", "bin", "cache", "config", "create", "exec", "dlx", "help", "info", "init", "link", "pack", "remove", "unlink", "upgrade-interactive", "version", "why", "workspace", "workspaces":
			return true
		}
	case "pnpm":
		switch name {
		case "audit", "bin", "ci", "config", "create", "deploy", "dlx", "env", "exec", "fetch", "help", "import", "info", "init", "licenses", "link", "list", "ls", "outdated", "pack", "patch", "patch-commit", "prune", "publish", "rebuild", "remove", "rm", "root", "setup", "store", "unlink", "update", "up", "view", "why":
			return true
		}
	case "bun":
		switch name {
		case "test", "x", "build", "bun", "create", "init", "install", "link", "pm", "remove", "unlink", "update", "upgrade":
			return true
		}
	}
	return false
}

func setupPackageExecKeepsWorkingDirectory(manager string, args []shellWord) bool {
	if manager == "npx" {
		return true
	}
	if manager != "npm" {
		return false
	}
	words := setupNodeInvocationArgs(manager, args)
	return len(words) > 0 && !words[0].Dynamic && (words[0].Text == "exec" || words[0].Text == "x")
}
