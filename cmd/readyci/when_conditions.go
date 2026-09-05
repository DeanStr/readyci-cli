package main

import (
	"os"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/verificationplan"
)

func currentWhenContextFromEnv() whenContext {
	return whenContext{
		Event:  firstNonEmptyEnv("READYCI_EVENT_NAME", "GITHUB_EVENT_NAME"),
		Branch: currentBranchFromEnv(),
		Tag:    currentTagFromEnv(),
		Status: currentStatusFromEnv(),
		Env:    currentEnvironment(),
	}
}

func currentBranchFromEnv() string {
	if branch := strings.TrimSpace(os.Getenv("READYCI_BRANCH")); branch != "" {
		return branch
	}
	if branch := strings.TrimSpace(os.Getenv("GITHUB_BASE_REF")); branch != "" {
		return branch
	}
	if branch := strings.TrimSpace(os.Getenv("GITHUB_HEAD_REF")); branch != "" {
		return branch
	}
	if os.Getenv("GITHUB_REF_TYPE") == "branch" {
		if branch := strings.TrimSpace(os.Getenv("GITHUB_REF_NAME")); branch != "" {
			return branch
		}
	}
	if branch, ok := strings.CutPrefix(strings.TrimSpace(os.Getenv("GITHUB_REF")), "refs/heads/"); ok {
		return branch
	}
	return ""
}

func currentTagFromEnv() string {
	if tag := strings.TrimSpace(os.Getenv("READYCI_TAG")); tag != "" {
		return tag
	}
	if os.Getenv("GITHUB_REF_TYPE") == "tag" {
		if tag := strings.TrimSpace(os.Getenv("GITHUB_REF_NAME")); tag != "" {
			return tag
		}
	}
	if tag, ok := strings.CutPrefix(strings.TrimSpace(os.Getenv("GITHUB_REF")), "refs/tags/"); ok {
		return tag
	}
	return ""
}

func currentStatusFromEnv() string {
	if status := firstNonEmptyEnv("READYCI_STATUS", "READYCI_PREVIOUS_STATUS"); status != "" {
		return status
	}
	return verificationplan.DefaultWhenStatus
}

func firstNonEmptyEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func currentEnvironment() map[string]string {
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		env[key] = value
	}
	return env
}
