package guestcontract

import (
	"fmt"
	"slices"
)

// EnvironmentCapabilityProbeVersion binds the fixed probe definitions used by
// acceptance and by fresh preparation inside a guest.
const EnvironmentCapabilityProbeVersion = "readyci.environment_probe.v1"

// RuntimePreparationFailureMarker reports failed preparation; it never changes
// terminal state or establishes verification authority.
const RuntimePreparationFailureMarker = `{"schema_version":"readyci.runtime_preparation.v1","status":"failed"}`

const (
	EnvironmentFilesystemBtrfs = "filesystem:btrfs"
	EnvironmentFilesystemMinix = "filesystem:minix"
	EnvironmentIPv6Loopback    = "network:ipv6-loopback"
	EnvironmentIPv6Interface   = "network:ipv6-interface"
)

// NormalizeEnvironmentRequirements accepts only capabilities whose meaning is
// owned by the service. These describe guest behavior, not requested privileges
// or permission to broaden network egress. Unknown names fail closed.
func NormalizeEnvironmentRequirements(values []string) ([]string, error) {
	if len(values) > 32 {
		return nil, fmt.Errorf("environment_requirements must contain at most 32 entries")
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		switch value {
		case EnvironmentFilesystemBtrfs, EnvironmentFilesystemMinix, EnvironmentIPv6Loopback, EnvironmentIPv6Interface:
		default:
			return nil, fmt.Errorf("unsupported environment requirement %q", value)
		}
		if !slices.Contains(result, value) {
			result = append(result, value)
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	slices.Sort(result)
	return result, nil
}

func CloneEnvironmentRequirements(values []string) []string {
	return append([]string(nil), values...)
}

func MergeEnvironmentRequirements(left, right []string) ([]string, error) {
	left, err := NormalizeEnvironmentRequirements(left)
	if err != nil {
		return nil, err
	}
	right, err = NormalizeEnvironmentRequirements(right)
	if err != nil {
		return nil, err
	}
	return NormalizeEnvironmentRequirements(append(left, right...))
}

// EnvironmentRequirementsSatisfied validates both sides before comparing them;
// malformed worker advertisements cannot satisfy an execution requirement.
func EnvironmentRequirementsSatisfied(requirements, capabilities []string) error {
	requirements, err := NormalizeEnvironmentRequirements(requirements)
	if err != nil {
		return err
	}
	capabilities, err = NormalizeEnvironmentRequirements(capabilities)
	if err != nil {
		return fmt.Errorf("invalid environment capabilities: %w", err)
	}
	for _, required := range requirements {
		if !slices.Contains(capabilities, required) {
			return fmt.Errorf("guest environment does not support %s", required)
		}
	}
	return nil
}
