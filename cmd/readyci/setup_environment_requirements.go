package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

const setupRuntimeDeclarationLimit = 1 << 20

var setupRuntimeValuePattern = regexp.MustCompile(`^[A-Za-z0-9.,!+_~^<>=|*/ @-]+$`)

// Read declarations, never execute repository setup code or resolve moving
// aliases against the local machine. setupDeclaredRuntimeRequirements applies
// the selected command scope and converts declarations into sealed constraints.
func setupRuntimeRequirements(root string, commands []string, selectedRuntimes ...string) ([]setupRuntimeRequirement, []string) {
	return setupRuntimeRequirementsForSelection(root, commands, nil, selectedRuntimes...)
}

func setupRuntimeRequirementsForSelection(root string, commands []string, selected map[string]string, selectedRuntimes ...string) ([]setupRuntimeRequirement, []string) {
	active := map[string]bool{}
	for runtime := range selected {
		active[runtime] = true
	}
	for _, runtime := range selectedRuntimes {
		active[runtime] = true
	}
	for runtime := range active {
		if runtime == "npm" || runtime == "pnpm" || runtime == "yarn" {
			active["node"] = true
		}
	}
	workspace, err := os.OpenRoot(root)
	if err != nil {
		return nil, []string{"runtime declarations could not be inspected within the repository"}
	}
	defer workspace.Close()
	scopes, warnings := setupRuntimeDeclarationScopes(root, commands, active)
	var requirements []setupRuntimeRequirement
	seen := map[setupRuntimeRequirement]bool{}
	add := func(runtime, value, source string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if len(value) > 160 || !setupRuntimeValuePattern.MatchString(value) {
			warnings = append(warnings, "review the unsupported runtime declaration in "+source)
			return
		}
		requirement := setupRuntimeRequirement{Runtime: runtime, Value: value, Source: source}
		if !seen[requirement] {
			requirements = append(requirements, requirement)
			seen[requirement] = true
		}
	}
	for _, scope := range scopes {
		directory := scope.directory
		sources := map[string]string{}
		readAt := func(directory, name string, nearest bool) ([]byte, bool) {
			// Version-manager files supply defaults. An explicit configured or
			// workflow selection takes precedence; package compatibility bounds
			// and package-manager integrity declarations remain mandatory.
			if runtime := setupDefaultRuntimeDeclaration(name); runtime != "" && selected[runtime] != "" && selected[runtime] != "installed" {
				return nil, false
			}
			family := map[string]string{".node-version": "node", ".nvmrc": "node", "package.json": "node", "pnpm-workspace.yaml": "node", "go.mod": "go", ".go-version": "go", ".python-version": "python", "pyproject.toml": "python", "rust-toolchain": "rust", "rust-toolchain.toml": "rust", "Cargo.toml": "rust", ".ruby-version": "ruby", ".java-version": "java", "global.json": "dotnet"}[name]
			if !scope.runtimes[family] {
				return nil, false
			}
			path := filepath.Join(directory, name)
			var err error
			if nearest {
				path, err = setupRuntimeDeclarationPath(workspace, directory, name)
			} else {
				_, err = workspace.Lstat(path)
			}
			if err != nil {
				if !os.IsNotExist(err) {
					warnings = append(warnings, "runtime declaration could not be inspected within the repository: "+filepath.ToSlash(path))
				}
				return nil, false
			}
			sources[name] = filepath.ToSlash(path)
			// Do not inspect symlinked directories or declarations that are not
			// ordinary repository files. OpenRoot confines subsequent reads.
			if !importManifestExists(root, filepath.Dir(path), filepath.Base(path)) {
				if _, err := workspace.Lstat(path); err == nil || !os.IsNotExist(err) {
					warnings = append(warnings, "runtime declaration is not an inspectable ordinary repository file: "+filepath.ToSlash(path))
				}
				return nil, false
			}
			file, err := workspace.Open(path)
			if err != nil {
				warnings = append(warnings, "runtime declaration could not be read within the repository: "+filepath.ToSlash(path))
				return nil, false
			}
			defer file.Close()
			info, err := file.Stat()
			if err != nil || !info.Mode().IsRegular() {
				return nil, false
			}
			data, err := io.ReadAll(io.LimitReader(file, setupRuntimeDeclarationLimit+1))
			if err != nil || len(data) > setupRuntimeDeclarationLimit {
				warnings = append(warnings, "runtime declaration is unreadable or exceeds the inspection limit: "+filepath.ToSlash(path))
				return nil, false
			}
			return data, true
		}
		read := func(name string) ([]byte, bool) { return readAt(directory, name, true) }
		source := func(name string) string { return sources[name] }
		for _, declaration := range []struct{ path, runtime string }{
			{".go-version", "go"}, {".node-version", "node"}, {".nvmrc", "node"}, {".python-version", "python"},
			{".ruby-version", "ruby"}, {".java-version", "java"}, {"rust-toolchain", "rust"},
		} {
			if data, ok := read(declaration.path); ok {
				var declarations []string
				for _, line := range strings.Split(string(data), "\n") {
					value, _, _ := strings.Cut(line, "#")
					if strings.TrimSpace(value) != "" {
						declarations = append(declarations, value)
					}
				}
				if len(declarations) != 1 {
					warnings = append(warnings, "runtime declaration must contain exactly one value: "+source(declaration.path))
					continue
				}
				add(declaration.runtime, declarations[0], source(declaration.path))
			}
		}
		if data, ok := read("go.mod"); ok {
			for _, line := range strings.Split(string(data), "\n") {
				directive, _, _ := strings.Cut(line, "//")
				fields := strings.Fields(directive)
				if len(fields) > 0 && (fields[0] == "go" || fields[0] == "toolchain") {
					if fields[0] == "toolchain" && selected["go"] != "" && selected["go"] != "installed" {
						continue
					}
					if len(fields) != 2 {
						warnings = append(warnings, "invalid Go runtime declaration in "+source("go.mod"))
						continue
					}
					add("go", fields[1], source("go.mod")+" ("+fields[0]+")")
				}
			}
		}
		for _, declaration := range []struct {
			path, key, runtime string
			tables             []string
		}{
			{"rust-toolchain.toml", "channel", "rust", []string{"toolchain"}}, {"Cargo.toml", "rust-version", "rust", []string{"package", "workspace.package"}},
			{"pyproject.toml", "requires-python", "python", []string{"project"}},
		} {
			if data, ok := read(declaration.path); ok {
				values, err := setupTOMLRuntimeValues(string(data), declaration.tables, declaration.key)
				if err != nil {
					warnings = append(warnings, "runtime declaration requires explicit configuration: "+source(declaration.path)+" ("+declaration.key+")")
				}
				for _, value := range values {
					add(declaration.runtime, value, source(declaration.path)+" ("+declaration.key+")")
				}
			}
		}
		if data, ok := read("package.json"); ok {
			var pkg struct {
				Engines        map[string]string `json:"engines"`
				PackageManager string            `json:"packageManager"`
			}
			if json.Unmarshal(data, &pkg) == nil {
				add("node", pkg.Engines["node"], source("package.json")+" (engines.node)")
				add("package_manager", pkg.PackageManager, source("package.json")+" (packageManager)")
				// Workspace ownership and package engines are separate. Inherit
				// a manager pin only from an ancestor that owns this package.
				packageDir := filepath.Dir(source("package.json"))
				for current := packageDir; strings.TrimSpace(pkg.PackageManager) == "" && current != "."; {
					current = filepath.Dir(current)
					parentData, ok := readAt(current, "package.json", false)
					if !ok {
						continue
					}
					var parent struct {
						PackageManager string            `json:"packageManager"`
						Workspaces     workspacePatterns `json:"workspaces"`
					}
					if json.Unmarshal(parentData, &parent) != nil {
						warnings = append(warnings, "invalid runtime declaration JSON: "+source("package.json"))
						break
					}
					if strings.TrimSpace(parent.PackageManager) == "" {
						continue
					}
					patterns := []string(parent.Workspaces)
					if workspaceData, ok := readAt(current, "pnpm-workspace.yaml", false); ok {
						var pnpm struct {
							Packages []string `yaml:"packages"`
						}
						if yaml.Unmarshal(workspaceData, &pnpm) != nil {
							warnings = append(warnings, "invalid package-manager workspace declaration: "+source("pnpm-workspace.yaml"))
							break
						}
						patterns = append(patterns, pnpm.Packages...)
					}
					patterns = cleanWorkspacePatterns(patterns)
					if !setupRuntimeWorkspacePatternsSupported(patterns) {
						warnings = append(warnings, "package-manager workspace patterns require explicit configuration: "+source("package.json")+" (workspaces) or pnpm-workspace.yaml")
						break
					}
					rel, err := filepath.Rel(current, packageDir)
					if err == nil && workspacePatternsInclude(patterns, filepath.ToSlash(rel)) {
						pkg.PackageManager = parent.PackageManager
						add("package_manager", pkg.PackageManager, source("package.json")+" (packageManager)")
					}
				}
			} else {
				warnings = append(warnings, "invalid runtime declaration JSON: "+source("package.json"))
			}
		}
		if data, ok := read("global.json"); ok {
			var global struct {
				SDK struct {
					Version string `json:"version"`
				} `json:"sdk"`
			}
			if json.Unmarshal(data, &global) == nil {
				add("dotnet", global.SDK.Version, source("global.json")+" (sdk.version)")
			}
		}
	}
	sort.Slice(requirements, func(i, j int) bool {
		return fmt.Sprint(requirements[i]) < fmt.Sprint(requirements[j])
	})
	return requirements, uniqueStrings(warnings)
}

func setupRuntimeWorkspacePatternsSupported(patterns []string) bool {
	for _, pattern := range patterns {
		// The repository matcher supports path globs, ** and exclusions. It
		// does not implement brace expansion, extglobs or escaped glob syntax.
		if strings.ContainsAny(pattern, "{}()\\") {
			return false
		}
		if _, err := path.Match(strings.TrimPrefix(pattern, "!"), ""); err != nil {
			return false
		}
	}
	return true
}

func setupDefaultRuntimeDeclaration(name string) string {
	switch name {
	case ".nvmrc", ".node-version":
		return "node"
	case ".go-version":
		return "go"
	case ".python-version":
		return "python"
	case "rust-toolchain", "rust-toolchain.toml":
		return "rust"
	case ".ruby-version":
		return "ruby"
	case ".java-version":
		return "java"
	default:
		return ""
	}
}

// Parse only the supported declaration tables and string fields. Unrelated
// tool settings do not become runtime policy, and a recognized unsupported
// value is an explicit error rather than a disappearing requirement.
func setupTOMLRuntimeValues(data string, tables []string, key string) ([]string, error) {
	var document map[string]any
	if err := toml.Unmarshal([]byte(data), &document); err != nil {
		return nil, err
	}
	var values []string
	for _, table := range tables {
		current := document
		found := true
		for _, part := range strings.Split(table, ".") {
			nested, exists := current[part]
			if !exists {
				found = false
				break
			}
			var ok bool
			current, ok = nested.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("runtime declaration table %s must be an object", table)
			}
		}
		if !found {
			continue
		}
		raw, exists := current[key]
		if !exists {
			continue
		}
		value, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("runtime declaration %s.%s must be a literal string", table, key)
		}
		values = append(values, value)
	}
	return values, nil
}
