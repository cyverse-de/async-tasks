package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cyverse-de/async-tasks/database"
	"github.com/cyverse-de/async-tasks/model"
	"github.com/gorilla/mux"
	"io"
	"io/ioutil"
	"net/http"
	"time"
)

const hundredMiB = 104857600

type AsyncTasksApp struct {
	db     *database.DBConnection
	router *mux.Router
}

func NewAsyncTasksApp(db *database.DBConnection, router *mux.Router) *AsyncTasksApp {
	app := &AsyncTasksApp{
		db:     db,
		router: router,
	}

	app.InitRoutes()

	return app
}

func (a *AsyncTasksApp) InitRoutes() {
	a.router.NotFoundHandler = http.HandlerFunc(a.NotFound)
	a.router.HandleFunc("/tasks/{id:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}}", a.GetByIdRequest).Methods("GET").Name("getById")
	a.router.HandleFunc("/tasks/{id:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}}", a.DeleteByIdRequest).Methods("DELETE").Name("deleteById")
	a.router.HandleFunc("/tasks/{id:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}}/status", a.AddStatusRequest).Methods("POST").Name("addStatus")
	a.router.HandleFunc("/tasks/{id:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}}/behaviors", a.AddBehaviorRequest).Methods("POST").Name("addBehavior")

	a.router.HandleFunc("/tasks", a.GetByFilterRequest).Methods("GET").Name("getByFilter")
	a.router.HandleFunc("/tasks", a.CreateTaskRequest).Methods("POST").Name("createTask")
}

func (a *AsyncTasksApp) NotFound(writer http.ResponseWriter, r *http.Request) {
	notFound(writer, fmt.Sprintf("no endpoint found at %s %s", r.Method, r.URL.Path))
}

func (a *AsyncTasksApp) GetByIdRequest(writer http.ResponseWriter, r *http.Request) {
	var (
		id string
		ok bool
		v  = mux.Vars(r)
	)

	if id, ok = v["id"]; !ok {
		badRequest(writer, "No ID in URL")
		return
	}

	log.Infof("Fetching async task %s", id)

	tx, err := a.db.BeginTx(context.TODO(), nil)
	if err != nil {
		errored(writer, err.Error())
		return
	}
	defer tx.Rollback()

	task, err := tx.GetTask(id)
	if err != nil {
		errored(writer, err.Error())
		return
	}

	if task.ID == "" {
		notFound(writer, "not found")
		return
	}

	log.Info(task)

	jsoned, err := json.Marshal(task)
	if err != nil {
		errored(writer, err.Error())
		return
	}

	writer.Write(jsoned)

	return
}

func (a *AsyncTasksApp) DeleteByIdRequest(writer http.ResponseWriter, r *http.Request) {
	var (
		id string
		ok bool
		v  = mux.Vars(r)
	)

	if id, ok = v["id"]; !ok {
		badRequest(writer, "No ID in URL")
		return
	}

	log.Infof("Fetching async task %s", id)

	tx, err := a.db.BeginTx(context.TODO(), nil)
	if err != nil {
		errored(writer, err.Error())
		return
	}
	defer tx.Rollback()

	task, err := tx.GetTask(id)
	if err != nil {
		errored(writer, err.Error())
		return
	}

	if task.ID == "" {
		notFound(writer, "not found")
		return
	}

	err = tx.DeleteTask(id)
	if err != nil {
		errored(writer, err.Error())
		return
	}

	tx.Commit()
	return
}

func (a *AsyncTasksApp) GetByFilterRequest(writer http.ResponseWriter, r *http.Request) {
	var (
		v = r.URL.Query()

		filters = database.TaskFilter{
			IDs:           v["id"],
			Types:         v["type"],
			Statuses:      v["status"],
			BehaviorTypes: v["behavior_types"],
			Usernames:     v["username"],
		}
		start_date_since  = v["start_date_since"]
		start_date_before = v["start_date_before"]
		end_date_since    = v["end_date_since"]
		end_date_before   = v["end_date_before"]
		null_end          = v["include_null_end"]
	)

	if len(null_end) > 0 {
		filters.IncludeNullEnd = true
	}

	for _, startdate := range start_date_since {
		parsed, err := time.Parse(time.RFC3339Nano, startdate)
		if err != nil {
			errored(writer, err.Error())
			return
		}
		filters.StartDateSince = append(filters.StartDateSince, parsed)
	}

	for _, startdate := range start_date_before {
		parsed, err := time.Parse(time.RFC3339Nano, startdate)
		if err != nil {
			errored(writer, err.Error())
			return
		}
		filters.StartDateBefore = append(filters.StartDateBefore, parsed)
	}

	for _, enddate := range end_date_since {
		parsed, err := time.Parse(time.RFC3339Nano, enddate)
		if err != nil {
			errored(writer, err.Error())
			return
		}
		filters.EndDateSince = append(filters.EndDateSince, parsed)
	}

	for _, enddate := range end_date_before {
		parsed, err := time.Parse(time.RFC3339Nano, enddate)
		if err != nil {
			errored(writer, err.Error())
			return
		}
		filters.EndDateBefore = append(filters.EndDateBefore, parsed)
	}

	tx, err := a.db.BeginTx(context.TODO(), nil)
	if err != nil {
		errored(writer, err.Error())
		return
	}
	defer tx.Rollback()

	tasks, err := tx.GetTasksByFilter(filters)

	jsoned, err := json.Marshal(tasks)
	if err != nil {
		errored(writer, err.Error())
		return
	}

	writer.Write(jsoned)

	return
}

func (a *AsyncTasksApp) CreateTaskRequest(writer http.ResponseWriter, r *http.Request) {
	var rawtask model.AsyncTask

	body, err := ioutil.ReadAll(io.LimitReader(r.Body, hundredMiB))
	if err != nil {
		errored(writer, err.Error())
		return
	}
	if err := r.Body.Close(); err != nil {
		errored(writer, err.Error())
		return
	}
	if err := json.Unmarshal(body, &rawtask); err != nil {
		errored(writer, err.Error())
		return
	}

	if rawtask.Type == "" {
		errored(writer, "Task type must be provided")
		return
	}

	for _, behavior := range rawtask.Behaviors {
		if behavior.BehaviorType == "" {
			errored(writer, "All behaviors must have a type")
			return
		}
	}

	if len(rawtask.Statuses) > 1 {
		errored(writer, "A new task may only include one initial status")
		return
	}

	if len(rawtask.Statuses) > 0 && rawtask.Statuses[0].Status == "" {
		errored(writer, "A blank status is not allowed")
		return
	}

	tx, err := a.db.BeginTx(context.TODO(), nil)
	if err != nil {
		errored(writer, err.Error())
		return
	}
	defer tx.Rollback()

	id, err := tx.InsertTask(rawtask)
	if err != nil {
		errored(writer, err.Error())
		return
	}

	log.Info(id)

	tx.Commit()

	url, _ := a.router.Get("getById").URL("id", id)
	log.Info(url)

	writer.Header().Set("Location", url.EscapedPath())
	writer.WriteHeader(http.StatusCreated)
}

func (a *AsyncTasksApp) AddStatusRequest(writer http.ResponseWriter, r *http.Request) {
	var (
		id        string
		ok        bool
		rawstatus model.AsyncTaskStatus
		v         = mux.Vars(r)
	)

	if id, ok = v["id"]; !ok {
		badRequest(writer, "No ID in URL")
		return
	}

	log.Infof("Fetching async task %s", id)

	tx, err := a.db.BeginTx(context.TODO(), nil)
	if err != nil {
		errored(writer, err.Error())
		return
	}
	defer tx.Rollback()

	task, err := tx.GetTask(id)
	if err != nil {
		errored(writer, err.Error())
		return
	}

	if task.ID == "" {
		notFound(writer, "not found")
		return
	}

	body, err := ioutil.ReadAll(io.LimitReader(r.Body, hundredMiB))

	if err != nil {
		errored(writer, err.Error())
		return
	}
	if err := r.Body.Close(); err != nil {
		errored(writer, err.Error())
		return
	}
	if err := json.Unmarshal(body, &rawstatus); err != nil {
		errored(writer, err.Error())
		return
	}

	err = tx.InsertTaskStatus(rawstatus, id)
	if err != nil {
		errored(writer, err.Error())
		return
	}

	tx.Commit()

	url, _ := a.router.Get("getById").URL("id", id)
	log.Info(url)

	writer.Header().Set("Location", url.EscapedPath())
	writer.WriteHeader(http.StatusCreated)
}

func (a *AsyncTasksApp) AddBehaviorRequest(writer http.ResponseWriter, r *http.Request) {
	var (
		id          string
		ok          bool
		rawbehavior model.AsyncTaskBehavior
		v           = mux.Vars(r)
	)

	if id, ok = v["id"]; !ok {
		badRequest(writer, "No ID in URL")
		return
	}

	log.Infof("Fetching async task %s", id)

	tx, err := a.db.BeginTx(context.TODO(), nil)
	if err != nil {
		errored(writer, err.Error())
		return
	}
	defer tx.Rollback()

	task, err := tx.GetTask(id)
	if err != nil {
		errored(writer, err.Error())
		return
	}

	if task.ID == "" {
		notFound(writer, "not found")
		return
	}

	body, err := ioutil.ReadAll(io.LimitReader(r.Body, hundredMiB))

	if err != nil {
		errored(writer, err.Error())
		return
	}
	if err := r.Body.Close(); err != nil {
		errored(writer, err.Error())
		return
	}
	if err := json.Unmarshal(body, &rawbehavior); err != nil {
		errored(writer, err.Error())
		return
	}

	err = tx.InsertTaskBehavior(rawbehavior, id)
	if err != nil {
		errored(writer, err.Error())
		return
	}

	tx.Commit()

	url, _ := a.router.Get("getById").URL("id", id)
	log.Info(url)

	writer.Header().Set("Location", url.EscapedPath())
	writer.WriteHeader(http.StatusCreated)
}

type ErrorResp struct {
	Msg string `json:"msg"`
}

func makeErrorJson(msg string) string {
	err := ErrorResp{Msg: msg}
	jsoned, _ := json.Marshal(err)
	return string(jsoned)
}

func badRequest(writer http.ResponseWriter, msg string) {
	http.Error(writer, makeErrorJson(msg), http.StatusBadRequest)
	log.Error(msg)
}

func errored(writer http.ResponseWriter, msg string) {
	http.Error(writer, makeErrorJson(msg), http.StatusInternalServerError)
	log.Error(msg)
}

func notFound(writer http.ResponseWriter, msg string) {
	http.Error(writer, makeErrorJson(msg), http.StatusNotFound)
	log.Error(msg)
}
