package api

import (
	"context"
	"net/http"
	"sort"
	"time"

	"arpegio/internal/history"
	"arpegio/internal/playlists"
)

// Playlists is the part of playlists.Service the API uses (playlists and likes).
type Playlists interface {
	List(ctx context.Context) ([]playlists.Playlist, error)
	Create(ctx context.Context, name, description string) (playlists.Playlist, error)
	Get(ctx context.Context, id string) (playlists.Playlist, []playlists.Item, error)
	Update(ctx context.Context, id string, name, description *string) (playlists.Playlist, error)
	Delete(ctx context.Context, id string) error
	AddTracks(ctx context.Context, id string, trackIDs []string) error
	MoveItem(ctx context.Context, id string, position float64, to int) error
	RemoveItem(ctx context.Context, id string, position float64) error
	SetLike(ctx context.Context, kind, id string) error
	RemoveLike(ctx context.Context, kind, id string) error
	Likes(ctx context.Context, kind string) ([]playlists.Like, error)
}

// History is the part of history.Service the API uses.
type History interface {
	Recent(ctx context.Context, limit int) ([]history.Entry, error)
	Stats(ctx context.Context, since time.Time) (history.Stats, error)
}

type PlaylistItemDTO struct {
	Position float64  `json:"position"`
	AddedAt  string   `json:"added_at"`
	Track    TrackDTO `json:"track"`
}

type PlaylistDTO struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	CreatedAt   string            `json:"created_at"`
	UpdatedAt   string            `json:"updated_at"`
	ItemCount   int               `json:"item_count"`
	Items       []PlaylistItemDTO `json:"items,omitempty"`
}

type HistoryEntryDTO struct {
	ID         string   `json:"id"`
	Track      TrackDTO `json:"track"`
	StartedAt  string   `json:"started_at"`
	ListenedMS int64    `json:"listened_ms"`
	Completed  bool     `json:"completed"`
	Skipped    bool     `json:"skipped"`
	Source     string   `json:"source"`
}

type CountDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Plays int    `json:"plays"`
}

type StatsDTO struct {
	Range      string     `json:"range"`
	ListenedMS int64      `json:"listened_ms"`
	Plays      int        `json:"plays"`
	TopTracks  []CountDTO `json:"top_tracks"`
	TopArtists []CountDTO `json:"top_artists"`
	TopGenres  []CountDTO `json:"top_genres"`
}

type HomeDTO struct {
	RecentlyPlayed []TrackDTO    `json:"recently_played"`
	MostPlayed     []TrackDTO    `json:"most_played"`
	RecentlyAdded  []TrackDTO    `json:"recently_added"`
	RecentAlbums   []AlbumDTO    `json:"recent_albums"`
	Playlists      []PlaylistDTO `json:"playlists"`
}

func (s *Server) collectionRoutes() {
	s.add("GET /playlists", s.listPlaylists)
	s.add("POST /playlists", s.createPlaylist)
	s.add("GET /playlists/{id}", s.getPlaylist)
	s.add("PATCH /playlists/{id}", s.patchPlaylist)
	s.add("DELETE /playlists/{id}", s.deletePlaylist)
	s.add("POST /playlists/{id}/items", s.addPlaylistItems)
	s.add("PATCH /playlists/{id}/items", s.movePlaylistItem)
	s.add("DELETE /playlists/{id}/items", s.removePlaylistItem)
	s.add("PUT /likes/{kind}/{id}", s.putLike)
	s.add("DELETE /likes/{kind}/{id}", s.deleteLike)
	s.add("GET /likes", s.listLikes)
	s.add("GET /history", s.getHistory)
	s.add("GET /stats", s.getStats)
	s.add("GET /home", s.getHome)
}

func playlistDTO(p playlists.Playlist) PlaylistDTO {
	return PlaylistDTO{ID: p.ID, Name: p.Name, Description: p.Description, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, ItemCount: p.ItemCount}
}

// trackOrStub looks a track up; a track that vanished from the catalog still shows up by id.
func (s *Server) trackOrStub(ctx context.Context, id string) TrackDTO {
	if t, err := s.Catalog.Track(ctx, id); err == nil {
		return trackDTO(t)
	}
	return TrackDTO{ID: id}
}

func (s *Server) listPlaylists(w http.ResponseWriter, r *http.Request) {
	ps, err := s.Playlists.List(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]PlaylistDTO, len(ps))
	for i, p := range ps {
		out[i] = playlistDTO(p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) createPlaylist(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decode(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.Playlists.Create(r.Context(), body.Name, body.Description)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, playlistDTO(p))
}

func (s *Server) playlistDetail(ctx context.Context, id string) (PlaylistDTO, error) {
	p, items, err := s.Playlists.Get(ctx, id)
	if err != nil {
		return PlaylistDTO{}, err
	}
	d := playlistDTO(p)
	d.Items = make([]PlaylistItemDTO, len(items))
	for i, it := range items {
		d.Items[i] = PlaylistItemDTO{Position: it.Position, AddedAt: it.AddedAt, Track: s.trackOrStub(ctx, it.TrackID)}
	}
	return d, nil
}

func (s *Server) getPlaylist(w http.ResponseWriter, r *http.Request) {
	d, err := s.playlistDetail(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) patchPlaylist(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := decode(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := s.Playlists.Update(r.Context(), r.PathValue("id"), body.Name, body.Description); err != nil {
		s.fail(w, r, err)
		return
	}
	s.getPlaylist(w, r)
}

func (s *Server) deletePlaylist(w http.ResponseWriter, r *http.Request) {
	if err := s.Playlists.Delete(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) addPlaylistItems(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TrackIDs []string `json:"track_ids"`
	}
	if err := decode(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	for _, id := range body.TrackIDs { // 404 for unknown tracks instead of a database error
		if _, err := s.Catalog.Track(r.Context(), id); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if err := s.Playlists.AddTracks(r.Context(), r.PathValue("id"), body.TrackIDs); err != nil {
		s.fail(w, r, err)
		return
	}
	s.getPlaylist(w, r)
}

func (s *Server) movePlaylistItem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Position *float64 `json:"position"`
		Index    *int     `json:"index"`
	}
	if err := decode(r, &body); err != nil || body.Position == nil || body.Index == nil {
		s.fail(w, r, orInvalid(err, "api: position and index are required"))
		return
	}
	if err := s.Playlists.MoveItem(r.Context(), r.PathValue("id"), *body.Position, *body.Index); err != nil {
		s.fail(w, r, err)
		return
	}
	s.getPlaylist(w, r)
}

func (s *Server) removePlaylistItem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Position *float64 `json:"position"`
	}
	if err := decode(r, &body); err != nil || body.Position == nil {
		s.fail(w, r, orInvalid(err, "api: position is required"))
		return
	}
	if err := s.Playlists.RemoveItem(r.Context(), r.PathValue("id"), *body.Position); err != nil {
		s.fail(w, r, err)
		return
	}
	s.getPlaylist(w, r)
}

// ---- likes ----

func (s *Server) putLike(w http.ResponseWriter, r *http.Request) {
	if err := s.Playlists.SetLike(r.Context(), r.PathValue("kind"), r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteLike(w http.ResponseWriter, r *http.Request) {
	if err := s.Playlists.RemoveLike(r.Context(), r.PathValue("kind"), r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listLikes returns the favourites of one kind (default track) as full objects.
func (s *Server) listLikes(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "track"
	}
	likes, err := s.Playlists.Likes(r.Context(), kind)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	items := []any{}
	for _, l := range likes {
		switch kind {
		case "track":
			if t, err := s.Catalog.Track(ctx, l.ID); err == nil {
				items = append(items, trackDTO(t))
			}
		case "album":
			if a, err := s.Catalog.Album(ctx, l.ID); err == nil {
				a.Tracks = nil
				items = append(items, albumDTO(a))
			}
		case "artist":
			if a, err := s.Catalog.Artist(ctx, l.ID); err == nil {
				items = append(items, ArtistDTO{ID: a.ID, Name: a.Name})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ---- history, stats, home ----

func (s *Server) getHistory(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 50)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	entries, err := s.History.Recent(r.Context(), limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	items := make([]HistoryEntryDTO, len(entries))
	for i, e := range entries {
		items[i] = HistoryEntryDTO{ID: e.ID, Track: s.trackOrStub(r.Context(), e.TrackID), StartedAt: e.StartedAt,
			ListenedMS: e.ListenedMS, Completed: e.Completed, Skipped: e.Skipped, Source: e.Source}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func counts(cs []history.Count) []CountDTO {
	out := make([]CountDTO, len(cs))
	for i, c := range cs {
		out[i] = CountDTO{ID: c.ID, Name: c.Name, Plays: c.Plays}
	}
	return out
}

func (s *Server) getStats(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = "30d"
	}
	since, err := history.ParseRange(rng, time.Now())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	st, err := s.History.Stats(r.Context(), since)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, StatsDTO{Range: rng, ListenedMS: st.ListenedMS, Plays: st.Plays,
		TopTracks: counts(st.TopTracks), TopArtists: counts(st.TopArtists), TopGenres: counts(st.TopGenres)})
}

// getHome builds the start screen: what was played lately, the most played of the month, and new additions.
func (s *Server) getHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	home := HomeDTO{RecentlyPlayed: []TrackDTO{}, MostPlayed: []TrackDTO{}, RecentlyAdded: []TrackDTO{},
		RecentAlbums: []AlbumDTO{}, Playlists: []PlaylistDTO{}}

	entries, err := s.History.Recent(ctx, 50)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if len(home.RecentlyPlayed) == 10 {
			break
		}
		if seen[e.TrackID] || e.Skipped {
			continue
		}
		seen[e.TrackID] = true
		if t, err := s.Catalog.Track(ctx, e.TrackID); err == nil {
			home.RecentlyPlayed = append(home.RecentlyPlayed, trackDTO(t))
		}
	}

	since, _ := history.ParseRange("30d", time.Now())
	st, err := s.History.Stats(ctx, since)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, c := range st.TopTracks {
		if t, err := s.Catalog.Track(ctx, c.ID); err == nil {
			home.MostPlayed = append(home.MostPlayed, trackDTO(t))
		}
	}

	added, err := s.Catalog.RecentTracks(ctx, 10)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	home.RecentlyAdded = tracksDTO(added)

	albums, err := s.Catalog.RecentAlbums(ctx, 10)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, a := range albums {
		home.RecentAlbums = append(home.RecentAlbums, albumDTO(a))
	}

	lists, err := s.Playlists.List(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sort.SliceStable(lists, func(i, j int) bool { return lists[i].UpdatedAt > lists[j].UpdatedAt })
	for _, p := range lists[:min(10, len(lists))] {
		home.Playlists = append(home.Playlists, playlistDTO(p))
	}
	writeJSON(w, http.StatusOK, home)
}
