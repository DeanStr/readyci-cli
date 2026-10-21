package preflightapi

import (
	"strings"
	"testing"
)

func TestSetupRuntimeRequirementPreservesActionComparatorSemantics(t *testing.T) {
	for _, test := range []struct {
		runtime, input, want, included, excluded string
	}{
		{"go", ">1.25", ">=1.26.0", "1.26.0", "1.25.99"},
		{"python", ">3.12", ">=3.13.0", "3.13.0", "3.12.99"},
		{"go", "<=1.25", "<1.26.0", "1.25.99", "1.26.0"},
		{"python", "<=3.12", "<3.13.0", "3.12.99", "3.13.0"},
		{"go", "=1.25", "1.25", "1.25.99", "1.26.0"},
		{"python", "=3.12", "3.12", "3.12.99", "3.13.0"},
		{"go", ">1.25.0", ">1.25.0", "1.25.1", "1.25.0"},
		{"go", "^1.25.0", "^1.25.0", "1.27.2", "2.0.0"},
		{"python", "~3.12", "~3.12", "3.12.9", "3.13.0"},
		{"node", ">=20.19 <21 || >=22.12", ">=20.19,<21 || >=22.12", "22.12.1", "21.0.0"},
		{"node", "20.19.x", "20.19", "20.19.1", "20.20.0"},
		{"pnpm", "10.x", "10", "10.33.4", "11.0.0"},
		{"pnpm", "v10.33.4", "10.33.4", "10.33.4", "10.33.5"},
	} {
		t.Run(test.runtime+"/"+test.input, func(t *testing.T) {
			got, err := NormalizeSetupRuntimeRequirement(test.runtime, test.input)
			if err != nil || got != test.want {
				t.Fatalf("normalize = %q, %v; want %q", got, err, test.want)
			}
			if !RuntimeVersionSatisfies(test.included, got) || RuntimeVersionSatisfies(test.excluded, got) {
				t.Fatalf("%q must include %s and exclude %s", got, test.included, test.excluded)
			}
		})
	}
}

func TestSetupRuntimeRequirementRejectsUnsupportedSyntaxWithFamily(t *testing.T) {
	for _, runtime := range []string{"node", "go", "python", "pnpm"} {
		for _, input := range []string{
			"", "*", "latest", "lts/*", "stable", "installed",
			"3.14t", "3.15-dev", "pypy3.10", "PyPy",
			"01.25", "3.1234567",
			">1.25.x", ">=1.25,<1.26", "!=1.25.0", "==1.25.0", "1.25 ||", ">=2 <1",
			"10.33.4+sha512." + strings.Repeat("a", 128),
		} {
			t.Run(runtime+"/"+input, func(t *testing.T) {
				_, err := NormalizeSetupRuntimeRequirement(runtime, input)
				if err == nil || !strings.Contains(err.Error(), runtime) {
					t.Fatalf("expected %s diagnostic for %q, got %v", runtime, input, err)
				}
				if runtime != "pnpm" && strings.Contains(err.Error(), "npm") {
					t.Fatalf("%s diagnostic uses npm family: %v", runtime, err)
				}
			})
		}
	}
}

func TestSetupRuntimeRequirementRejectsUnknownFamilies(t *testing.T) {
	for _, runtime := range []string{"", "npm", "rust", "cargo", "yarn", "Go", "node "} {
		_, err := NormalizeSetupRuntimeRequirement(runtime, "24")
		if err == nil || !strings.HasPrefix(err.Error(), "unsupported setup runtime ") {
			t.Errorf("expected unsupported setup runtime for %q, got %v", runtime, err)
		}
	}
}

func TestSetupRuntimeRequirementKeepsNodeEngineWrapper(t *testing.T) {
	for _, input := range []string{">20.19", "<=20", "=20", ">=20.19 <21 || >=22.12"} {
		engine, engineErr := NormalizeNodeRuntimeRequirement(input)
		action, actionErr := NormalizeSetupRuntimeRequirement("node", input)
		if engineErr != nil || actionErr != nil || engine != action {
			t.Errorf("node %q: engine=%q/%v, action=%q/%v", input, engine, engineErr, action, actionErr)
		}
	}
	for input, want := range map[string]string{
		">=20,<21": "comma-separated npm ranges are unsupported",
		"latest":   "unsupported npm runtime requirement \"latest\"",
		"!=20.1.0": "unsupported npm runtime comparator \"!=\"",
		">20.x":    "unsupported npm wildcard comparison",
		"":         "empty npm runtime alternative",
	} {
		_, err := NormalizeNodeRuntimeRequirement(input)
		if err == nil || err.Error() != want {
			t.Errorf("engine %q: got %v, want %s", input, err, want)
		}
	}
}
