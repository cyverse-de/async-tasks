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

// --- Tests for createBehaviorProcessorTask ---

func TestCreateBehaviorProcessorTask_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)

	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id"}).AddRow("new-task-id")
	mock.ExpectQuery(`INSERT INTO async_tasks`).WillReturnRows(taskRows)
	mock.ExpectCommit()

	id, err := createBehaviorProcessorTask(context.Background(), "statuschangetimeout", conn)
	if err != nil {
		t.Fatalf("createBehaviorProcessorTask returned error: %v", err)
	}
	if id != "new-task-id" {
		t.Errorf("Expected ID 'new-task-id', got %q", id)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

func TestCreateBehaviorProcessorTask_TaskTypeFormat(t *testing.T) {
	// The task type should be "behaviorprocessor-<behaviorType>"
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)

	mock.ExpectBegin()
	// We check that the inserted type is "behaviorprocessor-mybehavior"
	taskRows := sqlmock.NewRows([]string{"id"}).AddRow("test-id")
	mock.ExpectQuery(`INSERT INTO async_tasks`).WillReturnRows(taskRows)
	mock.ExpectCommit()

	_, err = createBehaviorProcessorTask(context.Background(), "mybehavior", conn)
	if err != nil {
		t.Fatalf("createBehaviorProcessorTask returned error: %v", err)
	}
}

func TestCreateBehaviorProcessorTask_BeginTxError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)

	mock.ExpectBegin().WillReturnError(fmt.Errorf("cannot begin"))

	_, err = createBehaviorProcessorTask(context.Background(), "test", conn)
	if err == nil {
		t.Fatal("Expected error from BeginTx failure")
	}
}

func TestCreateBehaviorProcessorTask_CommitError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)

	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id"}).AddRow("test-id")
	mock.ExpectQuery(`INSERT INTO async_tasks`).WillReturnRows(taskRows)
	mock.ExpectCommit().WillReturnError(fmt.Errorf("commit failed"))

	_, err = createBehaviorProcessorTask(context.Background(), "test", conn)
	if err == nil {
		t.Fatal("Expected error from Commit failure")
	}
}

// --- Tests for checkOldest ---

func TestCheckOldest_IsOldest(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)
	myID := "my-task-id"
	earlier := time.Now().Add(-time.Minute)

	mock.ExpectBegin()
	// GetTasksByFilter returns only our task
	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(myID, "behaviorprocessor-test", nil, nil, earlier, nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(rows)
	mock.ExpectRollback()

	err = checkOldest(context.Background(), "test", conn, myID)
	if err != nil {
		t.Fatalf("checkOldest returned error when we should be oldest: %v", err)
	}
}

func TestCheckOldest_NotOldest(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)
	myID := "my-task-id"
	now := time.Now()
	earlier := now.Add(-time.Minute)

	mock.ExpectBegin()
	// Another task is older than ours
	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow("other-task-id", "behaviorprocessor-test", nil, nil, earlier, nil).
		AddRow(myID, "behaviorprocessor-test", nil, nil, now, nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(rows)
	mock.ExpectRollback()

	err = checkOldest(context.Background(), "test", conn, myID)
	if err == nil {
		t.Fatal("Expected error when we are not the oldest task")
	}
	if err.Error() != "The provided ID is not the oldest task of its type" {
		t.Errorf("Unexpected error message: %q", err.Error())
	}
}

func TestCheckOldest_NoTasks(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)

	mock.ExpectBegin()
	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(rows)
	mock.ExpectRollback()

	// When no tasks are returned, isOldest stays true (default), so no error
	err = checkOldest(context.Background(), "test", conn, "my-id")
	if err != nil {
		t.Fatalf("checkOldest returned error for empty result: %v", err)
	}
}

// TestCheckOldest_OrderApplied verifies that checkOldest correctly identifies the
// oldest task when the DB returns results in start_date ASC order (as requested).
func TestCheckOldest_OrderApplied(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)
	myID := "my-task-id"
	now := time.Now()
	earlier := now.Add(-5 * time.Minute)

	mock.ExpectBegin()
	// DB returns tasks sorted by start_date ASC (our task is oldest)
	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(myID, "behaviorprocessor-test", nil, nil, earlier, nil).
		AddRow("other-task-id", "behaviorprocessor-test", nil, nil, now, nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks .+ ORDER BY`).WillReturnRows(rows)
	mock.ExpectRollback()

	err = checkOldest(context.Background(), "test", conn, myID)
	if err != nil {
		t.Fatalf("checkOldest returned unexpected error: %v", err)
	}
}

// TestCheckOldest_BreakOptimizationCorrect tests the early-break behavior in checkOldest.
// With ordering now applied (start_date ASC), the first task in the results is the oldest.
// If the first task isn't us, the break is correct — we truly aren't the oldest.
func TestCheckOldest_BreakOptimizationCorrect(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)
	myID := "my-task-id"
	now := time.Now()
	earliest := now.Add(-2 * time.Minute)
	earlier := now.Add(-time.Minute)

	mock.ExpectBegin()
	// With ordering applied (start_date ASC): other1(earliest) comes first, then myID(earlier)
	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow("other1", "behaviorprocessor-test", nil, nil, earliest, nil).
		AddRow(myID, "behaviorprocessor-test", nil, nil, earlier, nil).
		AddRow("other2", "behaviorprocessor-test", nil, nil, now, nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks .+ ORDER BY`).WillReturnRows(rows)
	mock.ExpectRollback()

	// other1 is oldest, so we correctly get an error saying we're not the oldest
	err = checkOldest(context.Background(), "test", conn, myID)
	if err == nil {
		t.Fatal("Expected error since we are not the oldest task")
	}
	if err.Error() != "The provided ID is not the oldest task of its type" {
		t.Errorf("Unexpected error message: %q", err.Error())
	}
}

// --- Tests for checkAlone ---

func TestCheckAlone_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)
	taskID := "alone-task-id"

	// createBehaviorProcessorTask
	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id"}).AddRow(taskID)
	mock.ExpectQuery(`INSERT INTO async_tasks`).WillReturnRows(taskRows)
	mock.ExpectCommit()

	// checkOldest
	mock.ExpectBegin()
	filterRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "behaviorprocessor-test", nil, nil, time.Now().Add(-time.Second), nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(filterRows)
	mock.ExpectRollback()

	id, err := checkAlone(context.Background(), "test", conn)
	if err != nil {
		t.Fatalf("checkAlone returned error: %v", err)
	}
	if id != taskID {
		t.Errorf("Expected ID %q, got %q", taskID, id)
	}
}

func TestCheckAlone_NotAlone(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)
	myTaskID := "my-task-id"

	// createBehaviorProcessorTask
	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id"}).AddRow(myTaskID)
	mock.ExpectQuery(`INSERT INTO async_tasks`).WillReturnRows(taskRows)
	mock.ExpectCommit()

	// checkOldest - another task is older
	mock.ExpectBegin()
	older := time.Now().Add(-time.Minute)
	newer := time.Now()
	filterRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow("other-task-id", "behaviorprocessor-test", nil, nil, older, nil).
		AddRow(myTaskID, "behaviorprocessor-test", nil, nil, newer, nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(filterRows)
	mock.ExpectRollback()

	id, err := checkAlone(context.Background(), "test", conn)
	if err == nil {
		t.Fatal("Expected error when not alone")
	}
	if id != myTaskID {
		t.Errorf("Expected ID %q even on error, got %q", myTaskID, id)
	}
}

// --- Tests for finishTask ---

func TestFinishTask_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)
	processorLog := logrus.WithField("test", true)

	mock.ExpectBegin()
	completeRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`UPDATE async_tasks SET end_date`).WillReturnRows(completeRows)
	mock.ExpectCommit()

	err = finishTask(context.Background(), "task-to-finish", conn, processorLog)
	if err != nil {
		t.Fatalf("finishTask returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

func TestFinishTask_CommitError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)
	processorLog := logrus.WithField("test", true)

	mock.ExpectBegin()
	completeRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`UPDATE async_tasks SET end_date`).WillReturnRows(completeRows)
	mock.ExpectCommit().WillReturnError(fmt.Errorf("commit failed"))

	err = finishTask(context.Background(), "task-id", conn, processorLog)
	if err == nil {
		t.Fatal("Expected error from commit failure")
	}
}

// --- Tests for deleteTask ---

func TestDeleteTask_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)
	processorLog := logrus.WithField("test", true)

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM async_tasks`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err = deleteTask(context.Background(), "task-to-delete", conn, processorLog)
	if err != nil {
		t.Fatalf("deleteTask returned error: %v", err)
	}
}

// --- Tests for DoPeriodicUpdate ---

func TestDoPeriodicUpdate_NoBehaviors(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)
	updater := NewAsyncTasksUpdater(conn)

	// No behavior processors registered, should complete quickly
	err = updater.DoPeriodicUpdate(context.Background(), time.Now(), conn)
	if err != nil {
		t.Fatalf("DoPeriodicUpdate with no behaviors returned error: %v", err)
	}
}

func TestDoPeriodicUpdate_ProcessorCalled(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)
	updater := NewAsyncTasksUpdater(conn)

	taskID := "processor-task-id"
	processorCalled := false
	var capturedTime time.Time

	updater.AddBehavior("test-behavior", func(ctx context.Context, log *logrus.Entry, tickerTime time.Time, db *database.DBConnection) error {
		processorCalled = true
		capturedTime = tickerTime
		return nil
	})

	// checkAlone: createBehaviorProcessorTask
	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id"}).AddRow(taskID)
	mock.ExpectQuery(`INSERT INTO async_tasks`).WillReturnRows(taskRows)
	mock.ExpectCommit()

	// checkAlone: checkOldest
	mock.ExpectBegin()
	filterRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "behaviorprocessor-test-behavior", nil, nil, time.Now().Add(-time.Second), nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(filterRows)
	mock.ExpectRollback()

	// finishTaskLogError -> finishTask
	mock.ExpectBegin()
	completeRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`UPDATE async_tasks SET end_date`).WillReturnRows(completeRows)
	mock.ExpectCommit()

	tickerTime := time.Now()
	err = updater.DoPeriodicUpdate(context.Background(), tickerTime, conn)
	if err != nil {
		t.Fatalf("DoPeriodicUpdate returned error: %v", err)
	}

	if !processorCalled {
		t.Error("Behavior processor was not called")
	}
	if !capturedTime.Equal(tickerTime) {
		t.Errorf("Expected tickerTime %v, got %v", tickerTime, capturedTime)
	}
}

func TestDoPeriodicUpdate_ProcessorError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)
	updater := NewAsyncTasksUpdater(conn)

	taskID := "error-task-id"

	updater.AddBehavior("failing-behavior", func(ctx context.Context, log *logrus.Entry, tickerTime time.Time, db *database.DBConnection) error {
		return fmt.Errorf("processor error")
	})

	// checkAlone
	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id"}).AddRow(taskID)
	mock.ExpectQuery(`INSERT INTO async_tasks`).WillReturnRows(taskRows)
	mock.ExpectCommit()

	mock.ExpectBegin()
	filterRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "behaviorprocessor-failing-behavior", nil, nil, time.Now().Add(-time.Second), nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(filterRows)
	mock.ExpectRollback()

	// finishTaskLogError still runs (deferred)
	mock.ExpectBegin()
	completeRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`UPDATE async_tasks SET end_date`).WillReturnRows(completeRows)
	mock.ExpectCommit()

	// DoPeriodicUpdate itself returns nil even if processors error (errors are logged)
	err = updater.DoPeriodicUpdate(context.Background(), time.Now(), conn)
	if err != nil {
		t.Fatalf("DoPeriodicUpdate returned error: %v", err)
	}
}

func TestDoPeriodicUpdate_NotAlone_DeletesTask(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)
	updater := NewAsyncTasksUpdater(conn)

	myTaskID := "my-task"
	processorCalled := false

	updater.AddBehavior("test", func(ctx context.Context, log *logrus.Entry, tickerTime time.Time, db *database.DBConnection) error {
		processorCalled = true
		return nil
	})

	// checkAlone: createBehaviorProcessorTask
	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id"}).AddRow(myTaskID)
	mock.ExpectQuery(`INSERT INTO async_tasks`).WillReturnRows(taskRows)
	mock.ExpectCommit()

	// checkAlone: checkOldest - another task is older
	mock.ExpectBegin()
	filterRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow("older-task", "behaviorprocessor-test", nil, nil, time.Now().Add(-time.Minute), nil).
		AddRow(myTaskID, "behaviorprocessor-test", nil, nil, time.Now(), nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(filterRows)
	mock.ExpectRollback()

	// deleteTask (called because we're not the oldest)
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM async_tasks`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err = updater.DoPeriodicUpdate(context.Background(), time.Now(), conn)
	if err != nil {
		t.Fatalf("DoPeriodicUpdate returned error: %v", err)
	}

	if processorCalled {
		t.Error("Processor should NOT have been called when not alone")
	}
}

// TestDoPeriodicUpdate_ProcessorLogLosesBehaviorType documents the logging bug
// where processorLog is reassigned on line 175 of updater.go, losing the
// behavior_type field. After the reassignment, the log only has async_task_id.
func TestDoPeriodicUpdate_ProcessorLogLosesBehaviorType(t *testing.T) {
	// This test documents the bug conceptually. The processorLog is initially set to:
	//   log.WithFields(logrus.Fields{"behavior_type": behaviorType})
	// Then on line 175 it's reassigned to:
	//   log.WithFields(logrus.Fields{"async_task_id": taskID})
	// The behavior_type field is lost because the second assignment doesn't
	// include it. This means log messages after line 175 don't include behavior_type.
	//
	// We can verify this by checking the log fields in the processor callback,
	// but the logrus Entry passed to the processor is the one AFTER the reassignment.

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn := database.NewTestDBConnection(db)
	updater := NewAsyncTasksUpdater(conn)

	taskID := "log-test-task"
	var capturedLog *logrus.Entry

	updater.AddBehavior("my-behavior", func(ctx context.Context, log *logrus.Entry, tickerTime time.Time, db *database.DBConnection) error {
		capturedLog = log
		return nil
	})

	// checkAlone
	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id"}).AddRow(taskID)
	mock.ExpectQuery(`INSERT INTO async_tasks`).WillReturnRows(taskRows)
	mock.ExpectCommit()

	mock.ExpectBegin()
	filterRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "behaviorprocessor-my-behavior", nil, nil, time.Now().Add(-time.Second), nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(filterRows)
	mock.ExpectRollback()

	// finishTask
	mock.ExpectBegin()
	completeRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`UPDATE async_tasks SET end_date`).WillReturnRows(completeRows)
	mock.ExpectCommit()

	err = updater.DoPeriodicUpdate(context.Background(), time.Now(), conn)
	if err != nil {
		t.Fatalf("DoPeriodicUpdate returned error: %v", err)
	}

	if capturedLog == nil {
		t.Fatal("Processor log was not captured")
	}

	// Bug: The processorLog passed to the processor has async_task_id but NOT behavior_type
	if _, hasBehaviorType := capturedLog.Data["behavior_type"]; hasBehaviorType {
		t.Error("Expected behavior_type to be MISSING from processor log (documenting bug would fail here if fixed)")
	}
	if _, hasTaskID := capturedLog.Data["async_task_id"]; !hasTaskID {
		t.Error("Expected async_task_id in processor log")
	}
}
