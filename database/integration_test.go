package database

import (
	"context"
	"os"
	"testing"

	"github.com/sirupsen/logrus"
)

// Integration tests run against a real Postgres database containing the
// async-tasks schema. They are skipped unless ASYNC_TASKS_TEST_DB_URI is set.
func integrationDB(t *testing.T) *DBConnection {
	t.Helper()
	uri := os.Getenv("ASYNC_TASKS_TEST_DB_URI")
	if uri == "" {
		t.Skip("ASYNC_TASKS_TEST_DB_URI not set")
	}
	db, err := SetupDB(uri, logrus.WithField("test", true))
	if err != nil {
		t.Fatalf("SetupDB failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func mustExec(t *testing.T, db *DBConnection, query string, args ...any) {
	t.Helper()
	if _, err := db.db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q failed: %v", query, err)
	}
}

// seedTask inserts a task with a statuschangetimeout behavior and a status
// history, returning its ID.
func seedTask(t *testing.T, db *DBConnection, ended bool, rules string, statuses ...string) string {
	t.Helper()
	var id string
	endDate := "NULL"
	if ended {
		endDate = "now()"
	}
	err := db.db.QueryRow(`INSERT INTO async_tasks (type, start_date, end_date) VALUES ('integration-test', now() - interval '1 day', ` + endDate + `) RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatalf("insert task: %v", err)
	}
	mustExec(t, db, `INSERT INTO async_task_behavior (async_task_id, behavior_type, data) VALUES ($1, 'statuschangetimeout', $2)`, id, rules)
	for i, s := range statuses {
		mustExec(t, db, `INSERT INTO async_task_status (async_task_id, status, created_date) VALUES ($1, $2, now() - interval '1 hour' + $3 * interval '1 minute')`, id, s, i)
	}
	return id
}

func behaviorData(t *testing.T, db *DBConnection, id string) string {
	t.Helper()
	var data string
	if err := db.db.QueryRow(`SELECT data::jsonb::text FROM async_task_behavior WHERE async_task_id = $1`, id).Scan(&data); err != nil {
		t.Fatalf("read behavior: %v", err)
	}
	return data
}

func isEnded(t *testing.T, db *DBConnection, id string) bool {
	t.Helper()
	var ended bool
	if err := db.db.QueryRow(`SELECT end_date IS NOT NULL FROM async_tasks WHERE id = $1`, id).Scan(&ended); err != nil {
		t.Fatalf("read task: %v", err)
	}
	return ended
}

func TestIntegration_CompleteLegacyStatusChangeTimeoutTasks(t *testing.T) {
	db := integrationDB(t)
	ctx := context.Background()
	t.Cleanup(func() { mustExec(t, db, `DELETE FROM async_tasks WHERE type = 'integration-test'`) })

	missing := seedTask(t, db, false,
		`{"statuses":[{"start_status":"registered","end_status":"detected-stalled","timeout":"10m"},`+
			`{"start_status":"running","end_status":"detected-stalled","timeout":"10m","complete":false}]}`,
		"registered")
	closed := seedTask(t, db, true,
		`{"statuses":[{"start_status":"running","end_status":"detected-stalled","timeout":"10m"}]}`,
		"running", "completed")
	stalledNoFlag := seedTask(t, db, false,
		`{"statuses":[{"start_status":"running","end_status":"detected-stalled","timeout":"10m"}]}`,
		"running", "detected-stalled")
	stalledExplicitFalse := seedTask(t, db, false,
		`{"statuses":[{"start_status":"running","end_status":"detected-stalled","timeout":"10m","complete":false}]}`,
		"running", "detected-stalled")
	stillRunning := seedTask(t, db, false,
		`{"statuses":[{"start_status":"running","end_status":"detected-stalled","timeout":"10m","complete":true}]}`,
		"running")
	malformed := seedTask(t, db, false, `{"statuses":"not-an-array"}`, "registered")

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	updated, err := tx.AddMissingCompleteFlags(ctx)
	if err != nil {
		t.Fatalf("AddMissingCompleteFlags: %v", err)
	}
	completed, err := tx.CompleteStalledTasks(ctx)
	if err != nil {
		t.Fatalf("CompleteStalledTasks: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Rules missing the flag get complete:true; explicit values are kept.
	want := `{"statuses": [{"timeout": "10m", "complete": true, "end_status": "detected-stalled", "start_status": "registered"}, ` +
		`{"timeout": "10m", "complete": false, "end_status": "detected-stalled", "start_status": "running"}]}`
	if got := behaviorData(t, db, missing); got != want {
		t.Errorf("behavior with a missing flag:\n got  %s\n want %s", got, want)
	}
	// Ended tasks are left alone.
	if got := behaviorData(t, db, closed); got != `{"statuses": [{"timeout": "10m", "end_status": "detected-stalled", "start_status": "running"}]}` {
		t.Errorf("ended task's behavior was modified: %s", got)
	}
	// Malformed data is left alone.
	if got := behaviorData(t, db, malformed); got != `{"statuses": "not-an-array"}` {
		t.Errorf("malformed behavior was modified: %s", got)
	}

	if !isEnded(t, db, stalledNoFlag) {
		t.Error("open task already at its end status (no complete flag) should be completed")
	}
	if isEnded(t, db, stalledExplicitFalse) {
		t.Error("task whose rule says complete:false must stay open")
	}
	if isEnded(t, db, stillRunning) {
		t.Error("task that has not reached an end status must stay open")
	}
	if isEnded(t, db, missing) {
		t.Error("task that has not reached an end status must stay open")
	}

	// missing + stalledNoFlag had rules without the flag; stalledNoFlag is the only one completed.
	if updated != 2 {
		t.Errorf("expected 2 behaviors updated, got %d", updated)
	}
	if completed != 1 {
		t.Errorf("expected 1 task completed, got %d", completed)
	}
}
