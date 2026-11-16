package main

import (
	"context"
	"runtime/debug"
	"time"
)

// runPeriodic calls update every interval until ctx is canceled, then returns.
// Each pass gets its own context, derived from ctx, that also expires after
// passTimeout so a stuck pass cannot stall the loop forever. A panic in a pass
// is logged and the loop carries on with the next tick.
func runPeriodic(ctx context.Context, interval, passTimeout time.Duration, update func(ctx context.Context, tick time.Time) error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			log.Infof("Got periodic timer tick: %s", t)
			runPass(ctx, passTimeout, t, update)
		}
	}
}

func runPass(ctx context.Context, passTimeout time.Duration, tick time.Time, update func(ctx context.Context, tick time.Time) error) {
	passCtx, cancel := context.WithTimeout(ctx, passTimeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("Recovered from panic in periodic update: %v\n%s", r, debug.Stack())
		}
	}()

	if err := update(passCtx, tick); err != nil {
		log.Error(err)
	}
}

// waitOrTimeout waits for done to close, giving up after timeout. It reports
// whether done closed in time.
func waitOrTimeout(done <-chan struct{}, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}
