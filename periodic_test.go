package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunPeriodic_CallsUpdateOnEachTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPeriodic(ctx, 5*time.Millisecond, time.Minute, func(ctx context.Context, tick time.Time) error {
			if calls.Add(1) == 3 {
				cancel()
			}
			return nil
		})
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runPeriodic did not return after its context was canceled")
	}
	if got := calls.Load(); got < 3 {
		t.Errorf("Expected at least 3 update calls, got %d", got)
	}
}

// A panic in one pass must not kill the process or stop later passes.
func TestRunPeriodic_SurvivesPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPeriodic(ctx, 5*time.Millisecond, time.Minute, func(ctx context.Context, tick time.Time) error {
			n := calls.Add(1)
			if n == 1 {
				panic("boom")
			}
			if n == 3 {
				cancel()
			}
			return nil
		})
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runPeriodic did not keep running after a panic")
	}
	if got := calls.Load(); got < 3 {
		t.Errorf("Expected updates to continue after a panic, got %d calls", got)
	}
}

// Canceling the parent context (e.g. on SIGTERM) must reach an in-flight pass
// so it can stop promptly and release its lock.
func TestRunPeriodic_ParentCancelReachesInFlightUpdate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	sawCancel := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPeriodic(ctx, 5*time.Millisecond, time.Minute, func(ctx context.Context, tick time.Time) error {
			close(started)
			<-ctx.Done()
			close(sawCancel)
			return ctx.Err()
		})
	}()

	<-started
	cancel()

	select {
	case <-sawCancel:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight update never saw the cancellation")
	}
	<-done
}

// A hung pass must be bounded by the per-pass timeout so later ticks still run.
func TestRunPeriodic_PassTimeoutBoundsHungUpdate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPeriodic(ctx, 5*time.Millisecond, 20*time.Millisecond, func(ctx context.Context, tick time.Time) error {
			if calls.Add(1) == 2 {
				cancel()
				return nil
			}
			<-ctx.Done() // hangs until the per-pass timeout fires
			return ctx.Err()
		})
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a hung update was not bounded by the pass timeout")
	}
}

func TestWaitOrTimeout_ReturnsTrueWhenDone(t *testing.T) {
	done := make(chan struct{})
	close(done)
	if !waitOrTimeout(done, time.Second) {
		t.Error("Expected true for a closed channel")
	}
}

func TestWaitOrTimeout_ReturnsFalseOnTimeout(t *testing.T) {
	start := time.Now()
	if waitOrTimeout(make(chan struct{}), 20*time.Millisecond) {
		t.Error("Expected false when the channel never closes")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("waitOrTimeout did not honor its timeout (took %s)", elapsed)
	}
}
