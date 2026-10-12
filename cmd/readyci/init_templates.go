package main

import (
	"fmt"
	"strconv"
	"strings"
)

func remoteCIProjectTemplate(projectID, image string) string {
	return remoteCIProjectTemplateWithProfile(projectID, image, "remote-ci")
}

func remoteCIProjectTemplateWithProfile(projectID, image, profile string) string {
	image = strings.TrimSpace(image)
	networkMode := "none"
	if profile == "strict" {
		networkMode = "none"
	}
	imageLine := fmt.Sprintf(`# Uncomment after your worker advertises this image:
# image: %s

`, strconv.Quote(remoteCIToolchainImage))
	if image != "" {
		imageLine = fmt.Sprintf("image: %s\n\n", strconv.Quote(image))
	}
	return fmt.Sprintf(`version: 1
project_id: %s
%ssync:
  include_untracked: false
  # Built-in filtering excludes .env*, build outputs, and dependency directories.
  exclude: [".npmrc*", "test-results", "tmp"]

cache:
  mode: cache-only
  paths:
    - target
    - ~/.npm

artifacts:
  paths:
    - coverage/**
    - test-results/**

checks:
  ci:
    command: make check
    sync:
      include_untracked: true
    timeout: 20m

network:
  mode: %s
`, strconv.Quote(projectID), imageLine, networkMode)
}

func remoteCIProjectTemplateWithStandardHelpers(projectID, image, profile string) string {
	image = strings.TrimSpace(image)
	networkMode := "none"
	if profile == "strict" {
		networkMode = "none"
	}
	imageLine := fmt.Sprintf(`# Uncomment after your worker advertises this image:
# image: %s

`, strconv.Quote(remoteCIToolchainImage))
	if image != "" {
		imageLine = fmt.Sprintf("image: %s\n\n", strconv.Quote(image))
	}
	return fmt.Sprintf(`version: 1
project_id: %s
%ssync:
  include_untracked: false
  # Built-in filtering excludes .env*, build outputs, and dependency directories.
  exclude: [".npmrc*", "test-results", "tmp"]

cache:
  mode: cache-only
  paths:
    - target
    - ~/.npm

artifacts:
  paths:
    - coverage/**
    - test-results/**

affected:
  rules:
    - paths:
        - "**"
      checks:
        - "ci"
    - paths:
        - "**/*.sh"
        - "scripts/git-hooks/**"
      checks:
        - "shell-syntax"
    - paths:
        - ".github/workflows/**"
        - "deploy/**"
        - "ops/**"
        - "infra/**"
        - "docker-compose*.yml"
        - "docker-compose*.yaml"
        - "compose.yml"
        - "compose.yaml"
        - "Caddyfile"
      checks:
        - "ops-config"

checks:
  ci:
    command: make check
    sync:
      include_untracked: true
    timeout: 20m
  shell-syntax:
    command: scripts/readyci-check-shell-syntax.sh
    sync:
      include_untracked: true
    timeout: 10m
  ops-config:
    command: scripts/readyci-check-ops-config.sh
    sync:
      include_untracked: true
    timeout: 10m

network:
  mode: %s
`, strconv.Quote(projectID), imageLine, networkMode)
}
