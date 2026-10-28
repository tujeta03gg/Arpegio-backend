// Package store owns the SQLite connections and schema migrations.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store has one write connection and a pool of read connections.
// For ":memory:" both point at the same single connection.
type Store struct {
	W *sql.DB
	R *sql.DB
}

// Open opens (creating if needed) the database at path and applies migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == ":memory:" {
		w, err := sql.Open("sqlite", "file::memory:?"+pragmas())
		if err != nil {
			return nil, fmt.Errorf("store: open memory: %w", err)
		}
		w.SetMaxOpenConns(1) // each in-memory connection is its own database
		return finish(ctx, &Store{W: w, R: w})
	}
	dsn := path + "?" + pragmas()
	w, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	w.SetMaxOpenConns(1)
	r, err := sql.Open("sqlite", dsn)
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("store: open %s (read): %w", path, err)
	}
	return finish(ctx, &Store{W: w, R: r})
}

func pragmas() string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	return q.Encode()
}

func finish(ctx context.Context, s *Store) (*Store, error) {
	if err := s.migrate(ctx); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("store: migrations fs: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, s.W, sub)
	if err != nil {
		return fmt.Errorf("store: migrations provider: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	return nil
}

// Close closes both pools.
func (s *Store) Close() error {
	err := s.W.Close()
	if s.R != s.W {
		if e := s.R.Close(); err == nil {
			err = e
		}
	}
	return err
}

// Tx runs fn in a write transaction, committing on nil and rolling back otherwise.
func (s *Store) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// NewID returns a new ULID string: sortable and stable across rescans.
func NewID() string { return ulid.Make().String() }

// Now returns the current time as ISO 8601 UTC text, the format used in every table.
func Now() string { return time.Now().UTC().Format(time.RFC3339) }
