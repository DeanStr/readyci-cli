package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func renderDetectedProjectTemplate(projectID, image, profile string, checks []detectedCheck, affectedRules []config.AffectedRule, signals []string, unmatchedChecks config.CommandList) string {
	image = strings.TrimSpace(image)
	networkMode := "default"
	if profile == "strict" {
		networkMode = "none"
	}
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
  respect_gitignore: true
  exclude:
    - .env
    - .env.local
    - .env.development
    - .env.test
    - .env.staging
    - .env.production
    - .env.preview
    - .env.*.local
    - .envrc
    - .envrc.local
    - .npmrc
    - .npmrc.local
    - node_modules
    - target
    - dist
    - coverage
    - test-results
    - tmp

cache:
  mode: cache-only
  paths:
    - ~/.cache/go-build
    - ~/go/pkg/mod
    - target
    - ~/.npm

artifacts:
  paths:
    - coverage/**
    - test-results/**

`)
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
resources:
  default:
    size: standard
    timeout: 20m

network:
  mode: %s
`, networkMode)
	return b.String()
}
