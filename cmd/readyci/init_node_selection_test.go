package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectedNodeScriptSkipsPlaceholderEntrypoints(t *testing.T) {
	for _, tc := range []struct {
		name    string
		scripts map[string]string
		want    string
	}{
		{"printed placeholder", map[string]string{"test": "echo 'Tests are not implemented'", "check": "vitest run"}, "check"},
		{"different printed placeholder", map[string]string{"test": "printf '%s\\n' 'TODO: add tests'", "check": "vitest run"}, "check"},
		{"failing npm placeholder", map[string]string{"test": "echo 'Error: no test specified' && exit 1", "check": "vitest run"}, "check"},
		{"success no-op", map[string]string{"test": "true", "check": "vitest run"}, "check"},
		{"empty test suite", map[string]string{"test": ":; exit 0", "check": "vitest run"}, "check"},
		{"fallback to CI suite", map[string]string{"test": "echo 'Tests are not implemented'", "test:ci": "node scripts/run-ci.js", "check": "vitest run"}, "test:ci"},
		{"only placeholders", map[string]string{"test": "echo 'Tests are not implemented'", "test:ci": "exit 0", "test:unit": "printf pending"}, ""},
		{"custom test script", map[string]string{"test": "node scripts/test.js", "check": "vitest run"}, "test"},
		{"custom executable", map[string]string{"test": "./scripts/check-suite", "check": "vitest run"}, "test"},
		{"shell assertion", map[string]string{"test": "[[ -f package.json ]]", "check": "vitest run"}, "test"},
		{"arithmetic assertion", map[string]string{"test": "(( 1 == 1 ))", "check": "vitest run"}, "test"},
		{"real suite after logging", map[string]string{"test": "echo 'Error: no test specified is an old placeholder'; vitest run", "check": "vitest run"}, "test"},
		{"real suite from command substitution", map[string]string{"test": "echo \"$(node scripts/test.js)\"", "check": "vitest run"}, "test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			data, err := json.Marshal(map[string]any{"scripts": tc.scripts})
			if err != nil {
				t.Fatal(err)
			}
			writeFileForRepoMapTest(t, filepath.Join(repo, "package.json"), string(data))
			got, ok := detectedNodeScript(repo)
			if got != tc.want || ok != (tc.want != "") {
				t.Fatalf("detectedNodeScript() = %q, %v; want %q, %v", got, ok, tc.want, tc.want != "")
			}
		})
	}
}

func TestNodeSetupUsesRealCheckInsteadOfPlaceholderTest(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "package.json"), `{"scripts":{"check":"vitest run","test":"echo 'Tests are not implemented'"}}`)
	template, ok := basicDetectedProjectTemplate(repo, "fixture", "", "remote-ci", false)
	if !ok || !strings.Contains(template, "npm run check") || strings.Contains(template, "npm test") {
		t.Fatalf("setup did not preserve the real suite: ok=%v\n%s", ok, template)
	}
}
