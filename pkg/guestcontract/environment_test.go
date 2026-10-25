package guestcontract

import (
	"reflect"
	"strings"
	"testing"
)

func TestEnvironmentContractNormalizesWithoutMutatingInput(t *testing.T) {
	values := []string{EnvironmentIPv6Loopback, EnvironmentFilesystemBtrfs, EnvironmentIPv6Loopback}
	got, err := NormalizeEnvironmentRequirements(values)
	want := []string{EnvironmentFilesystemBtrfs, EnvironmentIPv6Loopback}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized=%v error=%v", got, err)
	}
	got[0] = EnvironmentFilesystemMinix
	if values[0] != EnvironmentIPv6Loopback {
		t.Fatal("normalization mutated caller")
	}
	if err := EnvironmentRequirementsSatisfied(want, []string{EnvironmentIPv6Loopback}); err == nil {
		t.Fatal("missing filesystem satisfied requirement")
	}
	if err := EnvironmentRequirementsSatisfied(want, want); err != nil {
		t.Fatal(err)
	}
	merged, err := MergeEnvironmentRequirements([]string{EnvironmentIPv6Loopback}, []string{EnvironmentFilesystemBtrfs})
	if err != nil || !reflect.DeepEqual(merged, want) {
		t.Fatalf("merged=%v error=%v", merged, err)
	}
	cloned := CloneEnvironmentRequirements(values)
	cloned[0] = "invalid"
	if values[0] != EnvironmentIPv6Loopback {
		t.Fatal("clone aliases caller")
	}
}

func TestEnvironmentContractRejectsUnknownCapabilitiesAndKeepsWireValues(t *testing.T) {
	for _, values := range [][]string{{""}, {"btrfs"}, {"network:ipv6-egress"}, {"filesystem:btrfs "}, {"$(touch owned)"}, make([]string, 33)} {
		if _, err := NormalizeEnvironmentRequirements(values); err == nil {
			t.Errorf("accepted unsupported requirements %q", values)
		}
	}
	if err := EnvironmentRequirementsSatisfied(nil, []string{"unknown"}); err == nil {
		t.Fatal("malformed capability inventory accepted")
	}
	if EnvironmentCapabilityProbeVersion != "readyci.environment_probe.v1" || !strings.Contains(RuntimePreparationFailureMarker, `"schema_version":"readyci.runtime_preparation.v1"`) {
		t.Fatal("guest wire contract changed")
	}
}
