package lyrics

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"arpegio/internal/apperr"
	"arpegio/internal/store"
)

func TestParseSynced(t *testing.T) {
	l := Parse("\xef\xbb\xbf[ar:Band]\r\n[ti:Song]\r\n[00:12.50]First line\r\n[01:05.5]Second\r\n[00:03]Intro\r\n\r\n[02:00.123]  \r\n")
	want := []Line{{3000, "Intro"}, {12500, "First line"}, {65500, "Second"}, {120123, ""}}
	if !l.Synced || !reflect.DeepEqual(l.Lines, want) {
		t.Fatalf("lines = %+v", l)
	}
}

func TestParseMultipleTimestampsAndOffset(t *testing.T) {
	l := Parse("[offset:+500]\n[00:10.00][00:30.00]Chorus\n[00:20.00]Verse")
	want := []Line{{9500, "Chorus"}, {19500, "Verse"}, {29500, "Chorus"}}
	if !reflect.DeepEqual(l.Lines, want) {
		t.Errorf("lines = %+v, want %+v", l.Lines, want)
	}
	// a negative offset delays the lyrics, and nothing goes below zero
	l = Parse("[offset:-1000]\n[00:00.50]Early")
	if l.Lines[0].TimeMS != 1500 {
		t.Errorf("negative offset: %+v", l.Lines)
	}
	l = Parse("[offset:+5000]\n[00:01.00]Clamped")
	if l.Lines[0].TimeMS != 0 {
		t.Errorf("clamp: %+v", l.Lines)
	}
}

func TestParseFractionDigits(t *testing.T) {
	for in, want := range map[string]int64{"[00:01.5]x": 1500, "[00:01.50]x": 1500, "[00:01.500]x": 1500, "[00:01:25]x": 1250, "[00:01]x": 1000} {
		if got := Parse(in).Lines[0].TimeMS; got != want {
			t.Errorf("%s = %d, want %d", in, got, want)
		}
	}
}

func TestParsePlainTextAndEmpty(t *testing.T) {
	l := Parse("[ar:Someone]\nJust words\n\nMore words\n")
	if l.Synced || len(l.Lines) != 2 || l.Lines[0] != (Line{-1, "Just words"}) || l.Lines[1].Text != "More words" {
		t.Errorf("plain = %+v", l)
	}
	if l := Parse(""); l.Synced || len(l.Lines) != 0 {
		t.Errorf("empty = %+v", l)
	}
	// stray text inside a synced file is dropped
	l = Parse("a comment\n[00:01.00]Real")
	if len(l.Lines) != 1 || l.Lines[0].Text != "Real" {
		t.Errorf("mixed = %+v", l)
	}
}

func TestCurrentLine(t *testing.T) {
	l := Parse("[00:10.00]a\n[00:20.00]b\n[00:30.00]c")
	for pos, want := range map[int64]int{0: -1, 9999: -1, 10000: 0, 19999: 0, 20000: 1, 45000: 2} {
		if got := l.CurrentLine(pos); got != want {
			t.Errorf("CurrentLine(%d) = %d, want %d", pos, got, want)
		}
	}
	if got := Parse("plain").CurrentLine(5000); got != -1 {
		t.Errorf("plain lyrics have no current line, got %d", got)
	}
}

func newSvc(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.W.Exec(`INSERT INTO library_roots(id, path) VALUES ('r','/m')`)
	st.W.Exec(`INSERT INTO files(id, root_id, rel_path, content_hash, size_bytes, mtime, format, duration_ms) VALUES ('f1', 'r', 'a', 'h', 1, 't', 'mp3', 1000)`)
	if _, err := st.W.Exec(`INSERT INTO tracks(id, file_id, title, duration_ms, added_at) VALUES ('t1', 'f1', 'T', 1000, 't')`); err != nil {
		t.Fatal(err)
	}
	return New(st), st
}

func attach(t *testing.T, s *Service, st *store.Store, audio string) {
	t.Helper()
	err := st.Tx(context.Background(), func(tx *sql.Tx) error { return s.Attach(context.Background(), tx, "t1", audio) })
	if err != nil {
		t.Fatal(err)
	}
}

func TestAttachReadsLRCNextToTheAudio(t *testing.T) {
	ctx := context.Background()
	s, st := newSvc(t)
	dir := t.TempDir()
	audio := filepath.Join(dir, "My Song.flac")
	lrc := filepath.Join(dir, "My Song.lrc")

	attach(t, s, st, audio) // no file yet
	if _, err := s.Get(ctx, "t1"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("no lyrics yet: %v", err)
	}

	os.WriteFile(lrc, []byte("[00:01.00]Hello\n[00:05.00]World\n"), 0o644)
	attach(t, s, st, audio)
	l, err := s.Get(ctx, "t1")
	if err != nil || !l.Synced || len(l.Lines) != 2 || l.Lines[1].Text != "World" {
		t.Fatalf("lyrics = %+v %v", l, err)
	}

	os.WriteFile(lrc, []byte("[00:01.00]Hello again"), 0o644) // edited file replaces the stored one
	attach(t, s, st, audio)
	if l, _ := s.Get(ctx, "t1"); len(l.Lines) != 1 || l.Lines[0].Text != "Hello again" {
		t.Errorf("not refreshed: %+v", l)
	}

	os.WriteFile(lrc, []byte("just words"), 0o644)
	attach(t, s, st, audio)
	if l, _ := s.Get(ctx, "t1"); l.Synced || len(l.Lines) != 1 {
		t.Errorf("plain lyrics: %+v", l)
	}
	var synced int
	st.R.QueryRow(`SELECT synced FROM lyrics WHERE track_id = 't1'`).Scan(&synced)
	if synced != 0 {
		t.Errorf("synced flag = %d", synced)
	}

	os.Remove(lrc) // deleting the file removes the lyrics on the next ingest
	attach(t, s, st, audio)
	if _, err := s.Get(ctx, "t1"); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("lyrics should be gone: %v", err)
	}

	os.WriteFile(lrc, []byte("  \n "), 0o644) // an empty file is no lyrics
	attach(t, s, st, audio)
	if _, err := s.Get(ctx, "t1"); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("empty file: %v", err)
	}
}
