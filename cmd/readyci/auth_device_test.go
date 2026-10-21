package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestDeviceLoginStoresCredentialWithoutLocalhostCallback(t *testing.T) {
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	challenge := ""
	device := "terminal_redemption_secret_abcdefghijklmnopqrstuvwxyz"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Error("device login used browser callback")
			w.WriteHeader(400)
			return
		}
		switch r.URL.Path {
		case "/v1/auth/cli/device/start":
			var req preflightapi.CLIDeviceStartRequest
			json.NewDecoder(r.Body).Decode(&req)
			challenge = req.CodeChallenge
			writeCLITestJSON(w, 201, preflightapi.CLIDeviceAuthorization{DeviceCode: device, UserCode: "ABCDE-FGHIJ", VerificationURI: "https://app.readyci.test/cli/device?code=ABCDE-FGHIJ", ExpiresAt: time.Now().Add(time.Minute), IntervalSeconds: 1})
		case "/v1/auth/cli/device/poll":
			var req preflightapi.CLIDevicePollRequest
			json.NewDecoder(r.Body).Decode(&req)
			if req.DeviceCode != device || !preflightapi.VerifyPKCES256(challenge, req.CodeVerifier) {
				t.Error("terminal proof was not bound to initiation")
			}
			writeCLITestJSON(w, 200, preflightapi.CLIDevicePollResponse{State: "approved", Token: &preflightapi.CreateAPITokenResponse{Secret: "pfl_device_test", Token: preflightapi.APITokenSummary{ID: "token_device"}}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	output, err := captureStdoutForCLITest(t, func() error { return loginCommand([]string{"--device", "--api-url", server.URL, "--timeout", "5s"}) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, device) || strings.Contains(output, "pfl_device_test") || !strings.Contains(output, "ABCDE-FGHIJ") {
		t.Fatal("terminal output leaked secret or omitted approval code")
	}
	credential, found, err := loadStoredCredential(server.URL)
	if err != nil || !found || credential.TokenID != "token_device" {
		t.Fatal("device credential was not stored")
	}
}
