package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"arpegio/internal/apperr"
	"arpegio/internal/events"
	"arpegio/internal/playback"
)

// fakePlayer records what the API asked for and returns canned state.
type fakePlayer struct {
	calls []string
	state playback.State
	album []string // what Resolve("album:good") returns
	err   error
}

func (f *fakePlayer) rec(format string, a ...any) error {
	f.calls = append(f.calls, fmt.Sprintf(format, a...))
	return f.err
}
func (f *fakePlayer) State(context.Context) (playback.State, error) { return f.state, nil }
func (f *fakePlayer) Play(_ context.Context, r playback.PlayRequest) error {
	return f.rec("play %q %q", r.Context, r.Start)
}
func (f *fakePlayer) Pause(context.Context) error                 { return f.rec("pause") }
func (f *fakePlayer) Next(context.Context) error                  { return f.rec("next") }
func (f *fakePlayer) Previous(context.Context) error              { return f.rec("previous") }
func (f *fakePlayer) Seek(_ context.Context, ms int64) error      { return f.rec("seek %d", ms) }
func (f *fakePlayer) SetVolume(_ context.Context, l int) error    { return f.rec("volume %d", l) }
func (f *fakePlayer) SetShuffle(_ context.Context, on bool) error { return f.rec("shuffle %v", on) }
func (f *fakePlayer) SetRepeat(_ context.Context, m string) error { return f.rec("repeat %s", m) }
func (f *fakePlayer) Resolve(_ context.Context, spec string) (playback.PlayContext, error) {
	if spec == "album:good" {
		return playback.PlayContext{TrackIDs: f.album}, nil
	}
	return playback.PlayContext{}, apperr.Invalid("unknown context %q", spec)
}

func (f *fakePlayer) take() string {
	s := strings.Join(f.calls, "|")
	f.calls = nil
	return s
}

func TestPlayerStateAndCommands(t *testing.T) {
	e := newEnv(t)
	e.seed()
	var page Paged[TrackDTO]
	e.do("GET", "/tracks", nil, &page)
	e.player.state = playback.State{TrackID: page.Items[0].ID, PositionMS: 1500, DurationMS: 1000, Playing: true, Volume: 60, Shuffle: true, Repeat: "all"}

	var p PlayerDTO
	if code := e.do("GET", "/player", nil, &p); code != 200 || p.Track == nil || p.Track.ID != page.Items[0].ID || !p.Playing || p.Volume != 60 || !p.Shuffle || p.Repeat != "all" || p.PositionMS != 1500 {
		t.Fatalf("player = %d %+v", code, p)
	}

	tests := []struct {
		method, path string
		body         any
		want         string
	}{
		{"POST", "/player/play", map[string]string{"context": "album:A1", "start": "track:t1"}, `play "album:A1" "track:t1"`},
		{"POST", "/player/play", nil, `play "" ""`},
		{"POST", "/player/pause", nil, "pause"},
		{"POST", "/player/next", nil, "next"},
		{"POST", "/player/previous", nil, "previous"},
		{"PUT", "/player/position", map[string]int{"ms": 42000}, "seek 42000"},
		{"PUT", "/player/volume", map[string]int{"level": 0}, "volume 0"},
		{"PUT", "/player/shuffle", map[string]bool{"enabled": true}, "shuffle true"},
		{"PUT", "/player/repeat", map[string]string{"mode": "one"}, "repeat one"},
	}
	for _, tt := range tests {
		var out PlayerDTO
		if code := e.do(tt.method, tt.path, tt.body, &out); code != 200 {
			t.Errorf("%s %s = %d", tt.method, tt.path, code)
		}
		if got := e.player.take(); got != tt.want {
			t.Errorf("%s %s called %q, want %q", tt.method, tt.path, got, tt.want)
		}
		if out.Volume != 60 { // every command answers with the new state
			t.Errorf("%s %s did not return the player state: %+v", tt.method, tt.path, out)
		}
	}
}

func TestPlayerRequestValidation(t *testing.T) {
	e := newEnv(t)
	for _, tt := range []struct {
		method, path string
		body         any
	}{
		{"PUT", "/player/position", map[string]string{}},
		{"PUT", "/player/volume", map[string]string{}},
		{"PUT", "/player/volume", map[string]string{"level": "loud"}},
		{"PUT", "/player/shuffle", map[string]string{}},
		{"PUT", "/player/repeat", map[string]string{"nope": "x"}},
		{"POST", "/player/play", map[string]string{"bogus": "x"}},
	} {
		if code := e.do(tt.method, tt.path, tt.body, nil); code != 400 {
			t.Errorf("%s %s %v = %d, want 400", tt.method, tt.path, tt.body, code)
		}
	}
	if len(e.player.calls) != 0 {
		t.Errorf("player called on invalid input: %v", e.player.calls)
	}

	e.player.err = apperr.Invalid("the queue is empty")
	var body errBody
	if code := e.do("POST", "/player/play", nil, &body); code != 400 || body.Error.Code != "invalid" {
		t.Errorf("domain error = %d %+v", code, body)
	}
	e.player.err = apperr.ErrNotFound
	if code := e.do("POST", "/player/next", nil, nil); code != 404 {
		t.Errorf("not found = %d", code)
	}
	e.player.err = playback.ErrNotConnected
	if code := e.do("POST", "/player/play", nil, &body); code != 503 || body.Error.Code != "unavailable" {
		t.Errorf("mpv missing = %d %+v", code, body)
	}
}

func TestQueueEndpoints(t *testing.T) {
	e := newEnv(t)
	e.seed()
	var page Paged[TrackDTO]
	e.do("GET", "/tracks", nil, &page)
	a, b := page.Items[0].ID, page.Items[1].ID
	e.player.album = []string{a, b}

	var q QueueDTO
	if code := e.do("GET", "/queue", nil, &q); code != 200 || len(q.Items) != 0 || q.Items == nil {
		t.Fatalf("empty queue = %d %+v", code, q)
	}
	if code := e.do("POST", "/queue", map[string]any{"track_ids": []string{a}}, &q); code != 200 || len(q.Items) != 1 || q.Items[0].Track.Title == "" || q.Items[0].Source != "user" {
		t.Fatalf("add = %d %+v", code, q)
	}
	if code := e.do("POST", "/queue", map[string]any{"track_ids": []string{b}, "next": true}, &q); code != 200 || len(q.Items) != 2 || q.Items[0].Track.ID != b || q.Items[0].Priority != 1 {
		t.Fatalf("add next = %d %+v", code, q)
	}
	if code := e.do("POST", "/queue", map[string]any{"context": "album:good"}, &q); code != 200 || len(q.Items) != 4 {
		t.Fatalf("add context = %d %+v", code, q)
	}
	for _, bad := range []map[string]any{{"track_ids": []string{"nope"}}, {"context": "album:bad"}, {"track_ids": []string{}}, {"x": 1}} {
		if code := e.do("POST", "/queue", bad, nil); code < 400 {
			t.Errorf("POST /queue %v = %d, want an error", bad, code)
		}
	}

	pos := q.Items[3].Position
	if code := e.do("PATCH", fmt.Sprintf("/queue/%v", pos), map[string]int{"index": 0}, &q); code != 200 || q.Items[0].Position == q.Items[1].Position {
		t.Fatalf("move = %d %+v", code, q)
	}
	if code := e.do("PATCH", "/queue/abc", map[string]int{"index": 0}, nil); code != 400 {
		t.Errorf("bad position = %d", code)
	}
	if code := e.do("PATCH", fmt.Sprintf("/queue/%v", pos), map[string]int{}, nil); code != 400 {
		t.Errorf("missing index = %d", code)
	}
	if code := e.do("PATCH", "/queue/999", map[string]int{"index": 0}, nil); code != 404 {
		t.Errorf("unknown position = %d", code)
	}

	if code := e.do("DELETE", fmt.Sprintf("/queue/%v", q.Items[1].Position), nil, &q); code != 200 || len(q.Items) != 3 {
		t.Fatalf("remove = %d %+v", code, q)
	}
	if code := e.do("DELETE", "/queue/999", nil, nil); code != 404 {
		t.Errorf("remove unknown = %d", code)
	}
	if code := e.do("DELETE", "/queue", nil, &q); code != 200 || len(q.Items) != 0 {
		t.Fatalf("clear = %d %+v", code, q)
	}
}

func dialEvents(t *testing.T, e *env) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.ts.URL, "http")+prefix+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c
}

func readMsg(t *testing.T, c *websocket.Conn) Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m Message
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestWebSocketSnapshotThenEvents(t *testing.T) {
	e := newEnv(t)
	e.player.state = playback.State{Volume: 33, Repeat: "off"}
	c := dialEvents(t, e)

	snap := readMsg(t, c)
	if snap.Type != "snapshot" || snap.At == "" {
		t.Fatalf("first message = %+v", snap)
	}
	data := snap.Data.(map[string]any)
	if data["player"].(map[string]any)["volume"].(float64) != 33 || data["queue"] == nil || data["library"] == nil {
		t.Errorf("snapshot data = %v", data)
	}

	e.bus.Publish(events.Event{Type: "queue.changed"})
	if m := readMsg(t, c); m.Type != "queue.changed" {
		t.Errorf("message = %+v", m)
	}
	e.bus.Publish(events.Event{Type: "player.state_changed", Data: playback.State{Volume: 80, Playing: true, Repeat: "all"}})
	m := readMsg(t, c)
	pd := m.Data.(map[string]any)
	if m.Type != "player.state_changed" || pd["volume"].(float64) != 80 || pd["playing"] != true || pd["repeat"] != "all" {
		t.Errorf("state event = %+v", m)
	}
}
