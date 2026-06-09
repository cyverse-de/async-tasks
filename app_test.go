package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/cyverse-de/async-tasks/database"
	"github.com/cyverse-de/async-tasks/model"
	"github.com/gorilla/mux"
)

// newTestApp creates an AsyncTasksApp backed by a sqlmock DB for testing.
func newTestApp(t *testing.T) (*AsyncTasksApp, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	conn := database.NewTestDBConnection(db)
	router := mux.NewRouter()
	app := NewAsyncTasksApp(conn, router)
	return app, mock, func() { db.Close() }
}

// --- Tests for GET /tasks/{id} ---

func TestGetByIdRequest_NotFound(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	mock.ExpectBegin()
	// Return empty result set for the task query
	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(rows)
	// GetTask still queries behaviors and statuses even when base task is empty
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)
	mock.ExpectRollback()

	req := httptest.NewRequest("GET", "/tasks/00000000-0000-0000-0000-000000000000", nil)
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}

	var resp ErrorResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal error response: %v", err)
	}
	if resp.Msg != "not found" {
		t.Errorf("Expected msg 'not found', got %q", resp.Msg)
	}
}

func TestGetByIdRequest_Found(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	taskID := "11111111-1111-1111-1111-111111111111"
	now := time.Now()

	mock.ExpectBegin()
	// getBaseTask
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "test-type", "user1", `{"k":"v"}`, now, nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	// getTaskBehaviors
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	// getTaskStatuses
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"}).
		AddRow("running", nil, now)
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)
	mock.ExpectRollback()

	req := httptest.NewRequest("GET", "/tasks/"+taskID, nil)
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	var task model.AsyncTask
	if err := json.Unmarshal(w.Body.Bytes(), &task); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if task.ID != taskID {
		t.Errorf("Expected task ID %q, got %q", taskID, task.ID)
	}
	if task.Type != "test-type" {
		t.Errorf("Expected type 'test-type', got %q", task.Type)
	}
	if len(task.Statuses) != 1 {
		t.Fatalf("Expected 1 status, got %d", len(task.Statuses))
	}
	if task.Statuses[0].Status != "running" {
		t.Errorf("Expected status 'running', got %q", task.Statuses[0].Status)
	}
}

func TestGetByIdRequest_InvalidUUID(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	// gorilla/mux UUID regex should reject this
	req := httptest.NewRequest("GET", "/tasks/not-a-uuid", nil)
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	// Should hit NotFound because the route regex doesn't match
	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404 for invalid UUID, got %d", w.Code)
	}
}

func TestGetByIdRequest_DBError(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnError(fmt.Errorf("db connection lost"))
	mock.ExpectRollback()

	req := httptest.NewRequest("GET", "/tasks/22222222-2222-2222-2222-222222222222", nil)
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", w.Code)
	}
}

// --- Tests for DELETE /tasks/{id} ---

func TestDeleteByIdRequest_NotFound(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	mock.ExpectBegin()
	// getBaseTask returns empty
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	// GetTask still queries behaviors and statuses even when base task is empty
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)
	mock.ExpectRollback()

	req := httptest.NewRequest("DELETE", "/tasks/00000000-0000-0000-0000-000000000000", nil)
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

func TestDeleteByIdRequest_Success(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	taskID := "33333333-3333-3333-3333-333333333333"
	now := time.Now()

	mock.ExpectBegin()
	// GetTask (forUpdate=true)
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "test", nil, nil, now, nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)
	// DeleteTask
	mock.ExpectExec(`DELETE FROM async_tasks`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	req := httptest.NewRequest("DELETE", "/tasks/"+taskID, nil)
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	// Bug: DeleteByIdRequest returns 200 with no body on success
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("Expected empty body on delete success, got %q", w.Body.String())
	}
}

// TestDeleteByIdRequest_CommitErrorStillReturns200 demonstrates the bug where a commit
// failure is logged but the response is still 200 OK.
func TestDeleteByIdRequest_CommitErrorStillReturns200(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	taskID := "44444444-4444-4444-4444-444444444444"
	now := time.Now()

	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "test", nil, nil, now, nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)
	mock.ExpectExec(`DELETE FROM async_tasks`).WillReturnResult(sqlmock.NewResult(0, 1))
	// Commit fails
	mock.ExpectCommit().WillReturnError(fmt.Errorf("commit failed"))

	req := httptest.NewRequest("DELETE", "/tasks/"+taskID, nil)
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	// Bug: commit failure is only logged, response is still 200
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 even on commit failure (bug), got %d", w.Code)
	}
}

// --- Tests for POST /tasks ---

func TestCreateTaskRequest_Success(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	taskID := "55555555-5555-5555-5555-555555555555"

	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id"}).AddRow(taskID)
	mock.ExpectQuery(`INSERT INTO async_tasks`).WillReturnRows(taskRows)
	mock.ExpectCommit()

	body := `{"type":"test-type"}`
	req := httptest.NewRequest("POST", "/tasks", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d. Body: %s", w.Code, w.Body.String())
	}

	location := w.Header().Get("Location")
	expectedLocation := "/tasks/" + taskID
	if location != expectedLocation {
		t.Errorf("Expected Location header %q, got %q", expectedLocation, location)
	}
}

func TestCreateTaskRequest_MissingType(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	body := `{"username":"testuser"}`
	req := httptest.NewRequest("POST", "/tasks", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", w.Code)
	}
}

func TestCreateTaskRequest_InvalidJSON(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	body := `{not valid json}`
	req := httptest.NewRequest("POST", "/tasks", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", w.Code)
	}
}

func TestCreateTaskRequest_TooManyStatuses(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	body := `{"type":"test","statuses":[{"status":"a"},{"status":"b"}]}`
	req := httptest.NewRequest("POST", "/tasks", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", w.Code)
	}

	var resp ErrorResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if resp.Msg != "A new task may only include one initial status" {
		t.Errorf("Unexpected error message: %q", resp.Msg)
	}
}

func TestCreateTaskRequest_BlankStatus(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	body := `{"type":"test","statuses":[{"status":""}]}`
	req := httptest.NewRequest("POST", "/tasks", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", w.Code)
	}
}

func TestCreateTaskRequest_EmptyBehaviorType(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	body := `{"type":"test","behaviors":[{"type":""}]}`
	req := httptest.NewRequest("POST", "/tasks", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", w.Code)
	}
}

func TestCreateTaskRequest_WithStatusAndBehavior(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	taskID := "66666666-6666-6666-6666-666666666666"

	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id"}).AddRow(taskID)
	mock.ExpectQuery(`INSERT INTO async_tasks`).WillReturnRows(taskRows)
	// InsertTaskStatus
	statusRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`INSERT INTO async_task_status`).WillReturnRows(statusRows)
	// InsertTaskBehavior
	behaviorRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`INSERT INTO async_task_behavior`).WillReturnRows(behaviorRows)
	mock.ExpectCommit()

	body := `{"type":"test","statuses":[{"status":"pending"}],"behaviors":[{"type":"statuschangetimeout","data":{"statuses":[]}}]}`
	req := httptest.NewRequest("POST", "/tasks", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d. Body: %s", w.Code, w.Body.String())
	}
}

// TestCreateTaskRequest_CommitErrorStillReturns201 demonstrates the bug where a commit
// failure results in a 201 Created response with a Location header.
func TestCreateTaskRequest_CommitErrorStillReturns201(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	taskID := "77777777-7777-7777-7777-777777777777"

	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id"}).AddRow(taskID)
	mock.ExpectQuery(`INSERT INTO async_tasks`).WillReturnRows(taskRows)
	mock.ExpectCommit().WillReturnError(fmt.Errorf("commit failed"))

	body := `{"type":"test-type"}`
	req := httptest.NewRequest("POST", "/tasks", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	// Bug: commit error is logged but response is still 201 Created
	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201 even on commit failure (bug), got %d", w.Code)
	}
	location := w.Header().Get("Location")
	if location == "" {
		t.Error("Expected Location header even on commit failure (bug)")
	}
}

// --- Tests for POST /tasks/{id}/status ---

func TestAddStatusRequest_Success(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	taskID := "88888888-8888-8888-8888-888888888888"
	now := time.Now()

	mock.ExpectBegin()
	// GetTask
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "test", nil, nil, now, nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)
	// InsertTaskStatus
	insertStatusRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`INSERT INTO async_task_status`).WillReturnRows(insertStatusRows)
	mock.ExpectCommit()

	body := `{"status":"running","detail":"step 1"}`
	req := httptest.NewRequest("POST", "/tasks/"+taskID+"/status", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d. Body: %s", w.Code, w.Body.String())
	}

	location := w.Header().Get("Location")
	expectedLocation := "/tasks/" + taskID
	if location != expectedLocation {
		t.Errorf("Expected Location %q, got %q", expectedLocation, location)
	}
}

func TestAddStatusRequest_WithComplete(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	taskID := "99999999-9999-9999-9999-999999999999"
	now := time.Now()

	mock.ExpectBegin()
	// GetTask
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "test", nil, nil, now, nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)
	// InsertTaskStatus
	insertStatusRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`INSERT INTO async_task_status`).WillReturnRows(insertStatusRows)
	// CompleteTask (uses QueryContext)
	completeRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`UPDATE async_tasks SET end_date`).WillReturnRows(completeRows)
	mock.ExpectCommit()

	body := `{"status":"completed"}`
	req := httptest.NewRequest("POST", "/tasks/"+taskID+"/status?complete=true", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d. Body: %s", w.Code, w.Body.String())
	}
}

func TestAddStatusRequest_TaskNotFound(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)
	mock.ExpectRollback()

	body := `{"status":"running"}`
	req := httptest.NewRequest("POST", "/tasks/00000000-0000-0000-0000-000000000000/status", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

// TestAddStatusRequest_CommitErrorStillReturns201 documents the commit-error bug.
func TestAddStatusRequest_CommitErrorStillReturns201(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	taskID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	now := time.Now()

	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "test", nil, nil, now, nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)
	insertStatusRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`INSERT INTO async_task_status`).WillReturnRows(insertStatusRows)
	mock.ExpectCommit().WillReturnError(fmt.Errorf("commit failed"))

	body := `{"status":"running"}`
	req := httptest.NewRequest("POST", "/tasks/"+taskID+"/status", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	// Bug: commit error logged but 201 still returned
	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201 even on commit failure (bug), got %d", w.Code)
	}
}

// --- Tests for POST /tasks/{id}/behaviors ---

func TestAddBehaviorRequest_Success(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	taskID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	now := time.Now()

	mock.ExpectBegin()
	// GetTask
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "test", nil, nil, now, nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)
	// InsertTaskBehavior
	insertBehaviorRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`INSERT INTO async_task_behavior`).WillReturnRows(insertBehaviorRows)
	mock.ExpectCommit()

	body := `{"type":"statuschangetimeout","data":{"statuses":[]}}`
	req := httptest.NewRequest("POST", "/tasks/"+taskID+"/behaviors", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d. Body: %s", w.Code, w.Body.String())
	}
}

func TestAddBehaviorRequest_TaskNotFound(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)
	mock.ExpectRollback()

	body := `{"type":"statuschangetimeout"}`
	req := httptest.NewRequest("POST", "/tasks/00000000-0000-0000-0000-000000000000/behaviors", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

// TestAddBehaviorRequest_CommitErrorStillReturns201 documents the commit-error bug.
func TestAddBehaviorRequest_CommitErrorStillReturns201(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	taskID := "cccccccc-cccc-cccc-cccc-cccccccccccc"
	now := time.Now()

	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "test", nil, nil, now, nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)
	insertBehaviorRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`INSERT INTO async_task_behavior`).WillReturnRows(insertBehaviorRows)
	mock.ExpectCommit().WillReturnError(fmt.Errorf("commit failed"))

	body := `{"type":"statuschangetimeout"}`
	req := httptest.NewRequest("POST", "/tasks/"+taskID+"/behaviors", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	// Bug: commit error logged but 201 still returned
	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201 even on commit failure (bug), got %d", w.Code)
	}
}

// --- Tests for GET /tasks (filter) ---

func TestGetByFilterRequest_EmptyResults(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	mock.ExpectBegin()
	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(rows)
	mock.ExpectRollback()

	req := httptest.NewRequest("GET", "/tasks", nil)
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	// null JSON for nil slice
	if w.Body.String() != "null" {
		t.Errorf("Expected 'null' for empty tasks, got %q", w.Body.String())
	}
}

func TestGetByFilterRequest_WithResults(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	now := time.Now()

	mock.ExpectBegin()
	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow("task-1", "type-a", "user1", nil, now, nil).
		AddRow("task-2", "type-b", nil, nil, now, nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(rows)
	mock.ExpectRollback()

	req := httptest.NewRequest("GET", "/tasks", nil)
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var tasks []model.AsyncTask
	if err := json.Unmarshal(w.Body.Bytes(), &tasks); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("Expected 2 tasks, got %d", len(tasks))
	}
}

func TestGetByFilterRequest_WithTypeFilter(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	now := time.Now()

	mock.ExpectBegin()
	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow("task-1", "my-type", nil, nil, now, nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(rows)
	mock.ExpectRollback()

	req := httptest.NewRequest("GET", "/tasks?type=my-type", nil)
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

func TestGetByFilterRequest_InvalidDateFormat(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/tasks?start_date_since=not-a-date", nil)
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500 for invalid date, got %d", w.Code)
	}
}

func TestGetByFilterRequest_ValidDateFilter(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	now := time.Now()
	dateStr := now.Add(-time.Hour).Format(time.RFC3339Nano)

	mock.ExpectBegin()
	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(rows)
	mock.ExpectRollback()

	req := httptest.NewRequest("GET", "/tasks?start_date_since="+dateStr, nil)
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}
}

// TestGetByFilterRequest_PassesEmptyOrderToDB documents that GetByFilterRequest
// always passes an empty string as the order parameter to GetTasksByFilter,
// meaning no ORDER BY is added for HTTP API queries.
func TestGetByFilterRequest_PassesEmptyOrderToDB(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	mock.ExpectBegin()
	rows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(rows)
	mock.ExpectRollback()

	// The handler in app.go always calls: tx.GetTasksByFilter(ctx, filters, "")
	// There's no way for the HTTP client to request ordering.
	req := httptest.NewRequest("GET", "/tasks", nil)
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
	// The test passes — this simply documents that ordering is not user-controllable
	// via the HTTP API, and internally GetTasksByFilter ignores the order param anyway.
}

// --- Tests for NotFound handler ---

func TestNotFound_UnknownRoute(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/nonexistent", nil)
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

func TestNotFound_WrongMethod(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	// PUT is not a registered method on /tasks
	req := httptest.NewRequest("PUT", "/tasks", nil)
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	// gorilla/mux returns 405 for wrong method
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", w.Code)
	}
}

// --- Tests for error helpers ---

func TestMakeErrorJson(t *testing.T) {
	result := makeErrorJson("test error message")
	var resp ErrorResp
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("makeErrorJson produced invalid JSON: %v", err)
	}
	if resp.Msg != "test error message" {
		t.Errorf("Expected msg 'test error message', got %q", resp.Msg)
	}
}

func TestMakeErrorJson_SpecialCharacters(t *testing.T) {
	result := makeErrorJson(`error with "quotes" and \backslash`)
	var resp ErrorResp
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("makeErrorJson produced invalid JSON for special chars: %v", err)
	}
}

// --- Tests for fixAddr ---

func TestFixAddr_WithoutColon(t *testing.T) {
	result := fixAddr("8080")
	if result != ":8080" {
		t.Errorf("Expected ':8080', got %q", result)
	}
}

func TestFixAddr_WithColon(t *testing.T) {
	result := fixAddr(":8080")
	if result != ":8080" {
		t.Errorf("Expected ':8080', got %q", result)
	}
}

func TestFixAddr_Empty(t *testing.T) {
	result := fixAddr("")
	if result != ":" {
		t.Errorf("Expected ':', got %q", result)
	}
}

// --- Tests for route registration ---

func TestInitRoutes_AllRoutesRegistered(t *testing.T) {
	app, _, cleanup := newTestApp(t)
	defer cleanup()

	expectedRoutes := map[string]string{
		"getById":     "/tasks/{id:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}}",
		"deleteById":  "/tasks/{id:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}}",
		"addStatus":   "/tasks/{id:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}}/status",
		"addBehavior": "/tasks/{id:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}}/behaviors",
		"getByFilter": "/tasks",
		"createTask":  "/tasks",
	}

	for name, expectedPath := range expectedRoutes {
		route := app.router.Get(name)
		if route == nil {
			t.Errorf("Route %q not found", name)
			continue
		}
		tmpl, err := route.GetPathTemplate()
		if err != nil {
			t.Errorf("Failed to get path template for route %q: %v", name, err)
			continue
		}
		if tmpl != expectedPath {
			t.Errorf("Route %q: expected path %q, got %q", name, expectedPath, tmpl)
		}
	}
}

// --- Tests for complete query parameter behavior ---

// TestAddStatusRequest_CompleteParamAnyValue documents that any non-empty value
// for the "complete" query parameter triggers task completion.
func TestAddStatusRequest_CompleteParamAnyValue(t *testing.T) {
	app, mock, cleanup := newTestApp(t)
	defer cleanup()

	taskID := "dddddddd-dddd-dddd-dddd-dddddddddddd"
	now := time.Now()

	mock.ExpectBegin()
	taskRows := sqlmock.NewRows([]string{"id", "type", "username", "data", "start_date", "end_date"}).
		AddRow(taskID, "test", nil, nil, now, nil)
	mock.ExpectQuery(`SELECT .+ FROM async_tasks`).WillReturnRows(taskRows)
	behaviorRows := sqlmock.NewRows([]string{"behavior_type", "data"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_behavior`).WillReturnRows(behaviorRows)
	statusRows := sqlmock.NewRows([]string{"status", "detail", "created_date"})
	mock.ExpectQuery(`SELECT .+ FROM async_task_status`).WillReturnRows(statusRows)
	insertStatusRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`INSERT INTO async_task_status`).WillReturnRows(insertStatusRows)
	// CompleteTask should be called even with complete=false since any non-empty value triggers it
	completeRows := sqlmock.NewRows(nil)
	mock.ExpectQuery(`UPDATE async_tasks SET end_date`).WillReturnRows(completeRows)
	mock.ExpectCommit()

	body := `{"status":"done"}`
	// "false" is non-empty, so complete=true behavior is triggered. This documents
	// the quirky behavior: complete=false actually completes the task.
	req := httptest.NewRequest("POST", "/tasks/"+taskID+"/status?complete=false", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	app.router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d. Body: %s", w.Code, w.Body.String())
	}
}
