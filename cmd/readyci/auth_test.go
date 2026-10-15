package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestEffectiveAPITokenUsesStoredCredentialForMatchingAPIURL(t *testing.T) {
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	createdAt := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	if err := saveStoredCredential(storedCredential{
		APIURL:    "https://api.readyci.test/",
		Token:     "pfl_saved",
		TokenID:   "token_saved",
		CreatedAt: createdAt,
	}); err != nil {
		t.Fatalf("save credential: %v", err)
	}

	got, err := effectiveAPIToken("https://api.readyci.test", "")
	if err != nil {
		t.Fatalf("effectiveAPIToken: %v", err)
	}
	if got != "pfl_saved" {
		t.Fatalf("effective token = %q, want stored token", got)
	}

	got, err = effectiveAPIToken("https://other.readyci.test", "")
	if err != nil {
		t.Fatalf("effectiveAPIToken for other API: %v", err)
	}
	if got != "" {
		t.Fatalf("effective token for other API = %q, want empty", got)
	}

	got, err = effectiveAPIToken("https://api.readyci.test", " pfl_flag ")
	if err != nil {
		t.Fatalf("effectiveAPIToken with explicit token: %v", err)
	}
	if got != "pfl_flag" {
		t.Fatalf("effective token with explicit token = %q, want explicit token", got)
	}
}

func TestRemoveStoredCredentialLeavesOtherAPIURLs(t *testing.T) {
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	for _, credential := range []storedCredential{
		{APIURL: "https://api.readyci.test", Token: "pfl_one"},
		{APIURL: "https://staging.readyci.test", Token: "pfl_two"},
	} {
		if err := saveStoredCredential(credential); err != nil {
			t.Fatalf("save credential %s: %v", credential.APIURL, err)
		}
	}

	removed, err := removeStoredCredential("https://api.readyci.test/")
	if err != nil {
		t.Fatalf("remove credential: %v", err)
	}
	if !removed {
		t.Fatal("remove credential returned false, want true")
	}
	if _, found, err := loadStoredCredential("https://api.readyci.test"); err != nil || found {
		t.Fatalf("removed credential found=%t err=%v, want not found", found, err)
	}
	got, found, err := loadStoredCredential("https://staging.readyci.test")
	if err != nil {
		t.Fatalf("load remaining credential: %v", err)
	}
	if !found || got.Token != "pfl_two" {
		t.Fatalf("remaining credential = %+v found=%t, want pfl_two", got, found)
	}
}

func TestAuthStatusAndLogoutUseStoredCredential(t *testing.T) {
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	t.Setenv("READYCI_API_TOKEN", "")
	var revoked bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer pfl_saved" {
			t.Fatalf("authorization = %q, want stored bearer token", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/account":
			writeCLITestJSON(w, http.StatusOK, preflightapi.AccountUsageResponse{
				TeamID: "team_auth",
				Plan:   preflightapi.AccountPlanSummary{ID: "dev"},
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/auth/cli/token":
			revoked = true
			writeCLITestJSON(w, http.StatusOK, preflightapi.RevokeAPITokenResponse{Token: preflightapi.APITokenSummary{ID: "token_saved"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	expiresAt := time.Date(2026, 6, 23, 0, 0, 0, 0, time.UTC)
	if err := saveStoredCredential(storedCredential{
		APIURL:    server.URL,
		Token:     "pfl_saved",
		TokenID:   "token_saved",
		ExpiresAt: &expiresAt,
		CreatedAt: time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("save credential: %v", err)
	}

	output, err := captureStdoutForCLITest(t, func() error {
		return authStatusCommand([]string{"--api-url", server.URL})
	})
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	requireCLITestOutputContains(t, output, "source: credentials", "token_id: token_saved", "team_id: team_auth", "plan: dev")

	output, err = captureStdoutForCLITest(t, func() error {
		return logoutCommand([]string{"--api-url", server.URL})
	})
	if err != nil {
		t.Fatalf("auth logout: %v", err)
	}
	requireCLITestOutputContains(t, output, "Logged out of "+normalizeAPIURL(server.URL))
	if !revoked {
		t.Fatal("logout did not revoke remote CLI token")
	}
	if _, found, err := loadStoredCredential(server.URL); err != nil || found {
		t.Fatalf("credential found=%t err=%v, want removed", found, err)
	}
}

func TestLoginCommandCompletesLocalCallbackAndStoresCredential(t *testing.T) {
	t.Setenv("READYCI_CONFIG_DIR", t.TempDir())
	expiresAt := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	var exchanged struct {
		Code         string `json:"code"`
		CodeVerifier string `json:"code_verifier"`
		Name         string `json:"name"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/auth/cli/exchange" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&exchanged); err != nil {
			t.Fatalf("decode exchange request: %v", err)
		}
		writeCLITestJSON(w, http.StatusCreated, preflightapi.CreateAPITokenResponse{
			Token: preflightapi.APITokenSummary{
				ID:        "token_login",
				TeamID:    "team_login",
				Name:      "Ready CI CLI",
				Scopes:    []string{"runs:read"},
				ExpiresAt: &expiresAt,
			},
			Secret: "pfl_login_secret",
		})
	}))
	defer server.Close()

	previousStdout := os.Stdout
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = writeEnd
	defer func() {
		os.Stdout = previousStdout
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- loginCommand([]string{"--api-url", server.URL, "--no-browser", "--timeout", "5s"})
	}()

	reader := bufio.NewReader(readEnd)
	var loginURL string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read login stdout before URL: %v", err)
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, server.URL+"/v1/auth/cli/login?") {
			loginURL = line
			break
		}
	}
	if loginURL == "" {
		t.Fatal("login URL was not printed")
	}
	callbackURL := queryValueFromURLForCLITest(t, loginURL, "redirect_uri")
	callbackURL = callbackURL + "&code=cli_code_test"
	resp, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("call login callback: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("login command: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("login command did not finish")
	}
	if err := writeEnd.Close(); err != nil {
		t.Fatalf("close stdout pipe: %v", err)
	}
	remaining, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read remaining stdout: %v", err)
	}
	output := string(remaining)
	requireCLITestOutputContains(t, output, "Waiting for login", "Logged in to "+normalizeAPIURL(server.URL), "token_id: token_login", "expires_at: "+expiresAt.Format(time.RFC3339))
	if exchanged.Code != "cli_code_test" || exchanged.Name != "Ready CI CLI" {
		t.Fatalf("exchange request = %+v, want code/name", exchanged)
	}
	challenge := queryValueFromURLForCLITest(t, loginURL, "code_challenge")
	if !preflightapi.VerifyPKCES256(challenge, exchanged.CodeVerifier) || strings.Contains(loginURL, exchanged.CodeVerifier) {
		t.Fatal("CLI did not retain its verifier independently of the browser URL")
	}
	if method := queryValueFromURLForCLITest(t, loginURL, "code_challenge_method"); method != "S256" {
		t.Fatalf("PKCE method=%q", method)
	}
	credential, found, err := loadStoredCredential(server.URL)
	if err != nil {
		t.Fatalf("load stored login credential: %v", err)
	}
	if !found || credential.Token != "pfl_login_secret" || credential.TokenID != "token_login" || credential.ExpiresAt == nil || !credential.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("stored credential = %+v found=%t", credential, found)
	}
}

func TestCLILoginCallbackAcceptsCodeForMatchingState(t *testing.T) {
	resultCh := make(chan loginCallbackResult, 1)
	handler := cliLoginCallbackHandler("local_state", resultCh)
	req := httptest.NewRequest(http.MethodGet, "/callback?state=local_state&code=cli_code", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("callback status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("callback content type = %q, want html", got)
	}
	requireCLITestOutputContains(t, rec.Body.String(), "Browser sign-in complete", "Return to your terminal", "Local callback received")
	select {
	case result := <-resultCh:
		if result.err != nil || result.code != "cli_code" {
			t.Fatalf("callback result = %+v, want cli_code without error", result)
		}
	default:
		t.Fatal("callback did not deliver result")
	}
}

func queryValueFromURLForCLITest(t *testing.T, rawURL, key string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse URL %q: %v", rawURL, err)
	}
	value := parsed.Query().Get(key)
	if value == "" {
		t.Fatalf("URL %q missing query value %q", rawURL, key)
	}
	return value
}

func TestCLILoginCallbackRejectsStateMismatchWithoutCompletingLogin(t *testing.T) {
	resultCh := make(chan loginCallbackResult, 1)
	handler := cliLoginCallbackHandler("local_state", resultCh)
	req := httptest.NewRequest(http.MethodGet, "/callback?state=other&code=cli_code", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("callback status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	select {
	case result := <-resultCh:
		t.Fatalf("state mismatch delivered terminal result = %+v, want login to keep waiting", result)
	default:
	}

	req = httptest.NewRequest(http.MethodGet, "/callback?state=local_state&code=cli_code", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid callback status after mismatch = %d, want %d", rec.Code, http.StatusOK)
	}
	select {
	case result := <-resultCh:
		if result.err != nil || result.code != "cli_code" {
			t.Fatalf("callback result = %+v, want cli_code without error", result)
		}
	default:
		t.Fatal("valid callback did not deliver result")
	}
}

func TestAuthCommandDispatchAndUsageErrors(t *testing.T) {
	if err := authCommand(nil); err == nil || !strings.Contains(err.Error(), "readyci auth") {
		t.Fatalf("authCommand(nil) error = %v, want usage", err)
	}
	if err := authCommand([]string{"bogus"}); err == nil || !strings.Contains(err.Error(), "unknown auth command") {
		t.Fatalf("authCommand bogus error = %v, want unknown command", err)
	}
}

func TestCLILoginURLAndRandomToken(t *testing.T) {
	got := cliLoginURL(" https://api.readyci.test/ ", "http://127.0.0.1:1234/callback?state=a b", "", "challenge")
	if !strings.HasPrefix(got, "https://api.readyci.test/v1/auth/cli/login?") {
		t.Fatalf("login url = %q, want API login endpoint", got)
	}
	if !strings.Contains(got, "redirect_uri=http%3A%2F%2F127.0.0.1%3A1234%2Fcallback%3Fstate%3Da+b") {
		t.Fatalf("login url = %q, want encoded redirect_uri", got)
	}
	got = cliLoginURL("https://user:pass@api.readyci.test/base?token=secret#frag", "http://127.0.0.1:1234/callback?state=local", "acme", "challenge")
	if !strings.HasPrefix(got, "https://api.readyci.test/base/v1/auth/cli/login?") {
		t.Fatalf("login url = %q, want sanitized API login endpoint", got)
	}
	if strings.Contains(got, "user:pass") || strings.Contains(got, "token=secret") || strings.Contains(got, "#frag") {
		t.Fatalf("login url = %q, leaked API base userinfo/query/fragment", got)
	}
	if !strings.Contains(got, "sso=acme") {
		t.Fatalf("login url = %q, missing SSO connection slug", got)
	}
	if normalized := normalizeAPIURL("https://user:pass@api.readyci.test/base?token=secret#frag"); normalized != "https://api.readyci.test/base" {
		t.Fatalf("normalizeAPIURL = %q, want sanitized API URL", normalized)
	}
	token, err := randomAuthToken()
	if err != nil {
		t.Fatalf("random token: %v", err)
	}
	if len(token) < 32 || strings.ContainsAny(token, "+/=") {
		t.Fatalf("random token = %q, want unpadded URL token", token)
	}
}

func TestCLILoginCallbackRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name   string
		method string
		target string
		status int
		err    string
	}{
		{name: "method", method: http.MethodPost, target: "/callback?state=local_state&code=cli_code", status: http.StatusMethodNotAllowed},
		{name: "provider error", method: http.MethodGet, target: "/callback?state=local_state&error=access_denied", status: http.StatusUnauthorized, err: "access_denied"},
		{name: "missing code", method: http.MethodGet, target: "/callback?state=local_state", status: http.StatusBadRequest, err: "missing login code"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resultCh := make(chan loginCallbackResult, 1)
			handler := cliLoginCallbackHandler("local_state", resultCh)
			req := httptest.NewRequest(tc.method, tc.target, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			select {
			case result := <-resultCh:
				if tc.err == "" {
					t.Fatalf("delivered result = %+v, want no callback result", result)
				}
				if result.err == nil || !strings.Contains(result.err.Error(), tc.err) {
					t.Fatalf("callback error = %v, want %q", result.err, tc.err)
				}
			default:
				if tc.err != "" {
					t.Fatal("callback did not deliver expected error")
				}
			}
		})
	}
}

func TestDeliverLoginResultDoesNotBlockWhenChannelFull(t *testing.T) {
	resultCh := make(chan loginCallbackResult, 1)
	deliverLoginResult(resultCh, loginCallbackResult{code: "first"})
	deliverLoginResult(resultCh, loginCallbackResult{code: "second"})
	if got := <-resultCh; got.code != "first" {
		t.Fatalf("delivered result = %+v, want first result preserved", got)
	}
}

func TestCLILoginCallbackSubscriptionHandoff(t *testing.T) {
	for _, billingURL := range []string{"https://readyci.test/app/billing", "javascript:alert(1)", "https://user:pass@readyci.test/app/billing", "https://readyci.test/app/billing#fragment"} {
		t.Run(billingURL, func(t *testing.T) {
			results := make(chan loginCallbackResult, 1)
			handler := cliLoginCallbackHandler("bound-state", results)
			query := url.Values{"state": {"wrong-state"}, "error": {"plan_entitlement_required"}, "billing_url": {billingURL}}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/callback?"+query.Encode(), nil))
			if recorder.Code != http.StatusBadRequest || len(results) != 0 {
				t.Fatal("unbound error completed login")
			}
			query.Set("state", "bound-state")
			recorder = httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/callback?"+query.Encode(), nil))
			if recorder.Code != http.StatusPaymentRequired {
				t.Fatalf("status=%d", recorder.Code)
			}
			select {
			case result := <-results:
				var failure cliError
				if !errors.As(result.err, &failure) || failure.ExitCode != cliExitAuthOrPolicy || failure.Classification != preflightapi.ClassificationBillingRequired || result.code != "" {
					t.Fatalf("result=%+v", result)
				}
				requireCLITestOutputContains(t, failure.Message, "run readyci setup again")
			default:
				t.Fatal("login kept waiting after billing refusal")
			}
			hasLink := strings.Contains(recorder.Body.String(), ">Open Billing</a>")
			if hasLink != (billingURL == "https://readyci.test/app/billing") {
				t.Fatalf("unexpected billing link: %s", recorder.Body.String())
			}
		})
	}
}
