package database

import (
	"context"

	"database/sql"
	"github.com/Masterminds/squirrel"
	"github.com/cyverse-de/async-tasks/model"
	"github.com/cyverse-de/dbutil"
	"github.com/lib/pq"

	"errors"
	"github.com/sirupsen/logrus"
	"time"

	"encoding/json"
)

// DBConnection wraps a sql.DB
type DBConnection struct {
	db  *sql.DB
	log *logrus.Entry
}

// DBTx wraps a sql.Tx for this DB
type DBTx struct {
	tx  *sql.Tx
	log *logrus.Entry
}

// SetupDB initializes a DBConnection for the given dbURI
func SetupDB(dbURI string, log *logrus.Entry) (*DBConnection, error) {
	log.Info("Connecting to the database...")

	connector, err := dbutil.NewDefaultConnector("1m")
	if err != nil {
		return nil, err
	}

	db, err := connector.Connect("postgres", dbURI)
	if err != nil {
		return nil, err
	}

	log.Info("Created database connector")

	if err = db.Ping(); err != nil {
		return nil, err
	}

	log.Info("Successfully pinged the database")

	return &DBConnection{db: db, log: log}, nil
}

// Close defers to sql.DB Close()
func (d *DBConnection) Close() error {
	return d.db.Close()
}

// GetCount gets a count of async tasks in the DB
func (d *DBConnection) GetCount() (int64, error) {
	var res struct{ count int64 }
	err := squirrel.Select("COUNT(*)").From("async_tasks").RunWith(d.db).QueryRow().Scan(&res.count)
	if err != nil {
		return 0, err
	}
	return res.count, nil
}

// BeginTx starts a DBTx for the given DBConnection
func (d *DBConnection) BeginTx(ctx context.Context, opts *sql.TxOptions) (*DBTx, error) {
	tx, err := d.db.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &DBTx{tx: tx, log: d.log}, nil
}

// Rollback defers to underlying Rollback
func (t *DBTx) Rollback() error {
	return t.tx.Rollback()
}

// Commit defers to underlying Commit
func (t *DBTx) Commit() error {
	return t.tx.Commit()
}

var psql squirrel.StatementBuilderType = squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar)

var baseTaskSelect squirrel.SelectBuilder = psql.Select(
	"id", "type", "username", "data",
	"start_date at time zone (select current_setting('TIMEZONE'))",
	"end_date at time zone (select current_setting('TIMEZONE'))",
).From("async_tasks")

// GetBaseTask fetches a task from the database by ID (sans behaviors/statuses)
func (t *DBTx) GetBaseTask(id string, forUpdate bool) (*model.AsyncTask, error) {
	query := baseTaskSelect.Where("id::text = ?", id)

	if forUpdate {
		query = query.Suffix(" FOR UPDATE")
	}

	rows, err := query.RunWith(t.tx).Query()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dbtask model.DBTask
	for rows.Next() {
		if err := rows.Scan(&dbtask.ID, &dbtask.Type, &dbtask.Username, &dbtask.Data, &dbtask.StartDate, &dbtask.EndDate); err != nil {
			return nil, err
		}
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return makeTask(dbtask)
}

func makeTask(dbtask model.DBTask) (*model.AsyncTask, error) {
	var err error
	task := &model.AsyncTask{ID: dbtask.ID, Type: dbtask.Type}

	if dbtask.Username.Valid {
		task.Username = dbtask.Username.String
	}

	if dbtask.Data.Valid {
		jsonData := make(map[string]interface{})

		err = json.Unmarshal([]byte(dbtask.Data.String), &jsonData)
		if err != nil {
			return task, err
		}

		task.Data = jsonData
	}

	if dbtask.StartDate.Valid {
		task.StartDate = &dbtask.StartDate.Time
	}

	if dbtask.EndDate.Valid {
		task.EndDate = &dbtask.EndDate.Time
	}

	return task, nil
}

// DeleteTask deletes a task from the database by ID
func (t *DBTx) DeleteTask(id string) error {
	query := psql.Delete("async_tasks").Where("id::text = ?", id)

	_, err := query.RunWith(t.tx).Exec()
	if err != nil {
		return err
	}

	return nil
}

// CompleteTask marks a task as ended by setting the end date to now()
func (t *DBTx) CompleteTask(id string) error {
	query := psql.Update("async_tasks").Set("end_date", squirrel.Expr("now()")).Where("id::text = ?", id)

	rows, err := query.RunWith(t.tx).Query()
	if err != nil {
		return err
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}

	return nil
}

// GetTask fetches a task from the database by ID, including behaviors and statuses
func (t *DBTx) GetTask(id string, forUpdate bool) (*model.AsyncTask, error) {
	task, err := t.GetBaseTask(id, forUpdate)
	if err != nil {
		return task, err
	}

	behaviors, err := t.GetTaskBehaviors(id, forUpdate)
	if err != nil {
		return task, err
	}
	task.Behaviors = behaviors

	statuses, err := t.GetTaskStatuses(id, forUpdate)
	if err != nil {
		return task, err
	}
	task.Statuses = statuses

	return task, err
}

var baseTaskBehaviorSelect squirrel.SelectBuilder = psql.Select(
	"behavior_type", "data",
).From("async_task_behavior")

// GetTaskBehaviors fetches a task's set of behaviors from the DB by ID
func (t *DBTx) GetTaskBehaviors(id string, forUpdate bool) ([]model.AsyncTaskBehavior, error) {
	query := baseTaskBehaviorSelect.Where("async_task_id::text = ?", id)

	if forUpdate {
		query = query.Suffix(" FOR UPDATE")
	}

	rows, err := query.RunWith(t.tx).Query()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var behaviors []model.AsyncTaskBehavior
	for rows.Next() {
		var dbbehavior model.DBTaskBehavior
		if err := rows.Scan(&dbbehavior.BehaviorType, &dbbehavior.Data); err != nil {
			return nil, err
		}

		behavior := model.AsyncTaskBehavior{BehaviorType: dbbehavior.BehaviorType}
		if dbbehavior.Data.Valid {
			jsonData := make(map[string]interface{})

			err = json.Unmarshal([]byte(dbbehavior.Data.String), &jsonData)
			if err != nil {
				return behaviors, err
			}

			behavior.Data = jsonData
		}

		behaviors = append(behaviors, behavior)
	}

	return behaviors, nil
}

var baseTaskStatusSelect squirrel.SelectBuilder = psql.Select(
	"status", "detail", "created_date at time zone (select current_setting('TIMEZONE'))",
).From("async_task_status")

// GetTaskStatuses fetches a tasks's list of statuses from the DB by ID, ordered by creation date
func (t *DBTx) GetTaskStatuses(id string, forUpdate bool) ([]model.AsyncTaskStatus, error) {
	query := baseTaskStatusSelect.Where("async_task_id::text = ?", id).OrderBy("created_date ASC")

	if forUpdate {
		query = query.Suffix(" FOR UPDATE")
	}

	rows, err := query.RunWith(t.tx).Query()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var statuses []model.AsyncTaskStatus
	for rows.Next() {
		var dbstatus model.DBTaskStatus
		if err := rows.Scan(&dbstatus.Status, &dbstatus.Detail, &dbstatus.CreatedDate); err != nil {
			return nil, err
		}

		status := model.AsyncTaskStatus{Status: dbstatus.Status, CreatedDate: dbstatus.CreatedDate}

		if dbstatus.Detail.Valid {
			status.Detail = dbstatus.Detail.String
		}

		statuses = append(statuses, status)
	}

	return statuses, nil
}

type TaskFilter struct {
	IDs             []string
	Types           []string
	Usernames       []string
	StartDateSince  []time.Time
	StartDateBefore []time.Time
	EndDateSince    []time.Time
	EndDateBefore   []time.Time
	IncludeNullEnd  bool
	Statuses        []string
	BehaviorTypes   []string
}

// GetTasksByFilter fetches a set of tasks by a set of provided filters
func (t *DBTx) GetTasksByFilter(filters TaskFilter, order string) ([]model.AsyncTask, error) {
	var tasks []model.AsyncTask

	query := baseTaskSelect

	if len(filters.IDs) > 0 {
		query = query.Where("id::text = ANY(?)", pq.Array(filters.IDs))
	}

	if len(filters.Types) > 0 {
		query = query.Where("type = ANY(?)", pq.Array(filters.Types))
	}

	if len(filters.Usernames) > 0 {
		query = query.Where("username = ANY(?)", pq.Array(filters.Usernames))
	}

	if len(filters.StartDateSince) > 0 {
		if len(filters.StartDateSince) > 1 {
			t.log.Warn("More than one start_date_since filter is unsupported. Only the oldest date will be considered.")
		}
		query = query.Where("start_date > ANY(?)", pq.Array(filters.StartDateSince))
	}

	if len(filters.StartDateBefore) > 0 {
		if len(filters.StartDateBefore) > 1 {
			t.log.Warn("More than one start_date_before filter is unsupported. Only the newest date will be considered.")
		}
		query = query.Where("start_date < ANY(?)", pq.Array(filters.StartDateBefore))
	}

	if len(filters.EndDateSince) > 0 {
		if len(filters.EndDateSince) > 1 {
			t.log.Warn("More than one end_date_since filter is unsupported. Only the oldest date will be considered.")
		}
		if filters.IncludeNullEnd {
			query = query.Where("(end_date > ANY(?) OR end_date IS NULL)", pq.Array(filters.EndDateSince))
		} else {
			query = query.Where("end_date > ANY(?)", pq.Array(filters.EndDateSince))
		}
	}

	if len(filters.EndDateBefore) > 0 {
		if len(filters.EndDateBefore) > 1 {
			t.log.Warn("More than one end_date_before filter is unsupported. Only the newest date will be considered.")
		}
		query = query.Where("end_date < ANY(?)", pq.Array(filters.EndDateBefore))
	}

	if len(filters.Statuses) > 0 {
		query = query.Join("async_task_status ON (async_task_status.async_task_id = async_tasks.id AND async_task_status.created_date = (select max(created_date) FROM async_task_status WHERE async_task_id = async_tasks.id))").Where("status = ANY(?)", pq.Array(filters.Statuses))
	}

	if len(filters.BehaviorTypes) > 0 {
		nested := psql.Select("async_task_id", "ARRAY_AGG(behavior_type) AS behavior_types").From("async_task_behavior").GroupBy("async_task_id")
		nestedJoinSelect, _, _ := nested.ToSql()
		query = query.Join("(" + nestedJoinSelect + ") AS behaviors ON (behaviors.async_task_id = async_tasks.id)").Where(`behavior_types && ?`, pq.Array(filters.BehaviorTypes))
	}

	rows, err := query.RunWith(t.tx).Query()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var dbtask model.DBTask
		if err := rows.Scan(&dbtask.ID, &dbtask.Type, &dbtask.Username, &dbtask.Data, &dbtask.StartDate, &dbtask.EndDate); err != nil {
			return nil, err
		}

		task, err := makeTask(dbtask)
		if err != nil {
			return nil, err
		}

		tasks = append(tasks, *task)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return tasks, nil
}

// InsertTask inserts a provided AsyncTask into the DB and returns the task's generated ID as a string
func (t *DBTx) InsertTask(task model.AsyncTask) (string, error) {
	if task.Type == "" {
		return "", errors.New("Task type must be provided")
	}

	query := psql.Insert("async_tasks").Suffix("RETURNING id::text")

	var columns []string
	var args []interface{}

	columns = append(columns, "type")
	args = append(args, task.Type)

	if task.Username != "" {
		columns = append(columns, "username")
		args = append(args, task.Username)
	}

	if len(task.Data) > 0 {
		jsoned, err := json.Marshal(task.Data)
		if err != nil {
			return "", err
		}

		columns = append(columns, "data")
		args = append(args, jsoned)
	}

	if task.StartDate == nil || task.StartDate.IsZero() {
		columns = append(columns, "start_date")
		args = append(args, squirrel.Expr("now()"))
	} else {
		columns = append(columns, "start_date")
		args = append(args, squirrel.Expr("? AT TIME ZONE (select current_setting('TIMEZONE'))", task.StartDate))
	}

	if task.EndDate != nil && !task.EndDate.IsZero() {
		columns = append(columns, "end_date")
		args = append(args, squirrel.Expr("? AT TIME ZONE (select current_setting('TIMEZONE'))", task.EndDate))
	}

	query = query.Columns(columns...).Values(args...)

	rows, err := query.RunWith(t.tx).Query()
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var id string
	for rows.Next() {
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
	}

	if err = rows.Err(); err != nil {
		return "", err
	}

	// This will only insert one status, we're assuming only one gets to this point (enforced in the route, not here)
	if len(task.Statuses) > 0 {
		err = t.InsertTaskStatus(task.Statuses[0], id)
		if err != nil {
			return "", err
		}
	}

	// this could end up poorly performant if we want to insert a bunch at once
	for _, behavior := range task.Behaviors {
		err = t.InsertTaskBehavior(behavior, id)
		if err != nil {
			return "", err
		}
	}

	return id, nil
}

// InsertTaskStatus inserts a provided AsyncTaskStatus into the DB for the provided async task ID
func (t *DBTx) InsertTaskStatus(status model.AsyncTaskStatus, taskID string) error {
	if status.Status == "" {
		return errors.New("Status type must be provided")
	}

	query := psql.Insert("async_task_status").Columns("async_task_id", "status", "detail", "created_date")

	if status.CreatedDate.IsZero() {
		query = query.Values(taskID, status.Status, status.Detail, squirrel.Expr("now()"))
	} else {
		query = query.Values(taskID, status.Status, status.Detail, squirrel.Expr("? AT TIME ZONE (select current_setting('TIMEZONE'))", status.CreatedDate))
	}

	rows, err := query.RunWith(t.tx).Query()
	if err != nil {
		return err
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}

	return nil
}

// InsertTaskBehavior inserts a provided AsyncTaskBehavior into the DB for the provided async task ID
func (t *DBTx) InsertTaskBehavior(behavior model.AsyncTaskBehavior, taskID string) error {
	if behavior.BehaviorType == "" {
		return errors.New("Behavior type must be provided")
	}

	query := psql.Insert("async_task_behavior")

	if len(behavior.Data) > 0 {
		jsoned, err := json.Marshal(behavior.Data)
		if err != nil {
			return err
		}
		query = query.Columns("async_task_id", "behavior_type", "data").Values(taskID, behavior.BehaviorType, jsoned)
	} else {
		query = query.Columns("async_task_id", "behavior_type").Values(taskID, behavior.BehaviorType)
	}

	rows, err := query.RunWith(t.tx).Query()
	if err != nil {
		return err
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}

	return nil
}
