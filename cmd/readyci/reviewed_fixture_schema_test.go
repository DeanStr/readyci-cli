package main

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

func verificationSourceRecipeSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "schema", "readyci.verification.v0alpha1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	location := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), Fragment: "/$defs/verificationSourceRecipe"}).String()
	schema, err := jsonschema.Compile(location)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func normalizedSourceRecipeJSON(t *testing.T, fixtures []syncbundle.ReviewedFixture) map[string]any {
	t.Helper()
	request, err := preflightapi.NormalizeCreateVerificationSessionRequest(preflightapi.CreateVerificationSessionRequest{
		ProjectID: "project", IdempotencyKey: "source-schema",
		Recipe: preflightapi.VerificationRecipe{
			SchemaVersion: preflightapi.VerificationRecipeSchemaVersion, ChangeMode: "working",
			Source: &preflightapi.VerificationSourceRecipe{IncludeUntracked: true, UseProjectIgnore: true, ReviewedFixtures: fixtures},
		},
		Snapshot: preflightapi.VerificationSnapshot{Digest: "sha256:" + strings.Repeat("a", 64), ChangeMode: "working", ChangedFiles: []string{"main.go"}},
		Coverage: preflightapi.VerificationCoverage{MatchedFiles: []string{"main.go"}},
		Plan: preflightapi.VerificationPlan{Nodes: []preflightapi.VerificationNodePlan{{
			ID: "test", Name: "test", Kind: "check", Required: true,
			Run: preflightapi.CreateRunRequest{Command: "run-tests"},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(request.Recipe.Source)
	if err != nil {
		t.Fatal(err)
	}
	var source map[string]any
	if err := json.Unmarshal(encoded, &source); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestNormalizedVerificationSourceRecipeMatchesMandatoryArraySchema(t *testing.T) {
	schema := verificationSourceRecipeSchema(t)
	for _, fixtures := range [][]syncbundle.ReviewedFixture{nil, {{Path: "tests/.env", SHA256: strings.Repeat("b", 64)}}} {
		if err := schema.Validate(normalizedSourceRecipeJSON(t, fixtures)); err != nil {
			t.Fatalf("normalized recipe source violates schema: %v", err)
		}
	}
}

func TestVerificationSourceRecipeSchemaRejectsIncompleteFixtureContracts(t *testing.T) {
	schema := verificationSourceRecipeSchema(t)
	for _, tt := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing generated defaults", func(s map[string]any) { delete(s, "default_untracked_exclude_patterns") }},
		{"missing fixture declarations", func(s map[string]any) { delete(s, "reviewed_fixtures") }},
		{"null fixture declarations", func(s map[string]any) { s["reviewed_fixtures"] = nil }},
		{"glob path", func(s map[string]any) { s["reviewed_fixtures"].([]any)[0].(map[string]any)["path"] = "tests/*.env" }},
		{"parent path", func(s map[string]any) { s["reviewed_fixtures"].([]any)[0].(map[string]any)["path"] = "tests/../.env" }},
		{"git metadata", func(s map[string]any) {
			s["reviewed_fixtures"].([]any)[0].(map[string]any)["path"] = "tests/.GiT/config"
		}},
		{"directory path", func(s map[string]any) { s["reviewed_fixtures"].([]any)[0].(map[string]any)["path"] = "tests/" }},
		{"incomplete hash", func(s map[string]any) { s["reviewed_fixtures"].([]any)[0].(map[string]any)["sha256"] = "1234" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := normalizedSourceRecipeJSON(t, []syncbundle.ReviewedFixture{{Path: "tests/.env", SHA256: strings.Repeat("b", 64)}})
			tt.mutate(source)
			if err := schema.Validate(source); err == nil {
				t.Fatal("invalid source recipe satisfies the public schema")
			}
		})
	}
}
