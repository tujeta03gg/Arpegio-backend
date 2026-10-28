// Package catalog is the music model: artists, albums, tracks, genres and search.
package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"arpegio/internal/apperr"
	"arpegio/internal/events"
	"arpegio/internal/metadata"
	"arpegio/internal/store"
	"arpegio/internal/store/db"
)

const unknownArtist = "Unknown Artist"

type Service struct {
	st  *store.Store
	bus *events.Bus
}

func New(st *store.Store, bus *events.Bus) *Service { return &Service{st: st, bus: bus} }

type ArtistRef struct{ ID, Name, Role string }

type Track struct {
	ID, FileID, AlbumID string
	Title               string
	DiscNo, TrackNo     int
	DurationMS          int64
	BPM                 int
	AddedAt             string
	Album, Artist       string
	Artists             []ArtistRef // filled on detail only
}

type Album struct {
	ID, Title, ArtistID, Artist, ArtworkID string
	Year                                   int
	Tracks                                 []Track // filled on detail only
}

type Artist struct {
	ID, Name string
	Albums   []Album // detail only
	Tracks   []Track // detail only
}

type Genre struct{ ID, Name string }

type Hit struct{ ID, Title, Artist, Album string }

// Page is one page of a cursor-paginated list; Next is "" on the last page.
type Page[T any] struct {
	Items []T
	Next  string
}

// TrackInput is what library hands over for one scanned file.
type TrackInput struct {
	FileID    string
	Info      metadata.Info
	ArtworkID string
}

// UpsertTrack creates or refreshes the track for a file inside the caller's transaction:
// artists, album, genres, track rows and search index. Hand-edited fields (overrides) survive.
func (s *Service) UpsertTrack(ctx context.Context, tx *sql.Tx, in TrackInput) (string, error) {
	q := db.New(tx)
	info := in.Info

	mains, feats := metadata.SplitArtists(info.Artist)
	if len(mains) == 0 {
		mains = []string{unknownArtist}
	}
	type credit struct{ id, role string }
	var credits []credit
	var mainNames []string
	for _, n := range mains {
		id, err := s.artist(ctx, q, n)
		if err != nil {
			return "", err
		}
		credits = append(credits, credit{id, "main"})
		mainNames = append(mainNames, n)
	}
	for _, n := range feats {
		id, err := s.artist(ctx, q, n)
		if err != nil {
			return "", err
		}
		credits = append(credits, credit{id, "featured"})
	}

	var albumID sql.NullString
	if info.Album != "" {
		aaName := info.AlbumArtist
		if aaName == "" {
			aaName = mains[0]
		}
		aaID, err := s.artist(ctx, q, aaName)
		if err != nil {
			return "", err
		}
		id, err := s.album(ctx, q, info.Album, aaID, aaName, info.Year, in.ArtworkID)
		if err != nil {
			return "", err
		}
		albumID = sql.NullString{String: id, Valid: true}
	}

	title, trackNo, discNo := info.Title, info.TrackNo, info.DiscNo
	trackID, err := q.GetTrackIDByFile(ctx, in.FileID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		trackID = store.NewID()
		err = q.InsertTrack(ctx, db.InsertTrackParams{
			ID: trackID, FileID: in.FileID, AlbumID: albumID, Title: title,
			DiscNo: nullInt(discNo), TrackNo: nullInt(trackNo), DurationMs: info.DurationMS,
			Bpm: nullInt(info.BPM), AddedAt: store.Now(),
		})
	case err == nil:
		ov := s.overrides(ctx, q, trackID)
		if v, ok := ov.Title(); ok {
			title = v
		}
		if v, ok := ov.Int("track_no"); ok {
			trackNo = v
		}
		if v, ok := ov.Int("disc_no"); ok {
			discNo = v
		}
		err = q.UpdateTrackFromTags(ctx, db.UpdateTrackFromTagsParams{
			ID: trackID, AlbumID: albumID, Title: title, DiscNo: nullInt(discNo), TrackNo: nullInt(trackNo),
			DurationMs: info.DurationMS, Bpm: nullInt(info.BPM),
		})
	}
	if err != nil {
		return "", fmt.Errorf("catalog: upsert track for file %s: %w", in.FileID, err)
	}

	if err := q.DeleteTrackArtists(ctx, trackID); err != nil {
		return "", fmt.Errorf("catalog: reset credits: %w", err)
	}
	seen := map[string]bool{}
	for i, c := range credits {
		if seen[c.id+c.role] {
			continue
		}
		seen[c.id+c.role] = true
		if err := q.InsertTrackArtist(ctx, db.InsertTrackArtistParams{TrackID: trackID, ArtistID: c.id, Role: c.role, Position: int64(i)}); err != nil {
			return "", fmt.Errorf("catalog: credit: %w", err)
		}
	}

	if err := q.DeleteTrackGenres(ctx, trackID); err != nil {
		return "", fmt.Errorf("catalog: reset genres: %w", err)
	}
	for _, g := range info.Genres {
		gid, err := s.genre(ctx, q, g)
		if err != nil {
			return "", err
		}
		if err := q.InsertTrackGenre(ctx, db.InsertTrackGenreParams{TrackID: trackID, GenreID: gid}); err != nil {
			return "", fmt.Errorf("catalog: track genre: %w", err)
		}
	}

	return trackID, s.index(ctx, q, "track", trackID, title, strings.Join(mainNames, ", "), info.Album)
}

func (s *Service) artist(ctx context.Context, q *db.Queries, name string) (string, error) {
	name = metadata.CleanName(name)
	key := metadata.SortKey(name)
	a, err := q.GetArtistBySortName(ctx, key)
	if err == nil {
		return a.ID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("catalog: find artist %q: %w", name, err)
	}
	id := store.NewID()
	if err := q.InsertArtist(ctx, db.InsertArtistParams{ID: id, Name: name, SortName: key}); err != nil {
		return "", fmt.Errorf("catalog: create artist %q: %w", name, err)
	}
	return id, s.index(ctx, q, "artist", id, name, "", "")
}

func (s *Service) album(ctx context.Context, q *db.Queries, title, artistID, artistName string, year int, artworkID string) (string, error) {
	a, err := q.FindAlbum(ctx, db.FindAlbumParams{Title: title, ArtistID: sql.NullString{String: artistID, Valid: true}})
	id := a.ID
	if errors.Is(err, sql.ErrNoRows) {
		id = store.NewID()
		if err = q.InsertAlbum(ctx, db.InsertAlbumParams{ID: id, Title: title, AlbumArtistID: sql.NullString{String: artistID, Valid: true}, Year: nullInt(year)}); err == nil {
			err = s.index(ctx, q, "album", id, title, artistName, title)
		}
	}
	if err != nil {
		return "", fmt.Errorf("catalog: album %q: %w", title, err)
	}
	if artworkID != "" {
		if err := q.SetAlbumArtwork(ctx, db.SetAlbumArtworkParams{ArtworkID: sql.NullString{String: artworkID, Valid: true}, ID: id}); err != nil {
			return "", fmt.Errorf("catalog: album artwork: %w", err)
		}
	}
	return id, nil
}

func (s *Service) genre(ctx context.Context, q *db.Queries, name string) (string, error) {
	g, err := q.GetGenreByName(ctx, name)
	if err == nil {
		return g.ID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("catalog: find genre: %w", err)
	}
	id := store.NewID()
	if err := q.InsertGenre(ctx, db.InsertGenreParams{ID: id, Name: name}); err != nil {
		return "", fmt.Errorf("catalog: create genre: %w", err)
	}
	return id, nil
}

func (s *Service) index(ctx context.Context, q *db.Queries, kind, id, title, artist, album string) error {
	if err := q.DeleteSearchRef(ctx, db.DeleteSearchRefParams{Kind: kind, RefID: id}); err != nil {
		return fmt.Errorf("catalog: index: %w", err)
	}
	if err := q.InsertSearch(ctx, db.InsertSearchParams{Kind: kind, RefID: id, Title: title, Artist: artist, Album: album}); err != nil {
		return fmt.Errorf("catalog: index: %w", err)
	}
	return nil
}

// NotifyUpdated tells subscribers that tracks were added or edited.
func (s *Service) NotifyUpdated(changed int) {
	s.bus.Publish(events.Event{Type: "catalog.updated", Data: map[string]int{"changed": changed}})
}

// ---- overrides ----

type overrides map[string]json.RawMessage

func (s *Service) overrides(ctx context.Context, q *db.Queries, trackID string) overrides {
	raw, err := q.GetTrackOverrides(ctx, trackID)
	ov := overrides{}
	if err == nil && raw.Valid {
		_ = json.Unmarshal([]byte(raw.String), &ov) // corrupt JSON = no overrides
	}
	return ov
}

func (o overrides) Title() (string, bool) {
	var s string
	raw, ok := o["title"]
	return s, ok && json.Unmarshal(raw, &s) == nil
}

func (o overrides) Int(key string) (int, bool) {
	var n int
	raw, ok := o[key]
	return n, ok && json.Unmarshal(raw, &n) == nil
}

// TrackPatch edits metadata by hand; nil fields are left alone.
type TrackPatch struct {
	Title   *string
	TrackNo *int
	DiscNo  *int
}

// UpdateTrack stores the edit in overrides so rescans do not undo it.
func (s *Service) UpdateTrack(ctx context.Context, id string, p TrackPatch) (Track, error) {
	if p.Title != nil && strings.TrimSpace(*p.Title) == "" {
		return Track{}, apperr.Invalid("catalog: title cannot be empty")
	}
	for _, n := range []*int{p.TrackNo, p.DiscNo} {
		if n != nil && *n < 0 {
			return Track{}, apperr.Invalid("catalog: numbers cannot be negative")
		}
	}
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		q := db.New(tx)
		cur, err := q.GetTrack(ctx, id)
		if err != nil {
			return apperr.NotFound(err, "catalog: track "+id)
		}
		ov := s.overrides(ctx, q, id)
		title, disc, trk := cur.Title, cur.DiscNo, cur.TrackNo
		if p.Title != nil {
			title = metadata.CleanName(*p.Title)
			ov["title"], _ = json.Marshal(title)
		}
		if p.TrackNo != nil {
			trk = nullInt(*p.TrackNo)
			ov["track_no"], _ = json.Marshal(*p.TrackNo)
		}
		if p.DiscNo != nil {
			disc = nullInt(*p.DiscNo)
			ov["disc_no"], _ = json.Marshal(*p.DiscNo)
		}
		blob, _ := json.Marshal(ov)
		if err := q.SetTrackOverrides(ctx, db.SetTrackOverridesParams{
			ID: id, Title: title, DiscNo: disc, TrackNo: trk, Overrides: sql.NullString{String: string(blob), Valid: true},
		}); err != nil {
			return fmt.Errorf("catalog: save overrides: %w", err)
		}
		artist := cur.ArtistNames
		return s.index(ctx, q, "track", id, title, artist, cur.AlbumTitle)
	})
	if err != nil {
		return Track{}, err
	}
	s.NotifyUpdated(1)
	return s.Track(ctx, id)
}

// ---- reads ----

func (s *Service) Track(ctx context.Context, id string) (Track, error) {
	q := db.New(s.st.R)
	r, err := q.GetTrack(ctx, id)
	if err != nil {
		return Track{}, apperr.NotFound(err, "catalog: track "+id)
	}
	t := toTrack(r)
	refs, err := q.ListTrackArtists(ctx, id)
	if err != nil {
		return Track{}, fmt.Errorf("catalog: track artists: %w", err)
	}
	for _, a := range refs {
		t.Artists = append(t.Artists, ArtistRef{ID: a.ID, Name: a.Name, Role: a.Role})
	}
	return t, nil
}

func (s *Service) Tracks(ctx context.Context, after string, limit int) (Page[Track], error) {
	limit = clampLimit(limit)
	rows, err := db.New(s.st.R).ListTracks(ctx, db.ListTracksParams{After: after, Lim: int64(limit)})
	if err != nil {
		return Page[Track]{}, fmt.Errorf("catalog: list tracks: %w", err)
	}
	items := make([]Track, len(rows))
	for i, r := range rows {
		items[i] = toTrack(db.GetTrackRow(r))
	}
	return page(items, limit, func(t Track) string { return t.ID }), nil
}

func (s *Service) Artists(ctx context.Context, after string, limit int) (Page[Artist], error) {
	limit = clampLimit(limit)
	rows, err := db.New(s.st.R).ListArtists(ctx, db.ListArtistsParams{ID: after, Limit: int64(limit)})
	if err != nil {
		return Page[Artist]{}, fmt.Errorf("catalog: list artists: %w", err)
	}
	items := make([]Artist, len(rows))
	for i, r := range rows {
		items[i] = Artist{ID: r.ID, Name: r.Name}
	}
	return page(items, limit, func(a Artist) string { return a.ID }), nil
}

func (s *Service) Artist(ctx context.Context, id string) (Artist, error) {
	q := db.New(s.st.R)
	a, err := q.GetArtist(ctx, id)
	if err != nil {
		return Artist{}, apperr.NotFound(err, "catalog: artist "+id)
	}
	out := Artist{ID: a.ID, Name: a.Name}
	albums, err := q.ListAlbumsByArtist(ctx, sql.NullString{String: id, Valid: true})
	if err != nil {
		return Artist{}, fmt.Errorf("catalog: artist albums: %w", err)
	}
	for _, al := range albums {
		out.Albums = append(out.Albums, toAlbum(al, a.Name))
	}
	tracks, err := q.ListTracksByArtist(ctx, id)
	if err != nil {
		return Artist{}, fmt.Errorf("catalog: artist tracks: %w", err)
	}
	for _, r := range tracks {
		out.Tracks = append(out.Tracks, toTrack(db.GetTrackRow(r)))
	}
	return out, nil
}

func (s *Service) Albums(ctx context.Context, after string, limit int) (Page[Album], error) {
	limit = clampLimit(limit)
	q := db.New(s.st.R)
	rows, err := q.ListAlbums(ctx, db.ListAlbumsParams{ID: after, Limit: int64(limit)})
	if err != nil {
		return Page[Album]{}, fmt.Errorf("catalog: list albums: %w", err)
	}
	items := make([]Album, len(rows))
	for i, r := range rows {
		items[i] = toAlbum(r, s.artistName(ctx, q, r.AlbumArtistID))
	}
	return page(items, limit, func(a Album) string { return a.ID }), nil
}

func (s *Service) Album(ctx context.Context, id string) (Album, error) {
	q := db.New(s.st.R)
	r, err := q.GetAlbum(ctx, id)
	if err != nil {
		return Album{}, apperr.NotFound(err, "catalog: album "+id)
	}
	out := toAlbum(r, s.artistName(ctx, q, r.AlbumArtistID))
	tracks, err := q.ListTracksByAlbum(ctx, sql.NullString{String: id, Valid: true})
	if err != nil {
		return Album{}, fmt.Errorf("catalog: album tracks: %w", err)
	}
	for _, t := range tracks {
		out.Tracks = append(out.Tracks, toTrack(db.GetTrackRow(t)))
	}
	return out, nil
}

func (s *Service) Genres(ctx context.Context) ([]Genre, error) {
	rows, err := db.New(s.st.R).ListGenres(ctx)
	if err != nil {
		return nil, fmt.Errorf("catalog: list genres: %w", err)
	}
	out := make([]Genre, len(rows))
	for i, g := range rows {
		out[i] = Genre{ID: g.ID, Name: g.Name}
	}
	return out, nil
}

func (s *Service) GenreTracks(ctx context.Context, id string) ([]Track, error) {
	rows, err := db.New(s.st.R).ListTracksByGenre(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("catalog: genre tracks: %w", err)
	}
	out := make([]Track, len(rows))
	for i, r := range rows {
		out[i] = toTrack(db.GetTrackRow(r))
	}
	return out, nil
}

func (s *Service) artistName(ctx context.Context, q *db.Queries, id sql.NullString) string {
	if !id.Valid {
		return ""
	}
	a, err := q.GetArtist(ctx, id.String)
	if err != nil {
		return ""
	}
	return a.Name
}

// ---- search ----

// searchSQL lives here, not in queries/, because sqlc cannot parse MATCH on an FTS5 table.
const searchSQL = `SELECT ref_id, title, artist, album FROM search_index
WHERE search_index MATCH ? AND kind = ? ORDER BY rank LIMIT ?`

// Search runs a prefix search per kind (track, album, artist) and returns hits grouped by kind.
func (s *Service) Search(ctx context.Context, text string, kinds []string, limit int) (map[string][]Hit, error) {
	match, ok := matchQuery(text)
	if !ok {
		return nil, apperr.Invalid("catalog: empty search")
	}
	if len(kinds) == 0 {
		kinds = []string{"track", "album", "artist"}
	}
	limit = clampLimit(limit)
	out := map[string][]Hit{}
	for _, k := range kinds {
		if k != "track" && k != "album" && k != "artist" {
			return nil, apperr.Invalid("catalog: unknown search type %q", k)
		}
		rows, err := s.st.R.QueryContext(ctx, searchSQL, match, k, limit)
		if err != nil {
			return nil, fmt.Errorf("catalog: search: %w", err)
		}
		hits := []Hit{}
		for rows.Next() {
			var h Hit
			if err := rows.Scan(&h.ID, &h.Title, &h.Artist, &h.Album); err != nil {
				rows.Close()
				return nil, fmt.Errorf("catalog: search scan: %w", err)
			}
			hits = append(hits, h)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("catalog: search: %w", err)
		}
		out[k] = hits
	}
	return out, nil
}

// matchQuery turns free text into an FTS5 query of quoted prefix tokens: `"foo"* "bar"*`.
func matchQuery(text string) (string, bool) {
	toks := strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	if len(toks) == 0 {
		return "", false
	}
	for i, t := range toks {
		toks[i] = `"` + t + `"*`
	}
	return strings.Join(toks, " "), true
}

// ---- helpers ----

func clampLimit(n int) int {
	switch {
	case n <= 0:
		return 50
	case n > 500:
		return 500
	}
	return n
}

func page[T any](items []T, limit int, id func(T) string) Page[T] {
	p := Page[T]{Items: items}
	if len(items) == limit {
		p.Next = id(items[len(items)-1])
	}
	return p
}

func nullInt(n int) sql.NullInt64 { return sql.NullInt64{Int64: int64(n), Valid: n != 0} }

func toTrack(r db.GetTrackRow) Track {
	return Track{
		ID: r.ID, FileID: r.FileID, AlbumID: r.AlbumID.String, Title: r.Title,
		DiscNo: int(r.DiscNo.Int64), TrackNo: int(r.TrackNo.Int64), DurationMS: r.DurationMs,
		BPM: int(r.Bpm.Int64), AddedAt: r.AddedAt, Album: r.AlbumTitle, Artist: r.ArtistNames,
	}
}

func toAlbum(r db.Album, artist string) Album {
	return Album{ID: r.ID, Title: r.Title, ArtistID: r.AlbumArtistID.String, Artist: artist, Year: int(r.Year.Int64), ArtworkID: r.ArtworkID.String}
}

// RecentTracks lists the most recently added tracks, newest first.
func (s *Service) RecentTracks(ctx context.Context, limit int) ([]Track, error) {
	rows, err := db.New(s.st.R).ListRecentTracks(ctx, int64(clampLimit(limit)))
	if err != nil {
		return nil, fmt.Errorf("catalog: recent tracks: %w", err)
	}
	out := make([]Track, len(rows))
	for i, r := range rows {
		out[i] = toTrack(db.GetTrackRow(r))
	}
	return out, nil
}

// Exists returns ErrNotFound unless a track, album or artist with that id is in the catalog.
func (s *Service) Exists(ctx context.Context, kind, id string) error {
	var err error
	switch kind {
	case "track":
		_, err = s.Track(ctx, id)
		return err
	case "album":
		_, err = db.New(s.st.R).GetAlbum(ctx, id)
	case "artist":
		_, err = db.New(s.st.R).GetArtist(ctx, id)
	default:
		return apperr.Invalid("catalog: unknown kind %q", kind)
	}
	if err != nil {
		return apperr.NotFound(err, "catalog: "+kind+" "+id)
	}
	return nil
}
