package service

import (
	"errors"

	"github.com/gbschedule/gbschedule/internal/dto"
)

var (
	// ErrNotFound indicates a requested record does not exist.
	ErrNotFound = errors.New("not found")
	// ErrInvalid indicates malformed business input.
	ErrInvalid = errors.New("invalid input")
	// ErrConflict indicates a constraint violation.
	ErrConflict = errors.New("resource conflict")
	// ErrNoDraft indicates a publish/draft operation was attempted without a draft.
	ErrNoDraft = errors.New("no draft to publish")
)

// DraftConflictError wraps the conflict list that blocks publishing a draft.
// Handlers surface the conflicts in the 409 response data.
type DraftConflictError struct {
	Conflicts []dto.ConflictResponse
}

func (e *DraftConflictError) Error() string {
	return "draft has conflicts that must be resolved before publishing"
}

// Is reports DraftConflictError as the generic business conflict so existing
// error mapping still applies.
func (e *DraftConflictError) Is(target error) bool { return target == ErrConflict }
