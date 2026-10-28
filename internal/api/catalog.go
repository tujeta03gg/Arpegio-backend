package api

import (
	"net/http"
	"os"
	"strings"
	"time"

	"arpegio/internal/catalog"
)

// DTOs: what clients see. Domain types never leave the package boundary directly.

type ArtistRefDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type TrackDTO struct {
	ID         string         `json:"id"`
	Title      string         `json:"title"`
	Artist     string         `json:"artist"`
	Album      string         `json:"album"`
	AlbumID    string         `json:"album_id,omitempty"`
	DiscNo     int            `json:"disc_no,omitempty"`
	TrackNo    int            `json:"track_no,omitempty"`
	DurationMS int64          `json:"duration_ms"`
	BPM        int            `json:"bpm,omitempty"`
	AddedAt    string         `json:"added_at"`
	Artists    []ArtistRefDTO `json:"artists,omitempty"`
}

type AlbumDTO struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	ArtistID  string     `json:"artist_id,omitempty"`
	Artist    string     `json:"artist,omitempty"`
	Year      int        `json:"year,omitempty"`
	ArtworkID string     `json:"artwork_id,omitempty"`
	Tracks    []TrackDTO `json:"tracks,omitempty"`
}

type ArtistDTO struct {
	ID     string     `json:"id"`
	Name   string     `json:"name"`
	Albums []AlbumDTO `json:"albums,omitempty"`
	Tracks []TrackDTO `json:"tracks,omitempty"`
}

type GenreDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type HitDTO struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Artist string `json:"artist,omitempty"`
	Album  string `json:"album,omitempty"`
}

func trackDTO(t catalog.Track) TrackDTO {
	d := TrackDTO{ID: t.ID, Title: t.Title, Artist: t.Artist, Album: t.Album, AlbumID: t.AlbumID, DiscNo: t.DiscNo,
		TrackNo: t.TrackNo, DurationMS: t.DurationMS, BPM: t.BPM, AddedAt: t.AddedAt}
	for _, a := range t.Artists {
		d.Artists = append(d.Artists, ArtistRefDTO{ID: a.ID, Name: a.Name, Role: a.Role})
	}
	return d
}

func tracksDTO(ts []catalog.Track) []TrackDTO {
	out := make([]TrackDTO, len(ts))
	for i, t := range ts {
		out[i] = trackDTO(t)
	}
	return out
}

func albumDTO(a catalog.Album) AlbumDTO {
	return AlbumDTO{ID: a.ID, Title: a.Title, ArtistID: a.ArtistID, Artist: a.Artist, Year: a.Year, ArtworkID: a.ArtworkID, Tracks: tracksDTO(a.Tracks)}
}

func (s *Server) catalogRoutes() {
	s.add("GET /search", s.search)
	s.add("GET /artists", s.listArtists)
	s.add("GET /artists/{id}", s.getArtist)
	s.add("GET /albums", s.listAlbums)
	s.add("GET /albums/{id}", s.getAlbum)
	s.add("GET /tracks", s.listTracks)
	s.add("GET /tracks/{id}", s.getTrack)
	s.add("PATCH /tracks/{id}", s.patchTrack)
	s.add("GET /tracks/{id}/stream", s.streamTrack)
	s.add("GET /genres", s.listGenres)
	s.add("GET /genres/{id}/tracks", s.genreTracks)
	s.add("GET /artwork/{id}", s.artwork)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 10)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var kinds []string
	if t := r.URL.Query().Get("types"); t != "" {
		kinds = strings.Split(t, ",")
	}
	hits, err := s.Catalog.Search(r.Context(), r.URL.Query().Get("q"), kinds, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := map[string][]HitDTO{}
	for kind, hs := range hits {
		list := make([]HitDTO, len(hs))
		for i, h := range hs {
			list[i] = HitDTO{ID: h.ID, Title: h.Title, Artist: h.Artist, Album: h.Album}
		}
		out[kind+"s"] = list
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listArtists(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 50)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.Catalog.Artists(r.Context(), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	items := make([]ArtistDTO, len(p.Items))
	for i, a := range p.Items {
		items[i] = ArtistDTO{ID: a.ID, Name: a.Name}
	}
	writeJSON(w, http.StatusOK, Paged[ArtistDTO]{Items: items, NextCursor: p.Next})
}

func (s *Server) getArtist(w http.ResponseWriter, r *http.Request) {
	a, err := s.Catalog.Artist(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := ArtistDTO{ID: a.ID, Name: a.Name, Tracks: tracksDTO(a.Tracks)}
	for _, al := range a.Albums {
		d.Albums = append(d.Albums, albumDTO(al))
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) listAlbums(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 50)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.Catalog.Albums(r.Context(), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	items := make([]AlbumDTO, len(p.Items))
	for i, a := range p.Items {
		items[i] = albumDTO(a)
	}
	writeJSON(w, http.StatusOK, Paged[AlbumDTO]{Items: items, NextCursor: p.Next})
}

func (s *Server) getAlbum(w http.ResponseWriter, r *http.Request) {
	a, err := s.Catalog.Album(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, albumDTO(a))
}

func (s *Server) listTracks(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 50)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.Catalog.Tracks(r.Context(), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, Paged[TrackDTO]{Items: tracksDTO(p.Items), NextCursor: p.Next})
}

func (s *Server) getTrack(w http.ResponseWriter, r *http.Request) {
	t, err := s.Catalog.Track(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, trackDTO(t))
}

func (s *Server) patchTrack(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title   *string `json:"title"`
		TrackNo *int    `json:"track_no"`
		DiscNo  *int    `json:"disc_no"`
	}
	if err := decode(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	t, err := s.Catalog.UpdateTrack(r.Context(), r.PathValue("id"), catalog.TrackPatch{Title: body.Title, TrackNo: body.TrackNo, DiscNo: body.DiscNo})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, trackDTO(t))
}

// streamTrack serves the original file; http.ServeContent handles Range requests.
func (s *Server) streamTrack(w http.ResponseWriter, r *http.Request) {
	path, err := s.Library.TrackPath(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	serveFile(w, r, path)
}

func serveFile(w http.ResponseWriter, r *http.Request, path string) {
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "file unavailable", http.StatusNotFound)
		return
	}
	defer f.Close()
	var mod time.Time
	if fi, err := f.Stat(); err == nil {
		mod = fi.ModTime()
	}
	http.ServeContent(w, r, path, mod, f)
}

func (s *Server) listGenres(w http.ResponseWriter, r *http.Request) {
	gs, err := s.Catalog.Genres(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]GenreDTO, len(gs))
	for i, g := range gs {
		out[i] = GenreDTO{ID: g.ID, Name: g.Name}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) genreTracks(w http.ResponseWriter, r *http.Request) {
	ts, err := s.Catalog.GenreTracks(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": tracksDTO(ts)})
}

func (s *Server) artwork(w http.ResponseWriter, r *http.Request) {
	size, err := queryInt(r, "size", 0)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	path, err := s.Library.Artwork(r.Context(), r.PathValue("id"), size)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	serveFile(w, r, path)
}
