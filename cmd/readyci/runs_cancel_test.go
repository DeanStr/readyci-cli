package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestRunsCancelUsesCustomerScopedEndpoint(t *testing.T) {
	var request preflightapi.CancelRunRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/runs/run_123/cancel" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer pfl_test" {
			t.Fatalf("authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode cancellation: %v", err)
		}
		writeCLITestJSON(w, http.StatusOK, preflightapi.CancelRunResponse{RunID: "run_123", Status: preflightapi.StatusCancelled})
	}))
	defer server.Close()

	output, err := captureStdoutForCLITest(t, func() error {
		return runsCancelCommand([]string{"--api-url", server.URL, "--token", "pfl_test", "--reason", "client_interrupt", "--json", "run_123"})
	})
	if err != nil {
		t.Fatalf("runs cancel: %v", err)
	}
	if request.Reason != "client_interrupt" || !strings.Contains(output, `"status": "cancelled"`) {
		t.Fatalf("request/output = %+v / %s", request, output)
	}
}

func TestRunsCancelRejectsEmptyReasonBeforeNetwork(t *testing.T) {
	err := runsCancelCommand([]string{"--reason", " ", "run_123"})
	var typed cliError
	if err == nil || !strings.Contains(err.Error(), "must not be empty") || !errors.As(err, &typed) {
		t.Fatalf("error = %v, want typed config error", err)
	}
}
