package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// run executes the CLI against a fake daemon and returns its output.
func run(t *testing.T, h http.HandlerFunc, args ...string) (string, error) {
	t.Helper()
	ts := httptest.NewServer(h)
	defer ts.Close()
	var out bytes.Buffer
	cmd := New(ts.URL, &out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func TestSearchPrintsGroupedHits(t *testing.T) {
	out, err := run(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/search" || r.URL.Query().Get("q") != "cafe tacvba" {
			t.Errorf("unexpected request %s", r.URL)
		}
		w.Write([]byte(`{"artists":[{"id":"A1","title":"Café Tacvba"}],"albums":[{"id":"B1","title":"Re","artist":"Café Tacvba"}],
			"tracks":[{"id":"T1","title":"Ingrata","artist":"Café Tacvba","album":"Re"}]}`))
	}, "search", "cafe", "tacvba")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"artist A1  Café Tacvba", "album  B1  Re — Café Tacvba", "track  T1  Ingrata — Café Tacvba (Re)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestSearchNoResultsAndErrors(t *testing.T) {
	out, err := run(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }, "search", "zzz")
	if err != nil || !strings.Contains(out, "no results") {
		t.Errorf("out=%q err=%v", out, err)
	}
	_, err = run(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"code":"invalid","message":"empty search"}}`))
	}, "search", "x")
	if err == nil || !strings.Contains(err.Error(), "empty search") {
		t.Errorf("err = %v", err)
	}
	if _, err := run(t, nil, "search"); err == nil {
		t.Error("search without text should fail")
	}
}

func TestScanStartsAndWaits(t *testing.T) {
	var polls atomic.Int32
	h := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/library/scan":
			w.WriteHeader(202)
		case "/api/v1/library/status":
			if polls.Add(1) == 1 {
				w.Write([]byte(`{"scanning":true,"done":3,"total":10}`))
				return
			}
			w.Write([]byte(`{"scanning":false}`))
		}
	}
	out, err := run(t, h, "scan")
	if err != nil || !strings.Contains(out, "scan started") || polls.Load() != 0 {
		t.Fatalf("no-wait: out=%q err=%v polls=%d", out, err, polls.Load())
	}
	out, err = run(t, h, "scan", "--wait")
	if err != nil || !strings.Contains(out, "scanning 3/10") || !strings.Contains(out, "scan finished") {
		t.Fatalf("wait: out=%q err=%v", out, err)
	}
}

func TestLibraryCommands(t *testing.T) {
	var calls []string
	h := func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == "GET":
			w.Write([]byte(`{"items":[{"id":"R1","path":"/music","enabled":true},{"id":"R2","path":"/old","enabled":false}]}`))
		case r.Method == "POST":
			w.WriteHeader(201)
			w.Write([]byte(`{"id":"R3","path":"/new","enabled":true}`))
		default:
			w.WriteHeader(204)
		}
	}
	out, err := run(t, h, "library", "list")
	if err != nil || !strings.Contains(out, "R1  on  /music") || !strings.Contains(out, "R2  off /old") {
		t.Errorf("list: %q %v", out, err)
	}
	out, err = run(t, h, "library", "add", "/new")
	if err != nil || !strings.Contains(out, "watching /new (R3)") {
		t.Errorf("add: %q %v", out, err)
	}
	if _, err = run(t, h, "library", "remove", "R1"); err != nil {
		t.Errorf("remove: %v", err)
	}
	if got := strings.Join(calls, ","); got != "GET /api/v1/library/roots,POST /api/v1/library/roots,DELETE /api/v1/library/roots/R1" {
		t.Errorf("calls = %s", got)
	}
}

const playerJSON = `{"track":{"id":"T1","title":"Ingrata","artist":"Café Tacvba","album":"Re","duration_ms":225000},
	"position_ms":83000,"duration_ms":225000,"playing":true,"volume":70,"shuffle":true,"repeat":"all"}`

func TestPlaybackCommandsSendTheRightRequests(t *testing.T) {
	tests := []struct {
		args   []string
		method string
		path   string
		body   string
	}{
		{[]string{"play"}, "POST", "/api/v1/player/play", `{}`},
		{[]string{"play", "album:A1", "--start", "track:T1"}, "POST", "/api/v1/player/play", `{"context":"album:A1","start":"track:T1"}`},
		{[]string{"pause"}, "POST", "/api/v1/player/pause", ``},
		{[]string{"next"}, "POST", "/api/v1/player/next", ``},
		{[]string{"previous"}, "POST", "/api/v1/player/previous", ``},
		{[]string{"status"}, "GET", "/api/v1/player", ``},
		{[]string{"seek", "83.5"}, "PUT", "/api/v1/player/position", `{"ms":83500}`},
		{[]string{"volume", "35"}, "PUT", "/api/v1/player/volume", `{"level":35}`},
		{[]string{"shuffle", "on"}, "PUT", "/api/v1/player/shuffle", `{"enabled":true}`},
		{[]string{"shuffle", "off"}, "PUT", "/api/v1/player/shuffle", `{"enabled":false}`},
		{[]string{"repeat", "one"}, "PUT", "/api/v1/player/repeat", `{"mode":"one"}`},
	}
	for _, tt := range tests {
		var gotMethod, gotPath, gotBody string
		out, err := run(t, func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			gotMethod, gotPath, gotBody = r.Method, r.URL.Path, strings.TrimSpace(string(b))
			w.Write([]byte(playerJSON))
		}, tt.args...)
		if err != nil {
			t.Errorf("%v: %v", tt.args, err)
			continue
		}
		if gotMethod != tt.method || gotPath != tt.path || gotBody != tt.body {
			t.Errorf("%v sent %s %s %q, want %s %s %q", tt.args, gotMethod, gotPath, gotBody, tt.method, tt.path, tt.body)
		}
		if !strings.Contains(out, "▶ Ingrata — Café Tacvba (Re)") || !strings.Contains(out, "1:23 / 3:45") ||
			!strings.Contains(out, "vol 70  shuffle on  repeat all") {
			t.Errorf("%v printed %q", tt.args, out)
		}
	}
}

func TestStatusWhenStoppedAndPaused(t *testing.T) {
	out, _ := run(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"position_ms":0,"duration_ms":0,"playing":false,"volume":40,"shuffle":false,"repeat":"off"}`))
	}, "status")
	if !strings.Contains(out, "stopped  vol 40  shuffle off  repeat off") {
		t.Errorf("stopped: %q", out)
	}
	out, _ = run(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"track":{"id":"T","title":"X"},"playing":false,"volume":1,"repeat":"off"}`))
	}, "status")
	if !strings.Contains(out, "⏸ X") {
		t.Errorf("paused: %q", out)
	}
}

func TestPlaybackCommandsValidateArguments(t *testing.T) {
	never := func(w http.ResponseWriter, r *http.Request) { t.Errorf("request sent: %s", r.URL) }
	for _, args := range [][]string{{"seek", "abc"}, {"seek", "-5"}, {"volume", "loud"}, {"shuffle", "maybe"}, {"queue", "move", "1", "top"}, {"volume"}} {
		if _, err := run(t, never, args...); err == nil {
			t.Errorf("%v should fail", args)
		}
	}
	_, err := run(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"code":"invalid","message":"volume must be 0-100"}}`))
	}, "volume", "500")
	if err == nil || !strings.Contains(err.Error(), "volume must be 0-100") {
		t.Errorf("server error not surfaced: %v", err)
	}
}

func TestQueueCommands(t *testing.T) {
	queueJSON := `{"items":[
		{"position":1,"priority":0,"source":"album","current":true,"track":{"id":"A","title":"One","artist":"Band"}},
		{"position":1.5,"priority":1,"source":"user","current":false,"track":{"id":"B","title":"Two","artist":"Band"}}]}`
	var calls []string
	h := func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		calls = append(calls, r.Method+" "+r.URL.Path+" "+strings.TrimSpace(string(b)))
		w.Write([]byte(queueJSON))
	}
	out, err := run(t, h, "queue")
	if err != nil || !strings.Contains(out, "▶ 1        One — Band") || !strings.Contains(out, "  1.5      Two — Band  [next]") {
		t.Errorf("queue: %q %v", out, err)
	}
	run(t, h, "queue", "add", "--next", "T1", "T2")
	run(t, h, "queue", "add", "album:A9")
	run(t, h, "queue", "remove", "1.5")
	run(t, h, "queue", "move", "1.5", "0")
	run(t, h, "queue", "clear")
	want := []string{
		"GET /api/v1/queue ",
		`POST /api/v1/queue {"next":true,"track_ids":["T1","T2"]}`,
		`POST /api/v1/queue {"context":"album:A9","next":false}`,
		"DELETE /api/v1/queue/1.5 ",
		`PATCH /api/v1/queue/1.5 {"index":0}`,
		"DELETE /api/v1/queue ",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
	empty, _ := run(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"items":[]}`)) }, "queue")
	if !strings.Contains(empty, "queue is empty") {
		t.Errorf("empty queue: %q", empty)
	}
}

func TestCollectionCommands(t *testing.T) {
	type call struct{ method, path, body string }
	var calls []call
	responses := map[string]string{
		"GET /api/v1/playlists":             `{"items":[{"id":"P1","name":"Road trip","item_count":2}]}`,
		"POST /api/v1/playlists":            `{"id":"P2","name":"New one","item_count":0}`,
		"GET /api/v1/playlists/P1":          `{"id":"P1","name":"Road trip","item_count":1,"items":[{"position":1,"track":{"id":"T1","title":"Song","artist":"Band"}}]}`,
		"POST /api/v1/playlists/P1/items":   `{"id":"P1","name":"Road trip","item_count":1,"items":[{"position":1,"track":{"id":"T1","title":"Song","artist":"Band"}}]}`,
		"DELETE /api/v1/playlists/P1/items": `{"id":"P1","name":"Road trip","item_count":0}`,
		"GET /api/v1/likes":                 `{"items":[{"id":"T1","title":"Song","artist":"Band"}]}`,
		"GET /api/v1/history":               `{"items":[{"track":{"title":"Song","artist":"Band"},"started_at":"2026-10-27T10:00:00Z","listened_ms":65000,"skipped":false},{"track":{"title":"Meh"},"started_at":"2026-10-27T09:00:00Z","listened_ms":3000,"skipped":true}]}`,
		"GET /api/v1/stats":                 `{"range":"7d","plays":12,"listened_ms":7200000,"top_tracks":[{"id":"T1","name":"Song","plays":5}],"top_artists":[],"top_genres":[{"id":"G","name":"Rock","plays":9}]}`,
		"GET /api/v1/home":                  `{"recently_played":[{"id":"T1","title":"Song","artist":"Band"}],"most_played":[],"recently_added":[]}`,
	}
	h := func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		calls = append(calls, call{r.Method, r.URL.Path, strings.TrimSpace(string(b))})
		if body, ok := responses[r.Method+" "+r.URL.Path]; ok {
			w.Write([]byte(body))
			return
		}
		w.WriteHeader(204)
	}
	check := func(args []string, want ...string) {
		t.Helper()
		out, err := run(t, h, args...)
		if err != nil {
			t.Errorf("%v: %v", args, err)
			return
		}
		for _, w := range want {
			if !strings.Contains(out, w) {
				t.Errorf("%v output missing %q:\n%s", args, w, out)
			}
		}
	}
	check([]string{"playlist", "list"}, "P1  Road trip (2 tracks)")
	check([]string{"playlist", "create", "New", "one"}, "created New one (P2)")
	check([]string{"playlist", "show", "P1"}, "Road trip (1 tracks)", "1        Song — Band")
	check([]string{"playlist", "add", "P1", "T1"}, "Song — Band")
	check([]string{"playlist", "remove", "P1", "1.5"}, "Road trip (0 tracks)")
	check([]string{"playlist", "delete", "P1"})
	check([]string{"like", "track", "T1"})
	check([]string{"unlike", "album", "A1"})
	check([]string{"likes"}, "T1  Song — Band")
	check([]string{"history"}, "✓ 2026-10-27T10:00:00Z  Song — Band  (1:05)", "⤼ 2026-10-27T09:00:00Z  Meh  (0:03)")
	check([]string{"stats", "--range", "7d"}, "last 7d: 12 plays, 2.0 hours", "1. Song (5)", "Top genres", "Rock (9)")
	check([]string{"home"}, "Recently played", "T1  Song — Band", "Most played\n  —")

	want := []call{
		{"GET", "/api/v1/playlists", ""}, {"POST", "/api/v1/playlists", `{"name":"New one"}`}, {"GET", "/api/v1/playlists/P1", ""},
		{"POST", "/api/v1/playlists/P1/items", `{"track_ids":["T1"]}`}, {"DELETE", "/api/v1/playlists/P1/items", `{"position":1.5}`},
		{"DELETE", "/api/v1/playlists/P1", ""}, {"PUT", "/api/v1/likes/track/T1", ""}, {"DELETE", "/api/v1/likes/album/A1", ""},
	}
	for i, w := range want {
		if calls[i] != w {
			t.Errorf("call %d = %+v, want %+v", i, calls[i], w)
		}
	}
	if _, err := run(t, h, "playlist", "remove", "P1", "abc"); err == nil {
		t.Error("bad position should fail")
	}
}

func TestRadioLyricsAndImportCommands(t *testing.T) {
	var gotPath, gotBody, gotType string
	h := func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotPath, gotBody, gotType = r.URL.Path, string(b), r.Header.Get("Content-Type")
		switch r.URL.Path {
		case "/api/v1/radio":
			w.Write([]byte(playerJSON))
		case "/api/v1/tracks/T1/lyrics":
			w.Write([]byte(`{"synced":true,"lines":[{"time_ms":1500,"text":"Hello"},{"time_ms":65000,"text":"World"}]}`))
		case "/api/v1/tracks/T2/lyrics":
			w.Write([]byte(`{"synced":false,"lines":[{"text":"Just words"}]}`))
		case "/api/v1/library/import":
			w.WriteHeader(201)
			w.Write([]byte(`{"imported":[{"name":"a.mp3","path":"/m/a.mp3"}],"errors":[{"name":"notes.txt","error":"not audio"}]}`))
		}
	}
	out, err := run(t, h, "radio", "track:T1")
	if err != nil || gotPath != "/api/v1/radio" || gotBody != `{"seed":"track:T1"}`+"\n" || !strings.Contains(out, "▶ Ingrata") {
		t.Errorf("radio: %q %q %q %v", out, gotPath, gotBody, err)
	}
	out, err = run(t, h, "lyrics", "T1")
	if err != nil || !strings.Contains(out, "[0:01] Hello") || !strings.Contains(out, "[1:05] World") {
		t.Errorf("synced lyrics: %q %v", out, err)
	}
	out, _ = run(t, h, "lyrics", "T2")
	if out != "Just words\n" {
		t.Errorf("plain lyrics: %q", out)
	}

	dir := t.TempDir()
	audio, junk := dir+"/a.mp3", dir+"/notes.txt"
	os.WriteFile(audio, []byte("AUDIO-BYTES"), 0o644)
	os.WriteFile(junk, []byte("x"), 0o644)
	out, err = run(t, h, "import", audio, junk)
	if err != nil || !strings.Contains(out, "imported a.mp3") || !strings.Contains(out, "skipped notes.txt: not audio") {
		t.Errorf("import: %q %v", out, err)
	}
	if !strings.HasPrefix(gotType, "multipart/form-data") || !strings.Contains(gotBody, "AUDIO-BYTES") || !strings.Contains(gotBody, `filename="a.mp3"`) {
		t.Errorf("upload body: %q (%s)", gotBody, gotType)
	}
	if _, err := run(t, h, "import", dir+"/missing.mp3"); err == nil {
		t.Error("a missing local file should fail")
	}
}
