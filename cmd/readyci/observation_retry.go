package main

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
)

const (
	observationReadMaxAttempts   = 5
	observationReadMaxRetryDelay = 60 * time.Second
)

// Retry only read requests. Processing output and mutating remote jobs are never replayed.
func retryObservationRead(ctx context.Context, fn func() error) error {
	var err error
	for attempt := 0; attempt < observationReadMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err = fn()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A connection can end during headers/body without returning a net.Error.
		// JSON syntax errors are distinct and must still fail immediately.
		retryable := api.RetryableRequestError(ctx, err) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
		if attempt == observationReadMaxAttempts-1 || !retryable {
			return err
		}
		if sleepErr := sleepObservationRetry(ctx, api.RequestRetryDelay(attempt, err, observationReadMaxRetryDelay)); sleepErr != nil {
			return sleepErr
		}
	}
	return err
}

func sleepObservationRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
