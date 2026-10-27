package main

import (
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestLocalPreparationFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name, output, classification, action string
		retryable                            bool
	}{
		{
			name: "generic preparation exit one", output: "eng/bootstrap.py:42: setup failed before dependencies were ready\n",
			classification: preflightapi.ClassificationEnvironmentSetupFailed, action: preflightapi.AgentActionReportSetup,
		},
		{
			name: "ubuntu apt HTTP 503", output: "E: Failed to fetch http://archive.ubuntu.com/ubuntu/pool/main/p/python3/python3.deb  503 Service Unavailable [IP: 185.125.190.83 80]\n",
			classification: preflightapi.ClassificationDependencyDownloadFailed, action: preflightapi.AgentActionRetry, retryable: true,
		},
		{
			name: "ubuntu apt split status", output: "Err:1 http://archive.ubuntu.com/ubuntu noble/main amd64 python3\n  503 Service Unavailable\n",
			classification: preflightapi.ClassificationDependencyDownloadFailed, action: preflightapi.AgentActionRetry, retryable: true,
		},
		{
			name: "curl HTTP 503", output: "curl: (22) The requested URL returned error: 503\n",
			classification: preflightapi.ClassificationDependencyDownloadFailed, action: preflightapi.AgentActionRetry, retryable: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exit := 1
			events := []preflightapi.RunEvent{
				{ID: 10, Type: preflightapi.EventCommandStarted},
				{ID: 11, Type: preflightapi.EventStderr, Phase: "prepare", Step: "dependencies", Data: tc.output},
				{ID: 12, Type: preflightapi.EventCommandExited, Phase: "prepare", Step: "dependencies", ExitCode: &exit},
			}
			got := localAnalyzeRunFailureWithNetworkMode(preflightapi.StatusFailed, "prepare_dependencies; python -m pytest tests", &exit, events, "", "allowlist")
			if got.Classification != tc.classification || got.AgentAction != tc.action || got.Retryable != tc.retryable {
				t.Fatalf("analysis = %+v, want %s/%s retryable=%t", got, tc.classification, tc.action, tc.retryable)
			}
			if got.PrimaryFile != "" {
				t.Fatalf("preparation failure suggested a repository file repair: %+v", got)
			}
			if len(got.Evidence) == 0 || got.Evidence[0].EventID != 11 {
				t.Fatalf("missing preparation event evidence: %+v", got.Evidence)
			}
		})
	}
}

func TestLocalPreparationFailureDoesNotOverrideCommandAssertions(t *testing.T) {
	for _, tc := range []struct {
		name, preparation, service string
		exit                       int
	}{
		{name: "actual assertion mentions HTTP 503", exit: 1},
		{name: "recovered preparation warning", preparation: "warning: optional preparation probe unavailable; continuing\n", exit: 1},
		{name: "recovered preparation HTTP 503", preparation: "curl: (22) The requested URL returned error: 503\n", exit: 1},
		{name: "recovered Meson diagnostic", preparation: "meson-python: error: meson executable \"meson\" not found\n", exit: 78},
		{name: "recovered runtime marker", preparation: preflightapi.RuntimePreparationFailureMarker + "\n", exit: 78},
		{name: "service HTTP 503", service: "curl: (22) The requested URL returned error: 503\n", exit: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := []preflightapi.RunEvent{{ID: 20, Type: preflightapi.EventCommandStarted}}
			if tc.preparation != "" {
				events = append(events, preflightapi.RunEvent{ID: 21, Type: preflightapi.EventStderr, Phase: "prepare", Step: "dependencies", Data: tc.preparation})
			}
			events = append(events, preflightapi.RunEvent{
				ID: 22, Type: preflightapi.EventStderr, Phase: "command", Step: "tests",
				Data: "tests/test_registry.py:51: AssertionError: expected HTTP 503 Service Unavailable, got HTTP 200\n",
			})
			if tc.service != "" {
				events = append(events, preflightapi.RunEvent{ID: 23, Type: preflightapi.EventStderr, Phase: "prepare", Service: "mirror", Data: tc.service})
			}
			events = append(events, preflightapi.RunEvent{ID: 24, Type: preflightapi.EventCommandExited, Phase: "command", Step: "tests", ExitCode: &tc.exit})
			got := localAnalyzeRunFailureWithNetworkMode(preflightapi.StatusFailed, "python -m pytest tests", &tc.exit, events, "", "allowlist")
			if got.Classification != preflightapi.ClassificationTestFailed || got.AgentAction != preflightapi.AgentActionFixCode || got.Retryable {
				t.Fatalf("earlier preparation or service output replaced the actual assertion: %+v", got)
			}
			if strings.Contains(got.Summary, "Preparation failed") || strings.Contains(got.Summary, "runtime preparation failed") {
				t.Fatalf("summary revived recovered preparation: %q", got.Summary)
			}
		})
	}
}

func TestLocalPreparationFailureDoesNotResurrectEarlierScope(t *testing.T) {
	for _, phase := range []string{"always", "post"} {
		t.Run(phase, func(t *testing.T) {
			exit := 1
			events := []preflightapi.RunEvent{
				{ID: 30, Type: preflightapi.EventCommandStarted},
				{ID: 31, Type: preflightapi.EventStderr, Phase: "prepare", Step: "dependencies", Data: "curl: (22) The requested URL returned error: 503\n"},
				{ID: 32, Type: preflightapi.EventStderr, Phase: phase, Step: "cleanup", Data: "cleanup completed\n"},
				{ID: 33, Type: preflightapi.EventCommandExited, ExitCode: &exit},
			}
			got := localAnalyzeRunFailureWithNetworkMode(preflightapi.StatusFailed, "python -m pytest tests", &exit, events, "", "allowlist")
			if got.Classification != preflightapi.ClassificationTestFailed || got.Retryable || got.AgentAction != preflightapi.AgentActionFixCode {
				t.Fatalf("later %s scope revived an earlier preparation failure: %+v", phase, got)
			}
		})
	}
}

func TestLocalPreparationFailurePreservesAuthoritativeOutcomes(t *testing.T) {
	for _, tc := range []struct{ name, status, reason, classification string }{
		{"success", preflightapi.StatusSucceeded, "", preflightapi.ClassificationSuccess},
		{"cancelled", preflightapi.StatusCancelled, "", preflightapi.ClassificationCancelled},
		{"timeout", preflightapi.StatusTimedOut, "", preflightapi.ClassificationTimeout},
		{"denied", preflightapi.StatusDenied, "", preflightapi.ClassificationPolicyDenied},
		{"containment failure", preflightapi.StatusFailed, "firecracker shutdown failed: remove firecracker cgroup: device or resource busy", preflightapi.ClassificationWorkerInternalError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exit := 1
			events := []preflightapi.RunEvent{
				{Type: preflightapi.EventCommandStarted},
				{Type: preflightapi.EventStderr, Phase: "prepare", Data: "E: Failed to fetch http://archive.ubuntu.com/ubuntu/package.deb 503 Service Unavailable\n"},
				{Type: preflightapi.EventCommandExited, ExitCode: &exit},
			}
			got := localAnalyzeRunFailureWithNetworkMode(tc.status, "python -m pytest tests", &exit, events, tc.reason, "allowlist")
			if got.Classification != tc.classification {
				t.Fatalf("preparation replaced authoritative %s: %+v", tc.name, got)
			}
		})
	}
}

func TestLocalPreparationFailurePreservesPrimaryArtifactReason(t *testing.T) {
	exit := 1
	events := []preflightapi.RunEvent{{
		ID: 41, Type: preflightapi.EventStderr, Phase: "prepare",
		Data: "curl: (22) The requested URL returned error: 503\n",
	}}
	// Without a recorded nonzero command exit, a worker's primary artifact
	// failure cannot be downgraded to a secondary delivery diagnostic.
	got := localAnalyzeRunFailureWithNetworkMode(preflightapi.StatusFailed, "python -m pytest tests", &exit, events, "artifact upload failed: put artifacts: 503 unavailable", "allowlist")
	if got.Classification != preflightapi.ClassificationSyncFailed || got.AgentAction != preflightapi.AgentActionRetry {
		t.Fatalf("preparation output replaced the worker's primary artifact failure: %+v", got)
	}
}

func TestLocalPreparationFailureNetworkPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, network, output, classification, action string
		retryable                                     bool
	}{
		{
			name: "DNS with network none", network: "none", output: "curl: (6) Could not resolve host: archive.ubuntu.com\n",
			classification: preflightapi.ClassificationNetworkNotAllowed, action: preflightapi.AgentActionReportSetup,
		},
		{
			name: "HTTP 503 with network none", network: "none", output: "curl: (22) The requested URL returned error: 503\n",
			classification: preflightapi.ClassificationDependencyDownloadFailed, action: preflightapi.AgentActionRetry, retryable: true,
		},
		{
			name: "DNS with default network", network: "default", output: "curl: (6) Could not resolve host: archive.ubuntu.com\n",
			classification: preflightapi.ClassificationDependencyDownloadFailed, action: preflightapi.AgentActionRetry, retryable: true,
		},
		{
			name: "DNS without network metadata", output: "curl: (6) Could not resolve host: archive.ubuntu.com\n",
			classification: preflightapi.ClassificationDependencyDownloadFailed, action: preflightapi.AgentActionRetry, retryable: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exit := 1
			events := []preflightapi.RunEvent{
				{ID: 50, Type: preflightapi.EventCommandStarted},
				{ID: 51, Type: preflightapi.EventStderr, Phase: "prepare", Step: "dependencies", Data: tc.output},
				{ID: 52, Type: preflightapi.EventCommandExited, Phase: "prepare", Step: "dependencies", ExitCode: &exit},
			}
			got := localAnalyzeRunFailureWithNetworkMode(preflightapi.StatusFailed, "prepare_dependencies; python -m pytest tests", &exit, events, "", tc.network)
			if got.Classification != tc.classification || got.AgentAction != tc.action || got.Retryable != tc.retryable || got.PrimaryFile != "" {
				t.Fatalf("analysis = %+v, want %s/%s retryable=%t without a primary file", got, tc.classification, tc.action, tc.retryable)
			}
			if len(got.Evidence) == 0 || got.Evidence[0].EventID != 51 {
				t.Fatalf("missing observed download evidence: %+v", got.Evidence)
			}
		})
	}
}

func TestLocalPreparationFailureRequiresObservedFailedPreparation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []preflightapi.RunEvent
	}{
		{
			name: "blank command newline changes scope",
			events: []preflightapi.RunEvent{
				{ID: 61, Type: preflightapi.EventStderr, Phase: "prepare", Data: "curl: (22) The requested URL returned error: 503\n"},
				{ID: 62, Type: preflightapi.EventStdout, Phase: "command", Data: "\n"},
			},
		},
		{
			name: "successful preparation then silent exit",
			events: []preflightapi.RunEvent{
				{ID: 61, Type: preflightapi.EventStdout, Phase: "prepare", Data: "Dependencies installed successfully\n"},
			},
		},
		{name: "prepare phase without diagnostic", events: []preflightapi.RunEvent{{ID: 61, Type: preflightapi.EventStdout, Phase: "prepare", Data: "\n"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exit := 1
			events := append([]preflightapi.RunEvent{{ID: 60, Type: preflightapi.EventCommandStarted}}, tc.events...)
			events = append(events, preflightapi.RunEvent{ID: 63, Type: preflightapi.EventCommandExited, ExitCode: &exit})
			got := localAnalyzeRunFailureWithNetworkMode(preflightapi.StatusFailed, "python -m pytest tests", &exit, events, "", "allowlist")
			if got.Classification != preflightapi.ClassificationTestFailed || got.AgentAction != preflightapi.AgentActionFixCode || got.Retryable {
				t.Fatalf("incomplete output established a preparation cause: %+v", got)
			}
			if strings.Contains(got.Summary, "Preparation output reports a failure") || strings.Contains(got.Summary, "Dependency download failed during preparation") {
				t.Fatalf("summary claimed an unestablished preparation cause: %q", got.Summary)
			}
		})
	}
}

func TestLocalPreparationRetainsRepositoryCodeFailures(t *testing.T) {
	for _, tc := range []struct {
		name, output, classification, action, primaryFile string
	}{
		{"TypeScript build", "src/app.ts:12:7 - error TS2322: Type 'string' is not assignable to type 'number'.\n", preflightapi.ClassificationTypecheckFailed, preflightapi.AgentActionFixCode, "src/app.ts"},
		{"TypeScript nonpretty build", "src/app.ts(12,7): error TS2322: Type 'string' is not assignable to type 'number'.\n", preflightapi.ClassificationTypecheckFailed, preflightapi.AgentActionFixCode, "src/app.ts"},
		{"dependency TypeScript postinstall", "npm ci\nnode_modules/some-package/src/index.ts:12:7 - error TS2322: Type 'string' is not assignable to type 'number'.\n", preflightapi.ClassificationEnvironmentSetupFailed, preflightapi.AgentActionReportSetup, ""},
		{"dependency TypeScript nonpretty postinstall", "node_modules/some-package/src/index.ts(12,7): error TS2322: Type 'string' is not assignable to type 'number'.\n", preflightapi.ClassificationEnvironmentSetupFailed, preflightapi.AgentActionReportSetup, ""},
		{"dependency Python typecheck", ".venv/lib/python3.12/site-packages/pkg/mod.pyi:12: error: Incompatible types in assignment [assignment]\n", preflightapi.ClassificationEnvironmentSetupFailed, preflightapi.AgentActionReportSetup, ""},
		{"unattributed type diagnostic", "error TS2322: Type 'string' is not assignable to type 'number'.\n", preflightapi.ClassificationEnvironmentSetupFailed, preflightapi.AgentActionReportSetup, ""},
		{"Jest summary", "FAIL src/sum.test.js\nTests:       1 failed, 4 passed, 5 total\n", preflightapi.ClassificationTestFailed, preflightapi.AgentActionFixCode, ""},
		{"pytest failure", "FAILED tests/test_codegen.py::test_render - AssertionError: expected header\n", preflightapi.ClassificationTestFailed, preflightapi.AgentActionFixCode, "tests/test_codegen.py"},
		{"repository compiler error", "src/native/ext.c:12:5: error: unknown type name 'widget_t'\nerror: compilation failed\n", preflightapi.ClassificationBuildFailed, preflightapi.AgentActionFixCode, "src/native/ext.c"},
		{"repository lint error", "src/app.js:3:1: error: Unexpected var no-var\neslint: lint failed\n", preflightapi.ClassificationLintFailed, preflightapi.AgentActionFixCode, "src/app.js"},
		{"dependency compiler error", "/tmp/pip-install-x1/numpy/src/x.c:1:1: error: unknown type name 'npy'\nerror: compilation failed\n", preflightapi.ClassificationEnvironmentSetupFailed, preflightapi.AgentActionReportSetup, ""},
		{"installed dependency lint mention", "npm error eslint@9.1.0 postinstall failed\nnode_modules/eslint/bin/eslint.js:3:1: error\n", preflightapi.ClassificationEnvironmentSetupFailed, preflightapi.AgentActionReportSetup, ""},
		{"installer names test runner", "ERROR: Could not find a version that satisfies the requirement pytest-cov==99 (from versions: none)\n", preflightapi.ClassificationEnvironmentSetupFailed, preflightapi.AgentActionReportSetup, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exit := 1
			events := []preflightapi.RunEvent{
				{ID: 10, Type: preflightapi.EventCommandStarted},
				{ID: 11, Type: preflightapi.EventStderr, Phase: "prepare", Step: "build", Data: tc.output},
				{ID: 12, Type: preflightapi.EventCommandExited, Phase: "prepare", Step: "build", ExitCode: &exit},
			}
			got := localAnalyzeRunFailureWithNetworkMode(preflightapi.StatusFailed, "npm run build && npm test", &exit, events, "", "allowlist")
			if got.Classification != tc.classification || got.AgentAction != tc.action {
				t.Fatalf("analysis=%+v, want %s/%s", got, tc.classification, tc.action)
			}
			if got.PrimaryFile != tc.primaryFile {
				t.Fatalf("primary file=%q, want %q", got.PrimaryFile, tc.primaryFile)
			}
		})
	}
}

func TestLocalIncompleteArtifactCollectionRequestsReducedScope(t *testing.T) {
	exit := 0
	reason := "artifact upload failed: artifact collection incomplete: retained 2 of 9 selected files; 7 omitted (1 report candidates); inspect .readyci/artifact-retention.json"
	events := []preflightapi.RunEvent{
		{ID: 1, Type: preflightapi.EventStdout, Phase: "command", Data: "ok\n"},
		{ID: 2, Type: preflightapi.EventCommandExited, ExitCode: &exit},
	}
	got := localAnalyzeRunFailureWithNetworkMode(preflightapi.StatusFailed, "go test ./...", &exit, events, reason, "")
	if got.Classification != preflightapi.ClassificationArtifactLimitExceeded || got.AgentAction != preflightapi.AgentActionReduceScope || got.Retryable {
		t.Fatalf("incomplete artifact collection analysis=%+v, want artifact_limit_exceeded/reduce_scope", got)
	}
}
