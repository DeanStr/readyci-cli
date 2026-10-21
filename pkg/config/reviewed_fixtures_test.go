package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestReviewedFixtureConfigAndSchemaRequireExactPathsAndFullHashes(t *testing.T) {
	schema := compileReadyCISchema(t)
	for _, path := range []string{"tests/test_apps/.env", "testdata/public fixture.txt"} {
		text := fmt.Sprintf("sync:\n  reviewed_fixtures:\n    - path: %q\n      sha256: %s\nchecks:\n  tests: pytest\n", path, strings.Repeat("a", 64))
		cfg, err := ParseProjectConfig([]byte(text), "fixture.yml")
		if err != nil || len(cfg.Sync.ReviewedFixtures) != 1 || cfg.Sync.ReviewedFixtures[0].Path != path {
			t.Fatalf("valid reviewed fixture rejected: cfg=%+v err=%v", cfg, err)
		}
		for _, document := range []map[string]any{
			{"sync": map[string]any{"reviewed_fixtures": []any{map[string]any{"path": path, "sha256": strings.Repeat("a", 64)}}}},
			{"checks": map[string]any{"tests": map[string]any{"command": "pytest", "sync": map[string]any{"reviewed_fixtures": []any{map[string]any{"path": path, "sha256": strings.Repeat("a", 64)}}}}}},
		} {
			if err := schema.Validate(document); err != nil {
				t.Fatalf("schema rejected reviewed fixture: %v", err)
			}
		}
	}
	for _, path := range []string{"../fixture", "/fixture", "./fixture", "testdata/*.env", "testdata/", "testdata//fixture", ".git/config", "testdata/.GiT/config", "dir/../fixture", "dir\\fixture"} {
		text := fmt.Sprintf("sync:\n  reviewed_fixtures:\n    - path: %q\n      sha256: %s\n", path, strings.Repeat("a", 64))
		if _, err := ParseProjectConfig([]byte(text), "fixture.yml"); err == nil {
			t.Errorf("config accepted ambiguous reviewed fixture path %q", path)
		}
		if err := schema.Validate(map[string]any{"sync": map[string]any{"reviewed_fixtures": []any{map[string]any{"path": path, "sha256": strings.Repeat("a", 64)}}}}); err == nil {
			t.Errorf("schema accepted ambiguous reviewed fixture path %q", path)
		}
	}
	for _, hash := range []string{"", "abc", strings.Repeat("A", 64), strings.Repeat("g", 64)} {
		if _, err := ParseProjectConfig([]byte(fmt.Sprintf("sync:\n  reviewed_fixtures:\n    - path: testdata/.env\n      sha256: %q\n", hash)), "fixture.yml"); err == nil {
			t.Errorf("config accepted malformed reviewed hash %q", hash)
		}
	}
}
