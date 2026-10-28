package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"arpegio/internal/events"
	"arpegio/internal/library"
	"arpegio/internal/playback"
)

// Events is the part of events.Bus the WebSocket uses.
type Events interface {
	Subscribe(buffer int) (<-chan events.Event, func())
}

// Message is one WebSocket frame: {"type":"…","data":{…},"at":"…"}.
type Message struct {
	Type string `json:"type"`
	Data any    `json:"data"`
	At   string `json:"at"`
}

// snapshotDTO is sent on connect so a client can render without extra requests.
type snapshotDTO struct {
	Player  PlayerDTO `json:"player"`
	Queue   QueueDTO  `json:"queue"`
	Library StatusDTO `json:"library"`
}

func (s *Server) eventRoutes() { s.add("GET /events", s.events) }

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	// Subscribe before building the snapshot so no event falls in between.
	ch, cancel := s.Events.Subscribe(64)
	defer cancel()

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true}) // local API: any origin
	if err != nil {
		return // Accept already answered
	}
	defer conn.CloseNow()
	ctx := conn.CloseRead(r.Context()) // we never read; this also notices the client leaving

	snap, err := s.snapshot(ctx)
	if err != nil {
		s.Log.Error("api: snapshot", slog.Any("err", err))
		conn.Close(websocket.StatusInternalError, "snapshot failed")
		return
	}
	if !s.write(ctx, conn, "snapshot", snap) {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok || !s.write(ctx, conn, ev.Type, s.eventData(ctx, ev)) {
				return
			}
		}
	}
}

func (s *Server) snapshot(ctx context.Context) (snapshotDTO, error) {
	p, err := s.playerDTO(ctx)
	if err != nil {
		return snapshotDTO{}, err
	}
	q, err := s.queueDTO(ctx)
	if err != nil {
		return snapshotDTO{}, err
	}
	return snapshotDTO{Player: p, Queue: q, Library: s.statusDTO(ctx)}, nil
}

func (s *Server) write(ctx context.Context, conn *websocket.Conn, typ string, data any) bool {
	b, err := json.Marshal(Message{Type: typ, Data: data, At: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		s.Log.Error("api: encode event", slog.String("type", typ), slog.Any("err", err))
		return true // skip this event, keep the connection
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, b) == nil
}

// eventData maps internal event payloads to the DTOs clients see.
func (s *Server) eventData(ctx context.Context, ev events.Event) any {
	switch d := ev.Data.(type) {
	case playback.State:
		return s.toPlayerDTO(ctx, d)
	case library.Result:
		return map[string]any{"added": d.Added, "updated": d.Updated, "moved": d.Moved, "missing": d.Missing, "skipped": d.Skipped, "failed": d.Failed}
	}
	return ev.Data
}
