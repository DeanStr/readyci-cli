package main

import (
	"path/filepath"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

type detectedCheck struct {
	Name             string
	Command          string
	Prepare          []string
	Services         []string
	Changes          string
	Timeout          string
	Docker           bool
	Network          string
	ConcurrencyGroup string
	ConcurrencyLimit int
	KeepFailed       bool
}

type detectedService struct {
	Name     string
	Type     string
	Version  string
	Env      map[string]string
	Health   config.ServiceHealthConfig
	LogPaths []string
}

func detectedProjectTemplateWithProfile(cwd, projectID, image string, profile string) string {
	return detectedProjectTemplateWithProfileOptions(cwd, projectID, image, profile, false)
}

func detectedProjectTemplateWithProfileOptions(cwd, projectID, image string, profile string, includeStandardHelpers bool) string {
	template, ok := basicDetectedProjectTemplate(cwd, projectID, image, profile, includeStandardHelpers)
	if ok {
		return template
	}
	return remoteCIProjectTemplateWithProfile(projectID, image, profile)
}

func basicInferredProjectTemplate(cwd, projectID, image string) (string, bool) {
	return basicDetectedProjectTemplate(cwd, projectID, image, "remote-ci", false)
}

func basicDetectedProjectTemplate(cwd, projectID, image, profile string, includeStandardHelpers bool) (string, bool) {
	var checks []detectedCheck
	var affectedRules []config.AffectedRule
	var signals []string
	used := map[string]bool{}

	if fileExists(filepath.Join(cwd, "go.mod")) {
		checkName := uniqueCheckName("go", used)
		checks = append(checks, detectedCheck{
			Name:    checkName,
			Command: "go test ./...",
			Timeout: "20m",
		})
		affectedRules = appendDetectedAffectedRule(affectedRules, checkName, "go.mod", "go.sum", "**/*.go")
		signals = append(signals, "go.mod -> go test ./...")
	}
	if fileExists(filepath.Join(cwd, "Cargo.toml")) {
		checkName := uniqueCheckName("rust", used)
		checks = append(checks, detectedCheck{
			Name:    checkName,
			Command: "cargo test --workspace --all-targets",
			Timeout: "20m",
		})
		affectedRules = appendDetectedAffectedRule(affectedRules, checkName, "Cargo.toml", "Cargo.lock", "rust-toolchain", "rust-toolchain.toml", ".cargo/**", "**/*.rs")
		signals = append(signals, "Cargo.toml -> cargo test --workspace --all-targets")
	}
	if script, ok := detectedNodeScript(cwd); ok {
		command := nodeCheckCommand(cwd, script)
		checkName := uniqueCheckName("node", used)
		checks = append(checks, detectedCheck{
			Name:    checkName,
			Command: command,
			Timeout: "20m",
		})
		affectedRules = appendDetectedAffectedRule(affectedRules, checkName,
			"package.json",
			"package-lock.json",
			"pnpm-lock.yaml",
			"yarn.lock",
			"npm-shrinkwrap.json",
			"tsconfig*.json",
			"vite.config.*",
			"vitest.config.*",
			"playwright.config.*",
			"src/**",
			"public/**",
			"types/**",
			"app/**",
			"apps/**",
			"frontend/**",
			"packages/**",
			"services/**",
			"web/**",
		)
		signals = append(signals, "package.json scripts -> "+command)
	}
	makeMap := scanRepoMake(cwd)
	for _, candidate := range []struct{ target, check, changes string }{
		{"check", "ci", ""}, {"check-ci", "ci", ""}, {"check-touched", "affected", "working"}, {"web-check", "web", ""},
	} {
		if target, ok := makeMapTarget(makeMap, candidate.target); ok {
			checkName := uniqueCheckName(candidate.check, used)
			command := makeTargetCommand(target)
			checks = append(checks, detectedCheck{Name: checkName, Command: command, Changes: candidate.changes, Timeout: "20m"})
			affectedRules = appendDetectedAffectedRule(affectedRules, checkName, "**")
			signals = append(signals, "Makefile "+candidate.target+" target -> "+command)
		}
	}
	hasProjectChecks := len(checks) > 0
	unmatchedChecks := make(config.CommandList, 0, len(checks))
	for _, check := range checks {
		unmatchedChecks = append(unmatchedChecks, check.Name)
	}
	if includeStandardHelpers {
		if !hasProjectChecks {
			checkName := uniqueCheckName("ci", used)
			checks = append(checks, detectedCheck{
				Name:    checkName,
				Command: "make check",
				Timeout: "20m",
			})
			affectedRules = appendDetectedAffectedRule(affectedRules, checkName, "**")
			signals = append(signals, "generic starter -> make check")
		}
		shellCheckName := uniqueCheckName("shell-syntax", used)
		opsCheckName := uniqueCheckName("ops-config", used)
		checks = append(checks,
			detectedCheck{
				Name:    shellCheckName,
				Command: "scripts/readyci-check-shell-syntax.sh",
				Timeout: "10m",
			},
			detectedCheck{
				Name:    opsCheckName,
				Command: "scripts/readyci-check-ops-config.sh",
				Timeout: "10m",
			},
		)
		affectedRules = appendDetectedAffectedRule(affectedRules, shellCheckName, "**/*.sh", "scripts/git-hooks/**")
		affectedRules = appendDetectedAffectedRule(affectedRules, opsCheckName,
			".github/workflows/**",
			"deploy/**",
			"ops/**",
			"infra/**",
			"docker-compose*.yml",
			"docker-compose*.yaml",
			"compose.yml",
			"compose.yaml",
			"Caddyfile",
		)
		signals = append(signals, "setup helpers -> shell-syntax and ops-config")
	}
	if len(checks) == 0 {
		return "", false
	}

	return renderDetectedProjectTemplate(projectID, image, profile, checks, affectedRules, signals, unmatchedChecks), true
}

func appendDetectedAffectedRule(rules []config.AffectedRule, check string, paths ...string) []config.AffectedRule {
	if strings.TrimSpace(check) == "" || len(paths) == 0 {
		return rules
	}
	return append(rules, config.AffectedRule{
		Paths:  paths,
		Checks: config.CommandList{check},
	})
}

func makeMapTarget(makeMap repoMakeMap, target string) (repoMakeTarget, bool) {
	for _, candidate := range makeMap.Targets {
		if candidate.Name == target {
			return candidate, true
		}
	}
	return repoMakeTarget{}, false
}

func makeTargetCommand(target repoMakeTarget) string {
	dir := makeDirForPath(target.Path)
	if dir == "" {
		return "make " + shellQuoteArg(target.Name)
	}
	return "make -C " + shellQuoteArg(dir) + " " + shellQuoteArg(target.Name)
}
