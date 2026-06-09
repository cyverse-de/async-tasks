package database

import (
	"database/sql"

	"github.com/sirupsen/logrus"
)

// NewTestDBConnection creates a DBConnection wrapping the provided *sql.DB.
// This exists solely for use by tests in other packages that need to construct
// a DBConnection without connecting to a real database.
func NewTestDBConnection(db *sql.DB) *DBConnection {
	return &DBConnection{
		db:  db,
		log: logrus.WithField("test", true),
	}
}
