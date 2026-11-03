package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestDeviceLoginStoresCredentialWithoutLocalhostCallback(t *testing.T) {
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	challenge := ""
	userAgent := ""
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
			userAgent = r.UserAgent()
			writeCLITestJSON(w, 201, preflightapi.CLIDeviceAuthorization{DeviceCode: device, UserCode: "ABCDE-FGHIJ", VerificationURI: "https://app.readyci.test/cli/device", ExpiresAt: time.Now().Add(time.Minute), IntervalSeconds: 1})
		case "/v1/auth/cli/device/poll":
			var req preflightapi.CLIDevicePollRequest
			json.NewDecoder(r.Body).Decode(&req)
			if req.DeviceCode != device || !preflightapi.VerifyPKCES256(challenge, req.CodeVerifier) {
				t.Error("terminal proof was not bound to initiation")
			}
			writeCLITestJSON(w, 200, preflightapi.CLIDevicePollResponse{State: "approved", Token: &preflightapi.CreateAPITokenResponse{Secret: "pfl_device_test", Token: preflightapi.APITokenSummary{ID: "token_device", Scopes: []string{"runs:create", "runs:read"}}}})
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
	if strings.Contains(output, device) || strings.Contains(output, "pfl_device_test") {
		t.Fatal("terminal output leaked a secret")
	}
	// The code is printed for the user to type, separately from a URL that
	// does not carry it.
	if !strings.Contains(output, "  https://app.readyci.test/cli/device\n") || !strings.Contains(output, "type this code: ABCDE-FGHIJ") || strings.Contains(output, "?code=") {
		t.Fatalf("terminal output must show the plain URL and the code to type:\n%s", output)
	}
	if !strings.Contains(output, "readyci login --device") || !strings.Contains(output, "scopes: runs:create,runs:read") {
		t.Fatalf("terminal output omitted the deny warning or granted scopes:\n%s", output)
	}
	if !strings.HasPrefix(userAgent, "readyci/") {
		t.Fatalf("device start user agent = %q, want the CLI identity shown to the approver", userAgent)
	}
	credential, found, err := loadStoredCredential(server.URL)
	if err != nil || !found || credential.TokenID != "token_device" {
		t.Fatal("device credential was not stored")
	}
}

func TestDeviceLoginRejectsVerificationURLCarryingTheCode(t *testing.T) {
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	var polls atomic.Int32
	for _, uri := range []string{
		"https://app.readyci.test/cli/device?code=ABCDE-FGHIJ",
		"https://app.readyci.test/cli/device?user_code=abcdefghij",
		"https://app.readyci.test/cli/device#ABCDE-FGHIJ",
		"/cli/device",
		"javascript:alert(1)",
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/auth/cli/device/poll" {
				polls.Add(1)
			}
			writeCLITestJSON(w, 201, preflightapi.CLIDeviceAuthorization{DeviceCode: strings.Repeat("d", 43), UserCode: "ABCDE-FGHIJ", VerificationURI: uri, ExpiresAt: time.Now().Add(time.Minute), IntervalSeconds: 1})
		}))
		output, err := captureStdoutForCLITest(t, func() error { return loginCommand([]string{"--device", "--api-url", server.URL, "--timeout", "5s"}) })
		server.Close()
		if err == nil || !strings.Contains(err.Error(), "invalid device authorization response") {
			t.Fatalf("verification URL %q accepted: %v", uri, err)
		}
		if strings.Contains(output, "ABCDE-FGHIJ") {
			t.Fatalf("code printed for rejected verification URL %q", uri)
		}
	}
	if polls.Load() != 0 {
		t.Fatal("rejected device authorization was polled")
	}
}

// The server delivers an approval recorded before expiry to the next poll, so
// the CLI must not give up locally at the expiry instant.
func TestDeviceLoginPollsOnceMoreAfterExpiry(t *testing.T) {
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	expires := time.Now().Add(1500 * time.Millisecond)
	var afterExpiry atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/cli/device/start":
			writeCLITestJSON(w, 201, preflightapi.CLIDeviceAuthorization{DeviceCode: strings.Repeat("d", 43), UserCode: "ABCDE-FGHIJ", VerificationURI: "https://app.readyci.test/cli/device", ExpiresAt: expires, IntervalSeconds: 1})
		case "/v1/auth/cli/device/poll":
			if time.Now().Before(expires) {
				writeCLITestJSON(w, 200, preflightapi.CLIDevicePollResponse{State: "pending"})
				return
			}
			afterExpiry.Store(true)
			writeCLITestJSON(w, 200, preflightapi.CLIDevicePollResponse{State: "approved", Token: &preflightapi.CreateAPITokenResponse{Secret: "pfl_device_late", Token: preflightapi.APITokenSummary{ID: "token_late"}}})
		}
	}))
	defer server.Close()
	if _, err := captureStdoutForCLITest(t, func() error { return loginCommand([]string{"--device", "--api-url", server.URL}) }); err != nil {
		t.Fatal(err)
	}
	if !afterExpiry.Load() {
		t.Fatal("approval was not collected by a poll after expiry")
	}
	if credential, found, err := loadStoredCredential(server.URL); err != nil || !found || credential.TokenID != "token_late" {
		t.Fatal("late approval was not stored")
	}
}
