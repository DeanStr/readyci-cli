package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const requestTimeout = 30 * time.Second
const uploadTimeout = 5 * time.Minute
const uploadConditionalConflictRetries = 3
const maxJSONResponseBytes = 16 << 20

// A documented tail of 200 max-size log chunks can exceed the default JSON
// limit after base64 encoding, so only run-event endpoints get the larger cap.
const maxRunEventsJSONResponseBytes = 32 << 20
const runEventsPageLimit = 32

type Client struct {
	baseURL      string
	hostHeader   string
	forwardedFor string
	httpClient   *http.Client
	token        string
}

type UploadBundleOptions struct {
	Method                        string
	Headers                       map[string]string
	AllowCrossOrigin              bool
	OmitAPIAuthentication         bool
	TreatAlreadyUploadedAsSuccess bool
	DecodeJSONResponse            any
}

type ResponseError struct {
	Operation     string
	Status        string
	StatusCode    int
	Code          string
	Body          string
	RetryAfter    time.Duration
	HasRetryAfter bool
}

func responseErrorCode(body []byte) string {
	var payload struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	return strings.TrimSpace(payload.Code)
}

func (e ResponseError) Error() string {
	return sanitizeAPIErrorText(fmt.Sprintf("%s failed: %s: %s", e.Operation, e.Status, strings.TrimSpace(e.Body)), "")
}

var (
	apiBearerPattern      = regexp.MustCompile(`(?i)(authorization\s*:\s*bearer\s+|bearer\s+)[^\s,;]+`)
	apiURLUserPattern     = regexp.MustCompile(`(https?://)[^/@\s]+@`)
	apiSecretQueryPattern = regexp.MustCompile(`(?i)([?&](?:token|api[_-]?key|access[_-]?token|secret|signature|x-amz-signature|x-amz-credential|x-amz-security-token)=)[^&#\s]+`)
)

func sanitizeAPIErrorText(value, exactSecret string) string {
	exactSecret = strings.TrimSpace(exactSecret)
	if len(exactSecret) >= 4 {
		value = strings.ReplaceAll(value, exactSecret, "[REDACTED]")
	}
	value = apiBearerPattern.ReplaceAllString(value, "${1}[REDACTED]")
	value = apiURLUserPattern.ReplaceAllString(value, "${1}[REDACTED]@")
	return apiSecretQueryPattern.ReplaceAllString(value, "${1}[REDACTED]")
}

type EventStreamError struct {
	Err error
}

func (e EventStreamError) Error() string {
	if e.Err == nil {
		return "stream events interrupted"
	}
	return "stream events interrupted: " + e.Err.Error()
}

func (e EventStreamError) Unwrap() error {
	return e.Err
}

type Option func(*Client)

func WithToken(token string) Option {
	return func(client *Client) {
		client.token = strings.TrimSpace(token)
	}
}

func NewClient(baseURL string, opts ...Option) *Client {
	client := &Client{
		baseURL:      normalizeClientBaseURL(baseURL),
		hostHeader:   strings.TrimSpace(os.Getenv("READYCI_API_HOST_HEADER")),
		forwardedFor: strings.TrimSpace(os.Getenv("READYCI_API_FORWARDED_FOR")),
		httpClient:   http.DefaultClient,
	}
	for _, opt := range opts {
		opt(client)
	}
	return client
}

func normalizeClientBaseURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(value, "/")
	}
	parsed.User = nil
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

func (c *Client) doJSONPayload(ctx context.Context, method, path string, in any, out any) error {
	return c.doJSONPayloadWithTimeout(ctx, method, path, in, out, requestTimeout)
}

func (c *Client) doJSONPayloadWithTimeout(ctx context.Context, method, path string, in any, out any, timeout time.Duration) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}

	return c.doJSONWithResponseLimitAndTimeout(ctx, method, path, bytes.NewReader(body), out, maxJSONResponseBytes, timeout)
}

func (c *Client) doJSON(ctx context.Context, method, path string, body io.Reader, out any) error {
	return c.doJSONWithResponseLimit(ctx, method, path, body, out, maxJSONResponseBytes)
}

func getJSON[T any](ctx context.Context, c *Client, path string) (T, error) {
	return requestJSON[T](ctx, c, http.MethodGet, path, nil)
}

func deleteJSON[T any](ctx context.Context, c *Client, path string) (T, error) {
	return requestJSON[T](ctx, c, http.MethodDelete, path, nil)
}

func postJSONResponse[T any](ctx context.Context, c *Client, path string, in any) (T, error) {
	return requestJSONPayload[T](ctx, c, http.MethodPost, path, in)
}

func putJSONResponse[T any](ctx context.Context, c *Client, path string, in any) (T, error) {
	return requestJSONPayload[T](ctx, c, http.MethodPut, path, in)
}

func deleteJSONResponse[T any](ctx context.Context, c *Client, path string, in any) (T, error) {
	return requestJSONPayload[T](ctx, c, http.MethodDelete, path, in)
}

func requestJSONPayload[T any](ctx context.Context, c *Client, method, path string, in any) (T, error) {
	var out T
	err := c.doJSONPayload(ctx, method, path, in, &out)
	return out, err
}

func requestJSONPayloadWithTimeout[T any](ctx context.Context, c *Client, method, path string, in any, timeout time.Duration) (T, error) {
	var out T
	err := c.doJSONPayloadWithTimeout(ctx, method, path, in, &out, timeout)
	return out, err
}

func requestJSON[T any](ctx context.Context, c *Client, method, path string, body io.Reader) (T, error) {
	return requestJSONWithLimit[T](ctx, c, method, path, body, maxJSONResponseBytes)
}

func requestJSONWithLimit[T any](ctx context.Context, c *Client, method, path string, body io.Reader, maxResponseBytes int) (T, error) {
	var out T
	err := c.doJSONWithResponseLimit(ctx, method, path, body, &out, maxResponseBytes)
	return out, err
}

func (c *Client) doJSONWithResponseLimit(ctx context.Context, method, path string, body io.Reader, out any, maxResponseBytes int) error {
	return c.doJSONWithResponseLimitAndTimeout(ctx, method, path, body, out, maxResponseBytes, requestTimeout)
}

func (c *Client) doJSONWithResponseLimitAndTimeout(ctx context.Context, method, path string, body io.Reader, out any, maxResponseBytes int, timeout time.Duration) error {
	if timeout <= 0 {
		return errors.New("API request timeout must be positive")
	}
	hasBody := body != nil
	if body == nil {
		body = http.NoBody
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	c.authorize(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		retryAfter, hasRetryAfter := responseRetryAfter(resp)
		return ResponseError{
			Operation:     method + " " + path,
			Status:        resp.Status,
			StatusCode:    resp.StatusCode,
			Code:          responseErrorCode(respBody),
			Body:          sanitizeAPIErrorText(string(respBody), c.token),
			RetryAfter:    retryAfter,
			HasRetryAfter: hasRetryAfter,
		}
	}

	if out == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxResponseBytes)+1))
	if err != nil {
		return err
	}
	if len(data) > maxResponseBytes {
		return fmt.Errorf("%s %s response exceeded %d bytes", method, path, maxResponseBytes)
	}
	return json.Unmarshal(data, out)
}

func (c *Client) downloadResponse(ctx context.Context, method, path string, writer io.Writer) error {
	reqCtx, cancel := context.WithTimeout(ctx, uploadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, method, c.baseURL+path, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	c.authorize(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		retryAfter, hasRetryAfter := responseRetryAfter(resp)
		return ResponseError{
			Operation:     method + " " + path,
			Status:        resp.Status,
			StatusCode:    resp.StatusCode,
			Code:          responseErrorCode(respBody),
			Body:          sanitizeAPIErrorText(string(respBody), c.token),
			RetryAfter:    retryAfter,
			HasRetryAfter: hasRetryAfter,
		}
	}
	_, err = io.Copy(writer, resp.Body)
	return err
}

func responseRetryAfter(resp *http.Response) (time.Duration, bool) {
	if resp == nil {
		return 0, false
	}
	return parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	delay := when.Sub(now)
	if delay < 0 {
		delay = 0
	}
	return delay, true
}

func retryableAPIRequestError(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return false
	}
	var responseErr ResponseError
	if errors.As(err, &responseErr) {
		switch responseErr.StatusCode {
		case http.StatusRequestTimeout,
			http.StatusTooEarly,
			http.StatusInternalServerError,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout:
			return true
		case http.StatusTooManyRequests:
			return responseErr.HasRetryAfter
		default:
			return false
		}
	}
	// A nonexistent hostname requires a configuration/DNS correction, not
	// transport recovery. Preserve retries for DNS timeouts and temporary errors.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return false
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if errors.Is(urlErr.Err, context.Canceled) {
			return false
		}
		if errors.Is(urlErr.Err, context.DeadlineExceeded) {
			return true
		}
		var netErr net.Error
		return errors.As(urlErr.Err, &netErr)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// RetryableRequestError reports whether err is a transient API transport or response failure.
func RetryableRequestError(ctx context.Context, err error) bool {
	return retryableAPIRequestError(ctx, err)
}

func apiRequestRetryDelay(attempt int, err error, maxDelay time.Duration) time.Duration {
	var responseErr ResponseError
	if errors.As(err, &responseErr) && responseErr.HasRetryAfter {
		if responseErr.RetryAfter > maxDelay {
			return maxDelay
		}
		return responseErr.RetryAfter
	}
	switch attempt {
	case 0:
		return 5 * time.Second
	case 1:
		return 15 * time.Second
	case 2:
		return 40 * time.Second
	default:
		return maxDelay
	}
}

// RequestRetryDelay returns the shared API retry delay for a zero-based retry attempt.
func RequestRetryDelay(attempt int, err error, maxDelay time.Duration) time.Duration {
	return apiRequestRetryDelay(attempt, err, maxDelay)
}

func (c *Client) authorize(req *http.Request) {
	if c.hostHeader != "" {
		req.Host = c.hostHeader
	}
	if c.forwardedFor != "" {
		req.Header.Set("X-Forwarded-For", c.forwardedFor)
	}
	if c.token == "" {
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
}

func envDefault(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func (c *Client) resolveReturnedURL(rawURL string) (string, bool, error) {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", false, fmt.Errorf("invalid API URL %q: %w", c.baseURL, err)
	}
	if base.Scheme == "" || base.Host == "" {
		return "", false, fmt.Errorf("invalid API URL %q", c.baseURL)
	}
	if base.User != nil {
		return "", false, fmt.Errorf("invalid API URL %q: userinfo is not allowed", c.baseURL)
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", false, err
	}
	var resolved *url.URL
	if parsed.IsAbs() || parsed.Host != "" {
		resolved = parsed
	} else {
		resolved = resolveAgainstAPIBase(base, parsed)
	}
	if returnedURLHasDotSegment(resolved) {
		return "", false, fmt.Errorf("returned URL path must not contain dot segments")
	}
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return "", false, fmt.Errorf("unsupported returned URL scheme %q", resolved.Scheme)
	}
	if resolved.User != nil {
		return "", false, fmt.Errorf("returned URL userinfo is not allowed")
	}
	return resolved.String(), sameOrigin(base, resolved), nil
}

func resolveAgainstAPIBase(base, ref *url.URL) *url.URL {
	resolved := *base
	basePath := strings.TrimRight(base.Path, "/")
	switch {
	case ref.Path == "":
		resolved.Path = base.Path
	case strings.HasPrefix(ref.Path, "/"):
		resolved.Path = basePath + ref.Path
	default:
		resolved.Path = basePath + "/" + ref.Path
	}
	resolved.RawPath = ""
	resolved.ForceQuery = ref.ForceQuery
	resolved.RawQuery = ref.RawQuery
	resolved.Fragment = ref.Fragment
	return &resolved
}

func returnedURLHasDotSegment(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	for _, pathValue := range []string{parsed.Path, parsed.EscapedPath()} {
		for _, segment := range strings.Split(pathValue, "/") {
			segment = strings.ToLower(segment)
			if segment == "." || segment == ".." || segment == "%2e" || segment == "%2e%2e" {
				return true
			}
		}
	}
	return false
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(originHost(a), originHost(b))
}

func originHost(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	} else if parsed, err := strconv.Atoi(port); err == nil && parsed >= 0 {
		port = strconv.Itoa(parsed)
	}
	if port == "" {
		return host
	}
	return net.JoinHostPort(host, port)
}
