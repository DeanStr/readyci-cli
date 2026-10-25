package preflightapi

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRunnerEvidenceBindsEnvironmentRequirements(t *testing.T) {
	var plain, required RunSpec
	if err := json.Unmarshal([]byte(`{"secrets_requested":false}`), &plain); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"secrets_requested":false,"environment_requirements":["filesystem:btrfs"]}`), &required); err != nil {
		t.Fatal(err)
	}
	first, err := RunnerExecutionDigest(plain)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RunnerExecutionDigest(required)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("guest requirements disappeared from the attested execution digest")
	}
}

func TestEnvironmentRequirementsNormalizeAndFailClosed(t *testing.T) {
	input := []string{EnvironmentIPv6Loopback, EnvironmentFilesystemBtrfs, EnvironmentIPv6Loopback}
	want := []string{EnvironmentFilesystemBtrfs, EnvironmentIPv6Loopback}
	got, err := NormalizeEnvironmentRequirements(input)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized=%v error=%v", got, err)
	}
	got[0] = EnvironmentFilesystemMinix
	if input[0] != EnvironmentIPv6Loopback {
		t.Fatal("normalization mutated its caller")
	}
	for _, invalid := range [][]string{{""}, {"btrfs"}, {"network:ipv6-egress"}, {"filesystem:btrfs "}, {"$(touch owned)"}, {strings.Repeat("x", 1000)}, make([]string, 33)} {
		if _, err := NormalizeEnvironmentRequirements(invalid); err == nil {
			t.Errorf("accepted invalid requirements %q", invalid)
		}
	}
	if err := EnvironmentRequirementsSatisfied(want, []string{EnvironmentFilesystemBtrfs}); err == nil {
		t.Fatal("missing IPv6 capability satisfied requirements")
	}
	if err := EnvironmentRequirementsSatisfied(want, []string{EnvironmentIPv6Loopback, EnvironmentFilesystemBtrfs}); err != nil {
		t.Fatal(err)
	}
	if err := EnvironmentRequirementsSatisfied(nil, []string{"unknown"}); err == nil {
		t.Fatal("malformed worker capability inventory accepted")
	}
}

func TestVerificationPlanSealsNormalizedEnvironmentRequirements(t *testing.T) {
	req := validVerificationRequest()
	req.Plan.Nodes[0].Run.EnvironmentRequirements = []string{EnvironmentIPv6Loopback, EnvironmentFilesystemBtrfs, EnvironmentIPv6Loopback}
	normalized, err := NormalizeCreateVerificationSessionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(normalized.Plan.Nodes[0].Run.EnvironmentRequirements, []string{EnvironmentFilesystemBtrfs, EnvironmentIPv6Loopback}) {
		t.Fatalf("sealed requirements=%v", normalized.Plan.Nodes[0].Run.EnvironmentRequirements)
	}
	changed := validVerificationRequest()
	changed.Plan.Nodes[0].Run.EnvironmentRequirements = []string{EnvironmentFilesystemBtrfs}
	changed, err = NormalizeCreateVerificationSessionRequest(changed)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Plan.Digest == normalized.Plan.Digest {
		t.Fatal("changing guest requirements did not change sealed plan digest")
	}
	if req.Plan.Nodes[0].Run.EnvironmentRequirements[0] != EnvironmentIPv6Loopback {
		t.Fatal("sealing mutated original requirements")
	}
	changed.Plan.Digest = ""
	changed.Plan.Nodes[0].Run.EnvironmentRequirements = []string{"network:ipv6-egress"}
	if _, err := NormalizeCreateVerificationSessionRequest(changed); err == nil {
		t.Fatal("sealed unknown capability")
	}
	invalid := RunSpec{SecretsRequested: new(bool), EnvironmentRequirements: []string{"unknown"}}
	if _, err := RunnerExecutionDigest(invalid); err == nil {
		t.Fatal("attested unsupported requirements")
	}
}

func TestEnvironmentCapabilitiesRequireBoundReleaseAndNetworkMode(t *testing.T) {
	release, err := NewWorkerReleaseIdentity(strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64), strings.Repeat("4", 64), strings.Repeat("5", 64))
	if err != nil {
		t.Fatal(err)
	}
	spec := RunSpec{EnvironmentRequirements: []string{EnvironmentIPv6Loopback}, NetworkMode: "none"}
	valid := EnvironmentCapabilityInventory{KernelSHA256: strings.Repeat("a", 64), RootFSSHA256: strings.Repeat("b", 64), ReleaseIdentityDigest: release.Digest, ProbeVersion: EnvironmentCapabilityProbeVersion, NetworkModes: []string{"none"}, Capabilities: []string{EnvironmentIPv6Loopback}}
	if reasons := EnvironmentCapabilityMismatchReasons(spec, valid, &release); len(reasons) != 0 {
		t.Fatalf("valid exact-image inventory rejected: %v", reasons)
	}
	for _, mutate := range []func(*EnvironmentCapabilityInventory){
		func(value *EnvironmentCapabilityInventory) { value.ProbeVersion = "old-probe" },
		func(value *EnvironmentCapabilityInventory) {
			value.ReleaseIdentityDigest = "sha256:" + strings.Repeat("0", 64)
		},
		func(value *EnvironmentCapabilityInventory) { value.NetworkModes = []string{"allowlist"} },
		func(value *EnvironmentCapabilityInventory) { value.Capabilities = nil },
		func(value *EnvironmentCapabilityInventory) {
			value.Capabilities = []string{EnvironmentIPv6Loopback, "network:ipv6-egress"}
		},
		func(value *EnvironmentCapabilityInventory) { value.KernelSHA256 = "unknown" },
	} {
		changed := valid
		mutate(&changed)
		if reasons := EnvironmentCapabilityMismatchReasons(spec, changed, &release); len(reasons) == 0 {
			t.Fatalf("invalid advertisement satisfied required guest behavior: %+v", changed)
		}
	}
	if reasons := EnvironmentCapabilityMismatchReasons(spec, valid, nil); len(reasons) == 0 {
		t.Fatal("inventory without worker release satisfied requirements")
	}
}
