package radio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"arpegio/internal/apperr"
	"arpegio/internal/store"
	"arpegio/internal/store/db"
)

type env struct {
	t   *testing.T
	ctx context.Context
	st  *store.Store
	svc *Service
	now time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.W.Exec(`INSERT INTO library_roots(id, path) VALUES ('r','/m')`)
	e := &env{t: t, ctx: ctx, st: st, now: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	e.svc = New(st)
	e.svc.now = func() time.Time { return e.now }
	e.svc.jitter = func() float64 { return 0 }
	return e
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.st.W.Exec(q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

// track adds a track by artist (id "a_<artist>") on an album of the given year, with genres and bpm.
func (e *env) track(id, artist string, year, bpm int, genres ...string) {
	e.t.Helper()
	a, al := "a_"+artist, "al_"+artist+fmt.Sprint(year)
	e.exec(`INSERT OR IGNORE INTO artists(id, name, sort_name) VALUES (?, ?, ?)`, a, artist, artist)
	e.exec(`INSERT OR IGNORE INTO albums(id, title, album_artist_id, year) VALUES (?, ?, ?, ?)`, al, al, a, year)
	e.exec(`INSERT INTO files(id, root_id, rel_path, content_hash, size_bytes, mtime, format, duration_ms) VALUES (?, 'r', ?, ?, 1, 't', 'mp3', 1000)`, "f"+id, id, id)
	var b any
	if bpm > 0 {
		b = bpm
	}
	e.exec(`INSERT INTO tracks(id, file_id, album_id, title, duration_ms, bpm, added_at) VALUES (?, ?, ?, ?, 1000, ?, 't')`, id, "f"+id, al, id, b)
	e.exec(`INSERT INTO track_artists(track_id, artist_id, role, position) VALUES (?, ?, 'main', 0)`, id, a)
	for _, g := range genres {
		e.exec(`INSERT OR IGNORE INTO genres(id, name) VALUES (?, ?)`, "g_"+g, g)
		e.exec(`INSERT INTO track_genres(track_id, genre_id) VALUES (?, ?)`, id, "g_"+g)
	}
}

func (e *env) play(track string, ago time.Duration, skipped bool) {
	e.t.Helper()
	completed, skip := 1, 0
	if skipped {
		completed, skip = 0, 1
	}
	e.exec(`INSERT INTO plays(id, track_id, started_at, listened_ms, completed, skipped, source) VALUES (?, ?, ?, 1000, ?, ?, 'user')`,
		store.NewID(), track, e.now.Add(-ago).UTC().Format(time.RFC3339), completed, skip)
}

func (e *env) start(seed string) []string {
	e.t.Helper()
	ids, err := e.svc.Start(e.ctx, seed)
	if err != nil {
		e.t.Fatal(err)
	}
	return ids
}

func indexOf(ids []string, id string) int { return slices.Index(ids, id) }

func TestSimilarTracksRankFirst(t *testing.T) {
	e := newEnv(t)
	e.track("seed", "Seed", 1994, 120, "rock")
	e.track("rock90", "Other90s", 1996, 118, "rock") // genre + decade + bpm = 7
	e.track("rock10", "Modern", 2012, 0, "rock")     // genre only
	e.track("jazz60", "Jazzman", 1962, 120, "jazz")  // bpm only
	e.track("rock90b", "Third", 1991, 0, "rock")     // genre + decade

	got := e.start("track:seed")
	if got[0] != "seed" {
		t.Fatalf("a track seed leads its own radio: %v", got)
	}
	want := []string{"seed", "rock90", "rock90b", "rock10", "jazz60"}
	if !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestNearDecadeAndBPMBands(t *testing.T) {
	seed := seedProfile{artists: map[string]bool{}, genres: map[string]bool{}, year: 1994, bpm: 100}
	mk := func(year, bpm int) candidate { return candidate{year: year, bpm: bpm} }
	tests := []struct {
		name string
		c    candidate
		want float64
	}{
		{"same decade, bpm close", mk(1999, 108), pointsSameDecade + pointsBPMClose},
		{"next decade, bpm near", mk(2003, 120), pointsNearDecade + pointsBPMNear},
		{"far decade, bpm far", mk(1960, 160), 0},
		{"unknown year and bpm", mk(0, 0), 0},
	}
	for _, tt := range tests {
		if got := score(tt.c, seed, nil); got != tt.want {
			t.Errorf("%s: score = %v, want %v", tt.name, got, tt.want)
		}
	}
	same := candidate{artists: map[string]bool{"x": true}}
	if got := score(same, seedProfile{artists: map[string]bool{"x": true}}, nil); got != pointsSameArtist {
		t.Errorf("same artist = %v", got)
	}
}

func TestRecentAndOftenSkippedTracksAreLeftOut(t *testing.T) {
	e := newEnv(t)
	e.track("seed", "Seed", 1994, 0, "rock")
	e.track("fresh", "A", 1994, 0, "rock")
	e.track("justplayed", "B", 1994, 0, "rock")
	e.track("hated", "C", 1994, 0, "rock")
	e.track("oldplay", "D", 1994, 0, "rock")
	e.track("fewskips", "E", 1994, 0, "rock")

	e.play("justplayed", 30*time.Minute, false)
	e.play("oldplay", 5*time.Hour, false) // long enough ago to come back
	for i := 0; i < 3; i++ {
		e.play("hated", time.Duration(i+3)*24*time.Hour, true)
	}
	e.play("fewskips", 48*time.Hour, true) // a single skip is not a pattern
	e.play("fewskips", 72*time.Hour, false)

	got := e.start("track:seed")
	for _, bad := range []string{"justplayed", "hated"} {
		if indexOf(got, bad) >= 0 {
			t.Errorf("%s should be excluded: %v", bad, got)
		}
	}
	for _, ok := range []string{"fresh", "oldplay", "fewskips"} {
		if indexOf(got, ok) < 0 {
			t.Errorf("%s should be allowed: %v", ok, got)
		}
	}
}

func TestSessionsPullInArtistsListenedToTogether(t *testing.T) {
	e := newEnv(t)
	e.track("seed", "Seed", 1994, 0, "rock")
	e.track("buddy", "Buddy", 1960, 0, "folk") // nothing in common except being played with Seed
	e.track("loner", "Loner", 1960, 0, "folk")
	e.track("other", "Other", 1960, 0, "folk")
	e.track("seedmate", "Seed", 1994, 0, "rock") // a second Seed track, played so it has history

	// three evenings: Seed then Buddy. Another evening: Other alone, hours later.
	for day := 1; day <= 3; day++ {
		base := time.Duration(day) * 24 * time.Hour
		e.play("seedmate", base, false)
		e.play("buddy", base-10*time.Minute, false)
	}
	e.play("other", 10*24*time.Hour, false)

	got := e.start("track:seed")
	if indexOf(got, "buddy") < 0 || indexOf(got, "loner") < 0 || indexOf(got, "buddy") > indexOf(got, "loner") {
		t.Errorf("buddy (heard with Seed) must rank above loner: %v", got)
	}
	if indexOf(got, "other") < indexOf(got, "buddy") {
		t.Errorf("a different session must not count: %v", got)
	}
}

func TestSessionGapSplitsSessions(t *testing.T) {
	e := newEnv(t)
	e.track("seed", "Seed", 1990, 0)
	e.track("late", "Late", 1990, 0)
	e.track("seedmate", "Seed", 1990, 0)
	e.play("seedmate", 5*time.Hour, false)
	e.play("late", 5*time.Hour-45*time.Minute, false) // 45 min later: a new session
	q := newQueries(e)
	co, err := e.svc.coListening(e.ctx, q, map[string]bool{"a_Seed": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(co) != 0 {
		t.Errorf("co = %v", co)
	}
}

func TestArtistAndGenreSeeds(t *testing.T) {
	e := newEnv(t)
	e.track("a1", "Band", 1990, 0, "rock")
	e.track("a2", "Band", 1992, 0, "rock")
	e.track("r1", "Rocker", 1991, 0, "rock")
	e.track("j1", "Jazzer", 1991, 0, "jazz")

	got := e.start("artist:a_Band")
	if len(got) < 3 || indexOf(got, "j1") < indexOf(got, "r1") {
		t.Errorf("artist radio = %v (rock should beat jazz)", got)
	}
	if got[0] == "" || indexOf(got, "r1") < 0 {
		t.Errorf("artist radio = %v", got)
	}

	got = e.start("genre:g_jazz")
	if got[0] != "j1" {
		t.Errorf("genre radio = %v", got)
	}
}

func TestVarietyCapPerArtist(t *testing.T) {
	e := newEnv(t)
	e.track("seed", "Seed", 1994, 0, "rock")
	for i := 0; i < 5; i++ {
		e.track(fmt.Sprintf("same%d", i), "Prolific", 1994, 0, "rock")
	}
	e.track("diff", "Different", 1960, 0, "folk")
	got := e.start("track:seed")
	n := 0
	for _, id := range got {
		if len(id) > 4 && id[:4] == "same" {
			n++
		}
	}
	if n != maxPerArtistInPick || indexOf(got, "diff") < 0 {
		t.Errorf("radio = %v (want %d Prolific tracks and the other artist)", got, maxPerArtistInPick)
	}
}

func TestMoreSkipsWhatIsQueuedAndJitterVariesBatches(t *testing.T) {
	e := newEnv(t)
	e.track("seed", "Seed", 1994, 0, "rock")
	for i := 0; i < 6; i++ {
		e.track(fmt.Sprintf("t%d", i), fmt.Sprintf("Artist%d", i), 1994, 0, "rock")
	}
	e.svc.Queued = func(context.Context) ([]string, error) { return []string{"t0", "t1"}, nil }
	got, err := e.svc.More(e.ctx, "track:seed", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || indexOf(got, "t0") >= 0 || indexOf(got, "t1") >= 0 || indexOf(got, "seed") >= 0 {
		t.Errorf("More = %v", got)
	}

	e.svc.Queued = func(context.Context) ([]string, error) { return nil, errors.New("queue down") }
	if _, err := e.svc.More(e.ctx, "track:seed", 3); err == nil {
		t.Error("queue failure should surface")
	}
}

func TestBadSeeds(t *testing.T) {
	e := newEnv(t)
	e.track("t1", "A", 1990, 0)
	for _, seed := range []string{"", "track", "track:", "podcast:1"} {
		if _, err := e.svc.Start(e.ctx, seed); !errors.Is(err, apperr.ErrInvalid) {
			t.Errorf("seed %q: %v", seed, err)
		}
	}
	for _, seed := range []string{"track:nope", "artist:nope", "genre:nope"} {
		if _, err := e.svc.Start(e.ctx, seed); !errors.Is(err, apperr.ErrNotFound) {
			t.Errorf("seed %q: %v", seed, err)
		}
	}
}

func TestMissingFilesAreNeverSuggested(t *testing.T) {
	e := newEnv(t)
	e.track("seed", "Seed", 1994, 0, "rock")
	e.track("gone", "Gone", 1994, 0, "rock")
	e.exec(`UPDATE files SET missing = 1 WHERE id = 'fgone'`)
	if got := e.start("track:seed"); indexOf(got, "gone") >= 0 {
		t.Errorf("radio = %v", got)
	}
}

func newQueries(e *env) *db.Queries { return db.New(e.st.R) }
