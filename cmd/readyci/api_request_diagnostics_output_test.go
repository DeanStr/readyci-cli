package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

func diagnosticRequestFixture() *preflightapi.APIRequestDiagnostics {
	reused := true
	return &preflightapi.APIRequestDiagnostics{
		Method: http.MethodGet, Path: "/v1/runs/{id}/tests",
		RequestID: "cli_" + strings.Repeat("a", 32), ServerRequestID: "req_" + strings.Repeat("b", 32),
		Phase: "response_body", ElapsedMS: 30000, TimeoutMS: 30000,
		StatusCode: http.StatusOK, ConnectionReused: &reused,
	}
}

func TestNormalizeCLIRequestErrorPreservesDiagnosticsAndClassification(t *testing.T) {
	request := diagnosticRequestFixture()
	for _, tc := range []struct {
		name                 string
		err                  error
		code, classification string
		exit                 int
	}{
		{"deadline", api.RequestError{Request: request, Err: context.DeadlineExceeded}, "transport_timeout", preflightapi.ClassificationTransportFailure, cliExitVerificationUnknown},
		{"body failure", api.RequestError{Request: request, Err: io.ErrUnexpectedEOF}, "transport_failure", preflightapi.ClassificationTransportFailure, cliExitVerificationUnknown},
		{"cancellation", api.RequestError{Request: request, Err: context.Canceled}, "interrupted", preflightapi.ClassificationCancelled, 130},
		{"stream failure", api.EventStreamError{Err: api.RequestError{Request: request, Err: context.DeadlineExceeded}}, "verification_unknown", preflightapi.ClassificationVerificationUnknown, cliExitVerificationUnknown},
		{"API denial", api.ResponseError{Request: request, StatusCode: http.StatusForbidden, Code: "auth_forbidden"}, "auth_forbidden", preflightapi.ClassificationAuthForbidden, cliExitAuthOrPolicy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			normalized := normalizeCLIError(tc.err)
			if normalized.Code != tc.code || normalized.Classification != tc.classification || normalized.ExitCode != tc.exit {
				t.Fatalf("normalized error = %+v", normalized)
			}
			if !reflect.DeepEqual(normalized.Request, request) {
				t.Fatalf("request observations lost: got %+v, want %+v", normalized.Request, request)
			}
			if !errors.Is(normalized, tc.err) {
				t.Fatal("normalization lost original error")
			}
		})
	}
}

func TestRenderCLIErrorProjectsRequestDiagnostics(t *testing.T) {
	request := diagnosticRequestFixture()
	output, err := captureStdoutForCLITest(t, func() error {
		code := renderCLIError([]string{"runs", "tests", "--json", "run_example"}, api.RequestError{Request: request, Err: context.DeadlineExceeded})
		if code != cliExitVerificationUnknown {
			t.Fatalf("exit code = %d", code)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var document cliErrorEnvelope
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatal(err)
	}
	if document.Error.Code != "transport_timeout" || !reflect.DeepEqual(document.Error.Request, request) {
		t.Fatalf("structured error = %+v", document.Error)
	}
	validateDiagnosticJSONSchema(t, "readyci.cli.v1.schema.json", "", []byte(output))
}

func TestSessionClientErrorRetainsRequestWithoutChangingAuthoritativeResult(t *testing.T) {
	request := diagnosticRequestFixture()
	for _, verdict := range []string{preflightapi.VerificationVerdictVerified, preflightapi.VerificationVerdictFailed} {
		t.Run(verdict, func(t *testing.T) {
			session := preflightapi.VerificationSession{
				SessionID: "group_example", Verdict: verdict,
				Classification: preflightapi.ClassificationTestFailed,
				Currentness:    preflightapi.VerificationCurrentnessCurrent,
				ResultDigest:   "sha256:" + strings.Repeat("c", 64),
			}
			before, _ := json.Marshal(session)
			verdictExit := verificationVerdictExit(session)
			code := applyVerificationCommandError(&session, api.RequestError{Request: request, Err: context.DeadlineExceeded})
			if code != cliExitVerificationUnknown || session.ClientError == nil || session.ClientError.Code != "transport_timeout" || !reflect.DeepEqual(session.ClientError.Request, request) {
				t.Fatalf("client error = %+v, exit = %d", session.ClientError, code)
			}
			if got := verificationVerdictExit(session); !reflect.DeepEqual(got, verdictExit) {
				t.Fatalf("client error changed verdict exit: got %v, want %v", got, verdictExit)
			}
			encoded, err := json.Marshal(session.ClientError)
			if err != nil {
				t.Fatal(err)
			}
			validateDiagnosticJSONSchema(t, "readyci.verification.v0alpha1.schema.json", "/$defs/verificationClientError", encoded)
			session.ClientError = nil
			after, _ := json.Marshal(session)
			if !bytes.Equal(before, after) {
				t.Fatalf("authoritative receipt changed: before=%s after=%s", before, after)
			}
		})
	}
}

func TestAgentExplainRetainsFailedArtifactReadDiagnosticsAndNativeOutcome(t *testing.T) {
	nativeExit := 23
	detail := preflightapi.RunDetail{
		Summary: preflightapi.RunSummary{RunID: "run_example", Status: preflightapi.StatusFailed, ExitCode: &nativeExit, Classification: preflightapi.ClassificationTestFailed},
		Spec:    preflightapi.RunSpec{RunID: "run_example", Command: "run-tests"},
	}
	artifactRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/runs/run_example":
			writeCLITestJSON(w, http.StatusOK, detail)
		case "/v1/runs/run_example/events":
			writeCLITestJSON(w, http.StatusOK, preflightapi.RunEventsResponse{})
		case "/v1/runs/run_example/artifacts":
			artifactRequests++
			w.Header().Set(preflightapi.ServerRequestIDHeader, "req_"+strings.Repeat("b", 32))
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "{")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	output, err := captureStdoutForCLITest(t, func() error {
		return agentExplainCommand([]string{"--api-url", server.URL, "--token", "test-token", "--json", "run_example"})
	})
	if err != nil {
		t.Fatalf("partial Explain changed its outer success behavior: %v", err)
	}
	var report agentExplainReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != preflightapi.StatusFailed || report.ExitCode == nil || *report.ExitCode != nativeExit || report.Diagnosis.Kind != preflightapi.ClassificationTestFailed || report.Repair.Classification != preflightapi.ClassificationTestFailed {
		t.Fatalf("artifact read changed native outcome: %+v", report)
	}
	if artifactRequests != 1 || len(report.Warnings) != 1 || report.Warnings[0].ClientError == nil {
		t.Fatalf("artifact requests=%d warnings=%+v", artifactRequests, report.Warnings)
	}
	clientError := report.Warnings[0].ClientError
	if clientError.Code != "transport_failure" || clientError.Classification != preflightapi.ClassificationTransportFailure || clientError.Request == nil || clientError.Request.StatusCode != http.StatusOK || clientError.Request.ServerRequestID != "req_"+strings.Repeat("b", 32) {
		t.Fatalf("artifact warning lost observations: %+v", clientError)
	}
	validateAgentJSONSchema(t, []byte(output))
}

func TestRequestDiagnosticSchemasRejectUnmodelledAndInvalidFields(t *testing.T) {
	for _, filename := range []string{"readyci.cli.v1.schema.json", "readyci.verification.v0alpha1.schema.json"} {
		t.Run(filename, func(t *testing.T) {
			schema := diagnosticJSONSchema(t, filename, "/$defs/apiRequestDiagnostics")
			encoded, err := json.Marshal(diagnosticRequestFixture())
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				field string
				value any
			}{
				{"authorization", "unexpected"}, {"elapsed_ms", -1},
				{"timeout_ms", -1}, {"status_code", 99},
				{"connection_reused", "yes"}, {"response_body_incomplete", "yes"},
				{"request_id", "not-a-client-request-id"}, {"server_request_id", "not-a-server-request-id"},
				{"phase", "unknown"},
			} {
				var value map[string]any
				if err := json.Unmarshal(encoded, &value); err != nil {
					t.Fatal(err)
				}
				value[tc.field] = tc.value
				if err := schema.Validate(value); err == nil {
					t.Fatalf("schema accepted invalid %s", tc.field)
				}
			}
		})
	}
}

func diagnosticJSONSchema(t *testing.T, filename, fragment string) *jsonschema.Schema {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "schema", filename))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := jsonschema.Compile((&url.URL{Scheme: "file", Path: filepath.ToSlash(path), Fragment: fragment}).String())
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func validateDiagnosticJSONSchema(t *testing.T, filename, fragment string, data []byte) {
	t.Helper()
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	if err := diagnosticJSONSchema(t, filename, fragment).Validate(value); err != nil {
		t.Fatalf("request diagnostics violate schema: %v", err)
	}
}
