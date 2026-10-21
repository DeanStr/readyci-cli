package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Detection is a static source inspection. Never execute repository Makefiles,
// scripts, package managers, or workflow actions to discover a command.
func setupDetectedTemplate(root, projectID string, options setupOptions, candidates []repoImportCandidate) (string, error) {
	if imported, err := automaticSetupImport(candidates); err != nil {
		return "", err
	} else if imported != nil {
		template := setupImportedTemplateWithScope(root, options, projectID, *imported, candidates)
		template, err = applySetupNetworkPolicy(template, options)
		if err != nil {
			return "", err
		}
		if err := validateSetupRuntimeTemplate(root, template); err != nil {
			return "", err
		}
		return template, nil
	}
	if len(options.RuntimePins) > 0 {
		return "", cliConfigError("--runtime requires a matching imported workflow runtime; inspect readyci setup --preview --json and select --candidate ID", nil)
	}
	if err := validateSetupRootMakeTestScope(root); err != nil {
		return "", err
	}
	if template, ok := basicDetectedProjectTemplate(root, projectID, options.Image, options.Profile, false); ok {
		if options.WriteHelpers {
			template, _ = basicDetectedProjectTemplate(root, projectID, options.Image, options.Profile, true)
		}
		var err error
		template, err = applySetupNetworkPolicy(template, options)
		if err != nil {
			return "", err
		}
		if err := validateSetupRuntimeTemplate(root, template); err != nil {
			return "", err
		}
		return template, nil
	}
	return "", cliConfigError("configuration required: no supported root test entrypoint was found; inspect readyci setup --preview --json and select --candidate ID, or use readyci setup --print --detect=false to configure the repository's documented tests", nil)
}

// Conditional root verification targets are declared scope, even when native
// detection cannot select them. A language default must not silently replace
// that scope. Read only logical root entrypoints; nested Makefiles are unrelated
// components, and internal links retain the root working directory.
func validateSetupRootMakeTestScope(root string) error {
	source, err := os.OpenRoot(root)
	if err != nil {
		return nil
	}
	defer source.Close()
	targetFiles := map[string]string{}
	for _, file := range []string{"GNUmakefile", "makefile", "Makefile"} {
		resolved, err := resolveRepoMakeSource(source, file)
		if err != nil {
			continue
		}
		data, err := source.ReadFile(resolved)
		if err != nil {
			continue
		}
		for _, target := range repoMakeTargetsFromSource(data, file) {
			if target.Name != "test" && target.Name != "check" && target.Name != "check-ci" {
				continue
			}
			// Match native detection's per-target file precedence. Keep checking
			// all declarations in that file so conditional overrides still block.
			if targetFiles[target.Name] == "" {
				targetFiles[target.Name] = file
			}
			if targetFiles[target.Name] != file {
				continue
			}
			if target.Conditional {
				return cliConfigError(fmt.Sprintf("configuration required: root Make entrypoint make:%s:%s is defined conditionally; translate its required variables and test scope explicitly instead of using a language default; inspect readyci setup --preview --json and select --candidate ID, or configure the documented tests", file, target.Name), nil)
			}
		}
	}
	return nil
}

func automaticSetupImport(candidates []repoImportCandidate) (*repoImportCandidate, error) {
	var supported []repoImportCandidate
	var blockers []string
	var testJobs []string
	for _, candidate := range candidates {
		if candidate.Source != "github-actions" {
			continue
		}
		testIntent := candidate.ScopeUnknown || candidate.TestEntrypoint || hasSeparatedMarker(candidate.Name, "test") || hasSeparatedMarker(candidate.Name, "tests") || hasSeparatedMarker(candidate.Name, "unit") || hasSeparatedMarker(candidate.Name, "integration") || hasSeparatedMarker(candidate.Name, "e2e")
		if !testIntent {
			continue
		}
		testJobs = append(testJobs, candidate.ID)
		unsupported := append(append([]string(nil), candidate.Unsupported...), candidate.AutomaticUnsupported...)
		if !candidate.TestEntrypoint {
			unsupported = append(unsupported, "no concrete recognized test entrypoint is available; job names alone do not establish test scope")
		}
		for _, dir := range candidate.WorkingDirectories {
			if dir != "" && dir != "." {
				unsupported = append(unsupported, "component working directories require explicit candidate selection")
				break
			}
		}
		if strings.TrimSpace(candidate.Command) == "" {
			unsupported = append(unsupported, "no concrete shell command is available")
		}
		if len(unsupported) > 0 {
			blockers = append(blockers, candidate.ID+": "+strings.Join(uniqueStrings(unsupported), "; "))
			continue
		}
		supported = append(supported, candidate)
	}
	if len(testJobs) > 1 {
		message := "configuration required: multiple upstream test jobs are available; select --candidate ID after reviewing readyci setup --preview --json: " + strings.Join(testJobs, ", ")
		if len(blockers) > 0 {
			message += "; jobs requiring explicit translation: " + strings.Join(blockers, " | ")
		}
		return nil, cliConfigError(message, nil)
	}
	if len(supported) == 1 {
		return &supported[0], nil
	}
	if len(blockers) > 0 {
		return nil, cliConfigError("configuration required: upstream test jobs need explicit translation or selection; inspect readyci setup --preview --json: "+strings.Join(blockers, " | "), nil)
	}
	return nil, nil
}

// This recognizes test entrypoints, not successful test execution or complete
// repository coverage. Quoted log text and build/typecheck commands are not
// evidence of a test entrypoint.
func setupCommandRunsTests(command string) bool {
	for _, parsed := range shellCommandsFromCommand(command) {
		if setupShellCommandRunsTests(parsed, command) {
			return true
		}
	}
	return false
}

func setupShellCommandRunsTests(parsed shellCommand, command string) bool {
	executable := executableShellCommandFromCommand(parsed)
	args := executable.Args
	if len(args) == 0 || args[0].Dynamic {
		return false
	}
	name := filepath.Base(normalizeCommandInvocationToken(args[0].Text))
	// Keep argv positions, including unknown shell expansions. An unknown
	// module, script, or option must not promote a later operand to a command.
	words := args[1:]
	is := func(index int, value string) bool {
		return index < len(words) && !words[index].Dynamic && words[index].Text == value
	}
	has := func(value string) bool {
		for i := range words {
			if is(i, value) {
				return true
			}
		}
		return false
	}
	switch name {
	case "go", "cargo", "dotnet", "meson":
		if is(0, "test") || name == "cargo" && is(0, "nextest") && is(1, "run") {
			return true
		}
	case "npm", "pnpm", "yarn", "bun":
		if setupNodeInvocationRunsTests(name, words) {
			return true
		}
	case "npx", "bunx":
		if setupTestRunnerInvocation(setupNodeInvocationArgs(name, words)) {
			return true
		}
	case "node":
		if has("--test") {
			return true
		}
	case "python", "python3":
		if setupPythonInvocationRunsTests(words) {
			return true
		}
	case "pytest", "py.test", "tox", "nox", "ctest", "jest", "vitest", "mocha", "ava":
		return true
	case "mvn", "mvnw", "gradle", "gradlew":
		if has("test") || has("check") {
			return true
		}
	case "make", "gmake":
		for _, invocation := range makeInvocationsFromCommand(command) {
			if hasSeparatedMarker(invocation.Target, "test") || hasSeparatedMarker(invocation.Target, "unit") || hasSeparatedMarker(invocation.Target, "e2e") {
				return true
			}
		}
	default:
		if strings.Contains(args[0].Text, "/") && (hasSeparatedMarker(name, "test") || hasSeparatedMarker(name, "tests")) {
			return true
		}
	}
	return false
}

func setupPythonInvocationRunsTests(args []shellWord) bool {
	for i := 0; i < len(args); i++ {
		if args[i].Dynamic {
			return false
		}
		arg := args[i].Text
		switch arg {
		case "-m":
			return i+1 < len(args) && !args[i+1].Dynamic && (args[i+1].Text == "pytest" || args[i+1].Text == "unittest")
		case "-W", "-X", "--check-hash-based-pycs":
			if i+1 >= len(args) || args[i+1].Dynamic {
				return false
			}
			i++
		case "-b", "-bb", "-B", "-d", "-E", "-i", "-I", "-O", "-OO", "-P", "-q", "-R", "-s", "-S", "-u", "-v", "-x":
		default:
			if !strings.HasPrefix(arg, "-W") && !strings.HasPrefix(arg, "-X") && !strings.HasPrefix(arg, "--check-hash-based-pycs=") {
				// A script, -c command, or unsupported option ends interpreter
				// parsing; later words are data for that invocation.
				return false
			}
		}
	}
	return false
}

func setupNodeInvocationRunsTests(manager string, args []shellWord) bool {
	args = setupNodeInvocationArgs(manager, args)
	if len(args) == 0 || args[0].Dynamic {
		return false
	}
	command := args[0].Text
	if command == "test" || manager != "npm" && strings.HasPrefix(command, "test:") {
		return true
	}
	switch command {
	case "run", "run-script":
		args = setupNodeInvocationArgs(manager, args[1:])
		return len(args) > 0 && !args[0].Dynamic && (args[0].Text == "test" || strings.HasPrefix(args[0].Text, "test:"))
	case "exec", "dlx", "x":
		return setupTestRunnerInvocation(setupNodeInvocationArgs(manager, args[1:]))
	default:
		return manager != "npm" && setupTestRunnerInvocation(args)
	}
}

// Skip only understood manager options. Unknown or dynamic options require
// explicit selection rather than guessing which later word is the command.
func setupNodeInvocationArgs(manager string, args []shellWord) []shellWord {
	for i := 0; i < len(args); i++ {
		if args[i].Dynamic {
			return nil
		}
		arg := args[i].Text
		if arg == "--" {
			return args[i+1:]
		}
		if !strings.HasPrefix(arg, "-") {
			return args[i:]
		}
		option, _, inline := strings.Cut(arg, "=")
		takesValue := false
		switch option {
		case "--silent", "--quiet", "--verbose", "--offline", "--if-present":
		case "--frozen-lockfile", "--no-frozen-lockfile", "--ignore-scripts":
			if manager != "pnpm" && manager != "yarn" && manager != "bun" {
				return nil
			}
		case "--immutable", "--immutable-cache", "--check-cache":
			if manager != "yarn" {
				return nil
			}
		case "--workspace", "--userconfig", "--cache", "--registry", "--loglevel", "--package":
			takesValue = manager == "npm" || manager == "npx"
			if !takesValue {
				return nil
			}
		case "-p":
			takesValue = manager == "npx"
			if !takesValue {
				return nil
			}
		case "--prefix", "--dir", "-C", "--cwd":
			takesValue = setupNodeDirectoryOption(manager, option)
			if !takesValue {
				return nil
			}
		case "--filter", "-F":
			takesValue = manager == "pnpm"
			if !takesValue {
				return nil
			}
		case "-r", "--recursive", "--workspace-root", "--parallel", "--stream", "--no-bail":
			if manager != "pnpm" {
				return nil
			}
		case "-s":
		case "-y", "--yes", "--no-install":
			if manager != "npx" && manager != "bunx" {
				return nil
			}
		default:
			return nil
		}
		if takesValue && !inline {
			if i+1 >= len(args) || args[i+1].Dynamic {
				return nil
			}
			i++
		}
	}
	return args[len(args):]
}

func setupTestRunnerInvocation(args []shellWord) bool {
	if len(args) == 0 || args[0].Dynamic {
		return false
	}
	switch filepath.Base(args[0].Text) {
	case "jest", "vitest", "mocha", "ava":
		return true
	case "playwright":
		return len(args) > 1 && !args[1].Dynamic && args[1].Text == "test"
	default:
		return false
	}
}

func workflowAutomaticImportRequirements(job repoWorkflowJobYAML) []string {
	var unsupported []string
	for _, step := range job.Steps {
		action, _, _ := strings.Cut(step.Uses, "@")
		if action != "" && action != "actions/checkout" && !setupSupportedRuntimeAction(action) {
			unsupported = append(unsupported, fmt.Sprintf("%s is not executed by command import; review its requirements and select the candidate explicitly", action))
		}
		for _, parsed := range shellCommandsFromCommand(step.Run) {
			args := executableShellCommandFromCommand(parsed).Args
			if len(args) == 0 || args[0].Dynamic {
				continue
			}
			switch filepath.Base(args[0].Text) {
			case "sudo", "su", "mount", "modprobe", "sysctl", "docker", "podman":
				unsupported = append(unsupported, "privileged or container preparation requires explicit runner configuration")
			}
		}
	}
	return uniqueStrings(unsupported)
}

func setupWorkflowMakeRequirements(makeMap repoMakeMap, steps []repoWorkflowRunStep) []string {
	var unsupported []string
	for _, step := range steps {
		for _, parsed := range shellCommandsFromCommand(commandWithWorkingDirectory(step.Command, step.WorkingDirectory)) {
			executable := executableShellCommandFromCommand(parsed)
			args := executable.Args
			if len(args) == 0 || args[0].Dynamic || filepath.Base(args[0].Text) != "make" {
				continue
			}
			dir, file := executable.Dir, ""
			var targets []string
			problem := executable.DirDynamic
			for i := 1; i < len(args); i++ {
				arg := args[i]
				if arg.Dynamic {
					problem = true
					continue
				}
				value := arg.Text
				if strings.Contains(value, "=") && !strings.HasPrefix(value, "-") {
					continue
				}
				if value == "-f" || value == "--file" || value == "--makefile" {
					if file != "" || i+1 == len(args) || args[i+1].Dynamic {
						problem = true
						continue
					}
					i++
					file = args[i].Text
					continue
				}
				if strings.HasPrefix(value, "--file=") || strings.HasPrefix(value, "--makefile=") || strings.HasPrefix(value, "-f") && len(value) > 2 {
					if file != "" {
						problem = true
					}
					if _, inline, ok := strings.Cut(value, "="); ok {
						file = inline
					} else {
						file = value[2:]
					}
					continue
				}
				if inline, ok := makeInlineDirectoryValue(value); ok {
					var valid bool
					dir, valid = resolveMakeDir(dir, inline)
					problem = problem || !valid
					continue
				}
				if value == "-j" || value == "--jobs" {
					if i+1 < len(args) && !args[i+1].Dynamic && makeStaticJobCount(args[i+1].Text) {
						i++
					}
					continue
				}
				if kind := makeOptionValueKind(value); kind != "" {
					if i+1 == len(args) || args[i+1].Dynamic {
						problem = true
						continue
					}
					i++
					if kind == "directory" {
						var valid bool
						dir, valid = resolveMakeDir(dir, args[i].Text)
						problem = problem || !valid
					}
					continue
				}
				if strings.HasPrefix(value, "-") {
					continue
				}
				targets = append(targets, value)
			}
			if problem {
				unsupported = append(unsupported, "Make invocation has dynamic or unsupported file, directory, or target arguments; configure it explicitly")
				continue
			}
			if file == "" {
				for _, name := range []string{"GNUmakefile", "makefile", "Makefile"} {
					candidate := filepath.ToSlash(filepath.Join(dir, name))
					if containsString(makeMap.Files, candidate) {
						file = name
						break
					}
				}
			}
			selected := filepath.ToSlash(filepath.Join(dir, file))
			if file == "" || !filepath.IsLocal(selected) {
				unsupported = append(unsupported, "Make invocation has no statically inspectable Makefile in its working directory")
				continue
			}
			for _, name := range targets {
				found := false
				for _, target := range makeMap.Targets {
					found = found || target.Path == selected && target.Name == name && !target.Conditional
				}
				if !found {
					unsupported = append(unsupported, fmt.Sprintf("Make target %q is not statically defined in %s; included, generated, or conditional rules require explicit configuration", name, selected))
				}
			}
		}
	}
	return uniqueStrings(unsupported)
}
