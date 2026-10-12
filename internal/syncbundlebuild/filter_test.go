package syncbundlebuild

import (
	"context"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func TestPathFilterIncludesTrackedUntrackedAndIgnoredPaths(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	initRepo(t, repo)

	writeFile(t, repo, "tracked.txt", "tracked\n")
	writeFile(t, repo, "ignored.txt", "ignored\n")
	writeFile(t, repo, ".readyciignore", "ignored.txt\n")
	runGitTest(t, repo, "add", "tracked.txt", ".readyciignore")
	runGitTest(t, repo, "commit", "-m", "baseline")
	writeFile(t, repo, "untracked.txt", "new\n")
	writeFile(t, repo, "excluded.txt", "excluded\n")

	filter, err := NewPathFilter(ctx, repo, BuildOptions{ExcludePatterns: []string{"excluded.txt"}})
	if err != nil {
		t.Fatalf("new path filter: %v", err)
	}
	cases := []struct {
		path string
		want bool
	}{
		{path: "tracked.txt", want: true},
		{path: "untracked.txt", want: true},
		{path: "ignored.txt", want: false},
		{path: "excluded.txt", want: false},
		{path: ".git/config", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got, err := filter.Includes(tc.path)
			if err != nil {
				t.Fatalf("Includes(%q): %v", tc.path, err)
			}
			if got != tc.want {
				t.Fatalf("Includes(%q) = %t, want %t", tc.path, got, tc.want)
			}
		})
	}

	if _, err := filter.Includes("../escape"); err == nil {
		t.Fatal("path escaping repository was accepted")
	}
}

func TestPathFilterStagedModeExcludesUntrackedButKeepsKnownFiles(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	initRepo(t, repo)

	writeFile(t, repo, "tracked.txt", "tracked\n")
	writeFile(t, repo, "deleted.txt", "delete\n")
	writeFile(t, repo, "tracked/deep/deleted.txt", "delete nested\n")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")
	writeFile(t, repo, "tracked.txt", "changed\n")
	writeFile(t, repo, "staged.txt", "staged\n")
	writeFile(t, repo, "staged/deep/new.txt", "staged nested\n")
	writeFile(t, repo, "untracked.txt", "new\n")
	runGitTest(t, repo, "add", "staged.txt", "staged/deep/new.txt")
	runGitTest(t, repo, "rm", "tracked/deep/deleted.txt")

	filter, err := NewPathFilter(ctx, repo, BuildOptions{ChangeMode: syncbundle.ChangeModeStaged})
	if err != nil {
		t.Fatalf("new staged filter: %v", err)
	}
	for _, path := range []string{
		"tracked.txt",
		"staged.txt",
		"deleted.txt",
		"tracked",
		"tracked/deep",
		"tracked/deep/deleted.txt",
		"staged",
		"staged/deep",
		"staged/deep/new.txt",
	} {
		got, err := filter.Includes(path)
		if err != nil {
			t.Fatalf("Includes(%q): %v", path, err)
		}
		if !got {
			t.Fatalf("Includes(%q) = false, want known git path", path)
		}
	}
	got, err := filter.Includes("untracked.txt")
	if err != nil {
		t.Fatalf("Includes untracked: %v", err)
	}
	if got {
		t.Fatal("staged filter included untracked path")
	}
}

func TestNewPathFilterReturnsValidationAndRepositoryErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := NewPathFilter(ctx, t.TempDir(), BuildOptions{}); err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatalf("NewPathFilter outside git repo error = %v, want git error", err)
	}
	repo := t.TempDir()
	initRepo(t, repo)
	if _, err := NewPathFilter(ctx, repo, BuildOptions{ChangeMode: "bad"}); err == nil || !strings.Contains(err.Error(), "invalid change mode") {
		t.Fatalf("NewPathFilter invalid mode error = %v, want invalid change mode", err)
	}
}

func TestRegisteredSourceFilterIgnoresCandidateRulesInPreview(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	writeFile(t, repo, ".readyciignore", "candidate.txt\n")
	writeFile(t, repo, "candidate.txt", "actual")
	runGitTest(t, repo, "add", ".")
	runGitTest(t, repo, "commit", "-m", "baseline")
	writeFile(t, repo, "candidate.txt", "edited")
	no := false
	options := BuildOptions{UseProjectIgnore: &no, SourcePolicyDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", DefaultExcludePatterns: syncbundle.DefaultSourceExcludePatterns()}
	filter, err := NewPathFilter(context.Background(), repo, options)
	if err != nil {
		t.Fatal(err)
	}
	if included, err := filter.Includes("candidate.txt"); err != nil || !included {
		t.Fatalf("registered preview applied candidate exclusions: included=%v err=%v", included, err)
	}
	preview, err := ChangedFiles(context.Background(), repo, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.ChangedFiles) != 1 || preview.ChangedFiles[0] != "candidate.txt" {
		t.Fatalf("preview=%v", preview.ChangedFiles)
	}
}
