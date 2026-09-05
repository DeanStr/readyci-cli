package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func scanRepoCargo(ctx context.Context, cwd string) *repoCargoMap {
	if !fileExists(filepath.Join(cwd, "Cargo.toml")) {
		return nil
	}
	cargoCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	report, err := buildCargoAffectedReport(cargoCtx, cwd, "rust-")
	if err != nil || len(report.Packages) == 0 {
		return nil
	}
	out := repoCargoMap{WorkspaceRoot: report.WorkspaceRoot}
	for _, pkg := range report.Packages {
		out.Packages = append(out.Packages, repoCargoPackageMap{
			Name:       pkg.Name,
			Path:       pkg.Path,
			Check:      pkg.Check,
			Dependents: append([]string(nil), pkg.Dependents...),
		})
	}
	for _, rule := range report.Rules {
		out.Rules = append(out.Rules, repoCargoRuleMap{
			Paths:  append([]string(nil), rule.Paths...),
			Checks: append([]string(nil), rule.Checks...),
		})
	}
	return &out
}

func scanRepoGoModules(cwd string) []repoGoModuleMap {
	modules := detectGoModules(cwd)
	out := make([]repoGoModuleMap, 0, len(modules))
	for _, module := range modules {
		out = append(out, repoGoModuleMap{Path: displayRel(module)})
	}
	return out
}

func scanRepoNodePackages(cwd string) []repoNodePackageMap {
	var out []repoNodePackageMap
	for _, pkg := range detectNodePackages(cwd) {
		raw, ok := readNodePackage(pkg.Dir)
		if !ok {
			continue
		}
		packageManager := strings.TrimSpace(pkg.PackageManager)
		if packageManager == "" {
			packageManager = strings.TrimSpace(pkg.Manager)
		}
		scripts := sortedStringMapKeys(raw.Scripts)
		contracts := make([]string, 0)
		for _, name := range scripts {
			command := strings.ToLower(raw.Scripts[name])
			if strings.Contains(name, "contract") || strings.Contains(command, "openapi") {
				contracts = append(contracts, name)
			}
		}
		out = append(out, repoNodePackageMap{
			Path:            displayRel(pkg.Rel),
			PackageManager:  packageManager,
			Lockfile:        pkg.LockfileRel,
			PreferredScript: pkg.ScriptName,
			Scripts:         scripts,
			ContractScripts: contracts,
			HasVite:         isLikelyVitePackage(pkg),
			HasReact:        packageUses(raw, "react"),
			HasPlaywright:   pkg.HasPlaywright,
		})
	}
	return out
}

func scanRepoOpenAPI(cwd string, nodes []repoNodePackageMap, cfg config.ProjectConfig) []repoOpenAPIMap {
	var sources []string
	for _, rel := range []string{"docs/api/openapi.yaml", "docs/api/openapi.yml", "openapi.yaml", "openapi.yml"} {
		if fileExists(filepath.Join(cwd, filepath.FromSlash(rel))) {
			sources = append(sources, rel)
		}
	}
	var out []repoOpenAPIMap
	for _, source := range sources {
		item := repoOpenAPIMap{Source: source}
		for _, node := range nodes {
			if len(node.ContractScripts) > 0 {
				item.GeneratedConsumers = appendUniqueString(item.GeneratedConsumers, node.Path)
			}
		}
		for _, name := range sortedCheckNames(cfg.Checks) {
			lowerName := strings.ToLower(name)
			lowerCommand := strings.ToLower(cfg.Checks[name].Command)
			if strings.Contains(lowerName, "spec") || strings.Contains(lowerName, "contract") || strings.Contains(lowerCommand, "openapi") || strings.Contains(lowerCommand, "spectral") {
				item.CheckCandidates = appendUniqueString(item.CheckCandidates, name)
			}
		}
		out = append(out, item)
	}
	return out
}

func scanRepoOpsPaths(cwd string) []string {
	var out []string
	for _, rel := range []string{"deploy", "ops", "infra", ".github/workflows"} {
		if info, err := os.Stat(filepath.Join(cwd, filepath.FromSlash(rel))); err == nil && info.IsDir() {
			out = appendUniqueString(out, rel)
		}
	}
	for _, pattern := range []string{"Dockerfile", "docker-compose*.yml", "docker-compose*.yaml", "compose.yml", "compose.yaml", "Caddyfile"} {
		matches, err := filepath.Glob(filepath.Join(cwd, filepath.FromSlash(pattern)))
		if err != nil {
			continue
		}
		for _, match := range matches {
			if info, statErr := os.Stat(match); statErr == nil && !info.IsDir() {
				out = appendUniqueString(out, relPath(cwd, match))
			}
		}
	}
	sort.Strings(out)
	return out
}

func scanRepoShellPaths(cwd string) []string {
	var out []string
	_ = filepath.WalkDir(cwd, func(path string, entry os.DirEntry, err error) error {
		if err != nil || path == cwd {
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "target", "dist", "coverage", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		rel := relPath(cwd, path)
		rel = filepath.ToSlash(rel)
		if strings.HasSuffix(rel, ".sh") || strings.HasPrefix(rel, "scripts/git-hooks/") {
			out = appendUniqueString(out, rel)
			if len(out) >= 50 {
				return filepath.SkipAll
			}
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func inferRepoImportCandidates(report repoMapReport) []repoImportCandidate {
	var out []repoImportCandidate
	for _, target := range report.Make.Targets {
		if !isLikelyVerificationMakeTarget(target.Name) {
			continue
		}
		dir := makeDirForPath(target.Path)
		display := target.Name
		command := "make " + shellQuoteArg(target.Name)
		if dir != "" {
			display = dir + ":" + target.Name
			command = "make -C " + shellQuoteArg(dir) + " " + shellQuoteArg(target.Name)
		}
		out = append(out, repoImportCandidate{
			Source:     "make",
			Name:       "target " + display,
			Command:    command,
			Check:      checkNameFromMakeTarget(target.Name, dir),
			Reason:     "Make target looks like an existing verification entrypoint.",
			Confidence: 0.7,
			Evidence:   []string{target.Path + ":" + fmt.Sprint(target.Line)},
		})
	}
	for _, ref := range report.CheckReferences {
		if ref.Source != "make" || ref.Check == "" {
			continue
		}
		out = append(out, repoImportCandidate{
			Source:     "make",
			Name:       ref.Context,
			Check:      ref.Check,
			Reason:     "Make wrapper already delegates to readyci check.",
			Confidence: 0.86,
			Evidence:   []string{formatRefLocation(ref)},
		})
	}
	for _, workflow := range report.GitHubActions.Workflows {
		for _, job := range workflow.Jobs {
			if len(job.Commands) == 0 {
				continue
			}
			if !isLikelyVerificationWorkflowJob(workflow, job) {
				continue
			}
			out = append(out, repoImportCandidate{
				Source:     "github-actions",
				Name:       job.Name,
				Command:    strings.Join(job.Commands, " && "),
				Reason:     "GitHub Actions job runs existing verification command(s).",
				Confidence: 0.74,
				Evidence:   []string{workflow.Path},
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source == out[j].Source {
			return out[i].Name < out[j].Name
		}
		return out[i].Source < out[j].Source
	})
	return uniqueRepoImportCandidates(out)
}

func isLikelyVerificationWorkflowJob(workflow repoWorkflowMap, job repoWorkflowJob) bool {
	if isOperationalWorkflowJob(workflow, job) {
		return false
	}
	if isLikelyVerificationWorkflowName(workflow.Path) || isLikelyVerificationWorkflowName(job.Name) {
		return true
	}
	for _, command := range job.Commands {
		if len(readyCIChecksFromCommand(command)) > 0 {
			return true
		}
		for _, invocation := range makeInvocationsFromCommand(command) {
			if isLikelyVerificationMakeTarget(invocation.Target) {
				return true
			}
		}
		if shellCommandLooksLikeVerification(command) {
			return true
		}
	}
	return false
}

func isOperationalWorkflowJob(workflow repoWorkflowMap, job repoWorkflowJob) bool {
	namePath := strings.ToLower(workflow.Path + " " + job.Name)
	if hasSeparatedMarker(namePath, "deploy") || hasSeparatedMarker(namePath, "deployment") || hasSeparatedMarker(namePath, "publish") {
		return true
	}
	commands := strings.ToLower(strings.Join(job.Commands, "\n"))
	for _, marker := range []string{
		" gh release create",
		" gh release upload",
		" gh release delete-asset",
		" npm publish",
		" cargo publish",
		" docker push",
		" kubectl apply",
		" terraform apply",
		" wrangler deploy",
		" pages deploy",
		" readyci-api-deploy",
		" readyci-pages-deploy",
		" readyci-worker-deploy",
	} {
		if strings.Contains(" "+commands, marker) {
			return true
		}
	}
	if hasSeparatedMarker(namePath, "release") && strings.Contains(commands, "gh release") {
		return true
	}
	if hasSeparatedMarker(namePath, "release") && shellCommandLooksLikeReleaseOperation(commands) {
		return true
	}
	return false
}

func shellCommandLooksLikeReleaseOperation(command string) bool {
	lower := strings.ToLower(command)
	for _, marker := range []string{
		"build-release-artifacts",
		"draft release",
		"goreleaser",
		"minisign",
		"release artifact",
		"release_artifact",
		"release asset",
		"release_asset",
		"release signing",
		"release_version",
		"semantic-release",
		"signing key",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func isLikelyVerificationWorkflowName(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, marker := range []string{"check", "test", "lint", "vet", "ci", "e2e", "unit", "db", "spec", "audit", "verify", "validation", "build"} {
		if hasSeparatedMarker(lower, marker) {
			return true
		}
	}
	return false
}

func shellCommandLooksLikeVerification(command string) bool {
	lower := strings.ToLower(command)
	for _, marker := range []string{
		"go test",
		"go vet",
		"cargo test",
		"cargo nextest",
		"cargo clippy",
		"pytest",
		"tox",
		"nox",
		"npm test",
		"npm run test",
		"pnpm test",
		"pnpm check",
		"pnpm lint",
		"pnpm typecheck",
		"yarn test",
		"yarn lint",
		"playwright test",
		"actionlint",
		"shellcheck",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func isLikelyVerificationMakeTarget(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, "%") || strings.HasPrefix(name, ".") {
		return false
	}
	lower := strings.ToLower(name)
	for _, marker := range []string{"check", "test", "lint", "vet", "ci", "e2e", "unit", "db", "release", "spec", "audit", "verify"} {
		if lower == marker || strings.HasPrefix(lower, marker+"-") || strings.HasSuffix(lower, "-"+marker) || strings.Contains(lower, "-"+marker+"-") {
			return true
		}
	}
	return false
}

func hasSeparatedMarker(value string, marker string) bool {
	value = strings.ToLower(value)
	marker = strings.ToLower(marker)
	for _, part := range strings.FieldsFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		if part == marker {
			return true
		}
	}
	return false
}

func checkNameFromMakeTarget(target, dir string) string {
	name := strings.TrimSpace(target)
	lower := strings.ToLower(name)
	for _, prefix := range []string{"remote-check-", "check-", "test-", "lint-", "vet-", "verify-"} {
		if strings.HasPrefix(lower, prefix) && len(name) > len(prefix) {
			return name[len(prefix):]
		}
	}
	for _, suffix := range []string{"-check", "-test", "-lint", "-vet", "-verify"} {
		if strings.HasSuffix(lower, suffix) && len(name) > len(suffix) {
			return name[:len(name)-len(suffix)]
		}
	}
	if name == "check" && dir != "" {
		return strings.ReplaceAll(pathBaseSlash(dir), "_", "-")
	}
	return name
}

func pathBaseSlash(value string) string {
	value = strings.Trim(strings.TrimSpace(filepath.ToSlash(value)), "/")
	if value == "" || value == "." {
		return ""
	}
	return pathBase(value)
}

func pathBase(value string) string {
	if idx := strings.LastIndex(value, "/"); idx >= 0 {
		return value[idx+1:]
	}
	return value
}

func uniqueRepoImportCandidates(values []repoImportCandidate) []repoImportCandidate {
	seen := map[string]bool{}
	out := make([]repoImportCandidate, 0, len(values))
	for _, value := range values {
		key := value.Source + "\x00" + value.Name + "\x00" + value.Command + "\x00" + value.Check
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func repoSignalsFromMap(report repoMapReport) []repoMapSignal {
	var signals []repoMapSignal
	add := func(kind, path string, confidence float64, evidence ...string) {
		signals = append(signals, repoMapSignal{Kind: kind, Path: path, Confidence: confidence, Evidence: uniqueStrings(evidence)})
	}
	if len(report.Make.Targets) > 0 {
		add("make-wrappers", "Makefile", 0.83, fmt.Sprintf("%d target(s)", len(report.Make.Targets)))
	}
	if len(report.GitHubActions.Workflows) > 0 {
		add("github-actions", ".github/workflows", 0.78, fmt.Sprintf("%d workflow(s)", len(report.GitHubActions.Workflows)))
	}
	if report.Cargo != nil {
		add("cargo-workspace", "Cargo.toml", 0.9, fmt.Sprintf("%d package(s)", len(report.Cargo.Packages)))
	}
	for _, module := range report.GoModules {
		add("go-module", module.Path, 0.78, relJoin(module.Path, "go.mod"))
	}
	for _, node := range report.NodePackages {
		add("node-package", node.Path, 0.82, "script="+node.PreferredScript)
	}
	for _, openapi := range report.OpenAPI {
		add("openapi-spec", openapi.Source, 0.86, openapi.GeneratedConsumers...)
	}
	for _, opsPath := range report.OpsPaths {
		add("ops-path", opsPath, 0.72, opsPath)
	}
	for _, shellPath := range report.ShellPaths {
		add("shell-script", shellPath, 0.72, shellPath)
	}
	return signals
}

func undefinedRepoChecks(cfg config.ProjectConfig, refs []repoCheckReference) []repoUndefinedCheck {
	byCheck := map[string][]repoCheckReference{}
	for _, ref := range refs {
		check := strings.TrimSpace(ref.Check)
		if ref.Archive || check == "" || isDynamicCheckReference(check) {
			continue
		}
		if _, ok := cfg.Checks[check]; ok {
			continue
		}
		byCheck[check] = append(byCheck[check], ref)
	}
	var out []repoUndefinedCheck
	for check, refs := range byCheck {
		out = append(out, repoUndefinedCheck{Check: check, References: refs})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Check < out[j].Check })
	return out
}

func undefinedRepoMakeTargets(makeMap repoMakeMap, refs []repoMakeReference) []repoUndefinedMake {
	defined := map[string]bool{}
	for _, target := range makeMap.Targets {
		defined[makeTargetKey(makeDirForPath(target.Path), target.Name)] = true
	}
	byTarget := map[string][]repoMakeReference{}
	for _, ref := range refs {
		if ref.Archive || ref.Target == "" || defined[makeTargetKey(ref.Dir, ref.Target)] {
			continue
		}
		key := makeTargetKey(ref.Dir, ref.Target)
		byTarget[key] = append(byTarget[key], ref)
	}
	var out []repoUndefinedMake
	for _, refs := range byTarget {
		target := refs[0].Target
		if refs[0].Dir != "" {
			target = refs[0].Dir + ":" + target
		}
		out = append(out, repoUndefinedMake{Target: target, References: refs})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Target < out[j].Target })
	return out
}

func unreferencedRepoChecks(cfg config.ProjectConfig, configPath string, refs []repoCheckReference) []string {
	cfg = repoMapUsageConfig(cfg, configPath)
	used := map[string]bool{}
	for _, ref := range refs {
		if ref.Archive {
			continue
		}
		used[ref.Check] = true
	}
	for _, rule := range cfg.Affected.Rules {
		for _, check := range rule.Checks {
			used[check] = true
		}
	}
	for _, check := range cfg.Affected.UnmatchedChecks {
		used[check] = true
	}
	for _, test := range cfg.Affected.Tests {
		for _, check := range test.Checks {
			used[check] = true
		}
	}
	for parent, children := range cfg.Affected.Subsumes {
		used[parent] = true
		for _, child := range children {
			used[child] = true
		}
	}
	var out []string
	for _, name := range sortedCheckNames(cfg.Checks) {
		if !used[name] {
			out = append(out, name)
		}
	}
	return out
}

func repoMapUsageConfig(cfg config.ProjectConfig, configPath string) config.ProjectConfig {
	expanded, warnings, err := expandAffectedConfigForConfigPath(cfg, configPath)
	if err == nil && affectedProfileExpansionError(cfg, expanded, warnings) == nil {
		cfg = expanded
	}
	return projectConfigWithCheckWhenRules(cfg)
}
