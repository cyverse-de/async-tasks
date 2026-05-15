package logutil

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
)

func newTestEntry() (*logrus.Entry, *logrustest.Hook) {
	logger, hook := logrustest.NewNullLogger()
	return logrus.NewEntry(logger), hook
}

func TestLogIfError_NoError(t *testing.T) {
	entry, hook := newTestEntry()
	called := false

	LogIfError(entry, func() error {
		called = true
		return nil
	})

	if !called {
		t.Fatal("fn was not invoked")
	}
	if got := len(hook.AllEntries()); got != 0 {
		t.Fatalf("expected no log entries, got %d", got)
	}
}

func TestLogIfError_LogsError(t *testing.T) {
	entry, hook := newTestEntry()
	wantErr := errors.New("boom")

	LogIfError(entry, func() error { return wantErr })

	entries := hook.AllEntries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(entries))
	}
	if entries[0].Level != logrus.ErrorLevel {
		t.Errorf("expected ErrorLevel, got %s", entries[0].Level)
	}
	if entries[0].Message != wantErr.Error() {
		t.Errorf("expected message %q, got %q", wantErr.Error(), entries[0].Message)
	}
}

func TestLogIfError_IgnoresErrTxDone(t *testing.T) {
	entry, hook := newTestEntry()

	LogIfError(entry, func() error { return sql.ErrTxDone })

	if got := len(hook.AllEntries()); got != 0 {
		t.Fatalf("expected sql.ErrTxDone to be ignored, got %d log entries", got)
	}
}

func TestLogIfError_IgnoredErrorsTable(t *testing.T) {
	for ignored := range errorsToIgnore {
		t.Run(ignored.Error(), func(t *testing.T) {
			entry, hook := newTestEntry()

			LogIfError(entry, func() error { return ignored })

			if got := len(hook.AllEntries()); got != 0 {
				t.Fatalf("expected %v to be ignored, got %d log entries", ignored, got)
			}
		})
	}
}
