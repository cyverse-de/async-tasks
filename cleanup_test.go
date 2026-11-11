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
