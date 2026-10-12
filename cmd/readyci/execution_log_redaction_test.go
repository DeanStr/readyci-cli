package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestExecutionLogsRedactAcrossEventsAndPages(t *testing.T) {
	const token = "pfl_example_token_123456"
	for _, eventType := range []string{preflightapi.EventStdout, preflightapi.EventStderr} {
		t.Run(eventType, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			logs := &executionLogObserver{stdout: &stdout, stderr: &stderr, redactor: newCLISecretRedactor([]string{"--token", token})}
			chunks := []string{"pfl_example_", "token_123456\nBear", "er dynamic_", "credential\nhttps://user:", "password@example.invalid/path?tok", "en=query_", "credential\nfinal partial h"}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				cursor, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
				writeCLITestJSON(w, 200, preflightapi.RunEventsResponse{RunID: "run", NextCursor: int64(cursor + 1), HasMore: cursor+1 < len(chunks), Events: []preflightapi.RunEvent{{ID: int64(cursor + 1), Type: eventType, Data: chunks[cursor]}}})
			}))
			defer server.Close()
			session := preflightapi.VerificationSession{Recipe: preflightapi.VerificationRecipe{Purpose: preflightapi.ExecutionPurpose}, Nodes: []preflightapi.VerificationNode{{Name: "test", Attempts: []preflightapi.VerificationAttempt{{RunID: "run", Status: preflightapi.StatusSucceeded}}}}}
			if err := logs.observe(context.Background(), api.NewClient(server.URL), session); err != nil {
				t.Fatal(err)
			}
			output := stdout.String() + stderr.String()
			for _, secret := range []string{token, "dynamic_credential", "user:password", "query_credential"} {
				if strings.Contains(output, secret) {
					t.Errorf("credential escaped redaction: %s", secret)
				}
			}
			if !strings.Contains(output, "[REDACTED]\nBearer [REDACTED]\nhttps://[REDACTED]@example.invalid/path?token=[REDACTED]\nfinal partial h") {
				t.Errorf("unexpected redacted output: %q", output)
			}
		})
	}
}

func TestExecutionLogRedactorChunkBoundaries(t *testing.T) {
	redactor := cliSecretRedactor{exact: []string{"abcdef", "defghi", "token value"}}
	for _, input := range []string{
		"before abcdefghi after", "token value", "token valuenext", "prefix İbefore abcdef after",
		"Authorization: Bearer dynamic-token\n", "BEARER\tvalue; next", "bearer\nvalue,next",
		"https://user:pass@example.invalid/path?api_key=value#fragment", "HTTP output is fine\n",
		"before?access-token=value&x=ok", "?X-AMZ-SECURITY-TOKEN=value", "partial https://host.invalid",
		"plain output without a newline", "trailing h", "bearer ", "https://user:pass@host/?token=trailing",
	} {
		t.Run(input, func(t *testing.T) {
			expected := redactor.Redact(input)
			for split := 0; split <= len(input); split++ {
				stream := executionLogRedactor{redactor: redactor}
				got := stream.redact(input[:split], false) + stream.redact(input[split:], false) + stream.redact("", true)
				if got != expected {
					t.Fatalf("split %d: got %q want %q", split, got, expected)
				}
			}
			stream := executionLogRedactor{redactor: redactor}
			var got strings.Builder
			for i := range len(input) {
				got.WriteString(stream.redact(input[i:i+1], false))
			}
			got.WriteString(stream.redact("", true))
			if got.String() != expected {
				t.Fatalf("one byte chunks: got %q want %q", got.String(), expected)
			}
		})
	}
	stream := executionLogRedactor{redactor: redactor}
	if got := stream.redact("ordinary output!", false); got != "ordinary output!" {
		t.Fatalf("unrelated output was delayed: %q", got)
	}
}

func TestExecutionLogRedactionStateIsolatedByRunAndStream(t *testing.T) {
	observer := &executionLogObserver{redactor: cliSecretRedactor{exact: []string{"secret-value"}}, streams: make(map[string]map[string]*executionLogRedactor)}
	first := observer.logStream("one", preflightapi.EventStdout)
	if got := first.redact("secret-", false); got != "" {
		t.Fatalf("partial secret leaked: %q", got)
	}
	for _, other := range []*executionLogRedactor{observer.logStream("one", preflightapi.EventStderr), observer.logStream("two", preflightapi.EventStdout)} {
		if got := other.redact("value\n", false); got != "value\n" {
			t.Fatalf("state crossed streams: %q", got)
		}
	}
	if got := observer.logStream("one", preflightapi.EventStdout).redact("value\n", false); got != "[REDACTED]\n" {
		t.Fatalf("state was not retained: %q", got)
	}
}
