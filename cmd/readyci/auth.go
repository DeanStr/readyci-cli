package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

const credentialsFileVersion = 1

const cliLoginCompleteHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Ready CI login complete</title>
  <style>
    :root {
      color-scheme: dark;
      --readyci-dark: #050a14;
      --readyci-panel: #0f141c;
      --readyci-blue: #0066ff;
      --readyci-blue-soft: #93c5fd;
      --readyci-text: #ffffff;
      --readyci-muted: rgba(255, 255, 255, 0.74);
      --readyci-faint: rgba(255, 255, 255, 0.56);
      --readyci-border: rgba(255, 255, 255, 0.12);
    }

    * {
      box-sizing: border-box;
    }

    body {
      min-width: 320px;
      min-height: 100vh;
      margin: 0;
      display: grid;
      place-items: center;
      padding: clamp(1rem, 4vw, 2rem);
      color: var(--readyci-text);
      background:
        radial-gradient(ellipse 58% 42% at 74% 12%, rgba(0, 102, 255, 0.22), transparent 68%),
        linear-gradient(180deg, #0a101c 0%, var(--readyci-dark) 100%);
      font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      line-height: 1.5;
      -webkit-font-smoothing: antialiased;
      text-rendering: optimizeLegibility;
      overflow-x: clip;
    }

    @supports (height: 100svh) {
      body {
        min-height: 100svh;
      }
    }

    body::before {
      content: "";
      position: fixed;
      inset: auto -8rem -15rem 34%;
      height: 28rem;
      background:
        linear-gradient(rgba(255, 255, 255, 0.045) 1px, transparent 1px),
        linear-gradient(90deg, rgba(255, 255, 255, 0.045) 1px, transparent 1px);
      background-size: 2.75rem 2.75rem;
      -webkit-mask-image: linear-gradient(90deg, transparent, #000 28%, #000 72%, transparent);
      mask-image: linear-gradient(90deg, transparent, #000 28%, #000 72%, transparent);
      transform: rotate(-8deg);
      pointer-events: none;
    }

    main {
      position: relative;
      width: min(35rem, 100%);
      padding: clamp(1.5rem, 4vw, 2.25rem);
      border: 1px solid var(--readyci-border);
      border-radius: 0.75rem;
      background:
        linear-gradient(180deg, rgba(18, 24, 32, 0.94), rgba(10, 14, 20, 0.96)),
        var(--readyci-panel);
      box-shadow:
        inset 0 1px 0 rgba(255, 255, 255, 0.06),
        0 28px 80px rgba(0, 0, 0, 0.38),
        0 0 0 1px rgba(255, 255, 255, 0.025);
    }

    .brand {
      display: flex;
      align-items: center;
      gap: 0.8rem;
      padding-bottom: 1.5rem;
      border-bottom: 1px solid rgba(255, 255, 255, 0.08);
    }

    .mark {
      width: 2.25rem;
      height: 2.25rem;
      display: grid;
      place-items: center;
      border: 1.5px solid rgba(226, 232, 240, 0.92);
      border-radius: 0.18rem;
      color: #e6edf3;
      font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      font-size: 1.1rem;
      font-weight: 700;
      line-height: 1;
      box-shadow: inset 0 -0.2rem 0 rgba(0, 102, 255, 0.38);
    }

    .brand strong {
      display: block;
      font-size: 1rem;
      font-weight: 700;
    }

    .brand span span {
      display: block;
      margin-top: 0.15rem;
      color: var(--readyci-faint);
      font-size: 0.75rem;
    }

    h1 {
      margin: 1.5rem 0 0;
      font-size: clamp(1.55rem, 6vw, 1.95rem);
      line-height: 1.15;
      font-weight: 700;
      letter-spacing: 0;
    }

    p {
      margin: 0.75rem 0 0;
      color: var(--readyci-muted);
      font-size: 0.96rem;
    }

    .status {
      margin-top: 1.5rem;
      padding-top: 1.25rem;
      border-top: 1px solid rgba(255, 255, 255, 0.08);
      color: var(--readyci-faint);
      font-size: 0.88rem;
    }

    .status strong {
      color: var(--readyci-blue-soft);
      font-weight: 700;
    }
  </style>
</head>
<body>
  <main>
    <div class="brand" aria-label="Ready CI">
      <div class="mark" aria-hidden="true">&gt;_</div>
      <span>
        <strong>Ready CI</strong>
        <span>Cloud checks. Local flow.</span>
      </span>
    </div>
    <h1>Browser sign-in complete</h1>
    <p>Return to your terminal to finish saving your Ready CI CLI credentials.</p>
    <p class="status"><strong>Local callback received.</strong> This browser window can be closed.</p>
  </main>
</body>
</html>`

type credentialsFile struct {
	Version     int                `json:"version"`
	Credentials []storedCredential `json:"credentials"`
}

type storedCredential struct {
	APIURL    string     `json:"api_url"`
	Token     string     `json:"token"`
	TokenID   string     `json:"token_id,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type loginCallbackResult struct {
	code string
	err  error
}

func loginCommand(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL := fs.String("api-url", envDefault("READYCI_API_URL", defaultAPIURL()), "Ready CI API URL")
	ssoSlug := fs.String("sso", "", "enterprise SSO connection slug")
	noBrowser := fs.Bool("no-browser", false, "print the login URL instead of opening a browser")
	timeout := fs.Duration("timeout", 5*time.Minute, "maximum time to wait for browser login")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci login [--api-url URL] [--sso SLUG] [--no-browser] [--timeout DURATION]", nil)
	}

	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	localState, err := randomAuthToken()
	if err != nil {
		_ = listener.Close()
		return err
	}
	callbackURL := (&url.URL{
		Scheme: "http",
		Host:   listener.Addr().String(),
		Path:   "/callback",
		RawQuery: url.Values{
			"state": []string{localState},
		}.Encode(),
	}).String()
	loginURL := cliLoginURL(*apiURL, callbackURL, *ssoSlug)
	resultCh := make(chan loginCallbackResult, 1)
	server := &http.Server{
		Handler:           cliLoginCallbackHandler(localState, resultCh),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			deliverLoginResult(resultCh, loginCallbackResult{err: err})
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	fmt.Printf("Login URL:\n%s\n", loginURL)
	if *noBrowser {
		fmt.Println("Open the login URL in a browser that can reach this machine's 127.0.0.1 callback.")
	} else if err := openBrowser(loginURL); err != nil {
		fmt.Println("Could not open a browser automatically; open the login URL manually.")
	} else {
		fmt.Println("Opened browser for Ready CI login. You can also copy the login URL into another browser on this workstation.")
	}
	fmt.Println("Waiting for login...")

	var result loginCallbackResult
	select {
	case result = <-resultCh:
	case <-ctx.Done():
		return ctx.Err()
	}
	if result.err != nil {
		return result.err
	}

	resp, err := api.NewClient(*apiURL).ExchangeCLIAuthCode(ctx, result.code, "Ready CI CLI")
	if err != nil {
		return err
	}
	credential := storedCredential{
		APIURL:    normalizeAPIURL(*apiURL),
		Token:     resp.Secret,
		TokenID:   resp.Token.ID,
		ExpiresAt: resp.Token.ExpiresAt,
		CreatedAt: time.Now().UTC(),
	}
	if err := saveStoredCredential(credential); err != nil {
		return err
	}
	fmt.Printf("Logged in to %s\n", credential.APIURL)
	fmt.Printf("token_id: %s\n", credential.TokenID)
	if credential.ExpiresAt != nil {
		fmt.Printf("expires_at: %s\n", credential.ExpiresAt.Format(time.RFC3339))
	}
	return nil
}

func authCommand(args []string) error {
	if len(args) == 0 {
		printUsage()
		return cliConfigError("usage: readyci auth <status|logout>", nil)
	}
	switch args[0] {
	case "status":
		return authStatusCommand(args[1:])
	case "logout":
		return logoutCommand(args[1:])
	default:
		printUsage()
		return cliConfigError(fmt.Sprintf("unknown auth command %q", args[0]), nil)
	}
}

func authStatusCommand(args []string) error {
	fs := flag.NewFlagSet("auth status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL := fs.String("api-url", envDefault("READYCI_API_URL", defaultAPIURL()), "Ready CI API URL")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci auth status [--api-url URL]", nil)
	}

	token := strings.TrimSpace(envDefault("READYCI_API_TOKEN", ""))
	source := "environment"
	var credential storedCredential
	if token == "" {
		var found bool
		var err error
		credential, found, err = loadStoredCredential(*apiURL)
		if err != nil {
			return err
		}
		if !found {
			fmt.Printf("Not logged in to %s\n", normalizeAPIURL(*apiURL))
			return nil
		}
		token = credential.Token
		source = "credentials"
	}

	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	usage, err := api.NewClient(*apiURL, api.WithToken(token)).AccountUsage(ctx)
	if err != nil {
		return fmt.Errorf("auth check failed: %w", err)
	}
	fmt.Printf("api_url: %s\n", normalizeAPIURL(*apiURL))
	fmt.Printf("source: %s\n", source)
	if credential.TokenID != "" {
		fmt.Printf("token_id: %s\n", credential.TokenID)
	}
	if credential.ExpiresAt != nil {
		fmt.Printf("expires_at: %s\n", credential.ExpiresAt.Format(time.RFC3339))
	}
	fmt.Printf("team_id: %s\n", usage.TeamID)
	fmt.Printf("plan: %s\n", usage.Plan.ID)
	return nil
}

func logoutCommand(args []string) error {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL := fs.String("api-url", envDefault("READYCI_API_URL", defaultAPIURL()), "Ready CI API URL")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci auth logout [--api-url URL]", nil)
	}
	credential, found, err := loadStoredCredential(*apiURL)
	if err != nil {
		return err
	}
	if found {
		ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
		defer stop()
		if _, err := api.NewClient(*apiURL, api.WithToken(credential.Token)).RevokeCurrentCLIToken(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not revoke remote token: %v\n", err)
		}
	}
	removed, err := removeStoredCredential(*apiURL)
	if err != nil {
		return err
	}
	if removed {
		fmt.Printf("Logged out of %s\n", normalizeAPIURL(*apiURL))
	} else {
		fmt.Printf("No stored login for %s\n", normalizeAPIURL(*apiURL))
	}
	if strings.TrimSpace(envDefault("READYCI_API_TOKEN", "")) != "" {
		fmt.Println("READYCI_API_TOKEN is still set and will continue to authenticate commands.")
	}
	return nil
}

func effectiveAPIToken(apiURL, token string) (string, error) {
	token = strings.TrimSpace(token)
	if token != "" {
		return token, nil
	}
	credential, found, err := loadStoredCredential(apiURL)
	if err != nil {
		return "", err
	}
	if !found {
		return "", nil
	}
	return credential.Token, nil
}

func cliLoginURL(apiURL, callbackURL, ssoSlug string) string {
	base := normalizeAPIURL(apiURL)
	query := url.Values{"redirect_uri": []string{callbackURL}}
	if ssoSlug = strings.TrimSpace(ssoSlug); ssoSlug != "" {
		query.Set("sso", ssoSlug)
	}
	return base + "/v1/auth/cli/login?" + query.Encode()
}

func cliLoginCallbackHandler(localState string, resultCh chan<- loginCallbackResult) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if got := strings.TrimSpace(r.URL.Query().Get("state")); got != localState {
			http.Error(w, "invalid state", http.StatusBadRequest)
			return
		}
		if authError := strings.TrimSpace(r.URL.Query().Get("error")); authError != "" {
			if authError == "plan_entitlement_required" {
				message := "A paid subscription is required to create CLI credentials. Open Billing, choose a plan, then run readyci setup again."
				billingURL := r.URL.Query().Get("billing_url")
				billing, err := url.Parse(billingURL)
				if err != nil || billing.Scheme != "https" || billing.Host == "" || billing.User != nil || billing.Fragment != "" || billing.Path != "/app/billing" {
					billingURL = ""
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusPaymentRequired)
				body := strings.Replace(cliLoginCompleteHTML, "Browser sign-in complete", "Choose a plan to continue", 1)
				recovery := html.EscapeString(message)
				if billingURL != "" {
					recovery += ` <a href="` + html.EscapeString(billingURL) + `">Open Billing</a>`
					message += " Billing: " + billingURL
				}
				body = strings.Replace(body, "Return to your terminal to finish saving your Ready CI CLI credentials.", recovery, 1)
				body = strings.Replace(body, "This browser window can be closed.", "Your browser is signed in; CLI credentials have not been issued.", 1)
				_, _ = fmt.Fprint(w, body)
				deliverLoginResult(resultCh, loginCallbackResult{err: cliError{Code: authError, ExitCode: cliExitAuthOrPolicy, Classification: preflightapi.ClassificationBillingRequired, Message: message}})
				return
			}
			http.Error(w, "login failed", http.StatusUnauthorized)
			deliverLoginResult(resultCh, loginCallbackResult{err: cliError{Code: "auth_forbidden", ExitCode: cliExitAuthOrPolicy, Classification: preflightapi.ClassificationAuthForbidden, Message: fmt.Sprintf("login failed: %s; run readyci login again", authError)}})
			return
		}
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		if code == "" {
			http.Error(w, "missing login code", http.StatusBadRequest)
			deliverLoginResult(resultCh, loginCallbackResult{err: errors.New("missing login code")})
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, cliLoginCompleteHTML)
		deliverLoginResult(resultCh, loginCallbackResult{code: code})
	})
	return mux
}

func deliverLoginResult(resultCh chan<- loginCallbackResult, result loginCallbackResult) {
	select {
	case resultCh <- result:
	default:
	}
}

func openBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	return cmd.Start()
}
