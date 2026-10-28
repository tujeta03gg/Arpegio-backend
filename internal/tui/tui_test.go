package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"

	"arpegio/internal/api"
	"arpegio/internal/client"
)

type request struct{ method, path, query, body string }

// daemon is a fake arpegiod that records requests and answers with canned JSON.
type daemon struct {
	mu       sync.Mutex
	requests []request
	player   string
	queue    string
	search   string
	albums   string
	srv      *httptest.Server
}

const playingJSON = `{"track":{"id":"T1","title":"Ingrata","artist":"Café Tacvba","album":"Re"},"position_ms":60000,"duration_ms":120000,"playing":true,"volume":70,"shuffle":false,"repeat":"off"}`

func newDaemon(t *testing.T) *daemon {
	t.Helper()
	d := &daemon{player: playingJSON, queue: `{"items":[]}`, search: `{"tracks":[]}`, albums: `{"items":[],"next_cursor":""}`}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		d.requests = append(d.requests, request{r.Method, r.URL.Path, r.URL.RawQuery, strings.TrimSpace(string(b))})
		d.mu.Unlock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/player"):
			w.Write([]byte(d.player))
		case strings.HasPrefix(r.URL.Path, "/api/v1/queue"):
			w.Write([]byte(d.queue))
		case r.URL.Path == "/api/v1/search":
			w.Write([]byte(d.search))
		case r.URL.Path == "/api/v1/albums":
			w.Write([]byte(d.albums))
		case r.URL.Path == "/api/v1/events":
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			c.Write(r.Context(), websocket.MessageText, []byte(`{"type":"queue.changed","at":"t"}`))
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *daemon) last() request {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.requests) == 0 {
		return request{}
	}
	return d.requests[len(d.requests)-1]
}

func (d *daemon) model(t *testing.T) Model {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return New(ctx, client.New(d.srv.URL))
}

// ---- helpers ----

func key(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// press sends a key and, if it produced a command, runs it and feeds the result back.
func press(t *testing.T, m Model, s string) Model {
	t.Helper()
	next, cmd := m.Update(key(s))
	m = next.(Model)
	if cmd == nil {
		return m
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); ok {
		return m
	}
	next, _ = m.Update(msg)
	return next.(Model)
}

func typeText(t *testing.T, m Model, text string) Model {
	for _, r := range text {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	return m
}

func update(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

func album(id, title, artist string) api.AlbumDTO {
	return api.AlbumDTO{ID: id, Title: title, Artist: artist}
}

// ---- tests ----

func TestNowPlayingView(t *testing.T) {
	d := newDaemon(t)
	m := d.model(t)
	if v := m.View(); !strings.Contains(v, "nothing playing") || !strings.Contains(v, "Now playing") {
		t.Errorf("empty view:\n%s", v)
	}
	var p api.PlayerDTO
	json.Unmarshal([]byte(playingJSON), &p)
	m = update(m, playerMsg(p))
	v := m.View()
	for _, want := range []string{"▶ Ingrata", "Café Tacvba — Re", "1:00 / 2:00", "vol 70", "shuffle off", "repeat off", "━"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
	p.Playing = false
	if v := update(m, playerMsg(p)).View(); !strings.Contains(v, "⏸ Ingrata") {
		t.Errorf("paused view:\n%s", v)
	}
}

func TestPlaybackKeysSendRequests(t *testing.T) {
	d := newDaemon(t)
	m := d.model(t)
	var p api.PlayerDTO
	json.Unmarshal([]byte(playingJSON), &p)
	m = update(m, playerMsg(p))

	tests := []struct {
		key          string
		method, path string
		body         string
	}{
		{" ", "POST", "/api/v1/player/pause", ""},
		{"n", "POST", "/api/v1/player/next", ""},
		{"p", "POST", "/api/v1/player/previous", ""},
		{"+", "PUT", "/api/v1/player/volume", `{"level":75}`},
		{"-", "PUT", "/api/v1/player/volume", `{"level":65}`},
		{"s", "PUT", "/api/v1/player/shuffle", `{"enabled":true}`},
		{"r", "PUT", "/api/v1/player/repeat", `{"mode":"all"}`},
	}
	for _, tt := range tests {
		press(t, m, tt.key)
		got := d.last()
		if got.method != tt.method || got.path != tt.path || got.body != tt.body {
			t.Errorf("key %q sent %+v, want %s %s %q", tt.key, got, tt.method, tt.path, tt.body)
		}
	}

	// paused: space resumes; the volume stays within 0-100
	p.Playing, p.Volume = false, 98
	m = update(m, playerMsg(p))
	press(t, m, " ")
	if got := d.last(); got.path != "/api/v1/player/play" {
		t.Errorf("space while paused sent %+v", got)
	}
	press(t, m, "+")
	if got := d.last(); got.body != `{"level":100}` {
		t.Errorf("volume cap: %+v", got)
	}
	p.Volume = 2
	press(t, update(m, playerMsg(p)), "-")
	if got := d.last(); got.body != `{"level":0}` {
		t.Errorf("volume floor: %+v", got)
	}
	for cur, want := range map[string]string{"off": "all", "all": "one", "one": "off", "": "all"} {
		if nextRepeat(cur) != want {
			t.Errorf("nextRepeat(%q) = %q", cur, nextRepeat(cur))
		}
	}
}

func TestTabNavigationAndQuit(t *testing.T) {
	m := newDaemon(t).model(t)
	for k, want := range map[string]tab{"2": tabLibrary, "3": tabSearch, "4": tabQueue, "1": tabNow} {
		if got := update(m, key(k)).tab; got != want {
			t.Errorf("key %s -> tab %d, want %d", k, got, want)
		}
	}
	m = update(m, key("tab"))
	m = update(m, key("tab"))
	if m.tab != tabSearch {
		t.Errorf("after two tabs: %d", m.tab)
	}
	if got := update(update(newDaemon(t).model(t), key("shift+tab")), key("x")).tab; got != tabQueue {
		t.Errorf("shift+tab from the first tab should wrap to the last, got %d", got)
	}
	for _, k := range []string{"q", "ctrl+c"} {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		if k == "ctrl+c" {
			msg = tea.KeyMsg{Type: tea.KeyCtrlC}
		}
		_, cmd := m.Update(msg)
		if cmd == nil {
			t.Fatalf("%s: no command", k)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%s did not quit", k)
		}
	}
}

func TestLibraryTab(t *testing.T) {
	d := newDaemon(t)
	d.albums = `{"items":[{"id":"A1","title":"Re","artist":"Café Tacvba"},{"id":"A2","title":"Revés","artist":"Café Tacvba"}],"next_cursor":""}`
	m := d.model(t)
	m = update(m, key("2"))
	if v := m.View(); !strings.Contains(v, "run `arpegio scan`") {
		t.Errorf("empty library hint:\n%s", v)
	}
	m = update(m, m.fetchAlbums()()) // what Init does
	if got := d.last(); got.path != "/api/v1/albums" || !strings.Contains(got.query, "limit=500") {
		t.Errorf("albums request = %+v", got)
	}
	if v := m.View(); !strings.Contains(v, "Re — Café Tacvba") || !strings.Contains(v, "Revés") {
		t.Errorf("library view:\n%s", v)
	}

	m = update(m, key("down"))
	press(t, m, "enter")
	if got := d.last(); got.path != "/api/v1/player/play" || got.body != `{"context":"album:A2"}` {
		t.Errorf("enter sent %+v", got)
	}
	press(t, m, "a")
	if got := d.last(); got.path != "/api/v1/queue" || got.body != `{"context":"album:A2"}` {
		t.Errorf("a sent %+v", got)
	}
	m = update(update(m, key("down")), key("down")) // clamped at the end
	if m.albumCur != 1 {
		t.Errorf("cursor = %d", m.albumCur)
	}
	if m = update(m, key("up")); m.albumCur != 0 {
		t.Errorf("cursor = %d", m.albumCur)
	}
}

func TestSearchTab(t *testing.T) {
	d := newDaemon(t)
	d.search = `{"tracks":[{"id":"T9","title":"Ingrata","artist":"Café Tacvba","album":"Re"},{"id":"T8","title":"Eres","artist":"Café Tacvba"}]}`
	m := d.model(t)

	m = update(m, key("/"))
	if !m.typing || m.tab != tabSearch {
		t.Fatalf("/ should open the search box: typing=%v tab=%d", m.typing, m.tab)
	}
	m = typeText(t, m, "q2 cafe") // letters like q and digits are text while typing
	if m.tab != tabSearch || m.search.Value() != "q2 cafe" {
		t.Fatalf("typing: tab=%d value=%q", m.tab, m.search.Value())
	}
	m = press(t, m, "enter")
	if got := d.last(); got.path != "/api/v1/search" || !strings.Contains(got.query, "q=q2+cafe") || !strings.Contains(got.query, "types=track") {
		t.Errorf("search request = %+v", got)
	}
	if m.typing || len(m.results) != 2 || !strings.Contains(m.status, "2 results") {
		t.Fatalf("after search: typing=%v results=%d status=%q", m.typing, len(m.results), m.status)
	}
	if v := m.View(); !strings.Contains(v, "Ingrata — Café Tacvba (Re)") {
		t.Errorf("results view:\n%s", v)
	}

	m = update(m, key("down"))
	press(t, m, "enter")
	if got := d.last(); got.path != "/api/v1/player/play" || got.body != `{"context":"track:T8"}` {
		t.Errorf("play result sent %+v", got)
	}
	press(t, m, "a")
	if got := d.last(); got.path != "/api/v1/queue" || got.body != `{"next":false,"track_ids":["T8"]}` && got.body != `{"track_ids":["T8"]}` {
		t.Errorf("queue result sent %+v", got)
	}

	// esc leaves the box without searching; empty text does not search either
	before := len(d.requests)
	m = update(m, key("/"))
	m = update(m, key("esc"))
	m = update(update(m, key("/")), key("enter"))
	if len(d.requests) != before {
		t.Errorf("unexpected requests: %v", d.requests[before:])
	}
}

func TestQueueTab(t *testing.T) {
	d := newDaemon(t)
	d.queue = `{"items":[
		{"position":1,"priority":0,"source":"album","current":true,"track":{"id":"A","title":"One","artist":"Band"}},
		{"position":1.5,"priority":1,"source":"user","current":false,"track":{"id":"B","title":"Two","artist":"Band"}},
		{"position":2,"priority":0,"source":"album","current":false,"track":{"id":"C","title":"Three","artist":"Band"}}]}`
	m := d.model(t)
	m = update(m, m.fetchQueue()())
	m = update(m, key("4"))
	v := m.View()
	for _, want := range []string{"▶ One — Band", "Two — Band  [next]", "Three — Band"} {
		if !strings.Contains(v, want) {
			t.Errorf("queue view missing %q:\n%s", want, v)
		}
	}

	m = update(m, key("down"))
	press(t, m, "d")
	if got := d.last(); got.method != "DELETE" || got.path != "/api/v1/queue/1.5" {
		t.Errorf("d sent %+v", got)
	}
	press(t, m, "c")
	if got := d.last(); got.method != "DELETE" || got.path != "/api/v1/queue" {
		t.Errorf("c sent %+v", got)
	}
	m = press(t, m, "J") // move the highlighted item down
	if got := d.last(); got.method != "PATCH" || got.path != "/api/v1/queue/1.5" || got.body != `{"index":2}` || m.queueCur != 2 {
		t.Errorf("J sent %+v cursor=%d", got, m.queueCur)
	}
	m = press(t, m, "J") // already last: nothing happens
	before := len(d.requests)
	press(t, m, "J")
	if len(d.requests) != before {
		t.Error("moving past the end must not send a request")
	}
	m = press(t, m, "K")
	if got := d.last(); got.body != `{"index":1}` || m.queueCur != 1 {
		t.Errorf("K sent %+v cursor=%d", got, m.queueCur)
	}
}

func TestServerEventsUpdateTheScreen(t *testing.T) {
	d := newDaemon(t)
	m := d.model(t)
	ch := make(chan client.Event, 8)
	m = update(m, subscribedMsg{ch})

	next, cmd := m.Update(eventMsg{Type: "snapshot", Data: json.RawMessage(`{"player":` + playingJSON + `,"queue":{"items":[{"position":1,"current":true,"track":{"id":"T1","title":"Ingrata"}}]}}`)})
	m = next.(Model)
	if cmd == nil || m.player.Track == nil || len(m.queue.Items) != 1 {
		t.Fatalf("snapshot: player=%+v queue=%+v", m.player, m.queue)
	}
	m = update(m, eventMsg{Type: "player.position", Data: json.RawMessage(`{"position_ms":90000,"duration_ms":120000}`)})
	if m.player.PositionMS != 90000 || m.player.Track == nil {
		t.Errorf("position event: %+v", m.player)
	}
	m = update(m, eventMsg{Type: "player.state_changed", Data: json.RawMessage(`{"volume":10,"playing":false,"repeat":"one"}`)})
	if m.player.Volume != 10 || m.player.Playing || m.player.Repeat != "one" {
		t.Errorf("state event: %+v", m.player)
	}
	for _, typ := range []string{"queue.changed", "catalog.updated", "library.scan_finished", "something.else"} {
		if _, cmd := m.Update(eventMsg{Type: typ}); cmd == nil {
			t.Errorf("%s must keep listening", typ)
		}
	}
	m = update(m, eventMsg{Type: "player.position", Data: json.RawMessage(`garbage`)}) // ignored
	m = update(m, streamClosedMsg{})
	if !strings.Contains(m.status, "disconnected") {
		t.Errorf("status = %q", m.status)
	}
}

func TestSubscribeAgainstARealStream(t *testing.T) {
	d := newDaemon(t)
	m := d.model(t)
	sub, ok := m.subscribe()().(subscribedMsg)
	if !ok {
		t.Fatal("subscribe did not connect")
	}
	ev, ok := waitEvent(sub.ch)().(eventMsg)
	if !ok || ev.Type != "queue.changed" {
		t.Fatalf("event = %+v", ev)
	}
}

func TestErrorsShowInTheStatusLine(t *testing.T) {
	d := newDaemon(t)
	m := d.model(t)
	d.srv.Close()
	m = press(t, m, "n")
	if !strings.HasPrefix(m.status, "error:") || !strings.Contains(m.View(), "error:") {
		t.Errorf("status = %q", m.status)
	}
	if msg := m.subscribe()(); fmt.Sprintf("%T", msg) != "tui.errMsg" {
		t.Errorf("subscribe to a dead daemon = %T", msg)
	}
}

func TestListScrollsToTheCursor(t *testing.T) {
	m := newDaemon(t).model(t)
	m.width, m.height = 60, 14
	var albums albumsMsg
	for i := 0; i < 100; i++ {
		albums = append(albums, album(fmt.Sprint(i), fmt.Sprintf("Album %03d", i), "X"))
	}
	m = update(update(m, albums), key("2"))
	for i := 0; i < 60; i++ {
		m = update(m, key("down"))
	}
	v := m.View()
	if !strings.Contains(v, "Album 060") || strings.Contains(v, "Album 000") {
		t.Errorf("window does not follow the cursor:\n%s", v)
	}
	if lines := strings.Count(v, "\n"); lines > 16 {
		t.Errorf("view has %d lines for a 14-line terminal", lines)
	}
	m = update(m, tea.WindowSizeMsg{Width: 30, Height: 10})
	if m.width != 30 || m.height != 10 {
		t.Errorf("size = %dx%d", m.width, m.height)
	}
}

func TestHelpers(t *testing.T) {
	if got := truncate("abcdefghij", 5); got != "abcd…" {
		t.Errorf("truncate = %q", got)
	}
	if got := truncate("abc", 5); got != "abc" {
		t.Errorf("truncate short = %q", got)
	}
	if progress(0, 0, 4) != "────" || progress(50, 100, 4) != "━━──" || progress(200, 100, 4) != "━━━━" {
		t.Errorf("progress = %q %q %q", progress(0, 0, 4), progress(50, 100, 4), progress(200, 100, 4))
	}
	if clock(65000) != "1:05" || clock(0) != "0:00" {
		t.Errorf("clock = %q %q", clock(65000), clock(0))
	}
	if clamp(5, 0) != 0 || clamp(-1, 3) != 0 || clamp(9, 3) != 2 {
		t.Error("clamp")
	}
}
