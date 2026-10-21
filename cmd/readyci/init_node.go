package main

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"mvdan.cc/sh/v3/syntax"
)

func detectedNodeScript(cwd string) (string, bool) {
	pkg, ok := readNodePackage(cwd)
	if !ok {
		return "", false
	}
	// An existing test entrypoint has stronger scope evidence than a generic
	// check/build script, which commonly only typechecks or compiles a project.
	for _, name := range []string{"test", "test:ci", "test:unit"} {
		command := strings.TrimSpace(pkg.Scripts[name])
		if command != "" && !nodeScriptIsPlaceholder(command) {
			return name, true
		}
	}
	if setupCommandRunsTests(pkg.Scripts["check"]) {
		return "check", true
	}
	return "", false
}

// Reject scripts that only print a message or return a fixed status, regardless
// of their wording. Keep opaque custom harnesses and dynamic commands: their
// test scope cannot be established by looking for a known runner name alone.
func nodeScriptIsPlaceholder(command string) bool {
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return false
	}
	placeholder := true
	syntax.Walk(file, func(node syntax.Node) bool {
		switch node.(type) {
		case *syntax.TestClause, *syntax.ArithmCmd:
			// Shell assertions may themselves be the test suite.
			placeholder = false
			return false
		}
		call, ok := node.(*syntax.CallExpr)
		if !ok {
			return placeholder
		}
		args := executableShellArgs(shellWordsFromSyntax(call.Args))
		for _, arg := range args {
			if arg.Dynamic {
				placeholder = false
				return false
			}
		}
		if len(args) == 0 {
			return true
		}
		switch args[0].Text {
		case "echo", "printf", ":", "true", "false", "exit", "cd":
		default:
			placeholder = false
		}
		return placeholder
	})
	return placeholder
}

func nodeCheckCommand(cwd, scriptName string) string {
	pkg, ok := nodePackageForDir(cwd, cwd)
	if !ok {
		manager, lockfileRel, lockfileDir := nearestNodeLockfile(cwd, cwd, "")
		pkg = detectedNodePackage{
			Dir:            cwd,
			ScriptName:     scriptName,
			Manager:        manager,
			LockfileRel:    lockfileRel,
			LockfileDir:    lockfileDir,
			LockfileDirRel: relPath(cwd, lockfileDir),
		}
		return nodePackageCheckCommand(pkg)
	}
	pkg.ScriptName = scriptName
	return nodePackageCheckCommand(pkg)
}

type nodePackageJSON struct {
	Scripts         map[string]string `json:"scripts"`
	PackageManager  string            `json:"packageManager"`
	Workspaces      workspacePatterns `json:"workspaces"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

type workspacePatterns []string

func (patterns *workspacePatterns) UnmarshalJSON(data []byte) error {
	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		*patterns = cleanWorkspacePatterns(list)
		return nil
	}
	var object struct {
		Packages []string `json:"packages"`
	}
	if err := json.Unmarshal(data, &object); err == nil {
		*patterns = cleanWorkspacePatterns(object.Packages)
		return nil
	}
	return nil
}

type detectedNodePackage struct {
	Dir            string
	Rel            string
	ScriptName     string
	PackageManager string
	Manager        string
	LockfileRel    string
	LockfileDir    string
	LockfileDirRel string
	HasPlaywright  bool
	HasNext        bool
}

func detectNodePackages(cwd string) []detectedNodePackage {
	var packages []detectedNodePackage
	_ = filepath.WalkDir(cwd, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != cwd && shouldSkipDetectDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != "package.json" {
			return nil
		}
		if pkg, ok := nodePackageForDir(cwd, filepath.Dir(path)); ok {
			packages = append(packages, pkg)
		}
		return nil
	})
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].Rel == "" {
			return true
		}
		if packages[j].Rel == "" {
			return false
		}
		return packages[i].Rel < packages[j].Rel
	})
	return packages
}

func nodePackageForDir(cwd, dir string) (detectedNodePackage, bool) {
	pkg, ok := readNodePackage(dir)
	if !ok {
		return detectedNodePackage{}, false
	}
	scriptName, ok := preferredNodeScript(pkg.Scripts)
	if !ok {
		return detectedNodePackage{}, false
	}
	rel := relPath(cwd, dir)
	if pkg.PackageManager == "" {
		pkg.PackageManager = nearestPackageManager(cwd, dir)
	}
	manager, lockfileRel, lockfileDir := nearestNodeLockfile(cwd, dir, pkg.PackageManager)
	if manager == "" {
		manager = packageManagerName(pkg.PackageManager)
	}
	return detectedNodePackage{
		Dir:            dir,
		Rel:            rel,
		ScriptName:     scriptName,
		PackageManager: strings.TrimSpace(pkg.PackageManager),
		Manager:        manager,
		LockfileRel:    lockfileRel,
		LockfileDir:    lockfileDir,
		LockfileDirRel: relPath(cwd, lockfileDir),
		HasPlaywright:  hasAnyFile(dir, "playwright.config.ts", "playwright.config.js", "playwright.config.mjs", "playwright.config.cjs", "playwright.config.mts", "playwright.config.cts"),
		HasNext:        packageUses(pkg, "next"),
	}, true
}

func readNodePackage(dir string) (nodePackageJSON, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nodePackageJSON{}, false
	}
	var pkg nodePackageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nodePackageJSON{}, false
	}
	return pkg, true
}

func preferredNodeScript(scripts map[string]string) (string, bool) {
	for _, name := range []string{"check", "test", "build"} {
		if strings.TrimSpace(scripts[name]) != "" {
			return name, true
		}
	}
	return "", false
}

func nearestNodeLockfile(cwd, dir, packageManager string) (string, string, string) {
	preferred := packageManagerName(packageManager)
	if manager, lockRel, ok := nodeLockfileInDir(cwd, dir, preferred); ok {
		return manager, lockRel, dir
	}
	if sameFilepath(dir, cwd) {
		return "", "", ""
	}
	for current := filepath.Dir(dir); ; current = filepath.Dir(current) {
		if nodeWorkspaceOwnsPackage(current, dir) {
			if manager, lockRel, ok := nodeLockfileInDir(cwd, current, preferred); ok {
				return manager, lockRel, current
			}
		}
		if sameFilepath(current, cwd) {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return "", "", ""
}

func nodeLockfileInDir(cwd, dir, preferred string) (string, string, bool) {
	if preferred != "" {
		if lockRel, ok := lockfileForManager(cwd, dir, preferred); ok {
			return preferred, lockRel, true
		}
		return "", "", false
	}
	for _, manager := range []string{"pnpm", "yarn", "npm"} {
		if lockRel, ok := lockfileForManager(cwd, dir, manager); ok {
			return manager, lockRel, true
		}
	}
	return "", "", false
}

func lockfileForManager(cwd, dir, manager string) (string, bool) {
	name := map[string]string{
		"pnpm": "pnpm-lock.yaml",
		"yarn": "yarn.lock",
		"npm":  "package-lock.json",
	}[manager]
	if name == "" {
		return "", false
	}
	path := filepath.Join(dir, name)
	if !fileExists(path) {
		return "", false
	}
	return relPath(cwd, path), true
}

func nodeWorkspaceOwnsPackage(root, dir string) bool {
	rel := relPath(root, dir)
	if rel == "" {
		return false
	}
	return workspacePatternsInclude(nodeWorkspacePatterns(root), rel)
}

func nodeWorkspacePatterns(dir string) []string {
	var patterns []string
	if pkg, ok := readNodePackage(dir); ok {
		patterns = append(patterns, pkg.Workspaces...)
	}
	patterns = append(patterns, pnpmWorkspacePatterns(dir)...)
	return cleanWorkspacePatterns(patterns)
}

func pnpmWorkspacePatterns(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, "pnpm-workspace.yaml"))
	if err != nil {
		return nil
	}
	var workspace struct {
		Packages []string `yaml:"packages"`
	}
	if err := yaml.Unmarshal(data, &workspace); err != nil {
		return nil
	}
	return workspace.Packages
}

func cleanWorkspacePatterns(values []string) []string {
	patterns := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if value != "" {
			patterns = append(patterns, value)
		}
	}
	return patterns
}

func workspacePatternsInclude(patterns []string, rel string) bool {
	rel = cleanWorkspacePatternPath(rel)
	matched := false
	for _, pattern := range patterns {
		negated := strings.HasPrefix(pattern, "!")
		if negated {
			pattern = strings.TrimSpace(strings.TrimPrefix(pattern, "!"))
		}
		if workspacePatternMatches(pattern, rel) {
			matched = !negated
		}
	}
	return matched
}

func workspacePatternMatches(pattern, rel string) bool {
	pattern = cleanWorkspacePatternPath(pattern)
	if pattern == "" || rel == "" {
		return false
	}
	if ok, err := path.Match(pattern, rel); err == nil && ok {
		return true
	}
	return workspacePatternSegmentsMatch(strings.Split(pattern, "/"), strings.Split(rel, "/"))
}

func workspacePatternSegmentsMatch(pattern, rel []string) bool {
	if len(pattern) == 0 {
		return len(rel) == 0
	}
	if pattern[0] == "**" {
		for i := 0; i <= len(rel); i++ {
			if workspacePatternSegmentsMatch(pattern[1:], rel[i:]) {
				return true
			}
		}
		return false
	}
	if len(rel) == 0 {
		return false
	}
	ok, err := path.Match(pattern[0], rel[0])
	if err != nil || !ok {
		return false
	}
	return workspacePatternSegmentsMatch(pattern[1:], rel[1:])
}

func cleanWorkspacePatternPath(value string) string {
	value = filepath.ToSlash(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "./")
	value = strings.Trim(value, "/")
	return value
}

func nearestPackageManager(cwd, dir string) string {
	for current := dir; ; current = filepath.Dir(current) {
		if pkg, ok := readNodePackage(current); ok && strings.TrimSpace(pkg.PackageManager) != "" {
			if sameFilepath(current, dir) || nodeWorkspaceOwnsPackage(current, dir) {
				return strings.TrimSpace(pkg.PackageManager)
			}
		}
		if sameFilepath(current, cwd) {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return ""
}

func packageManagerName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	name := strings.SplitN(value, "@", 2)[0]
	name = strings.TrimSpace(name)
	switch name {
	case "pnpm", "yarn", "npm":
		return name
	default:
		return ""
	}
}

func nodePackageCheckCommand(pkg detectedNodePackage) string {
	manager := pkg.Manager
	if manager == "" {
		manager = "npm"
	}
	packageRelFromLockfile := relPath(pkg.LockfileDir, pkg.Dir)
	switch manager {
	case "pnpm":
		if pkg.LockfileDir != "" && !sameFilepath(pkg.LockfileDir, pkg.Dir) {
			command := "pnpm install --frozen-lockfile"
			if packageRelFromLockfile == "" {
				return command + " && pnpm " + shellQuoteArg(pkg.ScriptName)
			}
			return withWorkingDir(pkg.LockfileDirRel, command+" && pnpm --dir "+shellQuoteArg(packageRelFromLockfile)+" "+shellQuoteArg(pkg.ScriptName))
		}
		install := "pnpm install"
		if pkg.LockfileRel != "" {
			install += " --frozen-lockfile"
		}
		command := install + " && pnpm " + shellQuoteArg(pkg.ScriptName)
		return withWorkingDir(pkg.Rel, command)
	case "yarn":
		install := "yarn install"
		if pkg.LockfileRel != "" {
			install = "if yarn --version | grep -q '^1\\.'; then yarn install --frozen-lockfile; else yarn install --immutable; fi"
		}
		if pkg.LockfileDir != "" && !sameFilepath(pkg.LockfileDir, pkg.Dir) {
			return withWorkingDir(pkg.LockfileDirRel, install+" && yarn --cwd "+shellQuoteArg(packageRelFromLockfile)+" run "+shellQuoteArg(pkg.ScriptName))
		}
		return withWorkingDir(pkg.Rel, install+" && yarn run "+shellQuoteArg(pkg.ScriptName))
	case "npm":
		install := "npm install"
		if pkg.LockfileDir != "" && sameFilepath(pkg.LockfileDir, pkg.Dir) {
			install = "npm ci"
		}
		if pkg.LockfileDir != "" && !sameFilepath(pkg.LockfileDir, pkg.Dir) {
			return withWorkingDir(pkg.LockfileDirRel, "npm ci && npm --prefix "+shellQuoteArg(packageRelFromLockfile)+" run "+shellQuoteArg(pkg.ScriptName))
		}
		return withWorkingDir(pkg.Rel, install+" && npm run "+shellQuoteArg(pkg.ScriptName))
	default:
		return withWorkingDir(pkg.Rel, "npm install && npm run "+shellQuoteArg(pkg.ScriptName))
	}
}

func withWorkingDir(rel, command string) string {
	if rel == "" {
		return command
	}
	return "cd " + shellQuoteArg(rel) + " && " + command
}

func packageUses(pkg nodePackageJSON, name string) bool {
	if _, ok := pkg.Dependencies[name]; ok {
		return true
	}
	_, ok := pkg.DevDependencies[name]
	return ok
}
