package main

import (
	"context"
	"database/sql"

	"github.com/cyverse-de/async-tasks/database"
	"github.com/cyverse-de/async-tasks/internal/logutil"
	"github.com/pkg/errors"
)

// completeLegacyProcessorTasks marks the behaviorprocessor-* tasks that earlier
// versions used as locks as complete. Failures are logged; it runs again on the
// next startup.
func completeLegacyProcessorTasks(ctx context.Context, db *database.DBConnection) {
	n, err := db.CompleteBehaviorProcessorTasks(ctx)
	if err != nil {
		log.Error(errors.Wrap(err, "failed to complete legacy behavior processor tasks"))
		return
	}
	log.Infof("Marked %d legacy behavior processor tasks as complete", n)
}

// completeLegacyStatusChangeTimeoutTasks adds "complete": true to
// statuschangetimeout rules that don't specify it, then completes tasks that
// were already marked as stalled under those rules. Failures are logged; it
// runs again on the next startup.
func completeLegacyStatusChangeTimeoutTasks(ctx context.Context, db *database.DBConnection) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		log.Error(errors.Wrap(err, "failed to start transaction for legacy statuschangetimeout tasks"))
		return
	}
	defer logutil.LogIfError(log, tx.Rollback, sql.ErrTxDone)

	updated, err := tx.AddMissingCompleteFlags(ctx)
	if err != nil {
		log.Error(errors.Wrap(err, "failed to add missing complete flags to statuschangetimeout behaviors"))
		return
	}
	completed, err := tx.CompleteStalledTasks(ctx)
	if err != nil {
		log.Error(errors.Wrap(err, "failed to complete stalled tasks"))
		return
	}
	if err := tx.Commit(); err != nil {
		log.Error(errors.Wrap(err, "failed to commit legacy statuschangetimeout task updates"))
		return
	}
	log.Infof("Added complete flags to %d statuschangetimeout behaviors and completed %d stalled tasks", updated, completed)
}
