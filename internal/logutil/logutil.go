// Package logutil provides small helpers shared across the async-tasks
// service for error handling that should log-and-continue rather than
// propagate.
package logutil

import (
	"errors"

	"github.com/sirupsen/logrus"
)

// LogIfError invokes fn and logs its returned error, if any. It is intended
// to be used with `defer` for cleanup calls such as (*sql.Rows).Close,
// (*sql.DB).Close, and (*sql.Tx).Rollback, where the error is not actionable
// at the caller but is still worth surfacing in logs.
func LogIfError(log *logrus.Entry, fn func() error, errorsToIgnore ...error) {
	if err := fn(); err != nil {
		for _, ignoredError := range errorsToIgnore {
			if errors.Is(err, ignoredError) {
				return
			}
		}
		log.Error(err)
	}
}
