// Package playlists holds the user's manual playlists and "likes".
package playlists

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"arpegio/internal/apperr"
	"arpegio/internal/events"
	"arpegio/internal/metadata"
	"arpegio/internal/store"
	"arpegio/internal/store/db"
)

const minGap = 1e-6

type Service struct {
	st  *store.Store
	bus *events.Bus
	// exists reports whether a liked target exists (kind is track, album or artist).
	exists func(ctx context.Context, kind, id string) error
}

func New(st *store.Store, bus *events.Bus, exists func(ctx context.Context, kind, id string) error) *Service {
	return &Service{st: st, bus: bus, exists: exists}
}

type Playlist struct {
	ID, Name, Description string
	CreatedAt, UpdatedAt  string
	ItemCount             int
}

type Item struct {
	Position float64
	TrackID  string
	AddedAt  string
}

func (s *Service) changed(id string) {
	s.bus.Publish(events.Event{Type: "playlist.changed", Data: map[string]string{"id": id}})
}

func cleanName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "", apperr.Invalid("playlists: name cannot be empty")
	}
	return name, nil
}

// ---- playlists ----

func (s *Service) Create(ctx context.Context, name, description string) (Playlist, error) {
	name, err := cleanName(name)
	if err != nil {
		return Playlist{}, err
	}
	now := store.Now()
	p := Playlist{ID: store.NewID(), Name: name, Description: strings.TrimSpace(description), CreatedAt: now, UpdatedAt: now}
	err = db.New(s.st.W).InsertPlaylist(ctx, db.InsertPlaylistParams{
		ID: p.ID, Name: p.Name, Description: nullStr(p.Description), CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return Playlist{}, fmt.Errorf("playlists: create: %w", err)
	}
	s.changed(p.ID)
	return p, nil
}

func (s *Service) List(ctx context.Context) ([]Playlist, error) {
	rows, err := db.New(s.st.R).ListPlaylists(ctx)
	if err != nil {
		return nil, fmt.Errorf("playlists: list: %w", err)
	}
	out := make([]Playlist, len(rows))
	for i, r := range rows {
		out[i] = Playlist{ID: r.ID, Name: r.Name, Description: r.Description.String, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, ItemCount: int(r.ItemCount)}
	}
	// sorted here because SQLite NOCASE ignores accents wrongly
	sort.SliceStable(out, func(i, j int) bool { return metadata.SortKey(out[i].Name) < metadata.SortKey(out[j].Name) })
	return out, nil
}

// Get returns a playlist and its items in order.
func (s *Service) Get(ctx context.Context, id string) (Playlist, []Item, error) {
	q := db.New(s.st.R)
	row, err := q.GetPlaylist(ctx, id)
	if err != nil {
		return Playlist{}, nil, apperr.NotFound(err, "playlists: "+id)
	}
	items, err := loadItems(ctx, q, id)
	if err != nil {
		return Playlist{}, nil, err
	}
	return Playlist{ID: row.ID, Name: row.Name, Description: row.Description.String, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ItemCount: len(items)}, items, nil
}

func loadItems(ctx context.Context, q *db.Queries, id string) ([]Item, error) {
	rows, err := q.ListPlaylistItems(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("playlists: items of %s: %w", id, err)
	}
	items := make([]Item, len(rows))
	for i, r := range rows {
		items[i] = Item{Position: r.Position, TrackID: r.TrackID, AddedAt: r.AddedAt}
	}
	return items, nil
}

// Update renames and/or changes the description; nil fields stay as they are.
func (s *Service) Update(ctx context.Context, id string, name, description *string) (Playlist, error) {
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		q := db.New(tx)
		cur, err := q.GetPlaylist(ctx, id)
		if err != nil {
			return apperr.NotFound(err, "playlists: "+id)
		}
		n, d := cur.Name, cur.Description.String
		if name != nil {
			if n, err = cleanName(*name); err != nil {
				return err
			}
		}
		if description != nil {
			d = strings.TrimSpace(*description)
		}
		return q.UpdatePlaylist(ctx, db.UpdatePlaylistParams{ID: id, Name: n, Description: nullStr(d), UpdatedAt: store.Now()})
	})
	if err != nil {
		return Playlist{}, err
	}
	s.changed(id)
	p, _, err := s.Get(ctx, id)
	return p, err
}

// Delete removes a playlist and its items (the tracks themselves are untouched).
func (s *Service) Delete(ctx context.Context, id string) error {
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		n, err := db.New(tx).DeletePlaylist(ctx, id)
		if err != nil {
			return fmt.Errorf("playlists: delete: %w", err)
		}
		if n == 0 {
			return apperr.NotFound(sql.ErrNoRows, "playlists: "+id)
		}
		return nil
	})
	if err == nil {
		s.changed(id)
	}
	return err
}

// ---- items ----

// AddTracks appends tracks to the end of a playlist (duplicates allowed, like every player).
func (s *Service) AddTracks(ctx context.Context, id string, trackIDs []string) error {
	if len(trackIDs) == 0 {
		return apperr.Invalid("playlists: no tracks to add")
	}
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		q := db.New(tx)
		if _, err := q.GetPlaylist(ctx, id); err != nil {
			return apperr.NotFound(err, "playlists: "+id)
		}
		items, err := loadItems(ctx, q, id)
		if err != nil {
			return err
		}
		pos := 0.0
		if len(items) > 0 {
			pos = items[len(items)-1].Position
		}
		now := store.Now()
		for _, t := range trackIDs {
			pos++
			if err := q.InsertPlaylistItem(ctx, db.InsertPlaylistItemParams{PlaylistID: id, Position: pos, TrackID: t, AddedAt: now}); err != nil {
				return fmt.Errorf("playlists: add %s: %w", t, err)
			}
		}
		return q.TouchPlaylist(ctx, db.TouchPlaylistParams{ID: id, UpdatedAt: now})
	})
	if err == nil {
		s.changed(id)
	}
	return err
}

// MoveItem puts the item at position at list index to (0-based, clamped).
func (s *Service) MoveItem(ctx context.Context, id string, position float64, to int) error {
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		q := db.New(tx)
		items, err := loadItems(ctx, q, id)
		if err != nil {
			return err
		}
		from := -1
		for i, it := range items {
			if it.Position == position {
				from = i
			}
		}
		if from < 0 {
			return notFoundItem(ctx, q, id, position)
		}
		moved := items[from]
		rest := append(append([]Item{}, items[:from]...), items[from+1:]...)
		to = max(0, min(to, len(rest)))

		lo, hi := 0.0, 1.0
		switch {
		case len(rest) == 0:
			return nil
		case to == 0:
			hi = rest[0].Position
			lo = hi - 1
		case to == len(rest):
			lo = rest[len(rest)-1].Position
			hi = lo + 1
		default:
			lo, hi = rest[to-1].Position, rest[to].Position
		}
		if hi-lo < 2*minGap {
			return renumber(ctx, q, id, rest, moved, to)
		}
		if _, err := q.DeletePlaylistItem(ctx, db.DeletePlaylistItemParams{PlaylistID: id, Position: position}); err != nil {
			return fmt.Errorf("playlists: move: %w", err)
		}
		moved.Position = (lo + hi) / 2
		return insertItem(ctx, q, id, moved)
	})
	if err == nil {
		s.changed(id)
	}
	return err
}

func renumber(ctx context.Context, q *db.Queries, id string, rest []Item, moved Item, to int) error {
	order := append(append(append([]Item{}, rest[:to]...), moved), rest[to:]...)
	if err := q.DeletePlaylistItems(ctx, id); err != nil {
		return fmt.Errorf("playlists: renumber: %w", err)
	}
	for i, it := range order {
		it.Position = float64(i + 1)
		if err := insertItem(ctx, q, id, it); err != nil {
			return err
		}
	}
	return nil
}

func insertItem(ctx context.Context, q *db.Queries, id string, it Item) error {
	err := q.InsertPlaylistItem(ctx, db.InsertPlaylistItemParams{PlaylistID: id, Position: it.Position, TrackID: it.TrackID, AddedAt: it.AddedAt})
	if err != nil {
		return fmt.Errorf("playlists: insert item: %w", err)
	}
	return nil
}

func notFoundItem(ctx context.Context, q *db.Queries, id string, position float64) error {
	if _, err := q.GetPlaylist(ctx, id); err != nil {
		return apperr.NotFound(err, "playlists: "+id)
	}
	return apperr.NotFound(sql.ErrNoRows, fmt.Sprintf("playlists: position %v in %s", position, id))
}

// RemoveItem deletes the item at position.
func (s *Service) RemoveItem(ctx context.Context, id string, position float64) error {
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		q := db.New(tx)
		n, err := q.DeletePlaylistItem(ctx, db.DeletePlaylistItemParams{PlaylistID: id, Position: position})
		if err != nil {
			return fmt.Errorf("playlists: remove: %w", err)
		}
		if n == 0 {
			return notFoundItem(ctx, q, id, position)
		}
		return q.TouchPlaylist(ctx, db.TouchPlaylistParams{ID: id, UpdatedAt: store.Now()})
	})
	if err == nil {
		s.changed(id)
	}
	return err
}

// TrackIDs returns the playlist's tracks in order, for playback.
func (s *Service) TrackIDs(ctx context.Context, id string) ([]string, error) {
	_, items, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.TrackID
	}
	return ids, nil
}

func nullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }
