package preflightapi

import (
	"github.com/DeanStr/readyci-cli/pkg/guestcontract"
	"slices"
)

// Public API adapters share the guest's small capability contract without
// pulling service API types or source-bundle code into the guest binary.
const (
	EnvironmentFilesystemBtrfs = guestcontract.EnvironmentFilesystemBtrfs
	EnvironmentFilesystemMinix = guestcontract.EnvironmentFilesystemMinix
	EnvironmentIPv6Loopback    = guestcontract.EnvironmentIPv6Loopback
	EnvironmentIPv6Interface   = guestcontract.EnvironmentIPv6Interface
)

func NormalizeEnvironmentRequirements(values []string) ([]string, error) {
	return guestcontract.NormalizeEnvironmentRequirements(values)
}
func CloneEnvironmentRequirements(values []string) []string {
	return guestcontract.CloneEnvironmentRequirements(values)
}
func MergeEnvironmentRequirements(left, right []string) ([]string, error) {
	return guestcontract.MergeEnvironmentRequirements(left, right)
}
func EnvironmentRequirementsSatisfied(requirements, capabilities []string) error {
	return guestcontract.EnvironmentRequirementsSatisfied(requirements, capabilities)
}

// EnvironmentCapabilityMismatchReasons accepts only inventory bound to the
// promoted worker identity, exact image bytes and the selected network mode.
// The trusted worker validates these component hashes against its runtime
// manifest before advertisement; arbitrary labels are not capability evidence.
func EnvironmentCapabilityMismatchReasons(spec RunSpec, inventory EnvironmentCapabilityInventory, release *WorkerReleaseIdentity) []string {
	if len(spec.EnvironmentRequirements) == 0 {
		return nil
	}
	networkMode := spec.NetworkMode
	if networkMode == "" {
		networkMode = DefaultNetworkMode
	}
	if inventory.ProbeVersion != EnvironmentCapabilityProbeVersion || !slices.Contains(inventory.NetworkModes, networkMode) || release == nil || ValidateWorkerReleaseIdentity(*release) != nil || inventory.ReleaseIdentityDigest != release.Digest || ValidateSHA256Hex(inventory.KernelSHA256) != nil || ValidateSHA256Hex(inventory.RootFSSHA256) != nil {
		return []string{"environment capabilities are not advertised for this exact image and worker release"}
	}
	if err := EnvironmentRequirementsSatisfied(spec.EnvironmentRequirements, inventory.Capabilities); err != nil {
		return []string{err.Error()}
	}
	return nil
}
