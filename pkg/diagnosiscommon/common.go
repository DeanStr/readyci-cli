// Package diagnosiscommon contains deterministic mechanics shared by Ready CI's
// canonical hosted diagnosis and the conservative CLI fallback.
//
// It intentionally does not classify failures. Hosted classification policy
// remains private to the platform, while the CLI keeps enough fallback logic to
// explain results from older or incomplete API responses.
package diagnosiscommon

import (
	"net"
	"regexp"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

var (
	ansiSGR             = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	definiteTestSuccess = regexp.MustCompile(`(?i)^(?:\[[^\r\n]+\]\s*)?(?:test\s+.+\s+\.\.\.\s+ok\s*$|---\s+pass:[^\r\n]*$|test result:\s+ok\b[^\r\n]*$|[A-Za-z0-9_./-]+\.py(?:::[^\r\n]+)+\s+passed(?:\s+\[[^\r\n]*\])?$)`)
	definiteTestFailure = regexp.MustCompile(`(?im)^\s*(?:\[[^\r\n]+\]\s*)?test result:\s+failed\b`)
)

// RetryableClassification reports whether retrying is a valid default action
// for a diagnosis classification. Project or run policy may still prevent a
// retry.
func RetryableClassification(kind string) bool {
	switch kind {
	case preflightapi.ClassificationTimeout,
		preflightapi.ClassificationSyncFailed,
		preflightapi.ClassificationWorkerUnavailable,
		preflightapi.ClassificationCapacityExhausted,
		preflightapi.ClassificationRateLimited,
		preflightapi.ClassificationTransportFailure,
		preflightapi.ClassificationVerificationUnknown,
		preflightapi.ClassificationDependencyDownloadFailed,
		preflightapi.ClassificationWorkerInternalError:
		return true
	default:
		return false
	}
}

// AgentActionForClassification maps a diagnosis classification to the stable
// action vocabulary exposed by the Ready CI protocol.
func AgentActionForClassification(kind string) string {
	switch kind {
	case preflightapi.ClassificationSuccess:
		return preflightapi.AgentActionNone
	case preflightapi.ClassificationPending:
		return preflightapi.AgentActionWait
	case preflightapi.ClassificationTestFailed,
		preflightapi.ClassificationLintFailed,
		preflightapi.ClassificationBuildFailed,
		preflightapi.ClassificationTypecheckFailed,
		preflightapi.ClassificationDependencySetupFailed:
		return preflightapi.AgentActionFixCode
	case preflightapi.ClassificationAuthMissing, preflightapi.ClassificationAuthForbidden:
		return preflightapi.AgentActionAskForCredentials
	case preflightapi.ClassificationTimeout:
		return preflightapi.AgentActionRetryOrSplit
	case preflightapi.ClassificationSyncTooLarge, preflightapi.ClassificationCacheStorageExceeded:
		return preflightapi.AgentActionReduceScope
	case preflightapi.ClassificationSyncFailed, preflightapi.ClassificationDependencyDownloadFailed:
		return preflightapi.AgentActionRetry
	case preflightapi.ClassificationWorkerUnavailable,
		preflightapi.ClassificationCapacityExhausted,
		preflightapi.ClassificationRateLimited:
		return preflightapi.AgentActionWait
	case preflightapi.ClassificationWorkerCapabilityMismatch,
		preflightapi.ClassificationTransportFailure,
		preflightapi.ClassificationVerificationUnknown,
		preflightapi.ClassificationWorkerInternalError:
		return preflightapi.AgentActionWaitOrReportInfra
	case preflightapi.ClassificationCancelled:
		return preflightapi.AgentActionStop
	default:
		return preflightapi.AgentActionReportSetup
	}
}

// EventSequence returns the durable event ID when present and otherwise a
// stable one-based position within the supplied event slice.
func EventSequence(event preflightapi.RunEvent, index int) int {
	if event.ID > 0 {
		return int(event.ID)
	}
	return index + 1
}

// EventStream identifies log events that may contribute diagnostic evidence.
func EventStream(event preflightapi.RunEvent) (string, bool) {
	switch event.Type {
	case preflightapi.EventStderr:
		return "stderr", true
	case preflightapi.EventStdout:
		return "stdout", true
	default:
		return "", false
	}
}

// EventData returns decoded event data. Malformed encoded data is ignored so a
// diagnostic fallback cannot replace the original run failure with a decode
// error.
func EventData(event preflightapi.RunEvent) string {
	data, err := event.DataBytes()
	if err != nil {
		return ""
	}
	return string(data)
}

// EventSearchText builds the bounded-input source used by diagnosis rules. It
// deliberately includes data only for stdout and stderr events; lifecycle data
// is not user command output.
func EventSearchText(events []preflightapi.RunEvent) string {
	var builder strings.Builder
	for _, event := range events {
		if event.Message != "" {
			builder.WriteString(event.Message)
			builder.WriteByte('\n')
		}
		if _, ok := EventStream(event); ok {
			builder.WriteString(EventData(event))
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

// PrimaryFileFromEvidence returns the first path selected by an engine's own
// evidence filtering policy.
func PrimaryFileFromEvidence(evidence []preflightapi.RunFailureEvidence) string {
	for _, item := range evidence {
		if item.Path != "" {
			return item.Path
		}
	}
	return ""
}

// DefiniteTestSuccessLine recognizes test-runner records that mention failure
// vocabulary in a test name but explicitly report success.
func DefiniteTestSuccessLine(text string) bool {
	return definiteTestSuccess.MatchString(stripANSISGR(strings.TrimSpace(text)))
}

// DefiniteTestFailureText recognizes an explicit test-runner failure summary.
func DefiniteTestFailureText(text string) bool {
	return definiteTestFailure.MatchString(stripANSISGR(text))
}

func stripANSISGR(text string) string {
	return ansiSGR.ReplaceAllString(text, "")
}

// ValidPrimaryFilePath rejects URL, IP, and traversal-shaped matches before a
// diagnosis exposes a candidate repository path.
func ValidPrimaryFilePath(path string) bool {
	path = strings.TrimSpace(path)
	return path != "" &&
		!strings.HasPrefix(path, "//") &&
		!strings.Contains(path, "://") &&
		!PathHasParentTraversal(path) &&
		net.ParseIP(strings.Trim(path, "[]")) == nil
}

// PathHasParentTraversal recognizes parent traversal with either slash style.
func PathHasParentTraversal(path string) bool {
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return true
		}
	}
	return false
}

// ContainsAny reports whether value contains at least one candidate.
func ContainsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

// HostnameMentioned matches a hostname without accepting it as a substring of
// a larger DNS-like token.
func HostnameMentioned(value, hostname string) bool {
	for offset := 0; offset < len(value); {
		index := strings.Index(value[offset:], hostname)
		if index < 0 {
			return false
		}
		index += offset
		end := index + len(hostname)
		if (index == 0 || !hostnameByte(value[index-1])) && (end == len(value) || !hostnameByte(value[end])) {
			return true
		}
		offset = index + 1
	}
	return false
}

func hostnameByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '.' || value == '-' || value == '_'
}

// RegistryEgressReachabilityFailureText recognizes transport failures that
// specifically indicate unreachable name resolution or routing.
func RegistryEgressReachabilityFailureText(lower string) bool {
	if ContainsAny(lower,
		"temporary failure resolving", "temporary failure in name resolution",
		"could not resolve host", "could not resolve proxy", "failed to resolve address",
		"no such host", "name or service not known", "nodename nor servname provided",
		"name resolution error", "nameresolutionerror", "network is unreachable",
		"no route to host", "getaddrinfo", "eai_again", "enotfound",
	) {
		return true
	}
	return strings.Contains(lower, "server misbehaving") && ContainsAny(lower, "lookup ", ":53", "dns")
}

// RegistryLineFailureContextText recognizes package-fetch operations on a
// single output line.
func RegistryLineFailureContextText(value string) bool {
	return ContainsAny(value,
		"could not fetch", "failed to fetch", "failed to get", "failed to download",
		"http response from", "request to http", "while getting",
		"retrying after connection broken", "newconnectionerror", "max retries exceeded",
		"spurious network error", "npm err!", "npm error", "http error", "status code",
		"request failed", "get http", "get https", "get \"http", "reading http",
		"retrying source", "performing get request", "head \"http",
		"failed to resolve reference", "failed to pull the image", "pullimage",
	)
}

// RegistryAdjacentFailureContextText recognizes continuation lines emitted by
// package managers around a failed fetch.
func RegistryAdjacentFailureContextText(value string) bool {
	return ContainsAny(value, "retrying after connection broken", "newconnectionerror", "max retries exceeded", "spurious network error")
}

// RegistryTransportFailureText recognizes the shared transport vocabulary
// used by package and container registries.
func RegistryTransportFailureText(lower string) bool {
	return ContainsAny(lower,
		"certificate verify failed", "unable to get local issuer", "ssl certificate",
		"tls handshake timeout", "connection refused", "connection aborted", "connection reset",
		"connection timed out", "read timed out", "read timeout", "max retries exceeded",
		"temporary failure resolving", "temporary failure in name resolution",
		"could not resolve host", "could not resolve proxy", "failed to resolve address",
		"no such host", "name or service not known", "nodename nor servname provided",
		"name resolution error", "nameresolutionerror", "network is unreachable", "no route to host",
		"i/o timeout", "server misbehaving", "getaddrinfo", "eai_again", "enotfound",
		"etimedout", "econnrefused", "econnreset", "unable to verify the first certificate",
		"unable to verify first certificate", "self-signed certificate in certificate chain",
		"certificate signed by unknown authority", "certificate has expired", "cert_has_expired",
		"unable_to_verify_leaf_signature", "unexpected eof", "remote end closed connection",
	)
}

// DependencyRegistryMentioned recognizes the public registries supported by
// both the hosted classifier and the CLI fallback.
func DependencyRegistryMentioned(lower string) bool {
	return ContainsAny(lower,
		"registry.npmjs.org", "registry.yarnpkg.com", "proxy.golang.org", "sum.golang.org",
		"index.crates.io", "crates.io/api", "pypi.org", "files.pythonhosted.org",
		"rubygems.org", "repo.maven.apache.org", "repo1.maven.org", "nuget.org",
		"registry-1.docker.io", "docker.io", "ghcr.io", "quay.io", "gcr.io", "registry.k8s.io",
	)
}

// CommandHasDependencyDownloadContext recognizes commands whose primary
// purpose includes resolving or downloading dependencies.
func CommandHasDependencyDownloadContext(command string) bool {
	command = strings.ToLower(command)
	return ContainsAny(command,
		"npm install", "npm ci", "npm add", "npm exec", "npx ",
		"pnpm install", "pnpm add", "pnpm fetch", "pnpm dlx",
		"yarn install", "yarn add", "yarn dlx", "bun install", "bun add", "bunx ",
		"go mod download", "go get", "cargo fetch", "cargo install", "cargo update",
		"pip install", "pip3 install", "pip download", "pip wheel",
		"uv pip install", "uv sync", "uv add", "poetry install", "poetry add", "poetry update",
		"bundle install", "bundle update", "composer install", "composer update",
		"dotnet restore", "nuget install", "nuget restore",
	)
}
