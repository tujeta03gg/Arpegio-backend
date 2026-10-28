package api

import (
	"context"
	"net/http"
	"strconv"

	"arpegio/internal/apperr"
	"arpegio/internal/playback"
	"arpegio/internal/queue"
)

// Player is the part of playback.Service the API uses.
type Player interface {
	State(ctx context.Context) (playback.State, error)
	Play(ctx context.Context, req playback.PlayRequest) error
	Pause(ctx context.Context) error
	Next(ctx context.Context) error
	Previous(ctx context.Context) error
	Seek(ctx context.Context, ms int64) error
	SetVolume(ctx context.Context, level int) error
	SetShuffle(ctx context.Context, on bool) error
	SetRepeat(ctx context.Context, mode string) error
	Resolve(ctx context.Context, spec string) (playback.PlayContext, error)
	PlayQueueItem(ctx context.Context, position float64) error
}

// Queue is the part of queue.Service the API uses.
type Queue interface {
	State(ctx context.Context) (queue.State, error)
	Add(ctx context.Context, ids []string, next bool) error
	Move(ctx context.Context, position float64, to int) error
	Remove(ctx context.Context, position float64) error
	Clear(ctx context.Context) error
}

type PlayerDTO struct {
	Track      *TrackDTO `json:"track,omitempty"`
	PositionMS int64     `json:"position_ms"`
	DurationMS int64     `json:"duration_ms"`
	Playing    bool      `json:"playing"`
	Volume     int       `json:"volume"`
	Shuffle    bool      `json:"shuffle"`
	Repeat     string    `json:"repeat"`
}

type QueueItemDTO struct {
	Position float64  `json:"position"`
	Priority int      `json:"priority"`
	Source   string   `json:"source"`
	SourceID string   `json:"source_id,omitempty"`
	Current  bool     `json:"current"`
	Track    TrackDTO `json:"track"`
}

type QueueDTO struct {
	Items   []QueueItemDTO `json:"items"`
	Shuffle bool           `json:"shuffle"`
	Repeat  string         `json:"repeat"`
}

func (s *Server) playerRoutes() {
	s.add("GET /player", s.getPlayer)
	s.add("POST /player/play", s.play)
	s.add("POST /player/pause", s.simple(func(ctx context.Context) error { return s.Player.Pause(ctx) }))
	s.add("POST /player/next", s.simple(func(ctx context.Context) error { return s.Player.Next(ctx) }))
	s.add("POST /player/previous", s.simple(func(ctx context.Context) error { return s.Player.Previous(ctx) }))
	s.add("PUT /player/position", s.putPosition)
	s.add("PUT /player/volume", s.putVolume)
	s.add("PUT /player/shuffle", s.putShuffle)
	s.add("PUT /player/repeat", s.putRepeat)
	s.add("GET /queue", s.getQueue)
	s.add("POST /queue", s.addToQueue)
	s.add("PATCH /queue/{position}", s.moveQueueItem)
	s.add("POST /queue/{position}/play", s.playQueueItem)
	s.add("DELETE /queue/{position}", s.removeQueueItem)
	s.add("DELETE /queue", s.clearQueue)
}

func (s *Server) playerDTO(ctx context.Context) (PlayerDTO, error) {
	st, err := s.Player.State(ctx)
	if err != nil {
		return PlayerDTO{}, err
	}
	return s.toPlayerDTO(ctx, st), nil
}

func (s *Server) toPlayerDTO(ctx context.Context, st playback.State) PlayerDTO {
	d := PlayerDTO{PositionMS: st.PositionMS, DurationMS: st.DurationMS, Playing: st.Playing, Volume: st.Volume, Shuffle: st.Shuffle, Repeat: st.Repeat}
	if st.TrackID != "" {
		if t, err := s.Catalog.Track(ctx, st.TrackID); err == nil {
			td := trackDTO(t)
			d.Track = &td
		}
	}
	return d
}

func (s *Server) getPlayer(w http.ResponseWriter, r *http.Request) {
	d, err := s.playerDTO(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// reply runs an action and answers with the new player state.
func (s *Server) reply(w http.ResponseWriter, r *http.Request, err error) {
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.getPlayer(w, r)
}

func (s *Server) simple(fn func(ctx context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { s.reply(w, r, fn(r.Context())) }
}

func (s *Server) play(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Context string `json:"context"`
		Start   string `json:"start"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.reply(w, r, s.Player.Play(r.Context(), playback.PlayRequest{Context: body.Context, Start: body.Start}))
}

func (s *Server) putPosition(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MS *int64 `json:"ms"`
	}
	if err := decode(r, &body); err != nil || body.MS == nil {
		s.fail(w, r, orInvalid(err, "api: ms is required"))
		return
	}
	s.reply(w, r, s.Player.Seek(r.Context(), *body.MS))
}

func (s *Server) putVolume(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Level *int `json:"level"`
	}
	if err := decode(r, &body); err != nil || body.Level == nil {
		s.fail(w, r, orInvalid(err, "api: level is required"))
		return
	}
	s.reply(w, r, s.Player.SetVolume(r.Context(), *body.Level))
}

func (s *Server) putShuffle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decode(r, &body); err != nil || body.Enabled == nil {
		s.fail(w, r, orInvalid(err, "api: enabled is required"))
		return
	}
	s.reply(w, r, s.Player.SetShuffle(r.Context(), *body.Enabled))
}

func (s *Server) putRepeat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if err := decode(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	s.reply(w, r, s.Player.SetRepeat(r.Context(), body.Mode))
}

func orInvalid(err error, msg string) error {
	if err != nil {
		return err
	}
	return apperr.Invalid("%s", msg)
}

// ---- queue ----

func (s *Server) queueDTO(ctx context.Context) (QueueDTO, error) {
	st, err := s.Queue.State(ctx)
	if err != nil {
		return QueueDTO{}, err
	}
	out := QueueDTO{Items: []QueueItemDTO{}, Shuffle: st.Shuffle, Repeat: st.Repeat}
	for _, it := range st.Items {
		d := QueueItemDTO{Position: it.Position, Priority: it.Priority, Source: it.Source, SourceID: it.SourceID,
			Current: st.CurrentPos != nil && *st.CurrentPos == it.Position, Track: TrackDTO{ID: it.TrackID}}
		if t, err := s.Catalog.Track(ctx, it.TrackID); err == nil {
			d.Track = trackDTO(t)
		}
		out.Items = append(out.Items, d)
	}
	return out, nil
}

func (s *Server) getQueue(w http.ResponseWriter, r *http.Request) {
	d, err := s.queueDTO(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) addToQueue(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TrackIDs []string `json:"track_ids"`
		Context  string   `json:"context"`
		Next     bool     `json:"next"`
	}
	if err := decode(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	ids := body.TrackIDs
	if body.Context != "" {
		pc, err := s.Player.Resolve(ctx, body.Context)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		ids = append(ids, pc.TrackIDs...)
	}
	for _, id := range ids { // reject unknown tracks with 404 instead of a database error
		if _, err := s.Catalog.Track(ctx, id); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if err := s.Queue.Add(ctx, ids, body.Next); err != nil {
		s.fail(w, r, err)
		return
	}
	s.getQueue(w, r)
}

func parsePosition(r *http.Request) (float64, error) {
	p, err := strconv.ParseFloat(r.PathValue("position"), 64)
	if err != nil {
		return 0, apperr.Invalid("api: position must be a number")
	}
	return p, nil
}

func (s *Server) moveQueueItem(w http.ResponseWriter, r *http.Request) {
	pos, err := parsePosition(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var body struct {
		Index *int `json:"index"`
	}
	if err := decode(r, &body); err != nil || body.Index == nil {
		s.fail(w, r, orInvalid(err, "api: index is required"))
		return
	}
	if err := s.Queue.Move(r.Context(), pos, *body.Index); err != nil {
		s.fail(w, r, err)
		return
	}
	s.getQueue(w, r)
}

func (s *Server) removeQueueItem(w http.ResponseWriter, r *http.Request) {
	pos, err := parsePosition(r)
	if err == nil {
		err = s.Queue.Remove(r.Context(), pos)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.getQueue(w, r)
}

func (s *Server) clearQueue(w http.ResponseWriter, r *http.Request) {
	if err := s.Queue.Clear(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	s.getQueue(w, r)
}

// playQueueItem jumps to an item of the queue (for example one under "up next") and plays it.
func (s *Server) playQueueItem(w http.ResponseWriter, r *http.Request) {
	pos, err := parsePosition(r)
	if err == nil {
		err = s.Player.PlayQueueItem(r.Context(), pos)
	}
	s.reply(w, r, err)
}
