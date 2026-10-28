// Package history records every playback and answers listening statistics.
package history

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"arpegio/internal/apperr"
	"arpegio/internal/store"
	"arpegio/internal/store/db"
)

// Rule is the "counted play" threshold: min(Seconds, Percent of the track), whichever comes first.
type Rule struct {
	Seconds int
	Percent int
}

// Counted reports whether listening for listenedMS of a durationMS track counts as a play.
// Anything shorter is a skip.
func (r Rule) Counted(listenedMS, durationMS int64) bool {
	threshold := int64(r.Seconds) * 1000
	if byPercent := durationMS * int64(r.Percent) / 100; durationMS > 0 && byPercent < threshold {
		threshold = byPercent
	}
	return listenedMS > 0 && listenedMS >= threshold
}

type Service struct {
	st   *store.Store
	rule Rule
}

func New(st *store.Store, rule Rule) *Service { return &Service{st: st, rule: rule} }

// Play is one listening session of a track.
type Play struct {
	TrackID    string
	StartedAt  time.Time
	ListenedMS int64
	DurationMS int64
	Source     string // user, album, playlist, radio
}

// Record stores a play and reports whether it counted. Sessions with no listening time are ignored.
func (s *Service) Record(ctx context.Context, p Play) (bool, error) {
	if p.ListenedMS <= 0 {
		return false, nil
	}
	counted := s.rule.Counted(p.ListenedMS, p.DurationMS)
	src := p.Source
	if src == "" {
		src = "user"
	}
	err := db.New(s.st.W).InsertPlay(ctx, db.InsertPlayParams{
		ID: store.NewID(), TrackID: p.TrackID, StartedAt: p.StartedAt.UTC().Format(time.RFC3339),
		ListenedMs: p.ListenedMS, Completed: b2i(counted), Skipped: b2i(!counted), Source: src,
	})
	if err != nil {
		return false, fmt.Errorf("history: record play of %s: %w", p.TrackID, err)
	}
	return counted, nil
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

type Entry struct {
	ID, TrackID, StartedAt, Source string
	ListenedMS                     int64
	Completed, Skipped             bool
}

// Recent returns the latest plays, newest first.
func (s *Service) Recent(ctx context.Context, limit int) ([]Entry, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := db.New(s.st.R).ListRecentPlays(ctx, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("history: recent: %w", err)
	}
	out := make([]Entry, len(rows))
	for i, r := range rows {
		out[i] = Entry{ID: r.ID, TrackID: r.TrackID, StartedAt: r.StartedAt, Source: r.Source, ListenedMS: r.ListenedMs,
			Completed: r.Completed != 0, Skipped: r.Skipped != 0}
	}
	return out, nil
}

type Count struct {
	ID, Name string
	Plays    int
}

type Stats struct {
	ListenedMS int64
	Plays      int
	TopTracks  []Count
	TopArtists []Count
	TopGenres  []Count
}

// ParseRange turns "30d", "12h" or "all" into the earliest time to include.
func ParseRange(s string, now time.Time) (time.Time, error) {
	if s == "" || s == "all" {
		return time.Time{}, nil
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n <= 0 {
		return time.Time{}, apperr.Invalid("history: bad range %q (use 30d, 12h or all)", s)
	}
	switch s[len(s)-1] {
	case 'd':
		return now.AddDate(0, 0, -n), nil
	case 'h':
		return now.Add(-time.Duration(n) * time.Hour), nil
	}
	return time.Time{}, apperr.Invalid("history: bad range %q (use 30d, 12h or all)", s)
}

// Stats summarizes listening since the given time (zero = everything), with the top 10 of each kind.
func (s *Service) Stats(ctx context.Context, since time.Time) (Stats, error) {
	q := db.New(s.st.R)
	from := ""
	if !since.IsZero() {
		from = since.UTC().Format(time.RFC3339)
	}
	tot, err := q.PlayTotals(ctx, from)
	if err != nil {
		return Stats{}, fmt.Errorf("history: totals: %w", err)
	}
	out := Stats{ListenedMS: tot.ListenedMs, Plays: int(tot.Plays)}
	tracks, err := q.TopTracks(ctx, db.TopTracksParams{StartedAt: from, Limit: 10})
	if err != nil {
		return Stats{}, fmt.Errorf("history: top tracks: %w", err)
	}
	for _, r := range tracks {
		out.TopTracks = append(out.TopTracks, Count{r.ID, r.Name, int(r.Plays)})
	}
	artists, err := q.TopArtists(ctx, db.TopArtistsParams{StartedAt: from, Limit: 10})
	if err != nil {
		return Stats{}, fmt.Errorf("history: top artists: %w", err)
	}
	for _, r := range artists {
		out.TopArtists = append(out.TopArtists, Count{r.ID, r.Name, int(r.Plays)})
	}
	genres, err := q.TopGenres(ctx, db.TopGenresParams{StartedAt: from, Limit: 10})
	if err != nil {
		return Stats{}, fmt.Errorf("history: top genres: %w", err)
	}
	for _, r := range genres {
		out.TopGenres = append(out.TopGenres, Count{r.ID, r.Name, int(r.Plays)})
	}
	return out, nil
}
