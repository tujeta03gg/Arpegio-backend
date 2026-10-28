// Package apperr holds the domain errors shared by every service; only api maps them to HTTP.
package apperr

import (
	"database/sql"
	"errors"
	"fmt"
)

var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid")
	ErrConflict = errors.New("conflict")
)

// NotFound turns sql.ErrNoRows into ErrNotFound; other errors pass through wrapped with what.
func NotFound(err error, what string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", what, ErrNotFound)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// Invalid builds an ErrInvalid with a message.
func Invalid(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), ErrInvalid)
}

// Conflict builds an ErrConflict with a message.
func Conflict(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), ErrConflict)
}
