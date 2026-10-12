package api

import (
	"context"
	"net"
	"net/url"
	"testing"
)

func TestRetryableRequestErrorDistinguishesPermanentDNSFailures(t *testing.T) {
	for _, test := range []struct {
		name  string
		err   *net.DNSError
		retry bool
	}{
		{name: "unknown host", err: &net.DNSError{IsNotFound: true}},
		{name: "temporary resolver failure", err: &net.DNSError{IsTemporary: true}, retry: true},
		{name: "resolver timeout", err: &net.DNSError{IsTimeout: true}, retry: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, err := range []error{test.err, &url.Error{Op: "Get", URL: "https://example.invalid", Err: test.err}} {
				if got := RetryableRequestError(context.Background(), err); got != test.retry {
					t.Errorf("%T: retry=%t want %t", err, got, test.retry)
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if RetryableRequestError(ctx, err) {
					t.Error("cancelled DNS request was retryable")
				}
			}
		})
	}
}
