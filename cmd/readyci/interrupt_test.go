package main

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestInterruptContextCancelsOnFirstSignalAndRestoresDefaults(t *testing.T) {
	signals := make(chan os.Signal, 1)
	var restored atomic.Int32
	ctx, state, stop := interruptContextFromSignals(context.Background(), signals, func() { restored.Add(1) })
	defer stop()
	signals <- os.Interrupt
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("interrupt context did not cancel")
	}
	if state.Signal() != os.Interrupt {
		t.Fatalf("signal = %v, want interrupt", state.Signal())
	}
	if restored.Load() != 1 {
		t.Fatalf("signal defaults restored %d times, want once", restored.Load())
	}
	stop()
	if restored.Load() != 1 {
		t.Fatalf("stop restored defaults %d times, want once", restored.Load())
	}
}
