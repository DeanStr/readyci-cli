package netpolicy

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strings"
)

type IPValidator func(netip.Addr) bool

func NormalizeAllowlistEntries(entries []string) ([]string, error) {
	return NormalizeAllowlistEntriesWithIPValidator(entries, BasicPublicIPv4)
}

func NormalizeAllowlistEntriesWithIPValidator(entries []string, validator IPValidator) ([]string, error) {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		target, err := AllowlistTargetWithIPValidator(entry, validator)
		if err != nil {
			return nil, err
		}
		if target == "" {
			continue
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		out = append(out, target)
	}
	if len(out) == 0 {
		return nil, errors.New("network allowlist requires at least one host or IPv4 address")
	}
	sort.Strings(out)
	return out, nil
}

func AllowlistTarget(entry string) (string, error) {
	return AllowlistTargetWithIPValidator(entry, BasicPublicIPv4)
}

func AllowlistTargetWithIPValidator(entry string, validator IPValidator) (string, error) {
	if validator == nil {
		validator = BasicPublicIPv4
	}
	value := strings.TrimSpace(entry)
	if value == "" {
		return "", nil
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return "", fmt.Errorf("invalid network allowlist entry %q", entry)
		}
		value = parsed.Hostname()
	} else if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.Trim(strings.TrimSpace(value), "[]")
	if value == "" {
		return "", fmt.Errorf("invalid network allowlist entry %q", entry)
	}
	if strings.Contains(value, "*") {
		return "", fmt.Errorf("network allowlist wildcards are not supported yet: %q", entry)
	}
	if strings.Contains(value, "/") {
		if _, err := netip.ParsePrefix(value); err == nil {
			return "", fmt.Errorf("network allowlist CIDR entries are not supported yet: %q", entry)
		}
		return "", fmt.Errorf("invalid network allowlist entry %q", entry)
	}
	if ip, err := netip.ParseAddr(value); err == nil {
		ip = ip.Unmap()
		if !validator(ip) {
			return "", fmt.Errorf("network allowlist only supports public IPv4 targets: %q", entry)
		}
		return ip.String(), nil
	}
	if !validAllowlistHostname(value) {
		return "", fmt.Errorf("invalid network allowlist host %q", entry)
	}
	return strings.ToLower(value), nil
}

func BasicPublicIPv4(ip netip.Addr) bool {
	if !ip.Is4() {
		return false
	}
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	return true
}

var blockedTCPDestinationPorts = []int{
	25,
	465,
	587,
	2525,
}

func BlockedTCPDestinationPorts() []int {
	return append([]int(nil), blockedTCPDestinationPorts...)
}

func validAllowlistHostname(value string) bool {
	if len(value) == 0 || len(value) > 253 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z':
			case r >= 'A' && r <= 'Z':
			case r >= '0' && r <= '9':
			case r == '-':
			default:
				return false
			}
		}
	}
	return true
}
