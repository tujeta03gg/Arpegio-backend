package api

import (
	"context"
	"net/http"

	"arpegio/internal/lyrics"
	"arpegio/internal/playback"
	"arpegio/internal/version"
)

// Lyrics is the part of lyrics.Service the API uses.
type Lyrics interface {
	Get(ctx context.Context, trackID string) (lyrics.Lyrics, error)
}

type LyricLineDTO struct {
	TimeMS *int64 `json:"time_ms,omitempty"` // absent for lyrics without timestamps
	Text   string `json:"text"`
}

type LyricsDTO struct {
	Synced bool           `json:"synced"`
	Lines  []LyricLineDTO `json:"lines"`
}

func (s *Server) extraRoutes() {
	s.add("GET /tracks/{id}/lyrics", s.getLyrics)
	s.add("POST /radio", s.startRadio)
	s.add("GET /version", s.getVersion)
}

func (s *Server) getLyrics(w http.ResponseWriter, r *http.Request) {
	l, err := s.Lyrics.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := LyricsDTO{Synced: l.Synced, Lines: make([]LyricLineDTO, len(l.Lines))}
	for i, ln := range l.Lines {
		out.Lines[i].Text = ln.Text
		if l.Synced {
			ms := ln.TimeMS
			out.Lines[i].TimeMS = &ms
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// startRadio starts an endless queue from a seed such as track:ID, artist:ID or genre:ID.
func (s *Server) startRadio(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Seed string `json:"seed"`
	}
	if err := decode(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	s.reply(w, r, s.Player.Play(r.Context(), playback.PlayRequest{Context: "radio:" + body.Seed}))
}

// getVersion answers GET /version for the Settings screen.
func (s *Server) getVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, version.Get())
}
