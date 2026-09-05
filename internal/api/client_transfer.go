package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

const (
	streamEventsTransientErrorMaxAttempts = 5
	streamEventsRetryMaxDelay             = 2 * time.Second
)

type eventHandlerError struct {
	err error
}

func (e eventHandlerError) Error() string {
	return e.err.Error()
}

func (e eventHandlerError) Unwrap() error {
	return e.err
}

func (c *Client) UploadBundle(ctx context.Context, uploadURL string, reader io.Reader, size int64) error {
	return c.UploadBundleWithOptions(ctx, uploadURL, reader, size, UploadBundleOptions{})
}

func (c *Client) UploadBundleWithOptions(ctx context.Context, uploadURL string, reader io.Reader, size int64, options UploadBundleOptions) error {
	if uploadURL == "" {
		return errors.New("empty upload URL")
	}
	resolvedURL, sameOrigin, err := c.resolveReturnedURL(uploadURL)
	if err != nil {
		return err
	}
	if c.token != "" && !sameOrigin && !options.AllowCrossOrigin {
		return fmt.Errorf("refusing to send bearer token to cross-origin upload URL %q", resolvedURL)
	}
	reqCtx, cancel := context.WithTimeout(ctx, uploadTimeout)
	defer cancel()

	method := strings.TrimSpace(options.Method)
	if method == "" {
		method = http.MethodPut
	}
	client := &http.Client{Timeout: uploadTimeout}
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			seeker, ok := reader.(io.Seeker)
			if !ok {
				return fmt.Errorf("retrying upload after conditional conflict requires a seekable reader")
			}
			if _, err := seeker.Seek(0, io.SeekStart); err != nil {
				return err
			}
			if err := sleepContext(reqCtx, time.Duration(attempt)*100*time.Millisecond); err != nil {
				return err
			}
		}
		req, err := http.NewRequestWithContext(reqCtx, method, resolvedURL, reader)
		if err != nil {
			return err
		}
		for name, value := range options.Headers {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			req.Header.Set(name, value)
		}
		if req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", syncbundle.BundleMediaType)
		}
		if sameOrigin && !options.OmitAPIAuthentication {
			req.Header.Set("Accept", "application/json")
			c.authorize(req)
		}
		req.ContentLength = size

		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		if options.TreatAlreadyUploadedAsSuccess && resp.StatusCode == http.StatusPreconditionFailed {
			resp.Body.Close()
			return nil
		}
		if options.TreatAlreadyUploadedAsSuccess && resp.StatusCode == http.StatusConflict && attempt < uploadConditionalConflictRetries {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			return ResponseError{
				Operation:  "upload bundle",
				Status:     resp.Status,
				StatusCode: resp.StatusCode,
				Code:       responseErrorCode(respBody),
				Body:       sanitizeAPIErrorText(string(respBody), c.token),
			}
		}
		if options.DecodeJSONResponse != nil {
			if err := decodeUploadJSONResponse(resp.Body, options.DecodeJSONResponse); err != nil {
				resp.Body.Close()
				return fmt.Errorf("decode upload response: %w", err)
			}
		}
		resp.Body.Close()
		return nil
	}
}

func decodeUploadJSONResponse(body io.Reader, out any) error {
	data, err := io.ReadAll(io.LimitReader(body, maxJSONResponseBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxJSONResponseBytes {
		return fmt.Errorf("response exceeded %d bytes", maxJSONResponseBytes)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("response must contain a single JSON value")
	}
	return nil
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) StreamEvents(ctx context.Context, eventsURL string, handle func(preflightapi.RunEvent) error) error {
	if eventsURL == "" {
		return errors.New("empty events URL")
	}
	resolvedURL, sameOrigin, err := c.resolveReturnedURL(eventsURL)
	if err != nil {
		return err
	}
	if c.token != "" && !sameOrigin {
		return fmt.Errorf("refusing to send bearer token to cross-origin events URL %q", resolvedURL)
	}

	var lastEventID int64
	deliveredEvent := false
	resumeEstablished := false
	transientErrorAttempts := 0
	for attempt := 0; ; attempt++ {
		terminal, reconnectable, err := c.streamEventsOnce(ctx, resolvedURL, sameOrigin, lastEventID, func(event preflightapi.RunEvent) error {
			if event.ID > 0 && event.ID <= lastEventID {
				return nil
			}
			if event.ID > lastEventID {
				lastEventID = event.ID
			}
			if err := handle(event); err != nil {
				return err
			}
			deliveredEvent = true
			return nil
		})
		if reconnectable {
			resumeEstablished = true
		}
		if err != nil {
			var handlerErr eventHandlerError
			if errors.As(err, &handlerErr) {
				return handlerErr.err
			}
			retryable := retryableAPIRequestError(ctx, err)
			if transientErrorAttempts < streamEventsTransientErrorMaxAttempts-1 && retryable && (resumeEstablished || !deliveredEvent) {
				if sleepErr := sleepContext(ctx, streamEventsRetryDelay(transientErrorAttempts, err)); sleepErr != nil {
					return sleepErr
				}
				transientErrorAttempts++
				continue
			}
			if retryable {
				return EventStreamError{Err: err}
			}
			return err
		}
		transientErrorAttempts = 0
		if terminal {
			return nil
		}
		if !reconnectable || lastEventID == 0 {
			return nil
		}
		delay := time.Duration(250*(attempt+1)) * time.Millisecond
		if delay > 2*time.Second {
			delay = 2 * time.Second
		}
		if err := sleepContext(ctx, delay); err != nil {
			return err
		}
	}
}

func streamEventsRetryDelay(attempt int, err error) time.Duration {
	var responseErr ResponseError
	if errors.As(err, &responseErr) && responseErr.HasRetryAfter {
		if responseErr.RetryAfter > streamEventsRetryMaxDelay {
			return streamEventsRetryMaxDelay
		}
		return responseErr.RetryAfter
	}
	delay := time.Duration(250*(attempt+1)) * time.Millisecond
	if delay > streamEventsRetryMaxDelay {
		return streamEventsRetryMaxDelay
	}
	return delay
}

func (c *Client) streamEventsOnce(ctx context.Context, resolvedURL string, sameOrigin bool, lastEventID int64, handle func(preflightapi.RunEvent) error) (bool, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resolvedURL, nil)
	if err != nil {
		return false, false, err
	}
	req.Header.Set("Accept", "text/event-stream")
	if lastEventID > 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatInt(lastEventID, 10))
	}
	if sameOrigin {
		c.authorize(req)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		retryAfter, hasRetryAfter := responseRetryAfter(resp)
		return false, false, ResponseError{
			Operation:     "stream events",
			Status:        resp.Status,
			StatusCode:    resp.StatusCode,
			Code:          responseErrorCode(body),
			Body:          strings.TrimSpace(sanitizeAPIErrorText(string(body), c.token)),
			RetryAfter:    retryAfter,
			HasRetryAfter: hasRetryAfter,
		}
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var dataLines []string
	var pendingID int64
	var sawSSEID bool

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(dataLines) == 0 {
				pendingID = 0
				continue
			}
			var event preflightapi.RunEvent
			if err := json.Unmarshal([]byte(strings.Join(dataLines, "\n")), &event); err != nil {
				return false, sawSSEID, err
			}
			eventHadSSEID := pendingID > 0
			if event.ID == 0 && pendingID > 0 {
				event.ID = pendingID
			}
			if eventHadSSEID {
				sawSSEID = true
			}
			pendingID = 0
			dataLines = dataLines[:0]
			if err := handle(event); err != nil {
				return false, sawSSEID, eventHandlerError{err: err}
			}
			if isTerminalRunEvent(event.Type) {
				return true, sawSSEID, nil
			}
			continue
		}
		if strings.HasPrefix(line, "id:") {
			if parsed, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "id:")), 10, 64); err == nil && parsed > 0 {
				pendingID = parsed
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}

	if err := scanner.Err(); err != nil {
		return false, sawSSEID, EventStreamError{Err: err}
	}
	return false, sawSSEID, nil
}

func isTerminalRunEvent(eventType string) bool {
	switch eventType {
	case preflightapi.EventRunFinished, preflightapi.EventRunFailed, preflightapi.EventRunCancelled, preflightapi.EventRunTimedOut:
		return true
	default:
		return false
	}
}
