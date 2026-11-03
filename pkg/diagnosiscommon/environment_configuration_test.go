package diagnosiscommon

import (
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestMissingEnvironmentConfigurationRespectsChunkBoundaries(t *testing.T) {
	const diagnostic = "FATAL: DATABASE_URL must be configured before tests start"
	const output = "\x1b[31m" + diagnostic + "\x1b[0m\n"
	code := 1
	for split := 1; split < len(output); split++ {
		failure := MissingEnvironmentConfigurationFromEvents([]preflightapi.RunEvent{
			{ID: 1, Type: preflightapi.EventStderr, Phase: "command", Data: output[:split]},
			{ID: 2, Type: preflightapi.EventStdout, Phase: "command", Data: "> app@1.0.0 test\n"},
			{ID: 3, Type: preflightapi.EventStderr, Phase: "command", Data: output[split:]},
		}, &code)
		if failure == nil || failure.Evidence.Text != diagnostic || failure.Evidence.EventID != 1 || failure.Evidence.Stream != "stderr" ||
			!strings.Contains(failure.Summary, "required environment variable DATABASE_URL is not configured") {
			t.Fatalf("split %d failure = %+v", split, failure)
		}
	}
}

func TestMissingEnvironmentConfigurationRequiresFinalFailedCommandOutput(t *testing.T) {
	const diagnostic = "FATAL: DATABASE_URL must be configured\n"
	code, success := 1, 0
	for _, tc := range []struct {
		name   string
		events []preflightapi.RunEvent
		exit   *int
	}{
		{"unknown exit", []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Data: diagnostic}}, nil},
		{"successful exit", []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Data: diagnostic}}, &success},
		{"service output", []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Service: "postgres", Data: diagnostic}}, &code},
		{"lifecycle message", []preflightapi.RunEvent{{Type: preflightapi.EventRunFailed, Message: "DATABASE_URL must be configured"}}, &code},
		{"earlier step", []preflightapi.RunEvent{
			{Type: preflightapi.EventStderr, Phase: "command", Step: "setup", Data: diagnostic},
			{Type: preflightapi.EventStderr, Phase: "command", Step: "test", Data: "npm ERR! Test failed.\n"},
		}, &code},
		{"skipped test record", []preflightapi.RunEvent{{Type: preflightapi.EventStdout, Data: "test_database_url_required (tests.test_config.ConfigTest) ... skipped 'DATABASE_URL is not set'\n"}}, &code},
		{"go test log", []preflightapi.RunEvent{{Type: preflightapi.EventStdout, Data: "    store_test.go:68: READYCI_TEST_DATABASE_URL is not set\n--- SKIP: TestStore (0.00s)\n"}}, &code},
		{"jest console block", []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Data: "  console.error\n    DATABASE_URL is not set\n"}}, &code},
		{"vitest console block", []preflightapi.RunEvent{{Type: preflightapi.EventStderr, Data: "stderr | test/db.test.ts > query\nDATABASE_URL is not set\n"}}, &code},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if failure := MissingEnvironmentConfigurationFromEvents(tc.events, tc.exit); failure != nil {
				t.Fatalf("failure = %+v", failure)
			}
		})
	}
	// The same line outside a running test's log block is a command diagnostic.
	for _, output := range []string{"    DATABASE_URL is not set\n", "    store.go:68: READYCI_TEST_DATABASE_URL is not set\n"} {
		if failure := MissingEnvironmentConfigurationFromEvents([]preflightapi.RunEvent{{Type: preflightapi.EventStderr, Data: output}}, &code); failure == nil {
			t.Fatalf("output %q was not recognized", output)
		}
	}
}

func TestMissingEnvironmentConfigurationLinePatterns(t *testing.T) {
	for line, want := range map[string]string{
		"FATAL: DATABASE_URL must be configured before tests start":                 "DATABASE_URL",
		"Error: DATABASE_URL is not set":                                            "DATABASE_URL",
		"DATABASE_URL not set":                                                      "DATABASE_URL",
		"ValueError: `API_TOKEN` is required.":                                      "API_TOKEN",
		"time=\"2026-11-01T00:00:00Z\" level=fatal msg=\"REDIS_URL is not set\"":    "REDIS_URL",
		"2026-11-01 12:00:00,123 - ERROR - SECRET_KEY has not been set":             "SECRET_KEY",
		"Missing required environment variable: DATABASE_URL":                       "DATABASE_URL",
		"error: missing environment variables: DATABASE_URL, REDIS_URL":             "DATABASE_URL",
		"Error: missing required env var":                                           "",
		"environment variable DATABASE_URL for the integration database is not set": "DATABASE_URL",
		"Environment variable PORT is empty":                                        "PORT",
		"RuntimeError: TOKEN environment variable must be set":                      "TOKEN",
		"ImproperlyConfigured: Set the DATABASE_URL environment variable":           "DATABASE_URL",
		"sh: 1: DATABASE_URL: parameter not set":                                    "DATABASE_URL",
		"zsh:1: CI: parameter null or not set":                                      "CI",
	} {
		name, ok := missingEnvironmentConfigurationLine(line)
		if !ok || name != want {
			t.Errorf("missingEnvironmentConfigurationLine(%q) = %q, %v; want %q", line, name, ok, want)
		}
	}
	for _, line := range []string{
		"DATABASE_URL is not set; skipping integration tests",
		"warning: DATABASE_URL is not set, using sqlite",
		"DATABASE_URL is not set (defaulting to sqlite)",
		"REDIS_URL is unset, falling back to in-memory cache",
		"OPTIONAL_TOKEN is not set; ignoring",
		"when API_KEY is unset the client uses anonymous access",
		"the field API_KEY is required by the schema",
		"  1) config reports missing environment variable",
		"Error: ID is required",
		"Error: TOKEN is required",
		"ReferenceError: API_URL is not defined",
		"NameError: name 'DATABASE_URL' is not defined",
		"Error: database_url is not set",
		"    raise RuntimeError(\"DATABASE_URL is not set\")",
		"Error: DATABASE_URL is set",
		"Error: DATABASE_URL is unsettled",
	} {
		if name, ok := missingEnvironmentConfigurationLine(line); ok {
			t.Errorf("missingEnvironmentConfigurationLine(%q) matched %q", line, name)
		}
	}
}

func TestMissingEnvironmentConfigurationPythonKeyErrorNeedsEnvironLookup(t *testing.T) {
	code := 1
	for _, tc := range []struct {
		name, output string
		want         bool
	}{
		{"frozen os frame", "  File \"<frozen os>\", line 714, in __getitem__\nKeyError: 'DATABASE_URL'\n", true},
		{"os.py frame", "  File \"/usr/lib/python3.10/os.py\", line 680, in __getitem__\n    raise KeyError(key) from None\nKeyError: 'DATABASE_URL'\n", true},
		{"pytest reprcrash after key error", "E       KeyError: 'DATABASE_URL'\n\n<frozen os>:714: KeyError\n", true},
		{"environ subscript", "    url = os.environ['DATABASE_URL']\nKeyError: 'DATABASE_URL'\n", true},
		{"other mapping", "    url = settings['DATABASE_URL']\nKeyError: 'DATABASE_URL'\n", false},
		{"other environ key", "    url = os.environ['OTHER_URL']\nKeyError: 'DATABASE_URL'\n", false},
		{"lowercase key", "  File \"<frozen os>\", line 714, in __getitem__\nKeyError: 'database_url'\n", false},
		{"key error with context", "  File \"<frozen os>\", line 714, in __getitem__\nKeyError: 'DATABASE_URL' while loading settings\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := MissingEnvironmentConfigurationFromEvents([]preflightapi.RunEvent{{Type: preflightapi.EventStderr, Data: tc.output}}, &code)
			if (failure != nil) != tc.want {
				t.Fatalf("failure = %+v, want match %v", failure, tc.want)
			}
			if failure != nil && (!strings.Contains(failure.Evidence.Text, "KeyError: 'DATABASE_URL'") || !strings.Contains(failure.Summary, "DATABASE_URL")) {
				t.Fatalf("failure = %+v", failure)
			}
		})
	}
}
