package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func setupSupportedRuntimeAction(action string) bool {
	switch action {
	case "actions/setup-go", "actions/setup-node", "actions/setup-python", "pnpm/action-setup":
		return true
	default:
		return false
	}
}

// Runtime actions are requirements on an installed runner toolchain, rather
// than an execution of third-party actions or a download during detection.
var setupRuntimeResolutionNow = time.Now

func setupRuntimePinNotices(resolutions map[string]preflightapi.RuntimeResolution) []string {
	var notices []string
	names := make([]string, 0, len(resolutions))
	for name := range resolutions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		record := resolutions[name]
		notices = append(notices, fmt.Sprintf("Resolved %s %s to numeric series %s at import time; the saved check does not track future LTS changes. Review and regenerate it to change series.", name, record.Selector, record.Requirement))
	}
	return notices
}

func setupWorkflowRuntimeRequirements(root string, job repoWorkflowJobYAML, runtimePins ...map[string]string) (map[string]string, map[string]preflightapi.RuntimeResolution, []string) {
	var requirements map[string]string
	var resolutions map[string]preflightapi.RuntimeResolution
	var pins map[string]string
	if len(runtimePins) > 0 {
		pins = runtimePins[0]
	}
	var problems []string
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
		if !pnpmInstallDisabled(step.With["check-latest"]) {
			problems = append(problems, action+" check-latest requires remote release resolution; select an explicit installed runtime version")
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
		case "pnpm/action-setup":
			runtime, versionKey = "pnpm", "version"
		}
		version, ok := setupLiteralActionInput(step.With[versionKey])
		if !ok {
			problems = append(problems, action+" runtime version must be a quoted static numeric version or supported version range")
			continue
		}
		if version == "" && fileKey != "" {
			path, literal := setupLiteralActionInput(step.With[fileKey])
			if !literal {
				problems = append(problems, action+" version-file must name a supported static root declaration")
				continue
			}
			if path != "" {
				version, ok = setupWorkflowRuntimeVersionFile(root, runtime, path)
				if !ok {
					problems = append(problems, action+" version-file "+path+" requires explicit translation; use a supported root runtime declaration or a literal version")
					continue
				}
			}
		}
		if version == "" && runtime == "pnpm" {
			version, _ = setupWorkflowRuntimeVersionFile(root, "pnpm", "package.json")
		}
		if version == "" {
			problems = append(problems, action+" needs an explicit static runtime version; an action's moving default is not imported")
			continue
		}
		var err error
		if runtime == "node" && version == "lts/*" {
			nodeSelections := 0
			for _, other := range job.Steps {
				if name, _, _ := strings.Cut(other.Uses, "@"); name == "actions/setup-node" {
					nodeSelections++
				}
			}
			if nodeSelections != 1 {
				problems = append(problems, "a moving Node selector combined with multiple setup-node steps requires explicit configuration; sequential runtime changes are not imported")
				continue
			}
			if pins[runtime] == "" && step.Uses != preflightapi.NodeRuntimeAliasCatalogMetadata().ReviewedActionRef {
				problems = append(problems, action+" moving LTS selector requires the reviewed pinned setup-node action; other action references need explicit numeric configuration")
				continue
			}
			if pin := pins[runtime]; pin != "" {
				// An explicit user choice replaces this moving selector, not
				// the source's independent numeric compatibility requirements.
				version = pin
			} else {
				var record preflightapi.RuntimeResolution
				record, err = preflightapi.ResolveRuntimeAlias(runtime, version, setupRuntimeResolutionNow())
				if err != nil {
					err = fmt.Errorf("%w; choose an explicit numeric version with readyci setup --runtime node=VERSION", err)
				}
				if err == nil {
					version = record.Requirement
					if resolutions == nil {
						resolutions = map[string]preflightapi.RuntimeResolution{}
					}
					resolutions[runtime] = record
				}
			}
		} else if runtime == "pnpm" && strings.Contains(version, "+") {
			// Preserve the package.json/action integrity pin in the sealed
			// requirement. A version-only range cannot prove these bytes.
			if _, _, ok := preflightapi.PackageManagerIntegrityRequirement(version); !ok {
				err = fmt.Errorf("pnpm integrity requires an exact version and complete sha512 pin")
			}
		} else {
			version, err = preflightapi.NormalizeSetupRuntimeRequirement(runtime, version)
		}
		var normalized map[string]string
		if err == nil {
			normalized, err = preflightapi.NormalizeRuntimeRequirements(map[string]string{runtime: version})
		}
		if err == nil && pins[runtime] != "" {
			normalized, err = preflightapi.MergeRuntimeRequirements(normalized, map[string]string{runtime: pins[runtime]})
		}
		if err != nil {
			problems = append(problems, action+" runtime requirement requires explicit configuration: "+err.Error())
			continue
		}
		requirements, err = preflightapi.MergeRuntimeRequirements(requirements, normalized)
		if err != nil {
			problems = append(problems, action+" runtime requirements cannot be combined: "+err.Error())
		}
	}
	return requirements, resolutions, uniqueStrings(problems)
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
		"python": {".python-version": true}, "pnpm": {"package.json": true},
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
