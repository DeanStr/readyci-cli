package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectPolicyInputDiscoversConfiguration(t *testing.T) {
	for _, name := range []string{".readyci.yml", ".readyci.yaml"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			content := "version: 1\nproject_id: proj_test\nchecks:\n  test:\n    command: echo ok\n"
			if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir("nested", 0o700); err != nil {
				t.Fatal(err)
			}
			t.Chdir(filepath.Join(root, "nested"))
			project, yaml, err := projectPolicyCommandInput("https://api.readyci.test", "proj_test", "")
			if err != nil {
				t.Fatal(err)
			}
			if project != "proj_test" || yaml != content {
				t.Fatalf("project=%q yaml=%q", project, yaml)
			}
		})
	}
}

func TestProjectPolicyInputDoesNotSubstituteMissingExplicitFile(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile(".readyci.yml", []byte("version: 1\nproject_id: proj_test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := projectPolicyCommandInput("https://api.readyci.test", "proj_test", "missing.yml"); err == nil || !os.IsNotExist(err) {
		t.Fatalf("expected missing explicit file error, got %v", err)
	}
}

func TestProjectPolicyPreviewRejectsEmptyFileFlag(t *testing.T) {
	if err := projectPolicyPreviewCommand([]string{"--file="}); err == nil || !strings.Contains(err.Error(), "--file must not be empty") {
		t.Fatalf("expected empty file flag error, got %v", err)
	}
}
