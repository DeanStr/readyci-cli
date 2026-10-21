package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"mvdan.cc/sh/v3/syntax"
)

// npm-compatible package selection can come from configuration independently
// of argv. Keep that scope explicit instead of inspecting a different package.
func setupWorkflowPackageSelectionUnknown(root, directory, manager, command string, environment map[string]string, explicitDirectory bool) bool {
	if manager == "npx" {
		manager = "npm"
	}
	if manager != "npm" && manager != "pnpm" {
		return false
	}
	for key := range environment {
		if setupPackageSelectionEnvironmentKey(key) {
			return true
		}
	}
	commands := []string{command}
	for _, call := range setupExpandedShellCommands(command) {
		if payload, ok := setupLiteralBashPayload(call); ok {
			commands = appendUniqueString(commands, payload)
		}
	}
	unknown := false
	for _, command := range commands {
		file, err := syntax.NewParser().Parse(strings.NewReader(normalizeShellCommandForParse(command)), "")
		if err != nil {
			return true
		}
		syntax.Walk(file, func(node syntax.Node) bool {
			switch node := node.(type) {
			case *syntax.Assign:
				unknown = unknown || node.Name != nil && setupPackageSelectionEnvironmentKey(node.Name.Value)
			case *syntax.CallExpr:
				args := shellWordsFromSyntax(node.Args)
				prefix := len(args) - len(executableShellArgs(args))
				for _, word := range args[:prefix] {
					if key, _, assignment := strings.Cut(word.Text, "="); assignment && setupPackageSelectionEnvironmentKey(key) {
						unknown = true
					}
				}
			}
			return !unknown
		})
		if unknown {
			return true
		}
	}
	workspace, err := os.OpenRoot(root)
	if err != nil {
		return true
	}
	defer workspace.Close()
	directories, ok := setupPackageConfigurationDirectories(root, workspace, directory, manager, explicitDirectory)
	if !ok {
		return true
	}
	configFiles := []string{".npmrc"}
	if manager == "pnpm" {
		configFiles = append(configFiles, "pnpm-workspace.yaml")
	}
	for _, dir := range directories {
		for _, configFile := range configFiles {
			name := filepath.Join(dir, configFile)
			data, readable := setupReadRepositoryFile(root, name)
			if !readable {
				if _, err := workspace.Lstat(name); !os.IsNotExist(err) {
					return true
				}
				continue
			}
			if configFile == "pnpm-workspace.yaml" {
				var values map[string]any
				if yaml.Unmarshal(data, &values) != nil {
					return true
				}
				for key := range values {
					if setupPackageSelectionConfigKey(key) {
						return true
					}
				}
				continue
			}
			for _, line := range strings.Split(string(data), "\n") {
				key, _, _ := strings.Cut(strings.TrimSpace(line), "=")
				if setupPackageSelectionConfigKey(key) {
					return true
				}
			}
		}
	}
	return false
}

// An explicit npm prefix selects its project config directly. Otherwise npm
// uses the nearest project or its owning workspace, while pnpm also loads its
// nearest workspace's config. Independent ancestor configs do not apply.
func setupPackageConfigurationDirectories(root string, workspace *os.Root, directory, manager string, explicitDirectory bool) ([]string, bool) {
	project := filepath.Clean(defaultString(directory, "."))
	if !filepath.IsLocal(project) {
		return nil, false
	}
	if manager == "npm" && explicitDirectory {
		return []string{project}, true
	}
	manifest, err := setupRuntimeDeclarationPath(workspace, project, "package.json")
	if err == nil {
		if !importManifestExists(root, filepath.Dir(manifest), "package.json") {
			return nil, false
		}
		project = filepath.Dir(manifest)
	} else if !os.IsNotExist(err) {
		return nil, false
	}
	directories := []string{project}
	for current := project; ; current = filepath.Dir(current) {
		if manager == "pnpm" {
			if _, err := workspace.Lstat(filepath.Join(current, "pnpm-workspace.yaml")); err == nil {
				return appendUniqueString(directories, current), true
			} else if !os.IsNotExist(err) {
				return nil, false
			}
		} else if current != project {
			name := filepath.Join(current, "package.json")
			data, readable := setupReadRepositoryFile(root, name)
			if !readable {
				if _, err := workspace.Lstat(name); !os.IsNotExist(err) {
					return nil, false
				}
			} else {
				var parent struct {
					Workspaces json.RawMessage `json:"workspaces"`
				}
				if json.Unmarshal(data, &parent) != nil {
					return nil, false
				}
				patterns, ok := setupPackageWorkspacePatterns(parent.Workspaces)
				if !ok || !setupRuntimeWorkspacePatternsSupported(patterns) {
					return nil, false
				}
				rel, err := filepath.Rel(current, project)
				if err != nil {
					return nil, false
				}
				if workspacePatternsInclude(patterns, filepath.ToSlash(rel)) {
					return []string{current}, true
				}
			}
		}
		if current == "." {
			return directories, true
		}
	}
}

func setupPackageWorkspacePatterns(value json.RawMessage) ([]string, bool) {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return nil, true
	}
	if value[0] == '{' {
		var object struct {
			Packages json.RawMessage `json:"packages"`
		}
		if json.Unmarshal(value, &object) != nil {
			return nil, false
		}
		value = bytes.TrimSpace(object.Packages)
	}
	if len(value) == 0 || value[0] != '[' {
		return nil, false
	}
	var patterns []string
	if json.Unmarshal(value, &patterns) != nil {
		return nil, false
	}
	return cleanWorkspacePatterns(patterns), true
}

func setupPackageSelectionEnvironmentKey(key string) bool {
	key = strings.ToLower(key)
	for _, prefix := range []string{"npm_config_", "pnpm_config_"} {
		if suffix, ok := strings.CutPrefix(key, prefix); ok {
			return setupPackageSelectionConfigKey(suffix)
		}
	}
	return false
}

func setupPackageSelectionConfigKey(key string) bool {
	key = strings.TrimSpace(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(key)), "[]"))
	switch strings.NewReplacer("_", "", "-", "").Replace(key) {
	case "workspace", "workspaces", "includeworkspaceroot", "prefix", "localprefix", "global", "location", "userconfig", "globalconfig", "filter", "filterprod", "recursive", "dir", "workspaceroot":
		return true
	default:
		return false
	}
}
