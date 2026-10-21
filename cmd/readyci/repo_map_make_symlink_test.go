package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoMapMakeInternalSymlinkPreservesLogicalEntrypoint(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "build", "root", "Makefile"), "ifeq ($(PRINT_HELP),y)\ntest:\n\t@echo help\nelse\ntest:\n\thack/make-rules/test.sh $(WHAT) $(TESTS)\nendif\n")
	if err := os.Symlink("build/root/Makefile", filepath.Join(repo, "Makefile")); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(repo, "must-not-execute")
	writeFileForRepoMapTest(t, filepath.Join(repo, "build", "root", "Makefile"), "$(shell touch "+marker+")\nifeq ($(PRINT_HELP),y)\ntest:\n\t@echo help\nelse\ntest:\n\thack/make-rules/test.sh $(WHAT) $(TESTS)\nendif\n")
	candidates := inferRepoImportCandidates(repoMapReport{Make: scanRepoMake(repo)})
	var logical, canonical bool
	for _, candidate := range candidates {
		switch candidate.ID {
		case "make:Makefile:test":
			logical = true
			if candidate.Command != "make -f Makefile -- test" || len(candidate.WorkingDirectories) != 1 || candidate.WorkingDirectories[0] != "" {
				t.Fatalf("logical root candidate changed working directory: %+v", candidate)
			}
			if len(candidate.Unsupported) == 0 || !strings.Contains(candidate.Unsupported[0], "conditionally") {
				t.Fatalf("conditional review was lost: %+v", candidate)
			}
		case "make:build/root/Makefile:test":
			canonical = true
		}
	}
	if !logical || !canonical {
		t.Fatalf("logical and canonical entrypoints should both remain explicit candidates: logical=%t canonical=%t candidates=%+v", logical, canonical, candidates)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("discovery must not execute Make expressions: %v", err)
	}
}

func TestRepoMapMakeReferencesRevalidateChangedSymlink(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "build", "entry.mk"), "test:\n\treadyci check unit\n")
	logical := filepath.Join(repo, "Makefile")
	if err := os.Symlink("build/entry.mk", logical); err != nil {
		t.Fatal(err)
	}
	makeMap := scanRepoMake(repo)
	refs := checkReferencesFromMake(repo, makeMap)
	if len(refs) != 1 || refs[0].Path != "Makefile" || refs[0].Check != "unit" {
		t.Fatalf("internal logical references = %+v", refs)
	}
	external := filepath.Join(t.TempDir(), "outside.mk")
	writeFileForRepoMapTest(t, external, "test:\n\treadyci check external-private-check\n")
	if err := os.Remove(logical); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, logical); err != nil {
		t.Fatal(err)
	}
	if refs := checkReferencesFromMake(repo, makeMap); len(refs) != 0 {
		t.Fatalf("reference reread escaped through a changed Makefile symlink: %+v", refs)
	}
}

func TestRepoMapMakeSymlinkContainment(t *testing.T) {
	tests := []struct {
		name  string
		links map[string]string
		files map[string]string
		want  bool
	}{
		{name: "direct internal", links: map[string]string{"Makefile": "build/entry.mk"}, files: map[string]string{"build/entry.mk": "test:\n\ttrue\n"}, want: true},
		{name: "nested internal parent", links: map[string]string{"Makefile": "build/alias", "build/alias": "../entry.mk"}, files: map[string]string{"entry.mk": "test:\n\ttrue\n", "build/keep": ""}, want: true},
		{name: "internal directory hop", links: map[string]string{"Makefile": "alias/../entry.mk", "alias": "build/sub"}, files: map[string]string{"build/entry.mk": "test:\n\ttrue\n", "build/sub/keep": ""}, want: true},
		{name: "external parent", links: map[string]string{"Makefile": "../outside.mk"}},
		{name: "escape after directory symlink", links: map[string]string{"Makefile": "alias/../outside.mk", "alias": "."}, files: map[string]string{"outside.mk": "test:\n\ttrue\n"}},
		{name: "git control metadata", links: map[string]string{"Makefile": ".git/entry.mk"}, files: map[string]string{".git/entry.mk": "test:\n\ttrue\n"}},
		{name: "git control intermediate", links: map[string]string{"Makefile": ".GIT/../entry.mk"}, files: map[string]string{".GIT/keep": "", "entry.mk": "test:\n\ttrue\n"}},
		{name: "control target", links: map[string]string{"Makefile": "bad\nentry.mk"}, files: map[string]string{"bad\nentry.mk": "test:\n\ttrue\n"}},
		{name: "backslash target", links: map[string]string{"Makefile": "bad\\entry.mk"}, files: map[string]string{"bad\\entry.mk": "test:\n\ttrue\n"}},
		{name: "windows absolute target", links: map[string]string{"Makefile": "C:entry.mk"}, files: map[string]string{"C:entry.mk": "test:\n\ttrue\n"}},
		{name: "cycle", links: map[string]string{"Makefile": "entry.mk", "entry.mk": "Makefile"}},
		{name: "dangling", links: map[string]string{"Makefile": "missing.mk"}},
		{name: "directory target", links: map[string]string{"Makefile": "build"}, files: map[string]string{"build/keep": ""}},
		{name: "regular file as directory", links: map[string]string{"Makefile": "entry.mk/../actual.mk"}, files: map[string]string{"entry.mk": "test:\n\ttrue\n", "actual.mk": "test:\n\ttrue\n"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			for name, contents := range test.files {
				writeFileForRepoMapTest(t, filepath.Join(repo, name), contents)
			}
			for name, target := range test.links {
				if err := os.Symlink(target, filepath.Join(repo, name)); err != nil {
					t.Fatal(err)
				}
			}
			got := false
			for _, candidate := range inferRepoImportCandidates(repoMapReport{Make: scanRepoMake(repo)}) {
				got = got || candidate.ID == "make:Makefile:test"
			}
			if got != test.want {
				t.Fatalf("logical Makefile candidate = %t, want %t", got, test.want)
			}
		})
	}
}

func TestRepoMapMakeSymlinkRejectsAbsoluteAndBoundsTraversal(t *testing.T) {
	t.Run("control character logical path", func(t *testing.T) {
		repo := t.TempDir()
		writeFileForRepoMapTest(t, filepath.Join(repo, "bad\npath", "Makefile"), "test:\n\ttrue\n")
		if got := scanRepoMake(repo); len(got.Files) != 0 {
			t.Fatalf("control character source path was imported: %+v", got)
		}
	})
	t.Run("absolute internal target", func(t *testing.T) {
		repo := t.TempDir()
		target := filepath.Join(repo, "entry.mk")
		writeFileForRepoMapTest(t, target, "test:\n\ttrue\n")
		if err := os.Symlink(target, filepath.Join(repo, "Makefile")); err != nil {
			t.Fatal(err)
		}
		if got := scanRepoMake(repo); len(got.Files) != 0 {
			t.Fatalf("source-incompatible absolute target was imported: %+v", got)
		}
	})
	for _, count := range []int{40, 41} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			repo := t.TempDir()
			writeFileForRepoMapTest(t, filepath.Join(repo, "entry.mk"), "test:\n\ttrue\n")
			for i := 0; i < count; i++ {
				name, target := fmt.Sprintf("link-%d", i), fmt.Sprintf("link-%d", i+1)
				if i == 0 {
					name = "Makefile"
				}
				if i == count-1 {
					target = "entry.mk"
				}
				if err := os.Symlink(target, filepath.Join(repo, name)); err != nil {
					t.Fatal(err)
				}
			}
			got := scanRepoMake(repo)
			if (len(got.Files) == 1) != (count == 40) {
				t.Fatalf("%d-hop discovery: %+v", count, got)
			}
		})
	}
}
