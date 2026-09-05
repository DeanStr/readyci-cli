package main

import (
	"os/exec"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func remoteDisplayCommand(options remoteRunOptions) string {
	if command := strings.TrimSpace(options.Command); command != "" {
		return command
	}
	if len(options.Steps) > 0 {
		commands := make([]string, 0, len(options.Steps))
		for _, step := range options.Steps {
			if command := strings.TrimSpace(step.Command); command != "" {
				commands = append(commands, command)
			}
		}
		if len(commands) > 0 {
			return strings.Join(commands, "\n")
		}
	}
	return strings.TrimSpace(options.CommandAlias)
}

func shouldPrintFailedWorkspaceHint(keepFailed bool, terminalEventType string, commandExited bool, commandExitCode int, artifactUploaded, failedWorkspaceArtifactRetained, failedWorkspaceArtifactSkipped bool) bool {
	return keepFailed &&
		terminalEventType == preflightapi.EventRunFailed &&
		commandExited &&
		commandExitCode != 0 &&
		artifactUploaded &&
		failedWorkspaceArtifactRetained &&
		!failedWorkspaceArtifactSkipped
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func gitRepository() string {
	output, err := exec.Command("git", "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return ""
	}
	remoteURL := strings.TrimSpace(string(output))
	return githubRepositoryFromRemote(remoteURL)
}

func githubRepositoryFromRemote(remoteURL string) string {
	value := strings.TrimSpace(remoteURL)
	value = strings.TrimPrefix(value, "https://github.com/")
	value = strings.TrimPrefix(value, "http://github.com/")
	value = strings.TrimPrefix(value, "git@github.com:")
	value = strings.TrimSuffix(value, ".git")
	value = strings.Trim(value, "/")
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	if strings.ContainsAny(parts[0]+parts[1], " \t\r\n") {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func defaultInt(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}
