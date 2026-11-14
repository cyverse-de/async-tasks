package main

import (
	"context"
	"fmt"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/cyverse-de/async-tasks/database"
)

func TestCompleteLegacyProcessorTasks(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectExec(`UPDATE async_tasks SET end_date`).
		WithArgs("behaviorprocessor-%").
		WillReturnResult(sqlmock.NewResult(0, 3))

	completeLegacyProcessorTasks(context.Background(), database.NewTestDBConnection(db))

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

// A failure is logged, not fatal: the service works without the cleanup.
func TestCompleteLegacyProcessorTasks_ErrorIsNotFatal(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectExec(`UPDATE async_tasks`).WillReturnError(fmt.Errorf("db down"))

	completeLegacyProcessorTasks(context.Background(), database.NewTestDBConnection(db))

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

func TestCompleteLegacyStatusChangeTimeoutTasks_RunsBothStepsInOneTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	// The flags must be added before stalled tasks are completed.
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE async_task_behavior`).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(`UPDATE async_tasks`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	completeLegacyStatusChangeTimeoutTasks(context.Background(), database.NewTestDBConnection(db))

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

func TestCompleteLegacyStatusChangeTimeoutTasks_RollsBackOnError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE async_task_behavior`).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(`UPDATE async_tasks`).WillReturnError(fmt.Errorf("db down"))
	mock.ExpectRollback()

	completeLegacyStatusChangeTimeoutTasks(context.Background(), database.NewTestDBConnection(db))

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}
