package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func setupSupportedRuntimeAction(action string) bool {
	switch action {
	case "actions/setup-go", "actions/setup-node", "actions/setup-python", "actions/setup-java", "pnpm/action-setup":
		return true
	default:
		return false
	}
}

// Runtime actions are requirements on an installed runner toolchain, rather
// than an execution of third-party actions or a download during detection.
// A selector setup cannot translate into a numeric requirement is advisory: it
// is reported as an unresolved item and the runner image's installed toolchain
// is used, unless the user pins that runtime with --runtime. The returned notes
// are only check-latest freshness notes, whose numeric constraint is still
// applied; an unresolved item carries its own note text.
func setupWorkflowRuntimeRequirements(root string, job repoWorkflowJobYAML, runtimePins ...map[string]string) (map[string]string, []setupUnresolvedItem, []string, []string) {
	var requirements map[string]string
	var pins map[string]string
	if len(runtimePins) > 0 {
		pins = runtimePins[0]
	}
	nodeSelections := 0
	for _, step := range job.Steps {
		if name, _, _ := strings.Cut(step.Uses, "@"); name == "actions/setup-node" {
			nodeSelections++
		}
	}
	var unresolved []setupUnresolvedItem
	var notes, problems []string
	for _, step := range job.Steps {
		action, _, _ := strings.Cut(step.Uses, "@")
		if !setupSupportedRuntimeAction(action) {
			continue
		}
		dynamic := false
		for key, value := range step.With {
			if strings.Contains(fmt.Sprint(value), "${{") {
				problems = append(problems, action+" input "+key+" is dynamic and requires explicit translation")
				dynamic = true
			}
		}
		if dynamic {
			continue
		}
		runtime, versionKey, fileKey := "", "", ""
		switch action {
		case "actions/setup-go":
			runtime, versionKey, fileKey = "go", "go-version", "go-version-file"
		case "actions/setup-node":
			runtime, versionKey, fileKey = "node", "node-version", "node-version-file"
		case "actions/setup-python":
			runtime, versionKey, fileKey = "python", "python-version", "python-version-file"
		case "actions/setup-java":
			runtime, versionKey, fileKey = "java", "java-version", "java-version-file"
		case "pnpm/action-setup":
			runtime, versionKey = "pnpm", "version"
		}
		declaration, requirement, reason := setupWorkflowActionRuntime(root, runtime, versionKey, fileKey, step.With)
		if reason != "" {
			if runtime == "node" && nodeSelections > 1 {
				problems = append(problems, "an untranslated Node selector combined with multiple setup-node steps requires explicit configuration; sequential runtime changes are not imported")
				continue
			}
			if pins[runtime] == "" {
				item := setupUnresolvedItem{
					Kind: setupUnresolvedRuntimeDeclarationNotApplied, Subject: runtime, Source: action + " " + declaration.Source, Declared: declaration.Declared,
					Detail: setupRuntimeNoteDetail(action+" "+declaration.Described, runtime, reason, "pin it with readyci setup --runtime "+runtime+"=VERSION"),
				}
				unresolved = append(unresolved, item)
				continue
			}
			// An explicit user choice replaces only the untranslated selector;
			// repository compatibility declarations are still reviewed.
			requirement = pins[runtime]
		} else if pins[runtime] != "" {
			merged, err := preflightapi.MergeRuntimeRequirements(map[string]string{runtime: requirement}, map[string]string{runtime: pins[runtime]})
			if err != nil {
				problems = append(problems, action+" runtime requirement requires explicit configuration: --runtime "+runtime+"="+pins[runtime]+" conflicts with "+declaration.Described+": "+err.Error())
				continue
			}
			requirement = merged[runtime]
		}
		if reason == "" && !pnpmInstallDisabled(step.With["check-latest"]) {
			notes = append(notes, fmt.Sprintf("Runtime review: %s %s is imported as a %s runtime requirement %q; check-latest freshness is not guaranteed. Ready CI selects a matching installed toolchain or approved provider archive without resolving the latest remote release.", action, declaration.Described, runtime, requirement))
		}
		var err error
		requirements, err = preflightapi.MergeRuntimeRequirements(requirements, map[string]string{runtime: requirement})
		if err != nil {
			problems = append(problems, action+" runtime requirements cannot be combined: "+err.Error())
		}
	}
	if notes = uniqueStrings(notes); len(notes) == 0 {
		notes = nil
	}
	return requirements, mergeSetupUnresolved(unresolved), notes, uniqueStrings(problems)
}

// setupActionDeclaration identifies a workflow action's runtime declaration:
// the input or file it came from, the declared value, and the described form
// used in notes.
type setupActionDeclaration struct {
	Source, Declared, Described string
}

// setupWorkflowActionRuntime returns the action's declaration and either its
// numeric requirement or the reason it is not translated into a requirement.
func setupWorkflowActionRuntime(root, runtime, versionKey, fileKey string, with map[string]any) (setupActionDeclaration, string, string) {
	declaration := setupActionDeclaration{Source: versionKey, Described: versionKey}
	version, ok := setupLiteralActionInput(with[versionKey])
	if !ok {
		return declaration, "", "the version is not a quoted static string"
	}
	if version != "" {
		declaration.Declared = version
		declaration.Described += " " + setupNoteValue(version)
	} else if fileKey != "" {
		path, literal := setupLiteralActionInput(with[fileKey])
		if !literal {
			return setupActionDeclaration{Source: fileKey, Described: fileKey}, "", "the version file is not a static path"
		}
		if path != "" {
			declaration = setupActionDeclaration{Source: fileKey + " " + path, Described: fileKey + " " + path}
			if version, ok = setupWorkflowRuntimeVersionFile(root, runtime, path); !ok {
				return declaration, "", "only supported root runtime declarations are read"
			}
			declaration.Declared = version
			declaration.Described += " (" + setupNoteValue(version) + ")"
		}
	}
	if version == "" && runtime == "pnpm" {
		if version, _ = setupWorkflowRuntimeVersionFile(root, "pnpm", "package.json"); version != "" {
			declaration = setupActionDeclaration{Source: "package.json packageManager", Declared: "pnpm@" + version, Described: "package.json packageManager " + setupNoteValue("pnpm@"+version)}
		}
	}
	if version == "" {
		return declaration, "", "no version is declared, and the action's moving default is not imported"
	}
	if !pnpmInstallDisabled(with["check-latest"]) {
		declaration.Described += " with check-latest"
	}
	requirement, reason := setupTranslatableRuntimeRequirement(runtime, version, preflightapi.NormalizeSetupRuntimeRequirement)
	return declaration, requirement, reason
}

func setupLiteralActionInput(value any) (string, bool) {
	switch value := value.(type) {
	case nil:
		return "", true
	case string:
		return strings.TrimSpace(value), !strings.Contains(value, "${{") && !strings.ContainsAny(value, "\r\n\x00")
	case int:
		return fmt.Sprint(value), true
	default:
		// YAML floating point drops significant version digits (e.g. 1.20).
		return "", false
	}
}

func setupWorkflowRuntimeVersionFile(root, runtime, path string) (string, bool) {
	allowed := map[string]map[string]bool{
		"go": {"go.mod": true}, "node": {".nvmrc": true, ".node-version": true, "package.json": true},
		"python": {".python-version": true}, "java": {".java-version": true}, "pnpm": {"package.json": true},
	}
	if !allowed[runtime][path] {
		return "", false
	}
	data, ok := setupReadRepositoryFile(root, path)
	if !ok {
		return "", false
	}
	if path == "package.json" {
		var pkg struct {
			Engines        map[string]string `json:"engines"`
			PackageManager string            `json:"packageManager"`
		}
		if json.Unmarshal(data, &pkg) != nil {
			return "", false
		}
		if runtime == "pnpm" {
			manager, version, found := strings.Cut(pkg.PackageManager, "@")
			return version, found && manager == "pnpm" && version != ""
		}
		return pkg.Engines["node"], pkg.Engines["node"] != ""
	}
	if path == "go.mod" {
		version := ""
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "toolchain" {
				return strings.TrimPrefix(fields[1], "go"), true
			}
			if len(fields) >= 2 && fields[0] == "go" {
				version = fields[1]
			}
		}
		return version, version != ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		value, _, _ := strings.Cut(line, "#")
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}
