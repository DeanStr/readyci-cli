package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Workflow command names establish an entrypoint only after checking any
// statically available package script. This shares the root detector's
// placeholder rule without executing a package manager or repository code.
// Uninspectable invocations remain unknown scope, distinct from proven no-ops.
func setupWorkflowStepRunsTests(root string, step repoWorkflowRunStep) (runsTests, scopeUnknown bool) {
	command := commandWithWorkingDirectory(step.Command, step.WorkingDirectory)
	for _, parsed := range shellCommandsFromCommand(command) {
		if !setupShellCommandRunsTests(parsed, command) {
			continue
		}
		executable := executableShellCommandFromCommand(parsed)
		args := executable.Args
		if len(args) == 0 || args[0].Dynamic {
			scopeUnknown = true
			continue
		}
		manager := filepath.Base(normalizeCommandInvocationToken(args[0].Text))
		switch manager {
		case "npm", "pnpm", "yarn", "bun":
		default:
			runsTests = true
			continue
		}
		words := setupNodeInvocationArgs(manager, args[1:])
		if len(words) == 0 || words[0].Dynamic {
			scopeUnknown = true
			continue
		}
		script := words[0].Text
		if manager == "bun" && script == "test" {
			// Bun's built-in runner does not invoke scripts.test. `bun run
			// test` below still needs package-script inspection.
			runsTests = true
			continue
		}
		if script == "exec" || script == "dlx" || script == "x" {
			runsTests = true
			continue
		}
		if script == "run" || script == "run-script" {
			words = setupNodeInvocationArgs(manager, words[1:])
			if len(words) == 0 || words[0].Dynamic {
				scopeUnknown = true
				continue
			}
			script = words[0].Text
		}
		selection, ok := setupWorkflowPackageSelection(manager, executable.Dir, executable.DirDynamic, args[1:])
		if !ok || setupWorkflowPackageSelectionUnknown(root, selection.Directory, manager, command, step.Env, selection.ExplicitDirectory) {
			scopeUnknown = true
			continue
		}
		directory := selection.Directory
		manifest := filepath.Join(directory, "package.json")
		data, exists := setupReadRepositoryFile(root, manifest)
		if !exists {
			// The command may generate its package manifest in an earlier step.
			// An existing but uninspectable declaration requires explicit review.
			workspace, err := os.OpenRoot(root)
			if err != nil {
				scopeUnknown = true
				continue
			}
			_, statErr := workspace.Lstat(manifest)
			workspace.Close()
			if os.IsNotExist(statErr) {
				runsTests = true
				continue
			}
			scopeUnknown = true
			continue
		}
		var pkg nodePackageJSON
		if json.Unmarshal(data, &pkg) != nil {
			scopeUnknown = true
			continue
		}
		body := strings.TrimSpace(pkg.Scripts[script])
		if body != "" && !nodeScriptIsPlaceholder(body) || body == "" && setupTestRunnerInvocation(words) {
			runsTests = true
			continue
		}
		// Manager versions and configuration control lifecycle execution. A
		// placeholder main script cannot prove the invocation is a no-op when
		// substantive hooks exist, so retain that job for explicit scope review.
		for _, hook := range []string{"pre" + script, "post" + script} {
			if body := strings.TrimSpace(pkg.Scripts[hook]); body != "" && !nodeScriptIsPlaceholder(body) {
				scopeUnknown = true
			}
		}
	}
	return runsTests, scopeUnknown
}

type setupPackageSelection struct {
	Directory         string
	ExplicitDirectory bool
}

func setupWorkflowPackageSelection(manager, directory string, dynamic bool, args []shellWord) (setupPackageSelection, bool) {
	if dynamic {
		return setupPackageSelection{}, false
	}
	runner := manager == "npx" || manager == "bunx"
	if manager == "pnpm" || manager == "yarn" || manager == "bun" || runner {
		// These managers forward everything after the script or executable to it.
		// Only the prefix can select a workspace or package directory.
		script := setupNodeInvocationArgs(manager, args)
		if !runner && len(script) > 0 && (script[0].Text == "run" || script[0].Text == "run-script") {
			script = setupNodeInvocationArgs(manager, script[1:])
		}
		if len(script) == 0 || script[0].Dynamic {
			return setupPackageSelection{}, false
		}
		args = args[:len(args)-len(script)]
	}
	return setupPackageSelectionFromArgs(manager, directory, args)
}

// Install commands consume manager options after the command too. Yarn also
// accepts an implicit install when only understood install options are given.
// Test scripts retain their separate argument-forwarding rules above.
func setupRuntimePackageSelection(manager, directory string, args []shellWord) (setupPackageSelection, bool) {
	if manager == "pnpm" || manager == "yarn" || manager == "bun" {
		command := setupNodeInvocationArgs(manager, args)
		if command != nil {
			install := len(command) == 0 && manager == "yarn"
			if len(command) > 0 && !command[0].Dynamic {
				switch command[0].Text {
				case "install", "add":
					install = true
				case "i":
					install = manager != "yarn"
				}
			}
			if install {
				return setupPackageSelectionFromArgs(manager, directory, args)
			}
		}
	}
	return setupWorkflowPackageSelection(manager, directory, false, args)
}

func setupPackageSelectionFromArgs(manager, directory string, args []shellWord) (setupPackageSelection, bool) {
	base := directory
	explicit := false
	for i := 0; i < len(args); i++ {
		if args[i].Dynamic {
			return setupPackageSelection{}, false
		}
		if args[i].Text == "--" {
			break
		}
		option, value, inline := strings.Cut(args[i].Text, "=")
		switch option {
		case "--workspace", "--workspaces", "--workspace-root", "-w", "--filter", "-F", "-r", "--recursive":
			// These can select several packages or override the package directory.
			// Do not treat that directory's script as their verification contract.
			return setupPackageSelection{}, false
		case "--userconfig", "--globalconfig":
			// These files can independently select a package. Do not interpret
			// arbitrary configuration outside the uploaded repository.
			return setupPackageSelection{}, false
		case "--prefix", "--dir", "-C", "--cwd":
			if !setupNodeDirectoryOption(manager, option) {
				return setupPackageSelection{}, false
			}
			if !inline {
				i++
				if i >= len(args) || args[i].Dynamic {
					return setupPackageSelection{}, false
				}
				value = args[i].Text
			}
			if filepath.IsAbs(value) {
				return setupPackageSelection{}, false
			}
			directory = filepath.Join(base, value)
			explicit = true
		case "--cache", "--registry", "--loglevel", "--package", "-p":
			if option == "-p" && manager != "npx" {
				return setupPackageSelection{}, false
			}
			if !inline {
				i++
				if i >= len(args) || args[i].Dynamic {
					return setupPackageSelection{}, false
				}
			}
		}
	}
	if directory == "" {
		directory = "."
	}
	return setupPackageSelection{Directory: directory, ExplicitDirectory: explicit}, filepath.IsLocal(directory)
}

func setupNodeDirectoryOption(manager, option string) bool {
	switch option {
	case "--prefix":
		return manager == "npm" || manager == "npx"
	case "-C":
		return manager == "npm" || manager == "pnpm"
	case "--dir":
		return manager == "pnpm"
	case "--cwd":
		return manager == "yarn" || manager == "bun" || manager == "bunx"
	default:
		return false
	}
}
