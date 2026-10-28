package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"arpegio/internal/apperr"
	"arpegio/internal/library"
)

type RootDTO struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Enabled  bool   `json:"enabled"`
	LastScan string `json:"last_scan,omitempty"`
}

type StatusDTO struct {
	Scanning    bool  `json:"scanning"`
	Done        int64 `json:"done"`
	Total       int64 `json:"total"`
	PendingJobs int   `json:"pending_jobs"`
}

func rootDTO(r library.Root) RootDTO {
	return RootDTO{ID: r.ID, Path: r.Path, Enabled: r.Enabled, LastScan: r.LastScan}
}

func (s *Server) libraryRoutes() {
	s.add("GET /library/roots", s.listRoots)
	s.add("POST /library/roots", s.addRoot)
	s.add("DELETE /library/roots/{id}", s.removeRoot)
	s.add("POST /library/scan", s.scan)
	s.add("POST /library/import", s.importFiles)
	s.add("GET /library/status", s.libraryStatus)
}

func (s *Server) listRoots(w http.ResponseWriter, r *http.Request) {
	roots, err := s.Library.Roots(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]RootDTO, len(roots))
	for i, root := range roots {
		out[i] = rootDTO(root)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) addRoot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := decode(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	root, err := s.Library.AddRoot(r.Context(), body.Path)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, rootDTO(root))
}

func (s *Server) removeRoot(w http.ResponseWriter, r *http.Request) {
	if err := s.Library.RemoveRoot(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// scan starts a scan in the background (it can take minutes) and answers 202.
// Progress comes from GET /library/status or the WebSocket.
func (s *Server) scan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RootID string `json:"root_id"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if s.Library.Status().Scanning {
		s.fail(w, r, apperr.Conflict("library: a scan is already running"))
		return
	}
	go func() {
		if _, err := s.Library.Scan(s.Ctx, body.RootID); err != nil && !errors.Is(err, apperr.ErrConflict) {
			s.Log.Error("api: scan failed", slog.Any("err", err))
		}
	}()
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) libraryStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.statusDTO(r.Context()))
}

func (s *Server) statusDTO(ctx context.Context) StatusDTO {
	st := s.Library.Status()
	d := StatusDTO{Scanning: st.Scanning, Done: st.Done, Total: st.Total}
	if s.Jobs != nil {
		d.PendingJobs, _ = s.Jobs.Open(ctx)
	}
	return d
}

type ImportedDTO struct {
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
	Err  string `json:"error,omitempty"`
}

// importFiles accepts a multipart upload of audio files and hands each to the library.
// Files are streamed to disk, never held in memory.
func (s *Server) importFiles(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<30)
	mr, err := r.MultipartReader()
	if err != nil {
		s.fail(w, r, apperr.Invalid("api: expected a multipart/form-data upload"))
		return
	}
	var done, failed []ImportedDTO
	var firstErr error
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			s.fail(w, r, apperr.Invalid("api: bad multipart body: %v", err))
			return
		}
		name := part.FileName()
		if name == "" {
			continue // a plain form field
		}
		path, err := s.Library.Import(r.Context(), name, part)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			failed = append(failed, ImportedDTO{Name: name, Err: err.Error()})
			continue
		}
		done = append(done, ImportedDTO{Name: name, Path: path})
	}
	if len(done) == 0 {
		if firstErr == nil {
			firstErr = apperr.Invalid("api: no files in the upload")
		}
		s.fail(w, r, firstErr)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"imported": done, "errors": failed})
}
