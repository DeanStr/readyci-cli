package main

import (
	"fmt"
	"sort"
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
	for _, check := range checks {
		signals = append(signals, check.PreparationReview...)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "version: 1\nproject_id: %s\n%s", strconv.Quote(projectID), imageLine)
	if len(signals) > 0 {
		b.WriteString("# detected project signals:\n")
		for _, signal := range signals {
			// Signals contain repository-controlled names and requirement text.
			// Quote control characters so a newline cannot escape this comment.
			fmt.Fprintf(&b, "# - %s\n", strconv.Quote(signal))
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
			case "python":
				cachePaths = append(cachePaths, "~/.cache/pip")
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
		if len(check.Steps) == 0 {
			fmt.Fprintf(&b, "    command: %s\n", strconv.Quote(check.Command))
		} else {
			b.WriteString("    steps:\n")
			for _, step := range check.Steps {
				fmt.Fprintf(&b, "      - name: %s\n        command: %s\n", strconv.Quote(step.Name), strconv.Quote(step.Command))
				if len(step.Env) > 0 {
					b.WriteString("        env:\n")
					var keys []string
					for key := range step.Env {
						keys = append(keys, key)
					}
					sort.Strings(keys)
					for _, key := range keys {
						fmt.Fprintf(&b, "          %s: %s\n", key, strconv.Quote(step.Env[key]))
					}
				}
				if step.Timeout != "" {
					fmt.Fprintf(&b, "        timeout: %s\n", step.Timeout)
				}
			}
		}
		if len(check.RuntimeRequirements) > 0 {
			b.WriteString("    runtime_requirements:\n")
			var names []string
			for name := range check.RuntimeRequirements {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				fmt.Fprintf(&b, "      %s: %s\n", name, strconv.Quote(check.RuntimeRequirements[name]))
			}
		}
		if len(check.Prepare) > 0 {
			b.WriteString("    prepare:\n")
			for _, command := range check.Prepare {
				fmt.Fprintf(&b, "      - %s\n", strconv.Quote(command))
			}
		}
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
	hosts := detectedDependencyHosts(checks)
	if len(hosts) == 0 {
		return "  mode: none\n  # Add an explicit allowlist if this command fetches dependencies."
	}
	return "  mode: allowlist\n  allowlist:\n    - " + strings.Join(hosts, "\n    - ")
}

func detectedDependencyHosts(checks []detectedCheck) []string {
	var hosts []string
	seen := map[string]bool{}
	for _, check := range checks {
		var selected []string
		for _, runtime := range detectedCheckRuntimes(check) {
			switch runtime {
			case "go":
				selected = append(selected, "go.dev", "dl.google.com", "proxy.golang.org", "sum.golang.org", "storage.googleapis.com")
			case "node":
				selected = append(selected, "registry.npmjs.org", "registry.yarnpkg.com", "nodejs.org")
			case "npm", "pnpm", "yarn":
				selected = append(selected, "registry.npmjs.org", "registry.yarnpkg.com", "nodejs.org")
			case "rust":
				selected = append(selected, "index.crates.io", "static.crates.io", "static.rust-lang.org")
			case "python":
				selected = append(selected, "pypi.org", "files.pythonhosted.org")
			}
		}
		for _, host := range selected {
			if !seen[host] {
				seen[host] = true
				hosts = append(hosts, host)
			}
		}
	}
	return hosts
}

func detectedCheckRuntimes(check detectedCheck) []string {
	if len(check.Runtimes) > 0 {
		return check.Runtimes
	}
	return []string{check.Name}
}
