package history

import (
	"context"
	"errors"
	"testing"
	"time"

	"arpegio/internal/apperr"
	"arpegio/internal/store"
)

func TestRuleCounted(t *testing.T) {
	r := Rule{Seconds: 30, Percent: 50}
	tests := []struct {
		name               string
		listened, duration int64
		want               bool
	}{
		{"30s of a long track", 30_000, 300_000, true},
		{"29s of a long track is a skip", 29_999, 300_000, false},
		{"50% of a short track beats 30s", 20_000, 40_000, true},
		{"just under 50% of a short track", 19_999, 40_000, false},
		{"whole 10s jingle", 10_000, 10_000, true},
		{"nothing listened", 0, 10_000, false},
		{"unknown duration falls back to seconds", 30_000, 0, true},
		{"unknown duration, too short", 5_000, 0, false},
	}
	for _, tt := range tests {
		if got := r.Counted(tt.listened, tt.duration); got != tt.want {
			t.Errorf("%s: Counted(%d,%d) = %v, want %v", tt.name, tt.listened, tt.duration, got, tt.want)
		}
	}
	custom := Rule{Seconds: 10, Percent: 100}
	if !custom.Counted(10_000, 200_000) || custom.Counted(9_000, 200_000) {
		t.Error("custom rule not honoured")
	}
}

func newSvc(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	exec := func(q string, args ...any) {
		if _, err := st.W.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO library_roots(id, path) VALUES ('r','/m')`)
	exec(`INSERT INTO artists(id, name, sort_name) VALUES ('a1','Band','band'), ('a2','Duo','duo')`)
	exec(`INSERT INTO genres(id, name) VALUES ('g1','Rock')`)
	for _, tr := range []struct{ id, title, artist string }{{"t1", "One", "a1"}, {"t2", "Two", "a1"}, {"t3", "Three", "a2"}} {
		exec(`INSERT INTO files(id, root_id, rel_path, content_hash, size_bytes, mtime, format, duration_ms) VALUES (?, 'r', ?, ?, 1, 't', 'mp3', 100000)`, "f"+tr.id, tr.id, tr.id)
		exec(`INSERT INTO tracks(id, file_id, title, duration_ms, added_at) VALUES (?, ?, ?, 100000, 't')`, tr.id, "f"+tr.id, tr.title)
		exec(`INSERT INTO track_artists(track_id, artist_id, role, position) VALUES (?, ?, 'main', 0)`, tr.id, tr.artist)
	}
	exec(`INSERT INTO track_genres(track_id, genre_id) VALUES ('t1','g1'), ('t2','g1')`)
	return New(st, Rule{Seconds: 30, Percent: 50}), st
}

func TestRecordCountsAndSkips(t *testing.T) {
	ctx := context.Background()
	s, _ := newSvc(t)
	now := time.Now()

	counted, err := s.Record(ctx, Play{TrackID: "t1", StartedAt: now, ListenedMS: 45_000, DurationMS: 100_000, Source: "album"})
	if err != nil || !counted {
		t.Fatalf("counted=%v err=%v", counted, err)
	}
	counted, err = s.Record(ctx, Play{TrackID: "t2", StartedAt: now.Add(time.Minute), ListenedMS: 5_000, DurationMS: 100_000})
	if err != nil || counted {
		t.Fatalf("skip: counted=%v err=%v", counted, err)
	}
	if counted, _ := s.Record(ctx, Play{TrackID: "t3", StartedAt: now, ListenedMS: 0, DurationMS: 100_000}); counted {
		t.Error("zero listening must not count")
	}
	if _, err := s.Record(ctx, Play{TrackID: "ghost", StartedAt: now, ListenedMS: 1000, DurationMS: 1000}); err == nil {
		t.Error("unknown track should fail (foreign key)")
	}

	recent, err := s.Recent(ctx, 10)
	if err != nil || len(recent) != 2 {
		t.Fatalf("recent = %+v err=%v", recent, err)
	}
	if recent[0].TrackID != "t2" || !recent[0].Skipped || recent[0].Completed || recent[0].Source != "user" {
		t.Errorf("newest entry = %+v", recent[0])
	}
	if recent[1].TrackID != "t1" || !recent[1].Completed || recent[1].Skipped || recent[1].Source != "album" {
		t.Errorf("oldest entry = %+v", recent[1])
	}
}

func TestStats(t *testing.T) {
	ctx := context.Background()
	s, _ := newSvc(t)
	now := time.Now()
	rec := func(track string, ago time.Duration, listened int64) {
		t.Helper()
		if _, err := s.Record(ctx, Play{TrackID: track, StartedAt: now.Add(-ago), ListenedMS: listened, DurationMS: 100_000}); err != nil {
			t.Fatal(err)
		}
	}
	rec("t1", time.Hour, 100_000)
	rec("t1", 2*time.Hour, 100_000)
	rec("t3", 3*time.Hour, 60_000)
	rec("t2", 4*time.Hour, 3_000)       // skip: not in tops, but its time counts
	rec("t1", 40*24*time.Hour, 100_000) // outside a 30d window

	since, err := ParseRange("30d", now)
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.Stats(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	if st.Plays != 3 || st.ListenedMS != 263_000 {
		t.Errorf("totals = %d plays, %d ms", st.Plays, st.ListenedMS)
	}
	if len(st.TopTracks) != 2 || st.TopTracks[0] != (Count{"t1", "One", 2}) || st.TopTracks[1].ID != "t3" {
		t.Errorf("top tracks = %+v", st.TopTracks)
	}
	if len(st.TopArtists) != 2 || st.TopArtists[0] != (Count{"a1", "Band", 2}) {
		t.Errorf("top artists = %+v", st.TopArtists)
	}
	if len(st.TopGenres) != 1 || st.TopGenres[0] != (Count{"g1", "Rock", 2}) {
		t.Errorf("top genres = %+v", st.TopGenres)
	}

	all, _ := s.Stats(ctx, time.Time{})
	if all.Plays != 4 {
		t.Errorf("all-time plays = %d", all.Plays)
	}
	empty, err := New(mustStore(t), Rule{30, 50}).Stats(ctx, time.Time{})
	if err != nil || empty.Plays != 0 || len(empty.TopTracks) != 0 {
		t.Errorf("empty stats = %+v err=%v", empty, err)
	}
}

func mustStore(t *testing.T) *store.Store {
	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestParseRange(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	got, _ := ParseRange("7d", now)
	if !got.Equal(now.AddDate(0, 0, -7)) {
		t.Errorf("7d = %v", got)
	}
	got, _ = ParseRange("12h", now)
	if !got.Equal(now.Add(-12 * time.Hour)) {
		t.Errorf("12h = %v", got)
	}
	for _, s := range []string{"", "all"} {
		if got, err := ParseRange(s, now); err != nil || !got.IsZero() {
			t.Errorf("%q = %v %v", s, got, err)
		}
	}
	for _, bad := range []string{"d", "0d", "-3d", "5x", "abc", "30"} {
		if _, err := ParseRange(bad, now); !errors.Is(err, apperr.ErrInvalid) {
			t.Errorf("%q should be invalid, got %v", bad, err)
		}
	}
}
