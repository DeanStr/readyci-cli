package main

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

func TestArtifactRetentionSchemaRejectsOutOfContractBounds(t *testing.T) {
	path, err := filepath.Abs(filepath.Join("..", "..", "schema", "readyci.verification.v0alpha1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := jsonschema.Compile((&url.URL{Scheme: "file", Path: filepath.ToSlash(path), Fragment: "/$defs/artifactRetention"}).String())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{"empty-omissions", "omitted_files", []any{}},
		{"too-many-retained-files", "retained_file_count", float64(10001)},
		{"too-many-omitted-files", "omitted_file_count", float64(100001)},
		{"too-many-omitted-reports", "omitted_report_count", float64(100001)},
		{"invalid-unmatched-type", "unmatched_paths", "reports/missing.xml"},
		{"duplicate-unmatched-paths", "unmatched_paths", []any{"reports/missing.xml", "reports/missing.xml"}},
		{"empty-unmatched-path", "unmatched_paths", []any{""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var response map[string]any
			if err := json.Unmarshal([]byte(omittedReportsFixture), &response); err != nil {
				t.Fatal(err)
			}
			collection := response["collection"].(map[string]any)
			collection["unmatched_paths"] = []any{"reports/missing.xml"}
			if err := schema.Validate(collection); err != nil {
				t.Fatalf("valid collection rejected: %v", err)
			}
			collection[tc.field] = tc.value
			if err := schema.Validate(collection); err == nil {
				t.Fatal("schema accepted artifact collection outside the decoder contract")
			}
		})
	}
}
