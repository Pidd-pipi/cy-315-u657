package service

import (
	"errors"
	"fmt"

	"github.com/gbschedule/gbschedule/internal/dto"
)

var (
	// ErrNotFound indicates a requested record does not exist.
	ErrNotFound = errors.New("not found")
	// ErrInvalid indicates malformed business input.
	ErrInvalid = errors.New("invalid input")
	// ErrConflict indicates a constraint violation.
	ErrConflict = errors.New("resource conflict")
	// ErrNoDraft indicates that publishing was requested without a draft.
	ErrNoDraft = errors.New("no draft schedule")
)

// ConflictListError carries the conflict list that blocks a draft publish.
type ConflictListError struct {
	Conflicts []dto.ConflictResponse
}

func (e *ConflictListError) Error() string {
	return fmt.Sprintf("draft contains %d scheduling conflicts", len(e.Conflicts))
}
