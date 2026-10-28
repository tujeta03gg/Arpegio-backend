package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func openMem(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrationsCreateSchema(t *testing.T) {
	s := openMem(t)
	for _, table := range []string{
		"library_roots", "files", "artwork", "artists", "albums", "tracks", "track_artists",
		"genres", "track_genres", "search_index", "playlists", "playlist_items", "likes",
		"queue_items", "queue_state", "player_state", "plays", "lyrics", "jobs",
	} {
		var n int
		err := s.R.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = ?`, table).Scan(&n)
		if err != nil || n != 1 {
			t.Errorf("table %s missing (n=%d err=%v)", table, n, err)
		}
	}
}

func TestFileDBUsesWALAndReopens(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "a.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := s.R.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q, %v", mode, err)
	}
	if _, err := s.W.Exec(`INSERT INTO genres(id, name) VALUES ('1','rock')`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(ctx, path) // migrations must be idempotent and data must persist
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	if err := s.R.QueryRow(`SELECT count(*) FROM genres`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("genres after reopen = %d, %v", n, err)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	s := openMem(t)
	_, err := s.W.Exec(`INSERT INTO files(id, root_id, rel_path, content_hash, size_bytes, mtime, format, duration_ms)
		VALUES ('f','no-such-root','a.flac','h',1,'t','flac',1)`)
	if err == nil {
		t.Fatal("expected foreign key violation")
	}
}

func TestSearchIndexIgnoresDiacritics(t *testing.T) {
	s := openMem(t)
	if _, err := s.W.Exec(`INSERT INTO search_index(kind, ref_id, title, artist, album)
		VALUES ('track','t1','Canción de cuna','Café Tacvba','Re')`); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"cancion", "CAFE", "canción"} {
		var id string
		if err := s.R.QueryRow(`SELECT ref_id FROM search_index WHERE search_index MATCH ?`, q).Scan(&id); err != nil || id != "t1" {
			t.Errorf("query %q: id=%q err=%v", q, id, err)
		}
	}
}

func TestTxCommitAndRollback(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	count := func() int {
		var n int
		s.R.QueryRow(`SELECT count(*) FROM genres`).Scan(&n)
		return n
	}

	err := s.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO genres(id, name) VALUES ('1','rock')`)
		return err
	})
	if err != nil || count() != 1 {
		t.Fatalf("commit: err=%v count=%d", err, count())
	}

	boom := errors.New("boom")
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		tx.Exec(`INSERT INTO genres(id, name) VALUES ('2','jazz')`)
		return boom
	})
	if !errors.Is(err, boom) || count() != 1 {
		t.Fatalf("rollback: err=%v count=%d", err, count())
	}
}

func TestNewIDSortable(t *testing.T) {
	a, b := NewID(), NewID()
	if len(a) != 26 || a == b || a > b {
		t.Fatalf("ids not unique/ordered: %s %s", a, b)
	}
}
