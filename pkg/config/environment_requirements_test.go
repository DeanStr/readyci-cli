package config

import "testing"

func TestEnvironmentRequirementsAreExplicitConfiguration(t *testing.T) {
	_, err := ParseProjectConfig([]byte("version: 1\nenvironment_requirements: [network:ipv6-loopback]\nchecks:\n  unit:\n    command: cargo test\n    environment_requirements: [filesystem:btrfs]\n"), "requirements.yml")
	if err != nil {
		t.Fatalf("explicit guest requirements rejected: %v", err)
	}
}

func TestEnvironmentRequirementsRejectUnknownAtEitherScope(t *testing.T) {
	for _, input := range []string{
		"version: 1\nenvironment_requirements: [network:ipv6-egress]\nchecks: {unit: {command: cargo test}}\n",
		"version: 1\nchecks: {unit: {command: cargo test, environment_requirements: [filesystem:unknown]}}\n",
	} {
		if _, err := ParseProjectConfig([]byte(input), "requirements.yml"); err == nil {
			t.Fatal("unknown guest requirement was accepted")
		}
	}
}
