package main

import (
	"context"
	"runtime/debug"
	"sync"
	"time"

	"github.com/cyverse-de/async-tasks/database"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
)

type BehaviorProcessor func(ctx context.Context, log *logrus.Entry, tickerTime time.Time, db *database.DBConnection) error

type AsyncTasksUpdater struct {
	db                 *database.DBConnection
	behaviorProcessors map[string]BehaviorProcessor
}

func NewAsyncTasksUpdater(db *database.DBConnection) *AsyncTasksUpdater {
	processors := make(map[string]BehaviorProcessor)

	updater := &AsyncTasksUpdater{
		db:                 db,
		behaviorProcessors: processors,
	}

	return updater
}

// lockName is the advisory lock that ensures only one instance of the service
// runs a given behavior processor at a time.
func lockName(behaviorType string) string {
	return "async-tasks:behavior:" + behaviorType
}

// runProcessor runs a single behavior processor, converting a panic into a
// logged error so that one misbehaving processor cannot take down the service.
func runProcessor(ctx context.Context, processorLog *logrus.Entry, behaviorType string, processor BehaviorProcessor, tickerTime time.Time, db *database.DBConnection) {
	defer func() {
		if r := recover(); r != nil {
			processorLog.Errorf("Recovered from panic in behavior processor %s: %v\n%s", behaviorType, r, debug.Stack())
		}
	}()

	processorLog.Infof("Processing behavior type %s for time %s", behaviorType, tickerTime)
	if err := processor(ctx, processorLog, tickerTime, db); err != nil {
		processorLog.Error(err)
	}
	processorLog.Infof("Done processing behavior type %s for time %s", behaviorType, tickerTime)
}

func (u *AsyncTasksUpdater) DoPeriodicUpdate(ctx context.Context, tickerTime time.Time, db *database.DBConnection) error {
	log.Infof("Running update with time %s", tickerTime)
	ctx, span := otel.Tracer(otelName).Start(ctx, "DoPeriodicUpdate")
	defer span.End()

	var wg sync.WaitGroup

	for behaviorType, processor := range u.behaviorProcessors {
		wg.Add(1)
		go func(ctx context.Context, behaviorType string, processor BehaviorProcessor) {
			defer wg.Done()
			ctx, span := otel.Tracer(otelName).Start(ctx, "behavior processor "+behaviorType)
			defer span.End()
			processorLog := log.WithFields(logrus.Fields{
				"behavior_type": behaviorType,
			})

			// Only one instance may run a given processor at a time. The lock is
			// tied to a database session, so it is released automatically if this
			// process dies.
			release, acquired, err := db.TryAdvisoryLock(ctx, lockName(behaviorType))
			if err != nil {
				processorLog.Error(errors.Wrap(err, "Failed to take the behavior processor lock"))
				return
			}
			if !acquired {
				processorLog.Info("Another instance is already processing this behavior type; skipping")
				return
			}
			defer release()

			runProcessor(ctx, processorLog, behaviorType, processor, tickerTime, db)
		}(ctx, behaviorType, processor)
	}
	wg.Wait()
	log.Infof("Done running update with time %s", tickerTime)
	return nil
}

func (u *AsyncTasksUpdater) AddBehavior(behaviorType string, processor BehaviorProcessor) {
	u.behaviorProcessors[behaviorType] = processor
}
