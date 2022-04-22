package model

import (
	"database/sql"
	"time"

	"github.com/lib/pq"
)

// AsyncTaskBehavior describes a single behavior from the database
type AsyncTaskBehavior struct {
	BehaviorType string                 `json:"type"`
	Data         map[string]interface{} `json:"data"`
}

// AsyncTaskStatus describes a single status update from the database
type AsyncTaskStatus struct {
	Status      string    `json:"status"`
	Detail      string    `json:"detail,omitempty"`
	CreatedDate time.Time `json:"created_date"`
}

// AsyncTask describes an async task from the DB, including behaviors and statuses if available
type AsyncTask struct {
	ID              string                 `json:"id"`
	Type            string                 `json:"type"`
	Username        string                 `json:"username"`
	Data            map[string]interface{} `json:"data"`
	StartDate       *time.Time             `json:"start_date"`
	EndDate         *time.Time             `json:"end_date"`
	Behaviors       []AsyncTaskBehavior    `json:"behaviors,omitempty"`
	BehaviorsLoaded bool                   `json:"-"`
	Statuses        []AsyncTaskStatus      `json:"statuses,omitempty"`
	StatusesLoaded  bool                   `json:"-"`
}

// DBTaskBehavior is a special type for selecting from the DB
type DBTaskBehavior struct {
	BehaviorType string
	Data         sql.NullString
}

// DBTaskStatus is a special type for selectiong from the DB
type DBTaskStatus struct {
	Status      string
	Detail      sql.NullString
	CreatedDate time.Time
}

// DBTask is a special type for selecting from the DB
type DBTask struct {
	ID        string
	Type      string
	Username  sql.NullString
	Data      sql.NullString
	StartDate pq.NullTime
	EndDate   pq.NullTime
}
