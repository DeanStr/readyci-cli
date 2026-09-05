package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
)

type interruptState struct {
	mu     sync.Mutex
	signal os.Signal
}

func (s *interruptState) set(value os.Signal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.signal == nil {
		s.signal = value
	}
}

func (s *interruptState) Signal() os.Signal {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.signal
}

func interruptContext(parent context.Context) (context.Context, *interruptState, context.CancelFunc) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, shutdownSignals()...)
	return interruptContextFromSignals(parent, signals, func() { signal.Stop(signals) })
}

func interruptContextFromSignals(parent context.Context, signals <-chan os.Signal, restoreSignals func()) (context.Context, *interruptState, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	state := &interruptState{}
	done := make(chan struct{})
	var stopOnce sync.Once
	var restoreOnce sync.Once
	restore := func() {
		restoreOnce.Do(func() {
			if restoreSignals != nil {
				restoreSignals()
			}
		})
	}
	stop := func() {
		stopOnce.Do(func() {
			restore()
			close(done)
			cancel()
		})
	}
	go func() {
		select {
		case received := <-signals:
			state.set(received)
			// Restore the operating system's default behavior immediately. A
			// second signal therefore forces termination while the first signal's
			// best-effort remote cancellation is in flight.
			restore()
			cancel()
		case <-done:
		case <-parent.Done():
			cancel()
		}
	}()
	return ctx, state, stop
}

func interruptionExitCode(signal os.Signal) int {
	if signal != nil && signal.String() == "terminated" {
		return 143
	}
	return 130
}
