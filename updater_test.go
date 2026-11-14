package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/cyverse-de/async-tasks/database"
	"github.com/sirupsen/logrus"
)

// --- Tests for NewAsyncTasksUpdater ---

func TestNewAsyncTasksUpdater(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)
	updater := NewAsyncTasksUpdater(conn)

	if updater.db != conn {
		t.Error("Updater db doesn't match provided connection")
	}
	if updater.behaviorProcessors == nil {
		t.Error("behaviorProcessors map is nil")
	}
	if len(updater.behaviorProcessors) != 0 {
		t.Errorf("Expected 0 behavior processors initially, got %d", len(updater.behaviorProcessors))
	}
}

// --- Tests for AddBehavior ---

func TestAddBehavior(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)
	updater := NewAsyncTasksUpdater(conn)

	called := false
	processor := func(ctx context.Context, log *logrus.Entry, tickerTime time.Time, db *database.DBConnection) error {
		called = true
		return nil
	}

	updater.AddBehavior("test-behavior", processor)

	if len(updater.behaviorProcessors) != 1 {
		t.Fatalf("Expected 1 behavior processor, got %d", len(updater.behaviorProcessors))
	}

	p, ok := updater.behaviorProcessors["test-behavior"]
	if !ok {
		t.Fatal("Expected processor for 'test-behavior'")
	}

	// Verify it's the same function by calling it
	_ = p(context.Background(), logrus.WithField("test", true), time.Now(), conn)
	if !called {
		t.Error("Processor was not called")
	}
}

func TestAddBehavior_OverwriteExisting(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)
	updater := NewAsyncTasksUpdater(conn)

	firstCalled := false
	secondCalled := false

	updater.AddBehavior("test", func(ctx context.Context, log *logrus.Entry, tickerTime time.Time, db *database.DBConnection) error {
		firstCalled = true
		return nil
	})
	updater.AddBehavior("test", func(ctx context.Context, log *logrus.Entry, tickerTime time.Time, db *database.DBConnection) error {
		secondCalled = true
		return nil
	})

	if len(updater.behaviorProcessors) != 1 {
		t.Fatalf("Expected 1 processor after overwrite, got %d", len(updater.behaviorProcessors))
	}

	_ = updater.behaviorProcessors["test"](context.Background(), logrus.WithField("test", true), time.Now(), conn)
	if firstCalled {
		t.Error("First processor should not have been called after overwrite")
	}
	if !secondCalled {
		t.Error("Second processor should have been called")
	}
}

// --- Tests for DoPeriodicUpdate ---

func newUpdaterWithMock(t *testing.T) (*AsyncTasksUpdater, *database.DBConnection, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	conn := database.NewTestDBConnection(db)
	return NewAsyncTasksUpdater(conn), conn, mock
}

func expectLockAcquired(mock sqlmock.Sqlmock, behaviorType string) {
	mock.ExpectQuery(`SELECT pg_try_advisory_lock`).
		WithArgs("async-tasks:behavior:" + behaviorType).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
}

func expectUnlock(mock sqlmock.Sqlmock, behaviorType string) {
	mock.ExpectQuery(`SELECT pg_advisory_unlock`).
		WithArgs("async-tasks:behavior:" + behaviorType).
		WillReturnRows(sqlmock.NewRows([]string{"pg_advisory_unlock"}).AddRow(true))
}

func TestDoPeriodicUpdate_NoBehaviors(t *testing.T) {
	updater, conn, _ := newUpdaterWithMock(t)

	err := updater.DoPeriodicUpdate(context.Background(), time.Now(), conn)
	if err != nil {
		t.Fatalf("DoPeriodicUpdate with no behaviors returned error: %v", err)
	}
}

func TestDoPeriodicUpdate_ProcessorCalled(t *testing.T) {
	updater, conn, mock := newUpdaterWithMock(t)

	processorCalled := false
	var capturedTime time.Time

	updater.AddBehavior("test-behavior", func(ctx context.Context, log *logrus.Entry, tickerTime time.Time, db *database.DBConnection) error {
		processorCalled = true
		capturedTime = tickerTime
		return nil
	})

	expectLockAcquired(mock, "test-behavior")
	expectUnlock(mock, "test-behavior")

	tickerTime := time.Now()
	if err := updater.DoPeriodicUpdate(context.Background(), tickerTime, conn); err != nil {
		t.Fatalf("DoPeriodicUpdate returned error: %v", err)
	}

	if !processorCalled {
		t.Error("Behavior processor was not called")
	}
	if !capturedTime.Equal(tickerTime) {
		t.Errorf("Expected tickerTime %v, got %v", tickerTime, capturedTime)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

func TestDoPeriodicUpdate_ProcessorError_StillReleasesLock(t *testing.T) {
	updater, conn, mock := newUpdaterWithMock(t)

	updater.AddBehavior("failing-behavior", func(ctx context.Context, log *logrus.Entry, tickerTime time.Time, db *database.DBConnection) error {
		return fmt.Errorf("processor error")
	})

	expectLockAcquired(mock, "failing-behavior")
	expectUnlock(mock, "failing-behavior")

	// DoPeriodicUpdate itself returns nil even if processors error (errors are logged)
	if err := updater.DoPeriodicUpdate(context.Background(), time.Now(), conn); err != nil {
		t.Fatalf("DoPeriodicUpdate returned error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

func TestDoPeriodicUpdate_LockHeldElsewhere_SkipsProcessor(t *testing.T) {
	updater, conn, mock := newUpdaterWithMock(t)

	processorCalled := false
	updater.AddBehavior("test", func(ctx context.Context, log *logrus.Entry, tickerTime time.Time, db *database.DBConnection) error {
		processorCalled = true
		return nil
	})

	mock.ExpectQuery(`SELECT pg_try_advisory_lock`).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(false))

	if err := updater.DoPeriodicUpdate(context.Background(), time.Now(), conn); err != nil {
		t.Fatalf("DoPeriodicUpdate returned error: %v", err)
	}
	if processorCalled {
		t.Error("Processor should NOT have been called when another instance holds the lock")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

func TestDoPeriodicUpdate_LockError_SkipsProcessor(t *testing.T) {
	updater, conn, mock := newUpdaterWithMock(t)

	processorCalled := false
	updater.AddBehavior("test", func(ctx context.Context, log *logrus.Entry, tickerTime time.Time, db *database.DBConnection) error {
		processorCalled = true
		return nil
	})

	mock.ExpectQuery(`SELECT pg_try_advisory_lock`).WillReturnError(fmt.Errorf("db down"))

	if err := updater.DoPeriodicUpdate(context.Background(), time.Now(), conn); err != nil {
		t.Fatalf("DoPeriodicUpdate returned error: %v", err)
	}
	if processorCalled {
		t.Error("Processor should NOT have been called when the lock could not be taken")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

// A panicking processor must not take down the whole service, and must still
// release the lock.
func TestDoPeriodicUpdate_ProcessorPanic_IsRecoveredAndReleasesLock(t *testing.T) {
	updater, conn, mock := newUpdaterWithMock(t)

	updater.AddBehavior("panicky", func(ctx context.Context, log *logrus.Entry, tickerTime time.Time, db *database.DBConnection) error {
		panic("boom")
	})

	expectLockAcquired(mock, "panicky")
	expectUnlock(mock, "panicky")

	if err := updater.DoPeriodicUpdate(context.Background(), time.Now(), conn); err != nil {
		t.Fatalf("DoPeriodicUpdate returned error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

func TestDoPeriodicUpdate_ProcessorLogHasBehaviorType(t *testing.T) {
	updater, conn, mock := newUpdaterWithMock(t)

	var capturedLog *logrus.Entry
	updater.AddBehavior("my-behavior", func(ctx context.Context, log *logrus.Entry, tickerTime time.Time, db *database.DBConnection) error {
		capturedLog = log
		return nil
	})

	expectLockAcquired(mock, "my-behavior")
	expectUnlock(mock, "my-behavior")

	if err := updater.DoPeriodicUpdate(context.Background(), time.Now(), conn); err != nil {
		t.Fatalf("DoPeriodicUpdate returned error: %v", err)
	}
	if capturedLog == nil {
		t.Fatal("Processor log was not captured")
	}
	if got := capturedLog.Data["behavior_type"]; got != "my-behavior" {
		t.Errorf("Expected behavior_type 'my-behavior' in processor log, got %v", got)
	}
}
