// Package api exposes the domain services over REST (and later WebSocket). It holds no logic of its own.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"arpegio/internal/apperr"
	"arpegio/internal/catalog"
	"arpegio/internal/library"
	"arpegio/internal/playback"
)

const prefix = "/api/v1"

// Catalog is the part of catalog.Service the API uses.
type Catalog interface {
	Track(ctx context.Context, id string) (catalog.Track, error)
	Tracks(ctx context.Context, after string, limit int) (catalog.Page[catalog.Track], error)
	UpdateTrack(ctx context.Context, id string, p catalog.TrackPatch) (catalog.Track, error)
	Artists(ctx context.Context, after string, limit int) (catalog.Page[catalog.Artist], error)
	Artist(ctx context.Context, id string) (catalog.Artist, error)
	Albums(ctx context.Context, after string, limit int) (catalog.Page[catalog.Album], error)
	Album(ctx context.Context, id string) (catalog.Album, error)
	Genres(ctx context.Context) ([]catalog.Genre, error)
	GenreTracks(ctx context.Context, id string) ([]catalog.Track, error)
	Search(ctx context.Context, text string, kinds []string, limit int) (map[string][]catalog.Hit, error)
	RecentTracks(ctx context.Context, limit int) ([]catalog.Track, error)
	RecentAlbums(ctx context.Context, limit int) ([]catalog.Album, error)
}

// Library is the part of library.Service the API uses.
type Library interface {
	Roots(ctx context.Context) ([]library.Root, error)
	AddRoot(ctx context.Context, path string) (library.Root, error)
	RemoveRoot(ctx context.Context, id string) error
	Scan(ctx context.Context, rootID string) (library.Result, error)
	Status() library.Status
	TrackPath(ctx context.Context, trackID string) (string, error)
	Import(ctx context.Context, name string, r io.Reader) (string, error)
	Artwork(ctx context.Context, id string, size int) (string, error)
}

// Deps are the services the server needs.
type Deps struct {
	Ctx         context.Context // lifetime of background work such as scans
	Catalog     Catalog
	Library     Library
	AllowOrigin string // CORS origin, "" = off
	WebDir      string // built web frontend to serve at /, "" = off
	Player      Player
	Queue       Queue
	Events      Events
	Playlists   Playlists
	History     History
	Lyrics      Lyrics
	Jobs        interface {
		Open(ctx context.Context) (int, error)
	} // optional: pending background work
	Log *slog.Logger
}

type route struct {
	pattern string // "GET /tracks/{id}", relative to /api/v1
	handler http.HandlerFunc
}

type Server struct {
	Deps
	routes []route
}

// New builds the HTTP handler.
func New(d Deps) *Server {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Ctx == nil {
		d.Ctx = context.Background()
	}
	s := &Server{Deps: d}
	s.catalogRoutes()
	s.libraryRoutes()
	s.playerRoutes()
	s.eventRoutes()
	s.collectionRoutes()
	s.extraRoutes()
	return s
}

func (s *Server) add(pattern string, h http.HandlerFunc) {
	s.routes = append(s.routes, route{pattern, h})
}

// Patterns lists every route ("GET /tracks/{id}"), used to keep openapi.yaml honest.
func (s *Server) Patterns() []string {
	out := make([]string, len(s.routes))
	for i, r := range s.routes {
		out[i] = r.pattern
	}
	return out
}

// Handler returns the mux with all routes mounted under /api/v1.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, r := range s.routes {
		method, path, _ := splitPattern(r.pattern)
		mux.HandleFunc(method+" "+prefix+path, r.handler)
	}
	mux.HandleFunc(prefix+"/", func(w http.ResponseWriter, r *http.Request) {
		s.fail(w, r, apperr.ErrNotFound)
	})
	if s.WebDir != "" {
		mux.Handle("/", webApp(s.WebDir))
	}
	if s.AllowOrigin != "" {
		return withCORS(s.AllowOrigin, mux)
	}
	return mux
}

func splitPattern(p string) (method, path string, ok bool) {
	for i := range p {
		if p[i] == ' ' {
			return p[:i], p[i+1:], true
		}
	}
	return "", p, false
}

// ---- helpers ----

type errBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	status, code, msg := http.StatusInternalServerError, "internal", "internal error"
	switch {
	case errors.Is(err, apperr.ErrNotFound):
		status, code, msg = http.StatusNotFound, "not_found", err.Error()
	case errors.Is(err, apperr.ErrInvalid):
		status, code, msg = http.StatusBadRequest, "invalid", err.Error()
	case errors.Is(err, apperr.ErrConflict):
		status, code, msg = http.StatusConflict, "conflict", err.Error()
	case errors.Is(err, playback.ErrNotConnected):
		status, code, msg = http.StatusServiceUnavailable, "unavailable", "audio engine (mpv) is not running"
	case errors.Is(err, playback.ErrBusy):
		status, code, msg = http.StatusServiceUnavailable, "busy", "the audio engine is busy (still loading the track); try again in a moment"
	default:
		s.Log.Error("api: request failed", slog.String("method", r.Method), slog.String("path", r.URL.Path), slog.Any("err", err))
	}
	var b errBody
	b.Error.Code, b.Error.Message = code, msg
	writeJSON(w, status, b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decode reads a JSON body (max 1 MiB) and rejects unknown fields.
func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return apperr.Invalid("api: bad json body: %v", err)
	}
	return nil
}

func queryInt(r *http.Request, key string, def int) (int, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, apperr.Invalid("api: %s must be a number", key)
	}
	return n, nil
}

// Paged is the list envelope: items plus the cursor for the next page ("" = last page).
type Paged[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
	Total      int    `json:"total"`
}
