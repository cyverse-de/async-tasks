package logutil

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
)

func newTestEntry() (*logrus.Entry, *logrustest.Hook) {
	logger, hook := logrustest.NewNullLogger()
	return logrus.NewEntry(logger), hook
}

func TestLogIfError_InvokesFn(t *testing.T) {
	entry, _ := newTestEntry()
	called := false

	LogIfError(entry, func() error {
		called = true
		return nil
	})

	if !called {
		t.Fatal("fn was not invoked")
	}
}

func TestLogIfError_NilError(t *testing.T) {
	entry, hook := newTestEntry()

	LogIfError(entry, func() error { return nil })

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

func TestLogIfError_ErrorNotInIgnoreListIsLogged(t *testing.T) {
	entry, hook := newTestEntry()
	wantErr := errors.New("boom")

	LogIfError(entry, func() error { return wantErr }, sql.ErrTxDone, sql.ErrNoRows)

	entries := hook.AllEntries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(entries))
	}
	if entries[0].Message != wantErr.Error() {
		t.Errorf("expected message %q, got %q", wantErr.Error(), entries[0].Message)
	}
}

func TestLogIfError_IgnoresMatchingError(t *testing.T) {
	entry, hook := newTestEntry()

	LogIfError(entry, func() error { return sql.ErrTxDone }, sql.ErrTxDone)

	if got := len(hook.AllEntries()); got != 0 {
		t.Fatalf("expected ignored error to suppress logging, got %d entries", got)
	}
}

func TestLogIfError_IgnoresOneOfManyListed(t *testing.T) {
	entry, hook := newTestEntry()

	LogIfError(entry, func() error { return sql.ErrNoRows }, sql.ErrTxDone, sql.ErrNoRows, sql.ErrConnDone)

	if got := len(hook.AllEntries()); got != 0 {
		t.Fatalf("expected ignored error to suppress logging, got %d entries", got)
	}
}

func TestLogIfError_IgnoresWrappedError(t *testing.T) {
	entry, hook := newTestEntry()
	wrapped := fmt.Errorf("rolling back tx: %w", sql.ErrTxDone)

	LogIfError(entry, func() error { return wrapped }, sql.ErrTxDone)

	if got := len(hook.AllEntries()); got != 0 {
		t.Fatalf("expected wrapped sentinel to be matched via errors.Is, got %d entries", got)
	}
}

func TestLogIfError_EmptyIgnoreListLogsError(t *testing.T) {
	entry, hook := newTestEntry()

	LogIfError(entry, func() error { return sql.ErrTxDone })

	entries := hook.AllEntries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 log entry when ignore list is empty, got %d", len(entries))
	}
	if entries[0].Message != sql.ErrTxDone.Error() {
		t.Errorf("expected message %q, got %q", sql.ErrTxDone.Error(), entries[0].Message)
	}
}
