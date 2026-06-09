package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/cyverse-de/async-tasks/model"
	"github.com/sirupsen/logrus"
)

// newTestDBConnection creates a DBConnection wrapping a sqlmock DB.
func newTestDBConnection(t *testing.T) (*DBConnection, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	log := logrus.WithField("test", true)
	return &DBConnection{db: db, log: log}, mock
}

// --- Tests for makeTask ---

func TestMakeTask_MinimalFields(t *testing.T) {
	dbtask := model.DBTask{
		ID:   "abc-123",
		Type: "test-type",
	}

	task, err := makeTask(dbtask)
	if err != nil {
		t.Fatalf("makeTask returned error: %v", err)
	}

	if task.ID != "abc-123" {
		t.Errorf("Expected ID 'abc-123', got %q", task.ID)
	}
	if task.Type != "test-type" {
		t.Errorf("Expected Type 'test-type', got %q", task.Type)
	}
	if task.Username != "" {
		t.Errorf("Expected empty Username, got %q", task.Username)
	}
	if task.Data != nil {
		t.Errorf("Expected nil Data, got %v", task.Data)
	}
	if task.StartDate != nil {
		t.Errorf("Expected nil StartDate, got %v", task.StartDate)
	}
	if task.EndDate != nil {
		t.Errorf("Expected nil EndDate, got %v", task.EndDate)
	}
}

func TestMakeTask_AllFields(t *testing.T) {
	now := time.Now()
	later := now.Add(time.Hour)
	dbtask := model.DBTask{
		ID:        "def-456",
		Type:      "full-type",
		Username:  sql.NullString{String: "testuser", Valid: true},
		Data:      sql.NullString{String: `{"key":"value","num":42}`, Valid: true},
		StartDate: sql.NullTime{Time: now, Valid: true},
		EndDate:   sql.NullTime{Time: later, Valid: true},
	}

	task, err := makeTask(dbtask)
	if err != nil {
		t.Fatalf("makeTask returned error: %v", err)
	}

	if task.ID != "def-456" {
		t.Errorf("Expected ID 'def-456', got %q", task.ID)
	}
	if task.Username != "testuser" {
		t.Errorf("Expected Username 'testuser', got %q", task.Username)
	}
	if task.Data == nil {
		t.Fatal("Expected non-nil Data")
	}
	if task.Data["key"] != "value" {
		t.Errorf("Expected Data[key]='value', got %v", task.Data["key"])
	}
	// JSON numbers unmarshal to float64
	if task.Data["num"] != float64(42) {
		t.Errorf("Expected Data[num]=42, got %v", task.Data["num"])
	}
	if task.StartDate == nil || !task.StartDate.Equal(now) {
		t.Errorf("Expected StartDate %v, got %v", now, task.StartDate)
	}
	if task.EndDate == nil || !task.EndDate.Equal(later) {
		t.Errorf("Expected EndDate %v, got %v", later, task.EndDate)
	}
}

func TestMakeTask_InvalidJSON(t *testing.T) {
	dbtask := model.DBTask{
		ID:   "bad-json",
		Type: "test",
		Data: sql.NullString{String: `{not valid json}`, Valid: true},
	}

	task, err := makeTask(dbtask)
	if err == nil {
		t.Fatal("Expected error for invalid JSON, got nil")
	}
	// makeTask returns the partially-constructed task even on JSON error
	if task.ID != "bad-json" {
		t.Errorf("Expected ID 'bad-json' even on error, got %q", task.ID)
	}
}

func TestMakeTask_NullUsername(t *testing.T) {
	dbtask := model.DBTask{
		ID:       "null-user",
		Type:     "test",
		Username: sql.NullString{Valid: false},
	}

	task, err := makeTask(dbtask)
	if err != nil {
		t.Fatalf("makeTask returned error: %v", err)
	}
	if task.Username != "" {
		t.Errorf("Expected empty Username for NULL, got %q", task.Username)
	}
}

func TestMakeTask_NullData(t *testing.T) {
	dbtask := model.DBTask{
		ID:   "null-data",
		Type: "test",
		Data: sql.NullString{Valid: false},
	}

	task, err := makeTask(dbtask)
	if err != nil {
		t.Fatalf("makeTask returned error: %v", err)
	}
	if task.Data != nil {
		t.Errorf("Expected nil Data for NULL, got %v", task.Data)
	}
}

func TestMakeTask_EmptyJSONObject(t *testing.T) {
	dbtask := model.DBTask{
		ID:   "empty-data",
		Type: "test",
		Data: sql.NullString{String: `{}`, Valid: true},
	}

	task, err := makeTask(dbtask)
	if err != nil {
		t.Fatalf("makeTask returned error: %v", err)
	}
	if task.Data == nil {
		t.Fatal("Expected non-nil Data for '{}'")
	}
	if len(task.Data) != 0 {
		t.Errorf("Expected empty Data map, got %v", task.Data)
	}
}

// --- Tests for GetCount ---

func TestGetCount_Success(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	rows := sqlmock.NewRows([]string{"count"}).AddRow(int64(42))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM async_tasks`).WillReturnRows(rows)

	count, err := conn.GetCount(context.Background())
	if err != nil {
		t.Fatalf("GetCount returned error: %v", err)
	}
	if count != 42 {
		t.Errorf("Expected count 42, got %d", count)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

func TestGetCount_Error(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM async_tasks`).WillReturnError(sql.ErrConnDone)

	_, err := conn.GetCount(context.Background())
	if err == nil {
		t.Fatal("Expected error from GetCount, got nil")
	}
}

// --- Tests for BeginTx ---

func TestBeginTx_Success(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()

	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx returned error: %v", err)
	}
	if tx == nil {
		t.Fatal("Expected non-nil DBTx")
	}

	mock.ExpectRollback()
	_ = tx.Rollback()

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

// --- Tests for InsertTask ---

func TestInsertTask_RequiresType(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	// Task with no type should fail
	_, err = tx.InsertTask(context.Background(), model.AsyncTask{})
	if err == nil {
		t.Fatal("Expected error for empty task type, got nil")
	}
	if err.Error() != "task type must be provided" {
		t.Errorf("Expected 'task type must be provided', got %q", err.Error())
	}
}

func TestInsertTask_MinimalTask(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	returnedID := "aaaa-bbbb-cccc-dddd"
	rows := sqlmock.NewRows([]string{"id"}).AddRow(returnedID)
	mock.ExpectQuery(`INSERT INTO async_tasks`).
		WillReturnRows(rows)

	id, err := tx.InsertTask(context.Background(), model.AsyncTask{Type: "test-type"})
	if err != nil {
		t.Fatalf("InsertTask returned error: %v", err)
	}
	if id != returnedID {
		t.Errorf("Expected ID %q, got %q", returnedID, id)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

func TestInsertTask_WithUsernameAndData(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	returnedID := "1111-2222-3333-4444"
	rows := sqlmock.NewRows([]string{"id"}).AddRow(returnedID)
	mock.ExpectQuery(`INSERT INTO async_tasks`).
		WillReturnRows(rows)

	task := model.AsyncTask{
		Type:     "with-extras",
		Username: "testuser",
		Data:     map[string]interface{}{"foo": "bar"},
	}

	id, err := tx.InsertTask(context.Background(), task)
	if err != nil {
		t.Fatalf("InsertTask returned error: %v", err)
	}
	if id != returnedID {
		t.Errorf("Expected ID %q, got %q", returnedID, id)
	}
}

func TestInsertTask_WithStatusAndBehavior(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	returnedID := "5555-6666-7777-8888"

	// INSERT async_tasks
	taskRows := sqlmock.NewRows([]string{"id"}).AddRow(returnedID)
	mock.ExpectQuery(`INSERT INTO async_tasks`).WillReturnRows(taskRows)

	// INSERT async_task_status (InsertTaskStatus uses QueryContext)
	statusRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`INSERT INTO async_task_status`).WillReturnRows(statusRows)

	// INSERT async_task_behavior (InsertTaskBehavior uses QueryContext)
	behaviorRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`INSERT INTO async_task_behavior`).WillReturnRows(behaviorRows)

	task := model.AsyncTask{
		Type: "full-task",
		Statuses: []model.AsyncTaskStatus{
			{Status: "initial"},
		},
		Behaviors: []model.AsyncTaskBehavior{
			{BehaviorType: "statuschangetimeout"},
		},
	}

	id, err := tx.InsertTask(context.Background(), task)
	if err != nil {
		t.Fatalf("InsertTask returned error: %v", err)
	}
	if id != returnedID {
		t.Errorf("Expected ID %q, got %q", returnedID, id)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

// TestInsertTask_OnlyFirstStatusInserted verifies that InsertTask only inserts the
// first status even if multiple are provided. The route handler validates this, but
// the database layer only inserts Statuses[0].
func TestInsertTask_OnlyFirstStatusInserted(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	returnedID := "multi-status-id"

	taskRows := sqlmock.NewRows([]string{"id"}).AddRow(returnedID)
	mock.ExpectQuery(`INSERT INTO async_tasks`).WillReturnRows(taskRows)

	// Only one status insert should happen (the first one)
	statusRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`INSERT INTO async_task_status`).WillReturnRows(statusRows)

	// If a second INSERT INTO async_task_status were issued, ExpectationsWereMet would
	// still pass (no extra expectations). But the key test is that we DON'T get an error
	// from an unexpected query — sqlmock will error on unexpected queries by default.

	task := model.AsyncTask{
		Type: "multi-status",
		Statuses: []model.AsyncTaskStatus{
			{Status: "first"},
			{Status: "second-should-be-ignored"},
		},
	}

	_, err = tx.InsertTask(context.Background(), task)
	if err != nil {
		t.Fatalf("InsertTask returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

// --- Tests for InsertTaskStatus ---

func TestInsertTaskStatus_RequiresStatus(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	err = tx.InsertTaskStatus(context.Background(), model.AsyncTaskStatus{}, "some-task-id")
	if err == nil {
		t.Fatal("Expected error for empty status, got nil")
	}
	if err.Error() != "status type must be provided" {
		t.Errorf("Expected 'status type must be provided', got %q", err.Error())
	}
}

func TestInsertTaskStatus_WithDefaultDate(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	// InsertTaskStatus uses QueryContext (not ExecContext) for an INSERT without RETURNING
	rows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`INSERT INTO async_task_status`).WillReturnRows(rows)

	status := model.AsyncTaskStatus{Status: "running"}
	err = tx.InsertTaskStatus(context.Background(), status, "task-123")
	if err != nil {
		t.Fatalf("InsertTaskStatus returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

// --- Tests for InsertTaskBehavior ---

func TestInsertTaskBehavior_RequiresType(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	err = tx.InsertTaskBehavior(context.Background(), model.AsyncTaskBehavior{}, "some-task-id")
	if err == nil {
		t.Fatal("Expected error for empty behavior type, got nil")
	}
	if err.Error() != "behavior type must be provided" {
		t.Errorf("Expected 'behavior type must be provided', got %q", err.Error())
	}
}

func TestInsertTaskBehavior_WithoutData(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	rows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`INSERT INTO async_task_behavior`).WillReturnRows(rows)

	behavior := model.AsyncTaskBehavior{BehaviorType: "test-behavior"}
	err = tx.InsertTaskBehavior(context.Background(), behavior, "task-456")
	if err != nil {
		t.Fatalf("InsertTaskBehavior returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

func TestInsertTaskBehavior_WithData(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	rows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`INSERT INTO async_task_behavior`).WillReturnRows(rows)

	behavior := model.AsyncTaskBehavior{
		BehaviorType: "statuschangetimeout",
		Data:         map[string]interface{}{"key": "value"},
	}
	err = tx.InsertTaskBehavior(context.Background(), behavior, "task-789")
	if err != nil {
		t.Fatalf("InsertTaskBehavior returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

// --- Tests for CompleteTask ---

// TestCompleteTask_UsesQueryContextNotExec documents that CompleteTask uses QueryContext
// for an UPDATE without RETURNING clause. This is a known issue — ExecContext would be
// more appropriate.
func TestCompleteTask_UsesQueryContextNotExec(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	// CompleteTask uses QueryContext for an UPDATE — so we use ExpectQuery not ExpectExec
	rows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`UPDATE async_tasks SET end_date`).WillReturnRows(rows)

	err = tx.CompleteTask(context.Background(), "task-to-complete")
	if err != nil {
		t.Fatalf("CompleteTask returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

// --- Tests for DeleteTask ---

func TestDeleteTask_Success(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	// DeleteTask uses ExecContext (correctly)
	mock.ExpectExec(`DELETE FROM async_tasks WHERE`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = tx.DeleteTask(context.Background(), "task-to-delete")
	if err != nil {
		t.Fatalf("DeleteTask returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

// --- Tests for getBaseTask ---

func TestGetBaseTask_ReturnsEmptyTaskOnNoRows(t *testing.T) {
	// Bug: getBaseTask returns a zero-value AsyncTask (ID=="") when no rows match,
	// rather than returning an explicit error. Callers check task.ID == "" for not-found.
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	// Return empty result set
	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(rows)

	task, err := tx.getBaseTask(context.Background(), "nonexistent-id", false)
	if err != nil {
		t.Fatalf("getBaseTask returned error: %v", err)
	}

	// The function returns a zero-value task instead of an error for not-found
	if task.ID != "" {
		t.Errorf("Expected empty ID for non-existent task, got %q", task.ID)
	}
	if task.Type != "" {
		t.Errorf("Expected empty Type for non-existent task, got %q", task.Type)
	}
}

func TestGetBaseTask_WithForUpdate(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow("test-id", "test-type", nil, nil, now, nil)
	// When forUpdate=true, the query should include FOR UPDATE suffix
	mock.ExpectQuery(`SELECT .+ FROM async_tasks .+ FOR UPDATE`).WillReturnRows(rows)

	task, err := tx.getBaseTask(context.Background(), "test-id", true)
	if err != nil {
		t.Fatalf("getBaseTask returned error: %v", err)
	}
	if task.ID != "test-id" {
		t.Errorf("Expected ID 'test-id', got %q", task.ID)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

// --- Tests for GetTask (full task with behaviors and statuses) ---

func TestGetTask_FullTask(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	now := time.Now()

	// getBaseTask query
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow("full-task-id", "my-type", "user1", `{"k":"v"}`, now, nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)

	// getTaskBehaviors query
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"}).
		AddRow("statuschangetimeout", `{"statuses":[]}`)
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)

	// getTaskStatuses query
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"}).
		AddRow("pending", nil, now)
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)

	task, err := tx.GetTask(context.Background(), "full-task-id", false)
	if err != nil {
		t.Fatalf("GetTask returned error: %v", err)
	}

	if task.ID != "full-task-id" {
		t.Errorf("Expected ID 'full-task-id', got %q", task.ID)
	}
	if !task.BehaviorsLoaded {
		t.Error("Expected BehaviorsLoaded to be true")
	}
	if !task.StatusesLoaded {
		t.Error("Expected StatusesLoaded to be true")
	}
	if len(task.Behaviors) != 1 {
		t.Fatalf("Expected 1 behavior, got %d", len(task.Behaviors))
	}
	if task.Behaviors[0].BehaviorType != "statuschangetimeout" {
		t.Errorf("Expected behavior type 'statuschangetimeout', got %q", task.Behaviors[0].BehaviorType)
	}
	if len(task.Statuses) != 1 {
		t.Fatalf("Expected 1 status, got %d", len(task.Statuses))
	}
	if task.Statuses[0].Status != "pending" {
		t.Errorf("Expected status 'pending', got %q", task.Statuses[0].Status)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

// --- Tests for GetTasksByFilter ---

// TestGetTasksByFilter_OrderParameterApplied verifies that the order parameter
// passed to GetTasksByFilter is included in the generated SQL as an ORDER BY clause.
func TestGetTasksByFilter_OrderParameterApplied(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	now := time.Now()
	later := now.Add(time.Hour)

	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow("task-1", "type-a", nil, nil, now, nil).
		AddRow("task-2", "type-a", nil, nil, later, nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks .+ ORDER BY start_date ASC`).WillReturnRows(rows)

	filter := TaskFilter{Types: []string{"type-a"}}
	tasks, err := tx.GetTasksByFilter(context.Background(), filter, "start_date ASC")
	if err != nil {
		t.Fatalf("GetTasksByFilter returned error: %v", err)
	}

	if len(tasks) != 2 {
		t.Fatalf("Expected 2 tasks, got %d", len(tasks))
	}

	if tasks[0].ID != "task-1" {
		t.Errorf("Expected first task ID 'task-1', got %q", tasks[0].ID)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

// TestGetTasksByFilter_OrderWithBehaviorFilter verifies that ORDER BY composes
// correctly with JOIN-based filters like BehaviorTypes.
func TestGetTasksByFilter_OrderWithBehaviorFilter(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow("task-1", "test", nil, nil, now, nil).
		AddRow("task-2", "test", nil, nil, now.Add(time.Hour), nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks JOIN .+ ORDER BY start_date ASC`).WillReturnRows(rows)

	filter := TaskFilter{BehaviorTypes: []string{"statuschangetimeout"}}
	tasks, err := tx.GetTasksByFilter(context.Background(), filter, "start_date ASC")
	if err != nil {
		t.Fatalf("GetTasksByFilter returned error: %v", err)
	}

	if len(tasks) != 2 {
		t.Fatalf("Expected 2 tasks, got %d", len(tasks))
	}
	if tasks[0].ID != "task-1" {
		t.Errorf("Expected first task 'task-1', got %q", tasks[0].ID)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

func TestGetTasksByFilter_EmptyFilter(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(rows)

	tasks, err := tx.GetTasksByFilter(context.Background(), TaskFilter{}, "")
	if err != nil {
		t.Fatalf("GetTasksByFilter returned error: %v", err)
	}
	if tasks != nil {
		t.Errorf("Expected nil tasks for empty result, got %v", tasks)
	}
}

func TestGetTasksByFilter_CompletedFilter(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow("completed-task", "test", nil, nil, now, now)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks .+ end_date IS NOT NULL`).WillReturnRows(rows)

	filter := TaskFilter{Completed: true}
	tasks, err := tx.GetTasksByFilter(context.Background(), filter, "")
	if err != nil {
		t.Fatalf("GetTasksByFilter returned error: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("Expected 1 task, got %d", len(tasks))
	}
	if tasks[0].ID != "completed-task" {
		t.Errorf("Expected ID 'completed-task', got %q", tasks[0].ID)
	}
}

func TestGetTasksByFilter_ByBehaviorType(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow("behavior-task", "test", nil, nil, now, nil)
	// The behavior filter uses a nested subquery JOIN
	mock.ExpectQuery(`SELECT .+ FROM async_tasks JOIN`).WillReturnRows(rows)

	filter := TaskFilter{BehaviorTypes: []string{"statuschangetimeout"}}
	tasks, err := tx.GetTasksByFilter(context.Background(), filter, "")
	if err != nil {
		t.Fatalf("GetTasksByFilter returned error: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("Expected 1 task, got %d", len(tasks))
	}
}

func TestGetTasksByFilter_ByStatus(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow("status-task", "test", nil, nil, now, nil)
	// Status filter joins async_task_status
	mock.ExpectQuery(`SELECT .+ FROM async_tasks JOIN async_task_status`).WillReturnRows(rows)

	filter := TaskFilter{Statuses: []string{"running"}}
	tasks, err := tx.GetTasksByFilter(context.Background(), filter, "")
	if err != nil {
		t.Fatalf("GetTasksByFilter returned error: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("Expected 1 task, got %d", len(tasks))
	}
}

func TestGetTasksByFilter_IncludeNullEnd(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow("null-end-task", "test", nil, nil, now, nil)
	// IncludeNullEnd with EndDateSince should produce OR end_date IS NULL
	mock.ExpectQuery(`SELECT .+ FROM async_tasks .+ OR end_date IS NULL`).WillReturnRows(rows)

	filter := TaskFilter{
		EndDateSince:   []time.Time{now.Add(-time.Hour)},
		IncludeNullEnd: true,
	}
	tasks, err := tx.GetTasksByFilter(context.Background(), filter, "")
	if err != nil {
		t.Fatalf("GetTasksByFilter returned error: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("Expected 1 task, got %d", len(tasks))
	}
}

// --- Tests for TaskFilter edge cases ---

func TestGetTasksByFilter_BehaviorTypeFilterSubqueryHasNoPlaceholders(t *testing.T) {
	// The BehaviorTypes filter builds a nested SELECT but uses `nested.ToSql()` and
	// discards the args (_, _, _ pattern). This means the nested subquery has no
	// placeholders — it's always `SELECT async_task_id, ARRAY_AGG(behavior_type) AS
	// behavior_types FROM async_task_behavior GROUP BY async_task_id`.
	// This is technically fine since there are no WHERE conditions in the nested query,
	// but it's worth documenting.
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(rows)

	filter := TaskFilter{BehaviorTypes: []string{"statuschangetimeout"}}
	_, err = tx.GetTasksByFilter(context.Background(), filter, "")
	if err != nil {
		t.Fatalf("GetTasksByFilter returned error: %v", err)
	}
}

// --- Tests for Commit/Rollback ---

func TestDBTx_Commit(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	mock.ExpectCommit()
	err = tx.Commit()
	if err != nil {
		t.Fatalf("Commit returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

func TestDBTx_Rollback(t *testing.T) {
	conn, mock := newTestDBConnection(t)
	defer func() { _ = conn.db.Close() }()

	mock.ExpectBegin()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	mock.ExpectRollback()
	err = tx.Rollback()
	if err != nil {
		t.Fatalf("Rollback returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
}

// --- Tests for model JSON serialization ---

func TestAsyncTask_JSONSerialization(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	task := model.AsyncTask{
		ID:        "json-test",
		Type:      "test-type",
		Username:  "user1",
		Data:      map[string]interface{}{"key": "value"},
		StartDate: &now,
		Behaviors: []model.AsyncTaskBehavior{
			{BehaviorType: "statuschangetimeout", Data: map[string]interface{}{"timeout": "1h"}},
		},
		Statuses: []model.AsyncTaskStatus{
			{Status: "running", Detail: "in progress", CreatedDate: now},
		},
		BehaviorsLoaded: true,
		StatusesLoaded:  true,
	}

	jsoned, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var roundTripped model.AsyncTask
	err = json.Unmarshal(jsoned, &roundTripped)
	if err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if roundTripped.ID != task.ID {
		t.Errorf("ID mismatch: got %q, want %q", roundTripped.ID, task.ID)
	}
	if roundTripped.Type != task.Type {
		t.Errorf("Type mismatch: got %q, want %q", roundTripped.Type, task.Type)
	}
	// BehaviorsLoaded and StatusesLoaded have json:"-" tags, should not survive round-trip
	if roundTripped.BehaviorsLoaded {
		t.Error("BehaviorsLoaded should be false after round-trip (json:\"-\" tag)")
	}
	if roundTripped.StatusesLoaded {
		t.Error("StatusesLoaded should be false after round-trip (json:\"-\" tag)")
	}
}

func TestAsyncTask_JSONOmitsEmptyBehaviorsAndStatuses(t *testing.T) {
	task := model.AsyncTask{
		ID:   "omit-test",
		Type: "test",
	}

	jsoned, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var raw map[string]interface{}
	err = json.Unmarshal(jsoned, &raw)
	if err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	// behaviors and statuses have omitempty tags
	if _, ok := raw["behaviors"]; ok {
		t.Error("Expected 'behaviors' to be omitted when empty")
	}
	if _, ok := raw["statuses"]; ok {
		t.Error("Expected 'statuses' to be omitted when empty")
	}
}

// TestAsyncTaskBehavior_JSONFieldNames verifies the JSON field name mapping
func TestAsyncTaskBehavior_JSONFieldNames(t *testing.T) {
	behavior := model.AsyncTaskBehavior{
		BehaviorType: "statuschangetimeout",
		Data:         map[string]interface{}{"key": "value"},
	}

	jsoned, err := json.Marshal(behavior)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var raw map[string]interface{}
	err = json.Unmarshal(jsoned, &raw)
	if err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	// BehaviorType maps to "type" in JSON
	if _, ok := raw["type"]; !ok {
		t.Error("Expected JSON field 'type' for BehaviorType")
	}
	if _, ok := raw["data"]; !ok {
		t.Error("Expected JSON field 'data'")
	}
}
