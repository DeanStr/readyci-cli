package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/diagnosiscommon"
	"github.com/DeanStr/readyci-cli/pkg/netpolicy"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

const (
	networkAdvisoryLogLimit  = 1 << 20
	networkAdvisorySources   = 16
	networkAdvisoryLineLimit = 4096
	networkAdvisoryHostLimit = 3
)

// Resolver and connection errors name the host in these positions (Go, curl,
// Node.js, Python, Java, apt and wget). Only lines that also report a
// resolution or reachability failure are inspected.
var unreachableHostPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\blookup ([a-z0-9.-]+)`),
	regexp.MustCompile(`could not resolve host:? ['‘"]?([a-z0-9.-]+)`),
	regexp.MustCompile(`\b(?:enotfound|eai_again) ([a-z0-9.-]+)`),
	regexp.MustCompile(`(?:failed to resolve|temporary failure resolving|unable to resolve host address) ['‘"]([a-z0-9.-]+)`),
	regexp.MustCompile(`unknownhostexception: ([a-z0-9.-]+)`),
	regexp.MustCompile(`unknown host:? ['‘"]?([a-z0-9.-]+)`),
	regexp.MustCompile(`\bhost='([a-z0-9.-]+)'`),
	regexp.MustCompile(`\bresolving ([a-z0-9.-]+)\.{3} failed`),
}

// Names that cannot be public egress destinations, so an allowlist entry
// would not help.
var nonPublicHostSuffixes = []string{".local", ".localhost", ".internal", ".lan", ".home.arpa", ".svc", ".test", ".example", ".invalid"}

// networkPolicyAdvisory suggests reviewing the run's egress policy when its
// own output names hosts that could not be resolved or reached and that the
// policy does not permit. It is secondary guidance: the authoritative
// classification is unchanged and the failure may have another cause.
func networkPolicyAdvisory(status, networkMode string, allowlist []string, events []preflightapi.RunEvent) string {
	if status != preflightapi.StatusFailed && status != preflightapi.StatusTimedOut {
		return ""
	}
	if networkMode != "none" && networkMode != "allowlist" {
		return ""
	}
	permitted := map[string]bool{}
	for _, entry := range allowlist {
		if target, err := netpolicy.AllowlistTarget(entry); err == nil && target != "" {
			permitted[target] = true
		}
	}
	var hosts []string
	seen := map[string]bool{}
	scanCommandOutputLines(events, func(line string) bool {
		for _, host := range unreachableHosts(line) {
			if permitted[host] || seen[host] {
				continue
			}
			seen[host] = true
			hosts = append(hosts, host)
		}
		return len(hosts) < networkAdvisoryHostLimit
	})
	if len(hosts) == 0 {
		return ""
	}
	listed := strings.Join(hosts[:min(len(hosts), networkAdvisoryHostLimit)], ", ")
	const secondary = " This secondary guidance does not replace the terminal failure."
	if networkMode == "none" {
		return fmt.Sprintf("Command output reports unresolved or unreachable hosts while this run had no network access: %s. If the check needs them, fetch them during dependency preparation (network.warm_allowlist) or run the check with network_mode allowlist including them.%s", listed, secondary)
	}
	return fmt.Sprintf("Command output reports unresolved or unreachable hosts that are not in this run's network allowlist: %s. If the check needs them, add them to network.allowlist or the check's network_allowlist.%s", listed, secondary)
}

func unreachableHosts(line string) []string {
	lower := strings.ToLower(line)
	if !diagnosiscommon.RegistryEgressReachabilityFailureText(lower) && !strings.Contains(lower, "unknownhostexception") && !strings.Contains(lower, "unknown host") {
		return nil
	}
	var hosts []string
	for _, pattern := range unreachableHostPatterns {
		for _, match := range pattern.FindAllStringSubmatch(lower, -1) {
			if host := publicHostname(match[1]); host != "" {
				hosts = append(hosts, host)
			}
		}
	}
	return hosts
}

func publicHostname(candidate string) string {
	host, err := netpolicy.AllowlistTarget(strings.Trim(candidate, ".-"))
	if err != nil || host == "" || !strings.Contains(host, ".") {
		return ""
	}
	label := host[strings.LastIndex(host, ".")+1:]
	if !strings.HasPrefix(label, "xn--") && strings.IndexFunc(label, func(r rune) bool { return r < 'a' || r > 'z' }) >= 0 {
		// Public IPv4 targets are valid allowlist entries too.
		if strings.Count(host, ".") != 3 || strings.Trim(host, "0123456789.") != "" {
			return ""
		}
	}
	for _, suffix := range nonPublicHostSuffixes {
		if strings.HasSuffix(host, suffix) {
			return ""
		}
	}
	return host
}

// scanCommandOutputLines visits complete lines of the run's own stdout and
// stderr. Chunks are joined only within one stream, phase and step; service
// output, lifecycle events and undecodable chunks are skipped. Inspection is
// bounded by total bytes, distinct sources and line length.
func scanCommandOutputLines(events []preflightapi.RunEvent, visit func(string) bool) {
	type source struct{ stream, phase, step string }
	partial := map[source]string{}
	var order []source
	remaining := networkAdvisoryLogLimit
	emit := func(line string) bool {
		if len(line) > networkAdvisoryLineLimit {
			line = line[:networkAdvisoryLineLimit]
		}
		return visit(line)
	}
	for _, event := range events {
		if event.Service != "" || (event.Type != preflightapi.EventStdout && event.Type != preflightapi.EventStderr) {
			continue
		}
		key := source{event.Type, event.Phase, event.Step}
		if _, known := partial[key]; !known {
			if len(partial) >= networkAdvisorySources {
				continue
			}
			order = append(order, key)
		}
		if remaining == 0 {
			break
		}
		data, err := event.DataBytes()
		if err != nil {
			// Never join text across an undecodable chunk.
			partial[key] = ""
			continue
		}
		if len(data) > remaining {
			data = data[:remaining]
		}
		remaining -= len(data)
		lines := strings.Split(partial[key]+string(data), "\n")
		for _, line := range lines[:len(lines)-1] {
			if !emit(line) {
				return
			}
		}
		tail := lines[len(lines)-1]
		if len(tail) > networkAdvisoryLineLimit {
			tail = tail[len(tail)-networkAdvisoryLineLimit:]
		}
		partial[key] = tail
	}
	for _, key := range order {
		if partial[key] != "" && !emit(partial[key]) {
			return
		}
	}
}
