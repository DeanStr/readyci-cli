package verificationplan

import "testing"

func TestEffectiveRunNetworkModePreservesExplicitDockerAllowlist(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		got, err := EffectiveRunNetworkMode("allowlist", true, explicit)
		if err != nil || got != "allowlist" {
			t.Fatalf("docker allowlist (explicit=%t) = %q, %v; want allowlist, nil", explicit, got, err)
		}
	}
}
