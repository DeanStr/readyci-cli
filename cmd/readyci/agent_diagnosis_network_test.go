package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestNetworkPolicyAdvisoryNamesUnreachableHostsOutsidePolicy(t *testing.T) {
	stdout := func(text string) []preflightapi.RunEvent {
		return []preflightapi.RunEvent{{Type: preflightapi.EventStdout, Phase: "command", Data: text + "\n"}}
	}
	goLookup := `go: downloading example.org/mod v1.0.0: Get "https://proxy.golang.org/example.org/mod/@v/v1.0.0.zip": dial tcp: lookup proxy.golang.org on 127.0.0.53:53: no such host`
	for _, tc := range []struct {
		name, status, network string
		allowlist             []string
		events                []preflightapi.RunEvent
		want                  []string
	}{
		{"go lookup outside allowlist", "failed", "allowlist", []string{"sum.golang.org"}, stdout(goLookup), []string{"proxy.golang.org"}},
		{"timed out", "timed_out", "allowlist", []string{"sum.golang.org"}, stdout(goLookup), []string{"proxy.golang.org"}},
		{"host already allowed", "failed", "allowlist", []string{"proxy.golang.org"}, stdout(goLookup), nil},
		{"allowlist entry as URL", "failed", "allowlist", []string{"https://Proxy.Golang.org/"}, stdout(goLookup), nil},
		{"no network", "failed", "none", nil, stdout(goLookup), []string{"proxy.golang.org"}},
		{"default network", "failed", "default", nil, stdout(goLookup), nil},
		{"unknown network", "failed", "", nil, stdout(goLookup), nil},
		{"success", "succeeded", "allowlist", []string{"sum.golang.org"}, stdout(goLookup), nil},
		{"curl", "failed", "allowlist", []string{"github.com"}, stdout("curl: (6) Could not resolve host: objects.githubusercontent.com"), []string{"objects.githubusercontent.com"}},
		{"npm", "failed", "allowlist", []string{"github.com"}, stdout("npm error request to https://registry.npmjs.org/left-pad failed, reason: getaddrinfo ENOTFOUND registry.npmjs.org"), []string{"registry.npmjs.org"}},
		{"python", "failed", "allowlist", []string{"github.com"}, stdout("HTTPSConnectionPool(host='files.pythonhosted.org', port=443): Max retries exceeded (Caused by NameResolutionError(\"Failed to resolve 'files.pythonhosted.org' ([Errno -3] Temporary failure in name resolution)\"))"), []string{"files.pythonhosted.org"}},
		{"java", "failed", "allowlist", []string{"github.com"}, stdout("Caused by: java.net.UnknownHostException: services.gradle.org"), []string{"services.gradle.org"}},
		{"apt", "failed", "none", nil, stdout("Err:1 http://archive.ubuntu.com/ubuntu noble InRelease\n  Temporary failure resolving 'archive.ubuntu.com'"), []string{"archive.ubuntu.com"}},
		{"several hosts in order", "failed", "allowlist", []string{"github.com"}, stdout("getaddrinfo ENOTFOUND b.example.com\ngetaddrinfo ENOTFOUND a.example.com\ngetaddrinfo ENOTFOUND b.example.com"), []string{"b.example.com", "a.example.com"}},
		{"single-label service name", "failed", "allowlist", []string{"github.com"}, stdout("dial tcp: lookup postgres on 127.0.0.11:53: no such host"), nil},
		{"private name", "failed", "allowlist", []string{"github.com"}, stdout("dial tcp: lookup db.internal on 127.0.0.11:53: no such host"), nil},
		{"private address", "failed", "allowlist", []string{"github.com"}, stdout("Could not resolve host: 10.0.0.4"), nil},
		{"host without failure", "failed", "allowlist", []string{"github.com"}, stdout("lookup proxy.golang.org succeeded; expected 2, received 1"), nil},
		{"failure without host", "failed", "allowlist", []string{"github.com"}, stdout("Error: no valid Sigstore verifiers could be initialized"), nil},
		{"service output", "failed", "allowlist", []string{"github.com"}, []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Service: "database", Data: goLookup}}, nil},
		{"lifecycle data", "failed", "allowlist", []string{"github.com"}, []preflightapi.RunEvent{{Type: preflightapi.EventRunFailed, Data: goLookup, Message: goLookup}}, nil},
		{"message only", "failed", "allowlist", []string{"github.com"}, []preflightapi.RunEvent{{Type: preflightapi.EventStdout, Message: goLookup}}, nil},
		{"malformed encoding", "failed", "allowlist", []string{"github.com"}, []preflightapi.RunEvent{{Type: preflightapi.EventStdout, Data: goLookup, DataBase64: "!invalid"}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			advisory := networkPolicyAdvisory(tc.status, tc.network, tc.allowlist, tc.events)
			if len(tc.want) == 0 {
				if advisory != "" {
					t.Fatalf("unexpected advisory: %q", advisory)
				}
				return
			}
			if !strings.Contains(advisory, ": "+strings.Join(tc.want, ", ")+".") {
				t.Fatalf("advisory %q does not name %v in order", advisory, tc.want)
			}
			if !strings.Contains(advisory, "does not replace the terminal failure") {
				t.Fatalf("advisory claims a cause: %q", advisory)
			}
			wantHint := "network_allowlist"
			if tc.network == "none" {
				wantHint = "network.warm_allowlist"
			}
			if !strings.Contains(advisory, wantHint) {
				t.Fatalf("advisory %q lacks %s guidance", advisory, wantHint)
			}
		})
	}
}

func TestNetworkPolicyAdvisoryJoinsChunksOnlyWithinOneSourceAndStaysBounded(t *testing.T) {
	const line = "getaddrinfo ENOTFOUND registry.npmjs.org"
	allowlist := []string{"github.com"}
	for split := 1; split < len(line); split++ {
		events := []preflightapi.RunEvent{
			{Type: "stdout", Phase: "command", Step: "tests", Data: line[:split]},
			{Type: "stdout", Phase: "command", Step: "tests", DataBase64: base64.StdEncoding.EncodeToString([]byte(line[split:] + "\n"))},
		}
		if networkPolicyAdvisory("failed", "allowlist", allowlist, events) == "" {
			t.Fatalf("split %d: chunked output lost the unreachable host", split)
		}
	}
	split := len(line) / 2
	first := preflightapi.RunEvent{Type: "stdout", Phase: "command", Step: "tests", Data: line[:split]}
	for name, events := range map[string][]preflightapi.RunEvent{
		"different stream":       {first, {Type: "stderr", Phase: "command", Step: "tests", Data: line[split:]}},
		"different phase":        {first, {Type: "stdout", Phase: "prepare", Step: "tests", Data: line[split:]}},
		"different step":         {first, {Type: "stdout", Phase: "command", Step: "other", Data: line[split:]}},
		"malformed interruption": {first, {Type: "stdout", Phase: "command", Step: "tests", DataBase64: "!invalid"}, {Type: "stdout", Phase: "command", Step: "tests", Data: line[split:]}},
		"byte limit":             {{Type: "stdout", Phase: "command", Data: strings.Repeat("x", networkAdvisoryLogLimit)}, {Type: "stdout", Phase: "command", Data: "\n" + line}},
	} {
		t.Run(name, func(t *testing.T) {
			if advisory := networkPolicyAdvisory("failed", "allowlist", allowlist, events); advisory != "" {
				t.Fatalf("output across sources or beyond the bound created advisory: %q", advisory)
			}
		})
	}
	var events []preflightapi.RunEvent
	for index := 0; index < networkAdvisorySources; index++ {
		events = append(events, preflightapi.RunEvent{Type: "stdout", Phase: "command", Step: fmt.Sprintf("step-%d", index), Data: "ok\n"})
	}
	events = append(events, preflightapi.RunEvent{Type: "stdout", Phase: "command", Step: "late", Data: line + "\n"})
	if advisory := networkPolicyAdvisory("failed", "allowlist", allowlist, events); advisory != "" {
		t.Fatalf("advisory exceeded the source bound: %q", advisory)
	}
	var many strings.Builder
	for index := 0; index < 10; index++ {
		fmt.Fprintf(&many, "getaddrinfo ENOTFOUND host%d.example.com\n", index)
	}
	advisory := networkPolicyAdvisory("failed", "allowlist", allowlist, []preflightapi.RunEvent{{Type: "stdout", Data: many.String()}})
	if !strings.Contains(advisory, "host2.example.com.") || strings.Contains(advisory, "host3.example.com") || len(advisory) > 400 {
		t.Fatalf("advisory is not bounded to %d hosts: %q", networkAdvisoryHostLimit, advisory)
	}
}

func TestAgentExplainNetworkPolicyAdvisoryPreservesAuthoritativeFailure(t *testing.T) {
	exitCode := 1
	detail := preflightapi.RunDetail{
		Summary: preflightapi.RunSummary{RunID: "run_network", Status: "failed", ExitCode: &exitCode, Classification: "test_failed", AgentAction: "fix_code", FailureSummary: "Tests failed."},
		Spec:    preflightapi.RunSpec{RunID: "run_network", Command: "go test ./...", CommandAlias: "go", NetworkMode: "allowlist", NetworkAllowlist: []string{"proxy.golang.org"}},
	}
	events := []preflightapi.RunEvent{
		{Type: "stdout", Phase: "command", Data: "--- FAIL: TestFetchTrustRoot\n    root_test.go:12: Get \"https://tuf-repo-cdn.sigstore.dev/root.json\": dial tcp: lookup tuf-repo-cdn.sigstore.dev on 127.0.0.53:53: no such host\n"},
		{Type: preflightapi.EventRunFailed, Status: "failed", ExitCode: &exitCode},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/runs/run_network":
			writeCLITestJSON(w, http.StatusOK, detail)
		case "/v1/runs/run_network/events":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{RunID: "run_network", Events: events})
		case "/v1/runs/run_network/artifacts":
			writeCLITestJSON(w, http.StatusOK, preflightapi.ListArtifactsResponse{RunID: "run_network"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	report, err := buildAgentExplainReport(context.Background(), api.NewClient(server.URL), server.URL, "run_network")
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "failed" || report.ExitCode == nil || *report.ExitCode != 1 || report.Diagnosis.Kind != "test_failed" || report.Repair.Classification != "test_failed" || report.Repair.AgentAction != "fix_code" || report.Diagnosis.Summary != "Tests failed." {
		t.Fatalf("advisory changed the authoritative outcome: %+v", report)
	}
	if len(report.Warnings) != 1 {
		t.Fatalf("warnings = %+v, want one network policy advisory", report.Warnings)
	}
	warning := report.Warnings[0]
	if warning.Section != "network_policy" || warning.Code != "host_outside_network_policy" || !strings.Contains(warning.Message, "tuf-repo-cdn.sigstore.dev") || !strings.Contains(report.Repair.RootCause, warning.Message) || len(report.Repair.Evidence) == 0 {
		t.Fatalf("network advisory or original evidence missing: repair=%+v warnings=%+v", report.Repair, report.Warnings)
	}
}
