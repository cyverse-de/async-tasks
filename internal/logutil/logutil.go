// Package logutil provides small helpers shared across the async-tasks
// service for error handling that should log-and-continue rather than
// propagate.
package logutil

import (
	"database/sql"

	"github.com/sirupsen/logrus"
)

// Errors that we really want to ignore because they can occur as a result of
// normal operation.
var errorsToIgnore = map[error]bool{
	sql.ErrTxDone: true,
}

// LogIfError invokes fn and logs its returned error, if any. It is intended
// to be used with `defer` for cleanup calls such as (*sql.Rows).Close,
// (*sql.DB).Close, and (*sql.Tx).Rollback, where the error is not actionable
// at the caller but is still worth surfacing in logs.
func LogIfError(log *logrus.Entry, fn func() error) {
	if err := fn(); err != nil {
		if !errorsToIgnore[err] {
			log.Error(err)
		}
	}
}
