package preflightapi

import (
	"reflect"
	"strings"
	"testing"
)

func TestRuntimeRequirementVersionSemantics(t *testing.T) {
	cases := []struct {
		requirement, version string
		match                bool
	}{
		{"24", "24.18.0", true}, {"v24.x", "24.18.0", true}, {"24.18", "24.18.1", true}, {"24.18.0", "24.18.1", false},
		{">=20.19.0 || >=22.12.0", "24.18.0", true}, {">=20.19.0 <21 || >=22.12.0", "22.11.0", false},
		{">=3.9, <4", "3.12.3", true}, {">=3.9, <4", "4.0.0", false}, {"^0.2.3", "0.3.0", false}, {"^0.2.3", "0.2.9", true},
		{"^0.0.3", "0.0.4", false}, {"~1.25", "1.25.13", true}, {"~1.25", "1.26.0", false},
		{"==3.12", "3.12.0", true}, {"==3.12", "3.12.3", false}, {"==3.12.*", "3.12.3", true}, {"!=3.12.0", "3.12.3", true},
	}
	for _, test := range cases {
		t.Run(test.requirement+"/"+test.version, func(t *testing.T) {
			normalized, err := NormalizeRuntimeRequirements(map[string]string{"node": test.requirement})
			if err != nil {
				t.Fatal(err)
			}
			if got := RuntimeVersionSatisfies(test.version, normalized["node"]); got != test.match {
				t.Fatalf("%s normalized %s matches %s=%v,want %v", test.requirement, normalized["node"], test.version, got, test.match)
			}
			twice, err := NormalizeRuntimeRequirements(normalized)
			if err != nil || !reflect.DeepEqual(twice, normalized) {
				t.Fatalf("nonidempotent normalization %v %v", twice, err)
			}
		})
	}
}
func TestRuntimeRequirementsRejectUnsupportedAndUnsafeValues(t *testing.T) {
	for _, value := range []string{"", "*", "latest", "lts/*", "stable", "nightly-2026-10-19", "1.95.0-nightly", "24.18.0; touch /tmp/owned", "$(echo 24)", ">=3.9,", ">=3.9,,<4", "24.1.2.3", "01.2.3", "24 ||", "!=3.12.*", strings.Repeat("1", 513)} {
		if _, err := NormalizeRuntimeRequirements(map[string]string{"node": value}); err == nil {
			t.Errorf("accepted unsupported %q", value)
		}
	}
	if _, err := NormalizeRuntimeRequirements(map[string]string{"unknown": "1.2.3"}); err == nil {
		t.Fatal("accepted unknown runtime")
	}
	for _, inventory := range []RuntimeInventory{{"node": {"24"}}, {"node": {"v24.18.0"}}, {"node": {"24.18.0", "24.18.0"}}, {"rust": {"1.95.0-nightly"}}, {"python": {}}, {"unknown": {"1.2.3"}}} {
		if _, err := NormalizeRuntimeInventory(inventory); err == nil {
			t.Errorf("accepted invalid inventory %v", inventory)
		}
	}
}
func TestRuntimeRequirementMergingAndSelection(t *testing.T) {
	merged, err := MergeRuntimeRequirements(map[string]string{"node": "20 || 24"}, map[string]string{"node": ">=22"})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := SelectRuntimeVersions(merged, RuntimeInventory{"node": {"20.19.0", "24.18.0", "26.5.0"}})
	if err != nil || selected["node"] != "24.18.0" {
		t.Fatalf("selection=%v error=%v", selected, err)
	}
	for _, inventory := range []RuntimeInventory{nil, {"go": {"1.25.13"}}, {"node": {"20.19.0"}}} {
		if _, err := SelectRuntimeVersions(merged, inventory); err == nil {
			t.Fatalf("incompatible inventory accepted %v", inventory)
		}
	}
	if reasons := RuntimeRequirementMismatchReasons(map[string]string{"python": ">=3.9", "node": "24"}, nil); len(reasons) != 2 || !strings.Contains(reasons[0], "node") {
		t.Fatalf("reasons=%v", reasons)
	}
	if _, err := SelectRuntimeVersions(nil, nil); err != nil {
		t.Fatalf("unrequested runtimes require inventory: %v", err)
	}
}
func TestRuntimeInventoryCopiesAreIndependent(t *testing.T) {
	original := map[string]RuntimeInventory{"image": {"node": {"24.18.0"}}}
	copy := CloneImageRuntimeInventory(original)
	copy["image"]["node"][0] = "26.5.0"
	if original["image"]["node"][0] != "24.18.0" {
		t.Fatal("mutable worker metadata shared")
	}
}
func TestRunnerExecutionDigestBindsRuntimeRequirements(t *testing.T) {
	enabled := false
	spec := RunSpec{RunID: "r", SecretsRequested: &enabled, RuntimeRequirements: map[string]string{"node": "24"}}
	before, err := RunnerExecutionDigest(spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.RuntimeRequirements["node"] = "26"
	after, err := RunnerExecutionDigest(spec)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("runtime requirements missing from runner evidence digest")
	}
}

func TestNodeSourceRangesPreserveNpmPartialComparatorSemantics(t *testing.T) {
	cases := []struct {
		requirement, version string
		match                bool
	}{{">20.19", "20.19.1", false}, {">20.19", "20.20.0", true}, {"=20", "20.19.1", true}, {"<=20", "20.19.1", true}, {"<=20", "21.0.0", false}, {">=20.19 <21 || >=22.12", "22.12.1", true}}
	for _, test := range cases {
		canonical, err := NormalizeNodeRuntimeRequirement(test.requirement)
		if err != nil {
			t.Fatal(err)
		}
		if got := RuntimeVersionSatisfies(test.version, canonical); got != test.match {
			t.Errorf("npm %s canonical %s matches%s=%v", test.requirement, canonical, test.version, got)
		}
	}
	if _, err := NormalizeNodeRuntimeRequirement("!=20.1.0"); err == nil {
		t.Fatal("accepted invalid npm != comparator")
	}
}
func TestRuntimeRequirementContradictionsFailBeforeAdmission(t *testing.T) {
	for _, value := range []string{"24,26", ">=24,<24", ">24.18.0,<=24.18.0", "24.18.0,!=24.18.0"} {
		if _, err := NormalizeRuntimeRequirements(map[string]string{"node": value}); err == nil {
			t.Fatalf("accepted impossible version requirement %s", value)
		}
	}
}

func TestRuntimeRequirementImplicationPreservesStrongerConfiguration(t *testing.T) {
	for _, test := range []struct {
		configured, source string
		want               bool
	}{{"3.12.3", ">=3.9,<4", true}, {">=3.8", ">=3.9", false}, {"", ">=3.9", false}, {"24.18.0", ">=22", true}, {"24.18.0", "26", false}, {">=3.9,<4", ">=3.9,!=3.12.0", false}} {
		if got := RuntimeRequirementImplies(test.configured, test.source); got != test.want {
			t.Fatalf("%s implies%s=%v want%v", test.configured, test.source, got, test.want)
		}
	}
}

func TestRuntimePresenceUsesInstalledImageDefaultAndRequiresInventory(t *testing.T) {
	inventory, err := NormalizeRuntimeInventory(RuntimeInventory{"node": {"24.18.0", "26.5.0", "22.23.1"}})
	if err != nil {
		t.Fatal(err)
	}
	versions, err := SelectRuntimeVersions(map[string]string{"node": "installed"}, inventory)
	if err != nil || versions["node"] != "24.18.0" {
		t.Fatalf("presence switched image default: %v %v", versions, err)
	}
	if _, err := SelectRuntimeVersions(map[string]string{"python": "installed"}, nil); err == nil {
		t.Fatal("presence accepted missing runtime inventory")
	}
	merged, err := MergeRuntimeRequirements(map[string]string{"python": "installed"}, map[string]string{"python": ">=3.9"})
	if err != nil || merged["python"] != ">=3.9" {
		t.Fatalf("presence erased source version constraint: %v %v", merged, err)
	}
}
