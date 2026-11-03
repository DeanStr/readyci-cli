package config

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseProjectConfigReadsCheckImport(t *testing.T) {
	cfg, err := ParseProjectConfig([]byte(`
version: 1
checks:
  test:
    command: npm test
    imported_from:
      candidate: "github-actions:.github/workflows/ci.yml:test"
      runtime_pins:
        node: "24"
        python: "3.12"
  handwritten:
    command: go test ./...
`), "fixture.yml")
	if err != nil {
		t.Fatal(err)
	}
	want := &CheckImportConfig{Candidate: "github-actions:.github/workflows/ci.yml:test", RuntimePins: map[string]string{"node": "24", "python": "3.12"}}
	if got := cfg.Checks["test"].ImportedFrom; !reflect.DeepEqual(got, want) {
		t.Fatalf("imported_from = %+v, want %+v", got, want)
	}
	if cfg.Checks["handwritten"].ImportedFrom != nil {
		t.Fatalf("a check without imported_from gained one: %+v", cfg.Checks["handwritten"].ImportedFrom)
	}
}

// The parser and the published schema accept and reject the same imports.
func TestCheckImportValidationMatchesSchema(t *testing.T) {
	schema := compileReadyCISchema(t)
	for _, tc := range []struct {
		name     string
		imported string
		valid    bool
	}{
		{name: "candidate only", imported: `{candidate: "make:Makefile:test"}`, valid: true},
		{name: "pins", imported: `{candidate: "github-actions:ci.yml:test", runtime_pins: {go: "1.24", node: "24", pnpm: "9.15.0", python: "3"}}`, valid: true},
		{name: "empty", imported: `{}`},
		{name: "pins without candidate", imported: `{runtime_pins: {node: "24"}}`},
		{name: "blank candidate", imported: `{candidate: " "}`},
		{name: "multi-line candidate", imported: `{candidate: "github-actions:ci.yml:test\nchecks:"}`},
		{name: "moving selector pin", imported: `{candidate: "github-actions:ci.yml:test", runtime_pins: {node: "lts/*"}}`},
		{name: "range pin", imported: `{candidate: "github-actions:ci.yml:test", runtime_pins: {node: ">=24"}}`},
		{name: "leading zero pin", imported: `{candidate: "github-actions:ci.yml:test", runtime_pins: {node: "024"}}`},
		{name: "unsupported runtime pin", imported: `{candidate: "github-actions:ci.yml:test", runtime_pins: {rust: "1.80"}}`},
		{name: "unknown field", imported: `{candidate: "github-actions:ci.yml:test", warnings: ["stale"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := "version: 1\nchecks:\n  test:\n    command: npm test\n    imported_from: " + tc.imported + "\n"
			_, parseErr := ParseProjectConfig([]byte(document), "fixture.yml")
			var decoded map[string]any
			if err := yaml.Unmarshal([]byte(document), &decoded); err != nil {
				t.Fatal(err)
			}
			schemaErr := schema.Validate(decoded)
			if tc.valid && (parseErr != nil || schemaErr != nil) {
				t.Fatalf("valid import rejected: parse=%v schema=%v", parseErr, schemaErr)
			}
			if !tc.valid && (parseErr == nil || schemaErr == nil) {
				t.Fatalf("invalid import accepted: parse=%v schema=%v", parseErr, schemaErr)
			}
			if parseErr != nil && !strings.Contains(parseErr.Error(), "checks.test.imported_from") && !strings.Contains(parseErr.Error(), "not found in type") {
				t.Fatalf("parse error does not name the field: %v", parseErr)
			}
		})
	}
}
