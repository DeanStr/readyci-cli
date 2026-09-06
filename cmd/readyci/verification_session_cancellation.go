package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func cancelVerificationSessionRejectedAfterCreation(
	client *api.Client,
	session preflightapi.VerificationSession,
	rejection error,
) (preflightapi.VerificationSession, error) {
	if client == nil || strings.TrimSpace(session.SessionID) == "" || session.State == preflightapi.VerificationStateTerminal {
		return session, rejection
	}
	cancelCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cancelled, err := client.CancelVerificationSession(cancelCtx, session.SessionID)
	cancel()
	if err == nil {
		return cancelled, rejection
	}
	var typed cliError
	if errors.As(rejection, &typed) {
		typed.Message += fmt.Sprintf("; automatic cancellation of unusable session %s failed: %v", session.SessionID, err)
		typed.Cause = errors.Join(rejection, err)
		return session, typed
	}
	return session, fmt.Errorf("%w; automatic cancellation of unusable session %s failed: %v", rejection, session.SessionID, err)
}

func reconcileAndCancelInterruptedVerificationSession(
	apiURL string,
	client *api.Client,
	request *preflightapi.CreateVerificationSessionRequest,
	session preflightapi.VerificationSession,
	received os.Signal,
	priorErr error,
) (preflightapi.VerificationSession, cliInterruptedError) {
	interrupted := cliInterruptedError{Signal: received, ResourceID: session.SessionID, APIURL: apiURL}
	if session.SessionID == "" && client != nil && request != nil {
		reconcileCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		reconciled, err := client.CreateVerificationSession(reconcileCtx, *request)
		cancel()
		if err != nil {
			interrupted.CancellationError = errors.Join(priorErr, fmt.Errorf("reconcile interrupted verification-session creation: %w", err))
			return session, interrupted
		}
		session = reconciled.Session
		interrupted.ResourceID = session.SessionID
	}
	if session.SessionID == "" {
		return session, interrupted
	}
	cancelCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cancelled, err := client.CancelVerificationSession(cancelCtx, session.SessionID)
	cancel()
	acknowledged := err == nil
	interrupted.CancellationAcknowledged = &acknowledged
	interrupted.CancellationError = err
	if err == nil {
		session = cancelled
	}
	session.ClientInterruption = &preflightapi.VerificationClientInterruption{
		Signal:                   received.String(),
		ResourceID:               session.SessionID,
		CancellationAcknowledged: acknowledged,
		StatusCommand:            verificationSessionRecoveryCommand(apiURL, "get", session.SessionID),
		CancelCommand:            verificationSessionRecoveryCommand(apiURL, "cancel", session.SessionID),
	}
	return session, interrupted
}

func verificationSessionRecoveryCommand(apiURL, action, sessionID string) string {
	return "readyci sessions " + action + apiURLFlag(apiURL) + " " + shellQuoteArg(sessionID)
}
