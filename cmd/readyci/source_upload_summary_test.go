package main

import (
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func TestActualUploadSummaryReportsOmittedTestsAndIncludedSecretNames(t *testing.T) {
	bundle := syncbundlebuild.BuildResult{OmittedUntrackedFiles: []string{"new_test.go"}, Manifest: syncbundle.Manifest{Baseline: []syncbundle.Entry{{Path: ".npmrc"}, {Path: "testdata/credentials.json"}, {Path: "cert.pem"}, {Path: ".ssh/config"}, {Path: "kubeconfig"}}}}
	stderr, err := captureStderrForCLITest(t, func() error { printSourceUploadSummary(bundle); return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"source upload: 5 source entries, 0 untracked included, 1 untracked omitted", "new_test.go", "5 tracked secret-like paths included (", ".npmrc", "testdata/credentials.json", "cert.pem", ".ssh/config", "kubeconfig"} {
		if !strings.Contains(stderr, text) {
			t.Errorf("upload omitted %q: %s", text, stderr)
		}
	}
}

// Committed fixtures with secret-like names are part of the repository, so they
// must not print a warning line each on every upload. An untracked one is new
// and still gets its own line.
func TestUploadSummaryCollapsesTrackedSecretNames(t *testing.T) {
	var baseline []syncbundle.Entry
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"} {
		baseline = append(baseline, syncbundle.Entry{Path: "testdata/" + name + ".pem"})
	}
	bundle := syncbundlebuild.BuildResult{
		UntrackedFiles: []string{"server.pem"},
		Manifest:       syncbundle.Manifest{Baseline: baseline, Workspace: []syncbundle.Entry{{Path: "server.pem"}}},
	}
	stderr, err := captureStderrForCLITest(t, func() error { printSourceUploadSummary(bundle); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(stderr, "secret-like"); got != 2 {
		t.Fatalf("upload printed %d secret-like lines, want one for the untracked path and one for all tracked paths:\n%s", got, stderr)
	}
	for _, text := range []string{"secret-like untracked path included: server.pem", "12 tracked secret-like paths included", "testdata/a.pem"} {
		if !strings.Contains(stderr, text) {
			t.Errorf("upload omitted %q: %s", text, stderr)
		}
	}
}
