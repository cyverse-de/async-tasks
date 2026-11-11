package main

import (
	"context"

	"github.com/cyverse-de/async-tasks/database"
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
