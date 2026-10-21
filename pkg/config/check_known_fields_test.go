package config

import (
	"strconv"
	"strings"
	"testing"
)

func TestParseCheckRejectsUnsupportedFields(t *testing.T) {
	tests := []struct{ name, field, body string }{
		{"check env", "env", "    command: echo test\n    env: {MSRV_FEATURES: minimal}\n"},
		{"command typo", "commmand", "    command: echo test\n    commmand: echo lost\n"},
		{"step typo", "environ", "    steps:\n      - name: test\n        command: echo test\n        environ: {MODE: lost}\n"},
		{"sync typo", "include_untrackd", "    command: echo test\n    sync: {include_untrackd: false}\n"},
		{"cache typo", "szie", "    command: echo test\n    cache: {szie: 10GiB}\n"},
		{"condition typo", "brnach", "    command: echo test\n    when: {brnach: [main]}\n"},
		{"output typo", "reviewabel", "    command: echo test\n    outputs: {reviewabel: [report.txt]}\n"},
		{"fixture typo", "sha265", "    command: echo test\n    sync:\n      reviewed_fixtures:\n        - path: fixtures/.env\n          sha265: ignored\n"},
		{"merged unknown check", "env", "    <<: {command: echo test, env: {MODE: lost}}\n"},
		{"quoted merge key", "<<", "    command: echo test\n    '<<': {command: echo lost}\n"},
		{"merged nested typo", "szie", "    command: echo test\n    cache:\n      <<: {szie: 10GiB}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseProjectConfig([]byte("version: 1\nchecks:\n  test:\n"+tt.body), "strict-check.yml")
			if err == nil || !strings.Contains(err.Error(), "field "+tt.field+" not found") {
				t.Fatalf("unsupported %s: got %v, want field %s rejection", tt.name, err, tt.field)
			}
		})
	}
}

func TestParseCheckKnownFieldsPreservesSupportedFormsAndExternalAliases(t *testing.T) {
	cfg, err := ParseProjectConfig([]byte(`version: 1
checks:
  shorthand: echo shorthand
  base: &base
    command: echo base
    prepare: [echo prepare]
    post: echo post
    always: [echo always]
    cache: &cache
      mode: cache-only
      size: 10GiB
  alias: *base
  merge:
    <<: *base
    command: echo merged
    cache:
      <<: *cache
      size: 20GiB
    matrix:
      GO: ["1.25", "1.26"]
  structured:
    steps:
      - &step
        name: test
        command: echo test
        env: &stepenv {MODE: test, MSRV_FEATURES: "--no-default-features --features minimal-application,bugreport,build-assets"}
    matrix:
      axes: {GO: ["1.25"]}
      include: [{GO: "1.26", MODE: ci}]
  step_alias:
    steps:
      - <<: *step
        name: again
        env: {<<: *stepenv, MODE: again}
`), "aliases.yml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Checks["shorthand"].Command != "echo shorthand" || cfg.Checks["alias"].Command != "echo base" || cfg.Checks["merge"].Cache.Size != "20GiB" {
		t.Fatalf("shorthand/check merge lost: %+v", cfg.Checks)
	}
	if got := cfg.Checks["step_alias"].Steps[0]; got.Env["MODE"] != "again" || got.Env["MSRV_FEATURES"] != "--no-default-features --features minimal-application,bugreport,build-assets" {
		t.Fatalf("step env alias merge lost: %+v", got)
	}
	if len(cfg.Checks["merge"].Matrix.Axes["GO"]) != 2 || len(cfg.Checks["structured"].Matrix.Include) != 1 {
		t.Fatal("legacy or structured matrix lost")
	}
}

func TestParseCheckKnownFieldsHonorsExplicitOverridesOfMergedValues(t *testing.T) {
	cfg, err := ParseProjectConfig([]byte(`version: 1
checks:
  test:
    <<: {cache: {unused_typo: ignored}}
    command: echo test
    cache: {size: 10GiB}
`), "overridden.yml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Checks["test"].Cache.Size != "10GiB" {
		t.Fatal("explicit cache override lost")
	}
}

func TestParseCheckKnownFieldsMergeSequenceAndTypedAlias(t *testing.T) {
	cfg, err := ParseProjectConfig([]byte(`version: 1
checks:
  test:
    <<: [{command: echo first, cache: {size: 10GiB}}, {command: echo second, cache: {unused_typo: ignored}}]
`), "sequence.yml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Checks["test"].Command != "echo first" || cfg.Checks["test"].Cache.Size != "10GiB" {
		t.Fatal("earlier merge entry did not win")
	}
	_, err = ParseProjectConfig([]byte(`version: 1
checks:
  test:
    steps:
      - name: test
        command: echo test
        env: &freeform {szie: ignored}
    cache: *freeform
`), "typed-alias.yml")
	if err == nil || !strings.Contains(err.Error(), "field szie not found") {
		t.Fatalf("freeform map masked typed alias error: %v", err)
	}
}

func TestParseCheckKnownFieldsRejectsCyclicMerge(t *testing.T) {
	_, err := ParseProjectConfig([]byte("version: 1\nchecks:\n  test: &self {<<: *self, command: echo test}\n"), "cycle.yml")
	if err == nil {
		t.Fatal("accepted cyclic merge")
	}
}

func TestParseCheckKnownFieldsDoesNotTreatAliasKeyAsMerge(t *testing.T) {
	_, err := ParseProjectConfig([]byte(`version: 1
checks:
  test:
    command: echo test
    matrix: {AXIS: [&mergekey <<]}
    *mergekey: {timeout: 5m}
`), "alias-key.yml")
	if err == nil || !strings.Contains(err.Error(), "field << not found") {
		t.Fatalf("alias key became a synthetic merge: %v", err)
	}
}

func TestParseCheckKnownFieldsBoundsMergeExpansion(t *testing.T) {
	// A small YAML input can otherwise retain every growing merge prefix before
	// an unknown field reaches the ordinary decoder.
	var source strings.Builder
	source.WriteString("version: 1\nchecks:\n  test: {command: echo test, <<: ")
	for i := 0; i < 400; i++ {
		source.WriteString("{unknown_")
		source.WriteString(strconv.Itoa(i))
		source.WriteString(": value, <<: ")
	}
	source.WriteString("{}")
	source.WriteString(strings.Repeat("}", 400))
	source.WriteString("}\n")
	_, err := ParseProjectConfig([]byte(source.String()), "merge-complexity.yml")
	if err == nil || !strings.Contains(err.Error(), "complexity limit") {
		t.Fatalf("unbounded merge expansion: %v", err)
	}
}
