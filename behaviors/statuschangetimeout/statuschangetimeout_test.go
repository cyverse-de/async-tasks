package statuschangetimeout

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/cyverse-de/async-tasks/database"
	"github.com/sirupsen/logrus"
)

func newTestConn(t *testing.T) (*database.DBConnection, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	conn := database.NewTestDBConnection(db)
	return conn, mock, func() { db.Close() }
}

func testLog() *logrus.Entry {
	return logrus.WithField("test", true)
}

// --- Tests for StatusChangeTimeoutData ---

func TestStatusChangeTimeoutData_Structure(t *testing.T) {
	data := StatusChangeTimeoutData{
		StartStatus: "running",
		EndStatus:   "timed-out",
		Timeout:     "1h",
		Complete:    true,
		Delete:      false,
	}

	if data.StartStatus != "running" {
		t.Errorf("Expected StartStatus 'running', got %q", data.StartStatus)
	}
	if data.EndStatus != "timed-out" {
		t.Errorf("Expected EndStatus 'timed-out', got %q", data.EndStatus)
	}
	if data.Timeout != "1h" {
		t.Errorf("Expected Timeout '1h', got %q", data.Timeout)
	}
	if !data.Complete {
		t.Error("Expected Complete to be true")
	}
	if data.Delete {
		t.Error("Expected Delete to be false")
	}
}

// --- Tests for Processor ---

func TestProcessor_NoTasks(t *testing.T) {
	conn, mock, cleanup := newTestConn(t)
	defer cleanup()

	mock.ExpectBegin()
	// GetTasksByFilter returns empty
	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(rows)
	mock.ExpectRollback()

	err := Processor(context.Background(), testLog(), time.Now(), conn)
	if err != nil {
		t.Fatalf("Processor returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

func TestProcessor_FiltersByBehaviorType(t *testing.T) {
	conn, mock, cleanup := newTestConn(t)
	defer cleanup()

	mock.ExpectBegin()
	// Verify it queries with behavior_types filter
	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_tasks JOIN`).WillReturnRows(rows)
	mock.ExpectRollback()

	err := Processor(context.Background(), testLog(), time.Now(), conn)
	if err != nil {
		t.Fatalf("Processor returned error: %v", err)
	}
}

// TestProcessor_DoesNotFilterCompletedTasks documents that the Processor's filter
// does NOT exclude tasks that have end_date set (completed tasks). It processes all
// tasks with the statuschangetimeout behavior regardless of completion status.
func TestProcessor_DoesNotFilterCompletedTasks(t *testing.T) {
	conn, mock, cleanup := newTestConn(t)
	defer cleanup()

	now := time.Now()
	completedEndDate := now.Add(-time.Hour)

	mock.ExpectBegin()
	// Return a completed task (end_date is set)
	filterRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow("completed-task", "test", nil, nil, now.Add(-2*time.Hour), completedEndDate)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(filterRows)
	mock.ExpectRollback()

	// processSingleTask will be called for the completed task
	// It needs its own transaction
	mock.ExpectBegin()
	// GetTask (getBaseTask)
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow("completed-task", "test", nil, nil, now.Add(-2*time.Hour), completedEndDate)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	// getTaskBehaviors
	behaviorData, _ := json.Marshal(map[string]interface{}{
		"statuses": []map[string]interface{}{
			{"start_status": "running", "end_status": "timed-out", "timeout": "30m", "complete": true},
		},
	})
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"}).
		AddRow("statuschangetimeout", string(behaviorData))
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	// getTaskStatuses
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"}).
		AddRow("completed", nil, now.Add(-time.Hour))
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)
	// Commit (even if no changes were made, processSingleTask always commits)
	mock.ExpectCommit()

	err := Processor(context.Background(), testLog(), now, conn)
	if err != nil {
		t.Fatalf("Processor returned error: %v", err)
	}

	// The test passing means the completed task WAS processed (the DB interactions
	// for processSingleTask all happened). This documents the bug that completed
	// tasks are still processed.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

// TestProcessor_PassesOrderParam verifies that the Processor passes
// "end_date IS NOT NULL DESC" as the order parameter to GetTasksByFilter,
// which is now applied as an ORDER BY clause.
func TestProcessor_PassesOrderParam(t *testing.T) {
	conn, mock, cleanup := newTestConn(t)
	defer cleanup()

	mock.ExpectBegin()
	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_tasks .+ ORDER BY`).WillReturnRows(rows)
	mock.ExpectRollback()

	err := Processor(context.Background(), testLog(), time.Now(), conn)
	if err != nil {
		t.Fatalf("Processor returned error: %v", err)
	}
}

func TestProcessor_CancelledContext(t *testing.T) {
	conn, _, cleanup := newTestConn(t)
	defer cleanup()

	now := time.Now()

	// Cancel context before calling Processor.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Processor calls db.BeginTx first, which fails with context.Canceled
	// before the loop's context check (line 155) ever runs.
	// This documents that a pre-cancelled context is NOT handled gracefully
	// at the top level — it returns an error rather than returning nil.
	err := Processor(ctx, testLog(), now, conn)
	if err == nil {
		t.Fatal("Expected error from Processor with cancelled context, got nil")
	}
	if err != context.Canceled {
		t.Fatalf("Expected context.Canceled, got: %v", err)
	}
}

// --- Tests for processSingleTask ---

func TestProcessSingleTask_TimedOut(t *testing.T) {
	conn, mock, cleanup := newTestConn(t)
	defer cleanup()

	now := time.Now()
	taskID := "timeout-task"
	// Task's last status was set 2 hours ago, timeout is 1 hour
	twoHoursAgo := now.Add(-2 * time.Hour)

	mock.ExpectBegin()
	// GetTask
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "test", nil, nil, now.Add(-3*time.Hour), nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	behaviorData, _ := json.Marshal(map[string]interface{}{
		"statuses": []map[string]interface{}{
			{"start_status": "running", "end_status": "timed-out", "timeout": "1h", "complete": true},
		},
	})
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"}).
		AddRow("statuschangetimeout", string(behaviorData))
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"}).
		AddRow("running", nil, twoHoursAgo)
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)

	// Should insert new status "timed-out"
	insertStatusRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`INSERT INTO async_task_status`).WillReturnRows(insertStatusRows)
	// Should complete the task
	completeRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`UPDATE async_tasks SET end_date`).WillReturnRows(completeRows)
	mock.ExpectCommit()

	err := processSingleTask(context.Background(), testLog(), conn, taskID)
	if err != nil {
		t.Fatalf("processSingleTask returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

func TestProcessSingleTask_NotTimedOut(t *testing.T) {
	conn, mock, cleanup := newTestConn(t)
	defer cleanup()

	now := time.Now()
	taskID := "not-timed-out"
	// Task's last status was set 10 minutes ago, timeout is 1 hour
	tenMinutesAgo := now.Add(-10 * time.Minute)

	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "test", nil, nil, now.Add(-time.Hour), nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	behaviorData, _ := json.Marshal(map[string]interface{}{
		"statuses": []map[string]interface{}{
			{"start_status": "running", "end_status": "timed-out", "timeout": "1h", "complete": true},
		},
	})
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"}).
		AddRow("statuschangetimeout", string(behaviorData))
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"}).
		AddRow("running", nil, tenMinutesAgo)
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)
	// No INSERT or UPDATE expected — timeout hasn't elapsed
	mock.ExpectCommit()

	err := processSingleTask(context.Background(), testLog(), conn, taskID)
	if err != nil {
		t.Fatalf("processSingleTask returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

func TestProcessSingleTask_WrongStatus(t *testing.T) {
	conn, mock, cleanup := newTestConn(t)
	defer cleanup()

	now := time.Now()
	taskID := "wrong-status"
	// Status is "pending" not "running", so it won't match the start_status
	twoHoursAgo := now.Add(-2 * time.Hour)

	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "test", nil, nil, now.Add(-3*time.Hour), nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	behaviorData, _ := json.Marshal(map[string]interface{}{
		"statuses": []map[string]interface{}{
			{"start_status": "running", "end_status": "timed-out", "timeout": "1h", "complete": true},
		},
	})
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"}).
		AddRow("statuschangetimeout", string(behaviorData))
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"}).
		AddRow("pending", nil, twoHoursAgo) // "pending" != "running"
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)
	mock.ExpectCommit()

	err := processSingleTask(context.Background(), testLog(), conn, taskID)
	if err != nil {
		t.Fatalf("processSingleTask returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

func TestProcessSingleTask_NoStatuses_UsesStartDate(t *testing.T) {
	conn, mock, cleanup := newTestConn(t)
	defer cleanup()

	now := time.Now()
	taskID := "no-status-task"
	// Task has no statuses, comparison should use start_date
	twoHoursAgo := now.Add(-2 * time.Hour)

	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "test", nil, nil, twoHoursAgo, nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	behaviorData, _ := json.Marshal(map[string]interface{}{
		"statuses": []map[string]interface{}{
			// start_status is empty string when no statuses exist, comparisonStatus is also ""
			{"start_status": "", "end_status": "timed-out", "timeout": "1h", "complete": true},
		},
	})
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"}).
		AddRow("statuschangetimeout", string(behaviorData))
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	// No statuses
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)

	// Should trigger because comparisonTimestamp(=startDate=twoHoursAgo) + 1h is before now
	// and comparisonStatus("") == start_status("")
	insertStatusRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`INSERT INTO async_task_status`).WillReturnRows(insertStatusRows)
	completeRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`UPDATE async_tasks SET end_date`).WillReturnRows(completeRows)
	mock.ExpectCommit()

	err := processSingleTask(context.Background(), testLog(), conn, taskID)
	if err != nil {
		t.Fatalf("processSingleTask returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

func TestProcessSingleTask_WithDelete(t *testing.T) {
	conn, mock, cleanup := newTestConn(t)
	defer cleanup()

	now := time.Now()
	taskID := "delete-task"
	twoHoursAgo := now.Add(-2 * time.Hour)

	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "test", nil, nil, now.Add(-3*time.Hour), nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	behaviorData, _ := json.Marshal(map[string]interface{}{
		"statuses": []map[string]interface{}{
			{"start_status": "running", "end_status": "timed-out", "timeout": "1h", "complete": false, "delete": true},
		},
	})
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"}).
		AddRow("statuschangetimeout", string(behaviorData))
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"}).
		AddRow("running", nil, twoHoursAgo)
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)

	// Should insert new status
	insertStatusRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`INSERT INTO async_task_status`).WillReturnRows(insertStatusRows)
	// Should NOT complete (complete=false) but SHOULD delete
	mock.ExpectExec(`DELETE FROM async_tasks`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := processSingleTask(context.Background(), testLog(), conn, taskID)
	if err != nil {
		t.Fatalf("processSingleTask returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

func TestProcessSingleTask_MultipleStatusRules(t *testing.T) {
	conn, mock, cleanup := newTestConn(t)
	defer cleanup()

	now := time.Now()
	taskID := "multi-rule-task"
	twoHoursAgo := now.Add(-2 * time.Hour)

	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "test", nil, nil, now.Add(-3*time.Hour), nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	behaviorData, _ := json.Marshal(map[string]interface{}{
		"statuses": []map[string]interface{}{
			// This one won't match (wrong start_status)
			{"start_status": "pending", "end_status": "started", "timeout": "30m", "complete": false},
			// This one will match
			{"start_status": "running", "end_status": "timed-out", "timeout": "1h", "complete": true},
		},
	})
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"}).
		AddRow("statuschangetimeout", string(behaviorData))
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"}).
		AddRow("running", nil, twoHoursAgo)
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)

	// Only the second rule should trigger
	insertStatusRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`INSERT INTO async_task_status`).WillReturnRows(insertStatusRows)
	completeRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`UPDATE async_tasks SET end_date`).WillReturnRows(completeRows)
	mock.ExpectCommit()

	err := processSingleTask(context.Background(), testLog(), conn, taskID)
	if err != nil {
		t.Fatalf("processSingleTask returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

func TestProcessSingleTask_InvalidBehaviorData(t *testing.T) {
	conn, mock, cleanup := newTestConn(t)
	defer cleanup()

	now := time.Now()
	taskID := "bad-data-task"

	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "test", nil, nil, now, nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	// Behavior data "statuses" field is a string, not an array
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"}).
		AddRow("statuschangetimeout", `{"statuses":"not-an-array"}`)
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)
	mock.ExpectRollback()

	err := processSingleTask(context.Background(), testLog(), conn, taskID)
	if err == nil {
		t.Fatal("Expected error for invalid behavior data")
	}
}

func TestProcessSingleTask_InvalidTimeout(t *testing.T) {
	conn, mock, cleanup := newTestConn(t)
	defer cleanup()

	now := time.Now()
	taskID := "bad-timeout-task"

	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "test", nil, nil, now.Add(-2*time.Hour), nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	behaviorData, _ := json.Marshal(map[string]interface{}{
		"statuses": []map[string]interface{}{
			{"start_status": "running", "end_status": "timed-out", "timeout": "not-a-duration", "complete": true},
		},
	})
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"}).
		AddRow("statuschangetimeout", string(behaviorData))
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"}).
		AddRow("running", nil, now.Add(-2*time.Hour))
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)
	// Invalid timeout is logged but processing continues (doesn't die)
	mock.ExpectCommit()

	err := processSingleTask(context.Background(), testLog(), conn, taskID)
	if err != nil {
		t.Fatalf("processSingleTask should not return error for invalid timeout (logged only): %v", err)
	}
}

func TestProcessSingleTask_CancelledContext(t *testing.T) {
	conn, _, cleanup := newTestConn(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Should return nil without doing any work
	err := processSingleTask(ctx, testLog(), conn, "any-id")
	if err != nil {
		t.Fatalf("processSingleTask returned error on cancelled context: %v", err)
	}
}

func TestProcessSingleTask_DBError(t *testing.T) {
	conn, mock, cleanup := newTestConn(t)
	defer cleanup()

	mock.ExpectBegin().WillReturnError(fmt.Errorf("db down"))

	err := processSingleTask(context.Background(), testLog(), conn, "task-id")
	if err == nil {
		t.Fatal("Expected error from DB failure")
	}
}

// TestProcessSingleTask_CommitError_OnlyLogged documents that commit errors in
// processSingleTask are logged but not returned as errors.
func TestProcessSingleTask_CommitError_OnlyLogged(t *testing.T) {
	conn, mock, cleanup := newTestConn(t)
	defer cleanup()

	now := time.Now()
	taskID := "commit-error-task"

	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "test", nil, nil, now, nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"}).
		AddRow("statuschangetimeout", `{"statuses":[]}`)
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)
	mock.ExpectCommit().WillReturnError(fmt.Errorf("commit failed"))

	// processSingleTask logs the commit error but returns nil
	err := processSingleTask(context.Background(), testLog(), conn, taskID)
	if err != nil {
		t.Fatalf("Expected nil return despite commit error, got: %v", err)
	}
}

// TestProcessSingleTask_MostRecentStatus documents the behavior where
// processSingleTask finds the most recent status by iterating all statuses
// and comparing CreatedDate. This relies on GetTask returning statuses
// in created_date ASC order (which getTaskStatuses does enforce with OrderBy).
func TestProcessSingleTask_MostRecentStatus(t *testing.T) {
	conn, mock, cleanup := newTestConn(t)
	defer cleanup()

	now := time.Now()
	taskID := "multi-status-task"

	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "test", nil, nil, now.Add(-5*time.Hour), nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	behaviorData, _ := json.Marshal(map[string]interface{}{
		"statuses": []map[string]interface{}{
			// Only triggers if most recent status is "running"
			{"start_status": "running", "end_status": "timed-out", "timeout": "1h", "complete": true},
		},
	})
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"}).
		AddRow("statuschangetimeout", string(behaviorData))
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	// Multiple statuses — "running" is the most recent
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"}).
		AddRow("pending", nil, now.Add(-4*time.Hour)).
		AddRow("running", nil, now.Add(-3*time.Hour))
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)

	// Should trigger because most recent status is "running" and timeout (1h) has elapsed
	insertStatusRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`INSERT INTO async_task_status`).WillReturnRows(insertStatusRows)
	completeRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`UPDATE async_tasks SET end_date`).WillReturnRows(completeRows)
	mock.ExpectCommit()

	err := processSingleTask(context.Background(), testLog(), conn, taskID)
	if err != nil {
		t.Fatalf("processSingleTask returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}
