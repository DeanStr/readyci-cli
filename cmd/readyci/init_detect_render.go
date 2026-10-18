package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func renderDetectedProjectTemplate(projectID, image, profile string, checks []detectedCheck, affectedRules []config.AffectedRule, signals []string, unmatchedChecks config.CommandList) string {
	image = strings.TrimSpace(image)
	networkConfig := detectedNetworkConfig(profile, checks)
	imageLine := fmt.Sprintf(`# Uncomment after your worker advertises this image:
# image: %s

`, strconv.Quote(remoteCIToolchainImage))
	if image != "" {
		imageLine = fmt.Sprintf("image: %s\n\n", strconv.Quote(image))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "version: 1\nproject_id: %s\n%s", strconv.Quote(projectID), imageLine)
	if len(signals) > 0 {
		b.WriteString("# detected project signals:\n")
		for _, signal := range signals {
			fmt.Fprintf(&b, "# - %s\n", signal)
		}
		b.WriteString("\n")
	}
	b.WriteString(`sync:
  include_untracked: false
  # Built-in filtering excludes .env*, build outputs, and dependency directories.
  exclude: [".npmrc*", "test-results", "tmp"]

artifacts:
  paths:
    - coverage/**
    - test-results/**

`)
	cachePaths := []string{}
	for _, check := range checks {
		for _, runtime := range detectedCheckRuntimes(check) {
			switch runtime {
			case "go":
				cachePaths = append(cachePaths, "~/.cache/go-build", "~/go/pkg/mod")
			case "node":
				cachePaths = append(cachePaths, "~/.npm", "~/.cache/yarn", "~/.local/share/pnpm/store")
			case "rust":
				if len(check.RustTargetPaths) > 0 {
					cachePaths = append(cachePaths, check.RustTargetPaths...)
				} else {
					cachePaths = append(cachePaths, "target")
				}
				cachePaths = append(cachePaths, "~/.cargo/registry", "~/.cargo/git")
			}
		}
	}
	if len(cachePaths) > 0 {
		b.WriteString("cache:\n  paths:\n")
		for _, path := range cachePaths {
			fmt.Fprintf(&b, "    - %s\n", strconv.Quote(path))
		}
		b.WriteString("\n")
	}

	if len(affectedRules) > 0 {
		b.WriteString("affected:\n")
		if len(unmatchedChecks) > 0 {
			b.WriteString("  unmatched_checks:\n")
			for _, check := range unmatchedChecks {
				fmt.Fprintf(&b, "    - %s\n", strconv.Quote(check))
			}
		}
		b.WriteString("  rules:\n")
		for _, affectedRule := range affectedRules {
			b.WriteString("    - paths:\n")
			for _, path := range affectedRule.Paths {
				fmt.Fprintf(&b, "        - %s\n", strconv.Quote(path))
			}
			b.WriteString("      checks:\n")
			for _, check := range affectedRule.Checks {
				fmt.Fprintf(&b, "        - %s\n", strconv.Quote(check))
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("checks:\n")
	for _, check := range checks {
		fmt.Fprintf(&b, "  %s:\n", check.Name)
		fmt.Fprintf(&b, "    command: %s\n", strconv.Quote(check.Command))
		if check.Changes != "" {
			fmt.Fprintf(&b, "    changes: %s\n", check.Changes)
		}
		b.WriteString("    sync:\n      include_untracked: true\n")
		if strings.TrimSpace(check.Timeout) != "" {
			fmt.Fprintf(&b, "    timeout: %s\n", check.Timeout)
		}
	}
	fmt.Fprintf(&b, `
network:
%s
`, networkConfig)
	return b.String()
}

// Generated egress is visible in setup's preview and remains subject to server
// policy. Unknown project commands get no inferred network access.
func detectedNetworkConfig(profile string, checks []detectedCheck) string {
	if profile == "strict" {
		return "  mode: none"
	}
	var hosts []string
	seen := map[string]bool{}
	for _, check := range checks {
		var selected []string
		for _, runtime := range detectedCheckRuntimes(check) {
			switch runtime {
			case "go":
				selected = append(selected, "go.dev", "dl.google.com", "proxy.golang.org", "sum.golang.org", "storage.googleapis.com")
			case "node":
				selected = append(selected, "registry.npmjs.org", "registry.yarnpkg.com")
			case "rust":
				selected = append(selected, "index.crates.io", "static.crates.io", "static.rust-lang.org")
			}
		}
		for _, host := range selected {
			if !seen[host] {
				seen[host] = true
				hosts = append(hosts, host)
			}
		}
	}
	if len(hosts) == 0 {
		return "  mode: none\n  # Add an explicit allowlist if this command fetches dependencies."
	}
	return "  mode: allowlist\n  allowlist:\n    - " + strings.Join(hosts, "\n    - ")
}

func detectedCheckRuntimes(check detectedCheck) []string {
	if len(check.Runtimes) > 0 {
		return check.Runtimes
	}
	return []string{check.Name}
}
