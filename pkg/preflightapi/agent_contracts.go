package preflightapi

import (
	"strconv"
	"strings"
)

const AgentRetryIDPrefix = "__readyci_retry="

func AgentRetryIdempotencyKey(key string, attempt int) string {
	key = strings.TrimSpace(key)
	if key == "" || attempt <= 1 {
		return key
	}
	return key + ":" + AgentRetryIDPrefix + strconv.Itoa(attempt)
}

func AgentIdempotencyKeyMatches(candidate, baseKey string) bool {
	candidate = strings.TrimSpace(candidate)
	baseKey = strings.TrimSpace(baseKey)
	if baseKey == "" {
		return candidate == ""
	}
	return candidate == baseKey || strings.HasPrefix(candidate, AgentIdempotencyChildPrefix(baseKey))
}

func AgentIdempotencyChildPrefix(baseKey string) string {
	baseKey = strings.TrimSpace(baseKey)
	if baseKey == "" {
		return ""
	}
	return baseKey + ":"
}

func AgentIdempotencyGroup(baseKey, key string) string {
	baseKey = strings.TrimSpace(baseKey)
	key = strings.TrimSpace(key)
	if baseKey == "" {
		return key
	}
	if key == baseKey {
		return ""
	}
	prefix := AgentIdempotencyChildPrefix(baseKey)
	if !strings.HasPrefix(key, prefix) {
		return key
	}
	parts := strings.Split(strings.TrimPrefix(key, prefix), ":")
	for len(parts) > 0 && IsAgentRetryIdempotencySegment(parts[len(parts)-1]) {
		parts = parts[:len(parts)-1]
	}
	return strings.Join(parts, ":")
}

func IsAgentRetryIdempotencySegment(segment string) bool {
	value, ok := strings.CutPrefix(strings.TrimSpace(segment), AgentRetryIDPrefix)
	if !ok || value == "" {
		return false
	}
	attempt, err := strconv.Atoi(value)
	return err == nil && attempt > 1
}

func SplitCheckAliasVariant(alias string) (string, string) {
	alias = strings.TrimSpace(alias)
	if strings.HasSuffix(alias, "]") {
		if index := strings.LastIndex(alias, " ["); index > 0 {
			return strings.TrimSpace(alias[:index]), strings.TrimSuffix(alias[index+2:], "]")
		}
	}
	return alias, ""
}

func IsTerminalRunStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case StatusSucceeded,
		StatusFailed,
		StatusTimedOut,
		StatusCancelled,
		StatusUploadExpired,
		StatusBlocked,
		StatusDenied:
		return true
	default:
		return false
	}
}

func IsRunEventErrorTailCandidate(event RunEvent) bool {
	switch event.Type {
	case EventStdout:
		return RunEventTextLooksDiagnostic(event)
	case EventStderr,
		EventLogTruncated,
		EventRunFailed,
		EventRunTimedOut,
		EventRunCancelled:
		return true
	case EventCommandExited:
		return event.ExitCode != nil && *event.ExitCode != 0
	default:
		return false
	}
}

func RunEventTextLooksDiagnostic(event RunEvent) bool {
	if LogTextLooksDiagnostic(event.Message) {
		return true
	}
	switch event.Type {
	case EventStdout, EventStderr:
		data, err := event.DataBytes()
		return err == nil && LogTextLooksDiagnostic(string(data))
	default:
		return false
	}
}

func LogTextLooksDiagnostic(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if containsLogDiagnostic(lower,
		"modulenotfounderror",
		"no module named",
		"importerror",
		"traceback (most recent call last)",
		"error collecting",
		"assertionerror",
		"syntaxerror",
		"referenceerror",
		"typeerror",
		"=================================== failures",
		"--- fail:",
		"\nfail\t",
		"\nfail ",
		"\nfailed ",
	) {
		return true
	}
	return strings.HasPrefix(lower, "fail\t") ||
		strings.HasPrefix(lower, "fail ") ||
		strings.HasPrefix(lower, "failed ")
}

func containsLogDiagnostic(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func IsRunEventLogTailCandidate(event RunEvent) bool {
	switch event.Type {
	case EventStdout,
		EventStderr,
		EventLogTruncated,
		EventRunFailed,
		EventRunTimedOut,
		EventRunCancelled:
		return true
	case EventCommandExited:
		return event.ExitCode != nil && *event.ExitCode != 0
	default:
		return false
	}
}

func IsRunEventLifecycleTailCandidate(event RunEvent) bool {
	switch event.Type {
	case EventRunFinished, EventRuntimeDiagnostics:
		return true
	case EventCommandExited:
		return event.ExitCode != nil && *event.ExitCode == 0
	default:
		return false
	}
}
