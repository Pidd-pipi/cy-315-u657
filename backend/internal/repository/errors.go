package repository

import "errors"

var (
	// ErrNotFound is returned when a requested record does not exist.
	ErrNotFound = errors.New("not found")
	// ErrConstraint is returned when a database constraint rejects a write.
	ErrConstraint = errors.New("constraint violation")
	// ErrNoDraft is returned when publish is requested but no draft entries
	// exist for the targeted weeks.
	ErrNoDraft = errors.New("no draft schedule")
)
