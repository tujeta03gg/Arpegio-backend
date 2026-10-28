package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"arpegio/internal/catalog"
	"arpegio/internal/events"
	"arpegio/internal/history"
	"arpegio/internal/library"
	"arpegio/internal/metadata"
	"arpegio/internal/playlists"
	"arpegio/internal/queue"
	"arpegio/internal/store"
	"arpegio/internal/testutil"
)

type env struct {
	t      *testing.T
	srv    *Server
	ts     *httptest.Server
	dir    string
	lib    *library.Service
	queue  *queue.Service
	player *fakePlayer
	bus    *events.Bus
	hist   *history.Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bus := events.New()
	cat := catalog.New(st, bus)
	probe := func(context.Context, string) (metadata.Props, error) {
		return metadata.Props{DurationMS: 1000, BitrateKbps: 320, SampleRate: 44100, BitDepth: 16, Channels: 2}, nil
	}
	lib := library.New(st, bus, cat, metadata.Reader{Probe: probe}, filepath.Join(t.TempDir(), "art"), nil)
	q := queue.New(st, bus)
	fp := &fakePlayer{}
	pl := playlists.New(st, bus, cat.Exists)
	hist := history.New(st, history.Rule{Seconds: 30, Percent: 50})
	srv := New(Deps{Catalog: cat, Library: lib, Player: fp, Queue: q, Events: bus, Playlists: pl, History: hist})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &env{t: t, srv: srv, ts: ts, dir: t.TempDir(), lib: lib, queue: q, player: fp, bus: bus, hist: hist}
}

// do sends a request and decodes the JSON response into out (if non-nil).
func (e *env) do(method, path string, body any, out any) int {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, e.ts.URL+prefix+path, rd)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			e.t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return res.StatusCode
}

func (e *env) seed() {
	e.t.Helper()
	testutil.WriteMP3(e.t, e.dir, "a/one.mp3", map[string]string{"TIT2": "Canción uno", "TPE1": "Café Tacvba", "TALB": "Re", "TCON": "Rock", "TRCK": "1"}, "0123456789")
	testutil.WriteMP3(e.t, e.dir, "a/two.mp3", map[string]string{"TIT2": "Dos", "TPE1": "Café Tacvba", "TALB": "Re", "TCON": "Rock", "TRCK": "2"}, "abcdefghij")
	if code := e.do("POST", "/library/roots", map[string]string{"path": e.dir}, nil); code != http.StatusCreated {
		e.t.Fatalf("add root: %d", code)
	}
	if code := e.do("POST", "/library/scan", nil, nil); code != http.StatusAccepted {
		e.t.Fatalf("scan: %d", code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var tracks Paged[TrackDTO]
		e.do("GET", "/tracks", nil, &tracks)
		if len(tracks.Items) == 2 {
			return
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("scan did not finish, tracks=%d", len(tracks.Items))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestEveryRouteIsInOpenAPI(t *testing.T) {
	spec, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// collect "  /path:" keys and the verbs under each
	paths := map[string]map[string]bool{}
	var cur string
	pathRe := regexp.MustCompile(`^  (/\S*):$`)
	verbRe := regexp.MustCompile(`^    (get|post|put|patch|delete):$`)
	for _, line := range strings.Split(string(spec), "\n") {
		if m := pathRe.FindStringSubmatch(line); m != nil {
			cur = m[1]
			paths[cur] = map[string]bool{}
		} else if m := verbRe.FindStringSubmatch(line); m != nil && cur != "" {
			paths[cur][m[1]] = true
		}
	}
	for _, p := range New(Deps{}).Patterns() {
		method, path, _ := splitPattern(p)
		if !paths[path][strings.ToLower(method)] {
			t.Errorf("route %q is missing from api/openapi.yaml", p)
		}
	}
}

func TestCatalogEndpoints(t *testing.T) {
	e := newEnv(t)
	e.seed()

	var hits map[string][]HitDTO
	if code := e.do("GET", "/search?q=cancion&types=track", nil, &hits); code != 200 || len(hits["tracks"]) != 1 {
		t.Fatalf("search: %d %v", code, hits)
	}
	if code := e.do("GET", "/search?q=", nil, nil); code != 400 {
		t.Errorf("empty search = %d", code)
	}
	if code := e.do("GET", "/search?q=x&limit=abc", nil, nil); code != 400 {
		t.Errorf("bad limit = %d", code)
	}

	var page Paged[TrackDTO]
	e.do("GET", "/tracks?limit=1", nil, &page)
	if len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("page 1 = %+v", page)
	}
	var page2 Paged[TrackDTO]
	e.do("GET", "/tracks?limit=1&cursor="+page.NextCursor, nil, &page2)
	if len(page2.Items) != 1 || page2.Items[0].ID == page.Items[0].ID {
		t.Fatalf("page 2 = %+v", page2)
	}

	var track TrackDTO
	if code := e.do("GET", "/tracks/"+page.Items[0].ID, nil, &track); code != 200 || track.Artist != "Café Tacvba" || len(track.Artists) != 1 {
		t.Fatalf("track: %d %+v", code, track)
	}

	var albums Paged[AlbumDTO]
	e.do("GET", "/albums", nil, &albums)
	var album AlbumDTO
	e.do("GET", "/albums/"+albums.Items[0].ID, nil, &album)
	if len(album.Tracks) != 2 || album.Tracks[0].TrackNo != 1 || album.Artist != "Café Tacvba" {
		t.Fatalf("album = %+v", album)
	}

	var artists Paged[ArtistDTO]
	e.do("GET", "/artists", nil, &artists)
	var artist ArtistDTO
	e.do("GET", "/artists/"+artists.Items[0].ID, nil, &artist)
	if len(artist.Albums) != 1 || len(artist.Tracks) != 2 {
		t.Fatalf("artist = %+v", artist)
	}

	var genres struct{ Items []GenreDTO }
	e.do("GET", "/genres", nil, &genres)
	var gt struct{ Items []TrackDTO }
	e.do("GET", "/genres/"+genres.Items[0].ID+"/tracks", nil, &gt)
	if len(genres.Items) != 1 || len(gt.Items) != 2 {
		t.Fatalf("genres=%v tracks=%v", genres.Items, gt.Items)
	}
}

func TestErrorsUseTheDocumentedShape(t *testing.T) {
	e := newEnv(t)
	for _, tt := range []struct {
		method, path string
		status       int
		code         string
	}{
		{"GET", "/tracks/nope", 404, "not_found"},
		{"GET", "/albums/nope", 404, "not_found"},
		{"GET", "/does/not/exist", 404, "not_found"},
		{"GET", "/artwork/nope", 404, "not_found"},
		{"GET", "/artwork/nope?size=7", 400, "invalid"},
		{"DELETE", "/library/roots/nope", 404, "not_found"},
		{"POST", "/library/roots", 400, "invalid"},
	} {
		var body errBody
		if code := e.do(tt.method, tt.path, nil, &body); code != tt.status || body.Error.Code != tt.code || body.Error.Message == "" {
			t.Errorf("%s %s = %d %+v, want %d %s", tt.method, tt.path, code, body, tt.status, tt.code)
		}
	}
}

func TestPatchTrack(t *testing.T) {
	e := newEnv(t)
	e.seed()
	var page Paged[TrackDTO]
	e.do("GET", "/tracks", nil, &page)
	id := page.Items[0].ID

	var out TrackDTO
	if code := e.do("PATCH", "/tracks/"+id, map[string]any{"title": "Nuevo", "track_no": 9}, &out); code != 200 || out.Title != "Nuevo" || out.TrackNo != 9 {
		t.Fatalf("patch: %d %+v", code, out)
	}
	if code := e.do("PATCH", "/tracks/"+id, map[string]any{"title": ""}, nil); code != 400 {
		t.Errorf("empty title = %d", code)
	}
	if code := e.do("PATCH", "/tracks/"+id, map[string]any{"bogus": 1}, nil); code != 400 {
		t.Errorf("unknown field = %d", code)
	}
	if code := e.do("PATCH", "/tracks/nope", map[string]any{"title": "x"}, nil); code != 404 {
		t.Errorf("unknown track = %d", code)
	}
}

func TestStreamSupportsRange(t *testing.T) {
	e := newEnv(t)
	e.seed()
	var page Paged[TrackDTO]
	e.do("GET", "/tracks", nil, &page)

	req, _ := http.NewRequest("GET", e.ts.URL+prefix+"/tracks/"+page.Items[0].ID+"/stream", nil)
	req.Header.Set("Range", "bytes=0-2")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(res.Body)
	if res.StatusCode != http.StatusPartialContent || buf.String() != "ID3" {
		t.Fatalf("range = %d %q", res.StatusCode, buf.String())
	}
}

func TestLibraryEndpoints(t *testing.T) {
	e := newEnv(t)
	e.seed()

	var roots struct{ Items []RootDTO }
	e.do("GET", "/library/roots", nil, &roots)
	if len(roots.Items) != 1 || !roots.Items[0].Enabled {
		t.Fatalf("roots = %+v", roots)
	}
	var st StatusDTO
	e.do("GET", "/library/status", nil, &st)
	if st.Scanning {
		t.Errorf("status = %+v", st)
	}
	if code := e.do("POST", "/library/scan", map[string]string{"root_id": "x"}, nil); code != http.StatusAccepted {
		t.Errorf("scan with root = %d", code)
	}
	if code := e.do("DELETE", "/library/roots/"+roots.Items[0].ID, nil, nil); code != http.StatusNoContent {
		t.Errorf("delete root = %d", code)
	}
	var page Paged[TrackDTO]
	e.do("GET", "/tracks", nil, &page)
	if len(page.Items) != 0 {
		t.Errorf("tracks of a removed root should be hidden, got %d", len(page.Items))
	}
}

type fakeJobs struct{ open int }

func (f fakeJobs) Open(context.Context) (int, error) { return f.open, nil }

func upload(t *testing.T, e *env, files map[string][]byte) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("note", "ignored plain field")
	for name, data := range files {
		fw, _ := mw.CreateFormFile("files", name)
		fw.Write(data)
	}
	mw.Close()
	res, err := http.Post(e.ts.URL+prefix+"/library/import", mw.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestImportEndpoint(t *testing.T) {
	e := newEnv(t)
	if code := e.do("POST", "/library/roots", map[string]string{"path": e.dir}, nil); code != 201 {
		t.Fatalf("add root = %d", code)
	}

	// imports are off until import_dir is configured
	if code, _ := upload(t, e, map[string][]byte{"a.mp3": []byte("x")}); code != 400 {
		t.Errorf("disabled import = %d", code)
	}
	e.lib.SetImportDir(filepath.Join(e.dir, "_importados"))

	song := testutil.ID3(map[string]string{"TIT2": "Subida", "TPE1": "Alguien", "TALB": "Disco"}, "audio")
	code, out := upload(t, e, map[string][]byte{"Subida.mp3": song, "notes.txt": []byte("no")})
	if code != 201 {
		t.Fatalf("upload = %d %v", code, out)
	}
	if imported := out["imported"].([]any); len(imported) != 1 || imported[0].(map[string]any)["name"] != "Subida.mp3" {
		t.Errorf("imported = %v", out["imported"])
	}
	if errs := out["errors"].([]any); len(errs) != 1 || errs[0].(map[string]any)["name"] != "notes.txt" {
		t.Errorf("errors = %v", out["errors"])
	}
	var hits map[string][]HitDTO
	e.do("GET", "/search?q=subida&types=track", nil, &hits)
	if len(hits["tracks"]) != 1 {
		t.Errorf("imported track not searchable: %v", hits)
	}

	if code, _ := upload(t, e, map[string][]byte{"only-bad.txt": []byte("x")}); code != 400 {
		t.Errorf("nothing importable = %d", code)
	}
	if code := e.do("POST", "/library/import", map[string]string{"a": "b"}, nil); code != 400 {
		t.Errorf("not multipart = %d", code)
	}
	if code, _ := upload(t, e, nil); code != 400 {
		t.Errorf("empty upload = %d", code)
	}
}

func TestStatusReportsPendingJobs(t *testing.T) {
	e := newEnv(t)
	var st StatusDTO
	e.do("GET", "/library/status", nil, &st)
	if st.PendingJobs != 0 {
		t.Errorf("without a job queue: %+v", st)
	}
	e.srv.Jobs = fakeJobs{open: 7}
	e.do("GET", "/library/status", nil, &st)
	if st.PendingJobs != 7 {
		t.Errorf("status = %+v", st)
	}
}
