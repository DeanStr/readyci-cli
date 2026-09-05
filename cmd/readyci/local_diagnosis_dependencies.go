package main

import (
	"strconv"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/diagnosiscommon"
)

func localDependencyRegistryAccessFailureText(lower string) bool {
	lines := strings.Split(lower, "\n")
	for index, line := range lines {
		if !localDependencyRegistryMentioned(line) {
			continue
		}
		if localRegistryLineFailureContextText(line) &&
			(localDiagnosisRegistryHTTPFailurePattern.MatchString(line) || localRegistryTransportFailureText(line) || strings.Contains(line, "failed to get successful http response")) {
			return true
		}
		start := max(0, index-1)
		end := min(len(lines), index+3)
		window := strings.Join(lines[start:end], "\n")
		if (localRegistryLineFailureContextText(line) || localRegistryAdjacentFailureContextText(window)) &&
			(localDiagnosisRegistryHTTPFailurePattern.MatchString(window) || localRegistryTransportFailureText(window) || strings.Contains(window, "failed to get successful http response")) {
			return true
		}
	}
	return false
}

func localDependencyRegistryEgressFailureText(command, lower string, exitCode *int) bool {
	if localGoToolchainDownloadEgressFailureText(command, lower, exitCode) {
		return true
	}
	lines := strings.Split(lower, "\n")
	for index, line := range lines {
		if !localDependencyRegistryMentioned(line) {
			continue
		}
		start := max(0, index-1)
		end := min(len(lines), index+3)
		windowLines := lines[start:end]
		window := strings.Join(windowLines, "\n")
		egressFailure := false
		for _, windowLine := range windowLines {
			if localRegistryEgressReachabilityFailureText(windowLine) && !localAnalysisTextLooksTestFailure(windowLine, windowLine) {
				egressFailure = true
				break
			}
		}
		if egressFailure &&
			(localRegistryLineFailureContextText(line) || localRegistryAdjacentFailureContextText(window)) {
			return true
		}
	}
	return false
}

func localRegistryEgressReachabilityFailureText(lower string) bool {
	return diagnosiscommon.RegistryEgressReachabilityFailureText(lower)
}

func localRegistryLineFailureContextText(value string) bool {
	return diagnosiscommon.RegistryLineFailureContextText(value)
}

func localRegistryAdjacentFailureContextText(value string) bool {
	return diagnosiscommon.RegistryAdjacentFailureContextText(value)
}

func localDependencyDownloadFailureText(commandLower, lower string, exitCode *int) bool {
	if localContainsAny(lower, "dependency download", "err_pnpm_fetch", "npm err! network", "npm error network") ||
		localGoToolchainDownloadFailureText(commandLower, lower, exitCode) || localDependencyRegistryAccessFailureText(lower) {
		return true
	}
	if !localRegistryTransportFailureText(lower) && !localContainsAny(lower, "failed to get successful http response", "failed to download") {
		return false
	}
	commandContext := localCommandHasDependencyDownloadContext(commandLower) && !localCommandHasPostDependencyExecutionContext(commandLower)
	return commandContext || localDependencyDownloadOutputContext(lower)
}

func localDependencyDownloadOutputContext(lower string) bool {
	if localContainsAny(lower,
		"package manager",
		"package registry",
		"fetch package",
		"fetch dependency",
		"download dependency",
		"install dependency",
		"could not install packages due to an oserror",
	) {
		return true
	}
	for _, line := range strings.Split(lower, "\n") {
		if !localRegistryTransportFailureText(line) {
			continue
		}
		packageManagerFailure := localContainsAny(line,
			"warning: retrying after connection broken",
			"npm err!",
			"npm error code",
			"err_pnpm",
			"spurious network error",
		)
		goProxyFailure := localContainsAny(line, "/@v/", "/@latest", "/sumdb/") && localContainsAny(line, "reading http", "get \"http")
		if packageManagerFailure || goProxyFailure {
			return true
		}
	}
	return false
}

func localCommandHasDependencyDownloadContext(commandLower string) bool {
	return diagnosiscommon.CommandHasDependencyDownloadContext(commandLower)
}

func localCommandHasPostDependencyExecutionContext(commandLower string) bool {
	return localCommandLooksTest(commandLower) ||
		localCommandLooksTypecheck(commandLower) ||
		localCommandContainsToken(commandLower, "lint") ||
		localCommandContainsToken(commandLower, "build") ||
		strings.Contains(commandLower, "eslint") ||
		strings.Contains(commandLower, "golangci-lint") ||
		strings.Contains(commandLower, "ruff check") ||
		strings.Contains(commandLower, "shellcheck") ||
		localContainsAny(commandLower, "&&", "||", ";", "\n", " | ")
}

func localContainsAny(value string, candidates ...string) bool {
	return diagnosiscommon.ContainsAny(value, candidates...)
}

func localRegistryTransportFailureText(lower string) bool {
	return diagnosiscommon.RegistryTransportFailureText(lower)
}

func localDependencyRegistryMentioned(lower string) bool {
	return diagnosiscommon.DependencyRegistryMentioned(lower)
}

func localGoToolchainDownloadFailureText(command, lower string, exitCode *int) bool {
	if !localGoToolchainDownloadCommand(command) {
		return false
	}
	if localGoToolchainDownloadFailureWindow(lower) != "" {
		return true
	}
	postDependencyExecution := localCommandHasPostDependencyExecutionContext(strings.ToLower(command))
	for _, line := range strings.Split(lower, "\n") {
		if localGoToolchainCurlFailureLine(line) && (!postDependencyExecution || localCurlFailureCodeMatchesExit(line, exitCode)) {
			return true
		}
	}
	return false
}

func localGoToolchainDownloadEgressFailureText(command, lower string, exitCode *int) bool {
	if !localGoToolchainDownloadCommand(command) {
		return false
	}
	if window := localGoToolchainDownloadFailureWindow(lower); window != "" {
		for _, line := range strings.Split(window, "\n") {
			if localRegistryEgressReachabilityFailureText(line) && !localAnalysisTextLooksTestFailure(line, line) {
				return true
			}
		}
		return false
	}
	postDependencyExecution := localCommandHasPostDependencyExecutionContext(strings.ToLower(command))
	for _, line := range strings.Split(lower, "\n") {
		if localGoToolchainCurlSourceMentioned(line) && localRegistryEgressReachabilityFailureText(line) && !localAnalysisTextLooksTestFailure(line, line) &&
			(!postDependencyExecution || localCurlFailureCodeMatchesExit(line, exitCode)) {
			return true
		}
	}
	return false
}

func localGoToolchainDownloadCommand(command string) bool {
	lower := strings.ToLower(command)
	return localContainsAny(lower, "readyci_lab_go", "readyci-bootstrap-go", "go.dev/dl/", "dl.google.com/go/")
}

func localGoToolchainDownloadHostMentioned(value string) bool {
	return localHostnameMentioned(value, "go.dev") || localHostnameMentioned(value, "dl.google.com")
}

func localGoToolchainCurlFailureLine(line string) bool {
	return localGoToolchainCurlSourceMentioned(line) && localRegistryTransportFailureText(line)
}

func localGoToolchainCurlSourceMentioned(line string) bool {
	return strings.Contains(line, "curl:") &&
		(localGoToolchainDownloadHostMentioned(line) || strings.Contains(line, "could not resolve proxy"))
}

func localCurlFailureCodeMatchesExit(line string, exitCode *int) bool {
	if exitCode == nil {
		return false
	}
	match := localDiagnosisCurlFailureCodePattern.FindStringSubmatch(line)
	if len(match) != 2 {
		return false
	}
	code, err := strconv.Atoi(match[1])
	return err == nil && code == *exitCode
}

func localGoToolchainDownloadFailureWindow(lower string) string {
	lines := strings.Split(lower, "\n")
	for index, line := range lines {
		if !strings.Contains(line, localGoToolchainDownloadFailureMarker) {
			continue
		}
		start := max(0, index-2)
		end := min(len(lines), index+2)
		return strings.Join(lines[start:end], "\n")
	}
	return ""
}

func localHostnameMentioned(value, hostname string) bool {
	return diagnosiscommon.HostnameMentioned(value, hostname)
}

func localHostnameByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '.' || value == '-' || value == '_'
}

func localCommandLooksTypecheck(commandLower string) bool {
	return strings.Contains(commandLower, "typecheck") ||
		strings.Contains(commandLower, "type check") ||
		localCommandContainsToken(commandLower, "tsc") ||
		localCommandContainsToken(commandLower, "mypy") ||
		localCommandContainsToken(commandLower, "pyright")
}

func localCommandLooksTest(commandLower string) bool {
	return localCommandContainsToken(commandLower, "test") ||
		localCommandContainsToken(commandLower, "pytest") ||
		localCommandContainsToken(commandLower, "vitest") ||
		localCommandContainsToken(commandLower, "jest")
}

func localCommandContainsToken(value, token string) bool {
	return strings.Contains(" "+strings.NewReplacer("-", " ", "_", " ", ":", " ", "/", " ").Replace(value)+" ", " "+token+" ")
}

func localAnalysisTextLooksTestFailure(text, lower string) bool {
	return strings.Contains(lower, "--- fail:") ||
		strings.Contains(lower, "assertionerror") ||
		diagnosiscommon.DefiniteTestFailureText(text) ||
		localDiagnosisPytestFailedPattern.MatchString(text) ||
		localDiagnosisAssertionPattern.MatchString(lower)
}

func localConcreteTestFailureText(text, lower string) bool {
	return strings.Contains(lower, "--- fail:") ||
		strings.Contains(lower, "assertionerror") ||
		diagnosiscommon.DefiniteTestFailureText(text) ||
		localDiagnosisPytestFailedPattern.MatchString(text)
}

func localExplicitAssertionFailureText(lower string) bool {
	for _, line := range strings.Split(lower, "\n") {
		if strings.Contains(line, "assertionerror") || localDiagnosisAssertionPattern.MatchString(line) {
			return true
		}
	}
	return false
}
