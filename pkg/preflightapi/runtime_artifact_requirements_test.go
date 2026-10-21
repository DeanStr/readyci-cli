package preflightapi

import (
	"strings"
	"testing"
)

func TestPinnedPackageManagerIntegrityIsPreservedAndConjoined(t *testing.T) {
	pin := "10.33.4+sha512." + strings.Repeat("a", 128)
	got, err := NormalizeRuntimeRequirements(map[string]string{"pnpm": pin})
	if err != nil || got["pnpm"] != pin {
		t.Fatalf("package-manager integrity was lost or rejected: %v %v", got, err)
	}
	for _, pair := range [][2]string{{pin, "10"}, {"10", pin}, {pin, pin}} {
		merged, err := MergeRuntimeRequirements(map[string]string{"pnpm": pair[0]}, map[string]string{"pnpm": pair[1]})
		if err != nil || merged["pnpm"] != pin {
			t.Fatalf("integrity conjunction changed %v: %v %v", pair, merged, err)
		}
	}
	if _, err := MergeRuntimeRequirements(map[string]string{"pnpm": pin}, map[string]string{"pnpm": "11"}); err == nil {
		t.Fatal("incompatible version discarded integrity pin")
	}
	if _, err := MergeRuntimeRequirements(map[string]string{"pnpm": pin}, map[string]string{"pnpm": "10.33.4+sha512." + strings.Repeat("b", 128)}); err == nil {
		t.Fatal("conflicting integrity pins were accepted")
	}
	if !RuntimeRequirementImplies(pin, "10") || RuntimeRequirementImplies("10.33.4", pin) || !RuntimeRequirementImplies(pin, pin) {
		t.Fatal("integrity implication is not preserved")
	}
	if _, err := SelectRuntimeVersions(map[string]string{"pnpm": pin}, RuntimeInventory{"pnpm": {"10.33.4"}}); err == nil {
		t.Fatal("version-only installed inventory satisfied an artifact integrity pin")
	}
}

func TestPinnedPackageManagerIntegrityRejectsUnsupportedSyntax(t *testing.T) {
	for _, value := range []string{"10.33.4+sha512.abcd", "10+sha512." + strings.Repeat("a", 128), "10.33.4+sha256." + strings.Repeat("a", 64), "10.33.4+sha512." + strings.Repeat("A", 128), ">=10.33.4+sha512." + strings.Repeat("a", 128)} {
		if _, err := NormalizeRuntimeRequirements(map[string]string{"pnpm": value}); err == nil {
			t.Fatalf("unsupported package-manager integrity syntax accepted: %s", value)
		}
	}
	if _, err := NormalizeRuntimeRequirements(map[string]string{"node": "24.15.0+sha512." + strings.Repeat("a", 128)}); err == nil {
		t.Fatal("package-manager hash accepted for an unrelated runtime")
	}
}
