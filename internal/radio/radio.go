// Package radio builds an endless queue from a seed (track, artist or genre).
//
// Version 1 has no machine learning: every candidate gets a score from a few rules you can
// read below, and the best ones win. Each rule is a named constant so it is easy to tune.
package radio

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"arpegio/internal/apperr"
	"arpegio/internal/store"
	"arpegio/internal/store/db"
)

// Scoring rules (points added to a candidate).
const (
	pointsSameArtist   = 1.0 // low on purpose: radio should not keep repeating the seed artist
	pointsCoListened   = 3.0 // scaled by how often the artist shows up in sessions with the seed artist
	pointsSharedGenre  = 3.0
	pointsSameDecade   = 2.0
	pointsNearDecade   = 1.0
	pointsBPMClose     = 2.0 // within bpmClose
	pointsBPMNear      = 1.0 // within bpmNear
	bpmClose, bpmNear  = 8, 20
	maxPerArtistInPick = 2 // variety: at most this many tracks of one artist per batch
)

// Exclusions.
const (
	recentlyPlayed   = 2 * time.Hour
	skipMinPlays     = 3
	skipRatio        = 0.5 // skipped at least this fraction of the time
	sessionGap       = 30 * time.Minute
	sessionLookback  = 90 * 24 * time.Hour
	initialBatchSize = 30
)

type Service struct {
	st     *store.Store
	now    func() time.Time
	jitter func() float64 // 0..1, breaks ties so repeated batches differ
	// Queued lists tracks already waiting in the queue so radio does not repeat them (optional).
	Queued func(ctx context.Context) ([]string, error)
}

func New(st *store.Store) *Service {
	return &Service{st: st, now: time.Now, jitter: rand.Float64}
}

type candidate struct {
	id      string
	year    int
	bpm     int
	artists map[string]bool
	genres  map[string]bool
}

// seedProfile is what candidates are compared against.
type seedProfile struct {
	artists map[string]bool
	genres  map[string]bool
	year    int
	bpm     int
	trackID string // set for track seeds
}

// Start returns the first batch for a seed such as "track:ID", "artist:ID" or "genre:ID".
// For a track seed the track itself comes first.
func (s *Service) Start(ctx context.Context, seed string) ([]string, error) {
	ids, err := s.pick(ctx, seed, initialBatchSize)
	if err != nil {
		return nil, err
	}
	if kind, id, _ := strings.Cut(seed, ":"); kind == "track" {
		ids = append([]string{id}, ids...)
	}
	return ids, nil
}

// More returns n more tracks for the same seed, skipping what is queued or was played recently.
func (s *Service) More(ctx context.Context, seed string, n int) ([]string, error) {
	return s.pick(ctx, seed, n)
}

func (s *Service) pick(ctx context.Context, seed string, n int) ([]string, error) {
	q := db.New(s.st.R)
	cands, err := s.loadCandidates(ctx, q)
	if err != nil {
		return nil, err
	}
	prof, err := buildProfile(seed, cands)
	if err != nil {
		return nil, err
	}
	excluded, err := s.excluded(ctx, q, prof)
	if err != nil {
		return nil, err
	}
	co, err := s.coListening(ctx, q, prof.artists)
	if err != nil {
		return nil, err
	}

	type scored struct {
		c     candidate
		score float64
	}
	var ranked []scored
	for _, c := range cands {
		if excluded[c.id] {
			continue
		}
		ranked = append(ranked, scored{c, score(c, prof, co) + s.jitter()*0.5})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].c.id < ranked[j].c.id
	})

	perArtist := map[string]int{}
	var out []string
	for _, r := range ranked {
		if len(out) == n {
			break
		}
		if overArtistCap(r.c, perArtist) {
			continue
		}
		for a := range r.c.artists {
			perArtist[a]++
		}
		out = append(out, r.c.id)
	}
	return out, nil
}

func overArtistCap(c candidate, count map[string]int) bool {
	for a := range c.artists {
		if count[a] >= maxPerArtistInPick {
			return true
		}
	}
	return false
}

// score applies the rules. co maps artist id -> 0..1 co-listening weight.
func score(c candidate, p seedProfile, co map[string]float64) float64 {
	var pts float64
	for a := range c.artists {
		if p.artists[a] {
			pts += pointsSameArtist
			break
		}
	}
	best := 0.0
	for a := range c.artists {
		best = max(best, co[a])
	}
	pts += pointsCoListened * best
	for g := range c.genres {
		if p.genres[g] {
			pts += pointsSharedGenre
			break
		}
	}
	if c.year > 0 && p.year > 0 {
		switch d := abs(c.year/10 - p.year/10); {
		case d == 0:
			pts += pointsSameDecade
		case d == 1:
			pts += pointsNearDecade
		}
	}
	if c.bpm > 0 && p.bpm > 0 {
		switch d := abs(c.bpm - p.bpm); {
		case d <= bpmClose:
			pts += pointsBPMClose
		case d <= bpmNear:
			pts += pointsBPMNear
		}
	}
	return pts
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func (s *Service) loadCandidates(ctx context.Context, q *db.Queries) ([]candidate, error) {
	tracks, err := q.RadioTracks(ctx)
	if err != nil {
		return nil, fmt.Errorf("radio: tracks: %w", err)
	}
	byID := make(map[string]*candidate, len(tracks))
	out := make([]candidate, len(tracks))
	for i, t := range tracks {
		out[i] = candidate{id: t.ID, year: int(t.Year), bpm: int(t.Bpm), artists: map[string]bool{}, genres: map[string]bool{}}
		byID[t.ID] = &out[i]
	}
	artists, err := q.RadioTrackArtists(ctx)
	if err != nil {
		return nil, fmt.Errorf("radio: artists: %w", err)
	}
	for _, a := range artists {
		if c := byID[a.TrackID]; c != nil {
			c.artists[a.ArtistID] = true
		}
	}
	genres, err := q.RadioTrackGenres(ctx)
	if err != nil {
		return nil, fmt.Errorf("radio: genres: %w", err)
	}
	for _, g := range genres {
		if c := byID[g.TrackID]; c != nil {
			c.genres[g.GenreID] = true
		}
	}
	return out, nil
}

// buildProfile turns a seed spec into the features candidates are compared with.
func buildProfile(seed string, cands []candidate) (seedProfile, error) {
	kind, id, ok := strings.Cut(seed, ":")
	if !ok || id == "" {
		return seedProfile{}, apperr.Invalid("radio: seed must look like track:ID, artist:ID or genre:ID")
	}
	p := seedProfile{artists: map[string]bool{}, genres: map[string]bool{}}
	switch kind {
	case "track":
		for _, c := range cands {
			if c.id == id {
				p.trackID, p.year, p.bpm = id, c.year, c.bpm
				p.artists, p.genres = c.artists, c.genres
				return p, nil
			}
		}
	case "artist":
		var years, n int
		for _, c := range cands {
			if c.artists[id] {
				n++
				years += c.year
				for g := range c.genres {
					p.genres[g] = true
				}
			}
		}
		if n > 0 {
			p.artists[id] = true
			p.year = years / n
			return p, nil
		}
	case "genre":
		for _, c := range cands {
			if c.genres[id] {
				p.genres[id] = true
				return p, nil
			}
		}
	default:
		return seedProfile{}, apperr.Invalid("radio: seed kind must be track, artist or genre")
	}
	return seedProfile{}, apperr.NotFound(sql.ErrNoRows, "radio: seed "+seed)
}

// excluded lists tracks radio must not suggest: the seed, recent plays, often-skipped tracks and queued ones.
func (s *Service) excluded(ctx context.Context, q *db.Queries, p seedProfile) (map[string]bool, error) {
	out := map[string]bool{}
	if p.trackID != "" {
		out[p.trackID] = true
	}
	stats, err := q.RadioPlayStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("radio: play stats: %w", err)
	}
	cutoff := s.now().Add(-recentlyPlayed).UTC().Format(time.RFC3339)
	for _, st := range stats {
		if st.LastPlayed >= cutoff {
			out[st.TrackID] = true
		}
		if st.Plays >= skipMinPlays && float64(st.Skips)/float64(st.Plays) >= skipRatio {
			out[st.TrackID] = true
		}
	}
	if s.Queued != nil {
		ids, err := s.Queued(ctx)
		if err != nil {
			return nil, fmt.Errorf("radio: queued tracks: %w", err)
		}
		for _, id := range ids {
			out[id] = true
		}
	}
	return out, nil
}

// coListening finds artists that tend to be played in the same listening session as the seed artists.
// A session is a run of plays with no gap longer than sessionGap. Result is scaled to 0..1.
func (s *Service) coListening(ctx context.Context, q *db.Queries, seedArtists map[string]bool) (map[string]float64, error) {
	if len(seedArtists) == 0 {
		return nil, nil
	}
	since := s.now().Add(-sessionLookback).UTC().Format(time.RFC3339)
	rows, err := q.RadioSessionPlays(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("radio: sessions: %w", err)
	}
	counts := map[string]float64{}
	flush := func(session map[string]bool) {
		hit := false
		for a := range session {
			if seedArtists[a] {
				hit = true
			}
		}
		if !hit {
			return
		}
		for a := range session {
			if !seedArtists[a] {
				counts[a]++
			}
		}
	}
	session := map[string]bool{}
	var last time.Time
	for _, r := range rows {
		at, err := time.Parse(time.RFC3339, r.StartedAt)
		if err != nil {
			continue
		}
		if !last.IsZero() && at.Sub(last) > sessionGap {
			flush(session)
			session = map[string]bool{}
		}
		session[r.ArtistID] = true
		last = at
	}
	flush(session)

	peak := 0.0
	for _, n := range counts {
		peak = max(peak, n)
	}
	for a := range counts {
		counts[a] /= peak
	}
	return counts, nil
}
