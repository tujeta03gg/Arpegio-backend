package api

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"arpegio/internal/apperr"
)

func pngFile(t *testing.T, path string) {
	t.Helper()
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 400, 400)))
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTracksCarryCoverTechnicalInfoAndMissingFlag(t *testing.T) {
	e := newEnv(t)
	pngFile(t, filepath.Join(e.dir, "a", "cover.png"))
	e.seed()

	var page Paged[TrackDTO]
	e.do("GET", "/tracks", nil, &page)
	tr := page.Items[0]
	if tr.ArtworkID == "" || tr.Format != "mp3" || tr.BitrateKbps != 320 || tr.SampleRate != 44100 || tr.BitDepth != 16 || tr.Missing {
		t.Fatalf("track = %+v", tr)
	}
	// the cover id works as is, without asking the album first
	res, err := http.Get(e.ts.URL + prefix + "/artwork/" + tr.ArtworkID + "?size=96")
	if err != nil || res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "image/") {
		t.Fatalf("artwork: %v %v", res, err)
	}
	res.Body.Close()

	var detail TrackDTO
	e.do("GET", "/tracks/"+tr.ID, nil, &detail)
	if detail.ArtworkID != tr.ArtworkID || detail.Format != "mp3" {
		t.Errorf("detail = %+v", detail)
	}
	var raw map[string]any
	e.do("GET", "/tracks/"+tr.ID, nil, &raw)
	if v, ok := raw["missing"]; !ok || v != false {
		t.Errorf("missing must always be present: %v", raw)
	}
}

func TestSearchRowsCarryAlbumDurationAndCover(t *testing.T) {
	e := newEnv(t)
	pngFile(t, filepath.Join(e.dir, "a", "cover.png"))
	e.seed()
	var hits map[string][]HitDTO
	e.do("GET", "/search?q=cancion", nil, &hits)
	h := hits["tracks"][0]
	if h.AlbumID == "" || h.DurationMS != 1000 || h.ArtworkID == "" {
		t.Errorf("track hit = %+v", h)
	}
	e.do("GET", "/search?q=re&types=album", nil, &hits)
	if len(hits["albums"]) != 1 || hits["albums"][0].ArtworkID == "" {
		t.Errorf("album hit = %+v", hits["albums"])
	}
}

func TestPagedListsReportTotals(t *testing.T) {
	e := newEnv(t)
	e.seed()
	var tracks Paged[TrackDTO]
	e.do("GET", "/tracks?limit=1", nil, &tracks)
	var albums Paged[AlbumDTO]
	e.do("GET", "/albums?limit=1", nil, &albums)
	var artists Paged[ArtistDTO]
	e.do("GET", "/artists?limit=1", nil, &artists)
	if len(tracks.Items) != 1 || tracks.Total != 2 || albums.Total != 1 || artists.Total != 1 {
		t.Errorf("totals: tracks=%d (page %d) albums=%d artists=%d", tracks.Total, len(tracks.Items), albums.Total, artists.Total)
	}
}

func TestPlayQueueItemEndpoint(t *testing.T) {
	e := newEnv(t)
	for _, tt := range []struct {
		path string
		call string
	}{{"/queue/1.5/play", "playqueue 1.5"}, {"/queue/3/play", "playqueue 3"}} {
		if code := e.do("POST", tt.path, nil, nil); code != 200 {
			t.Errorf("%s = %d", tt.path, code)
		}
		if got := e.player.take(); got != tt.call {
			t.Errorf("%s called %q", tt.path, got)
		}
	}
	if code := e.do("POST", "/queue/abc/play", nil, nil); code != 400 {
		t.Errorf("bad position = %d", code)
	}
	e.player.err = fmt.Errorf("jump: %w", apperr.ErrNotFound)
	if code := e.do("POST", "/queue/9/play", nil, nil); code != 404 {
		t.Errorf("unknown item = %d", code)
	}
}

func TestVersionEndpoint(t *testing.T) {
	e := newEnv(t)
	var v map[string]string
	if code := e.do("GET", "/version", nil, &v); code != 200 || v["version"] == "" || v["go_version"] == "" || v["os"] == "" || v["arch"] == "" {
		t.Errorf("version = %d %v", code, v)
	}
}

func TestHomeIncludesAlbumsAndPlaylists(t *testing.T) {
	e := newEnv(t)
	a, _ := e.trackIDs()
	var empty HomeDTO
	e.do("GET", "/home", nil, &empty)
	if len(empty.Playlists) != 0 || empty.Playlists == nil {
		t.Errorf("playlists = %#v", empty.Playlists)
	}

	for _, name := range []string{"First", "Second"} {
		var p PlaylistDTO
		e.do("POST", "/playlists", map[string]string{"name": name}, &p)
		e.do("POST", "/playlists/"+p.ID+"/items", map[string]any{"track_ids": []string{a}}, nil)
	}
	var home HomeDTO
	e.do("GET", "/home", nil, &home)
	if len(home.RecentAlbums) != 1 || home.RecentAlbums[0].Title != "Re" || home.RecentAlbums[0].Artist != "Café Tacvba" {
		t.Errorf("recent albums = %+v", home.RecentAlbums)
	}
	if len(home.Playlists) != 2 || home.Playlists[0].ItemCount != 1 {
		t.Errorf("playlists = %+v", home.Playlists)
	}
}

func TestCORS(t *testing.T) {
	e := newEnv(t)
	e.srv.AllowOrigin = "*"
	ts := httptest.NewServer(e.srv.Handler())
	defer ts.Close()

	res, _ := http.Get(ts.URL + prefix + "/library/status")
	res.Body.Close()
	if res.Header.Get("Access-Control-Allow-Origin") != "*" || !strings.Contains(res.Header.Get("Access-Control-Expose-Headers"), "Content-Range") {
		t.Errorf("headers = %v", res.Header)
	}

	req, _ := http.NewRequest("OPTIONS", ts.URL+prefix+"/player/play", nil)
	req.Header.Set("Origin", "http://localhost:5000")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "content-type")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 204 || !strings.Contains(res.Header.Get("Access-Control-Allow-Methods"), "PATCH") || !strings.Contains(res.Header.Get("Access-Control-Allow-Headers"), "Content-Type") {
		t.Errorf("preflight = %d %v", res.StatusCode, res.Header)
	}

	e.srv.AllowOrigin = "http://localhost:5000"
	ts2 := httptest.NewServer(e.srv.Handler())
	defer ts2.Close()
	res, _ = http.Get(ts2.URL + prefix + "/library/status")
	res.Body.Close()
	if res.Header.Get("Access-Control-Allow-Origin") != "http://localhost:5000" || res.Header.Get("Vary") != "Origin" {
		t.Errorf("single origin headers = %v", res.Header)
	}
}

func TestNoCORSHeadersByDefault(t *testing.T) {
	e := newEnv(t)
	res, _ := http.Get(e.ts.URL + prefix + "/library/status")
	res.Body.Close()
	if res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("CORS must be opt-in: %v", res.Header)
	}
}

func TestServesTheWebFrontend(t *testing.T) {
	e := newEnv(t)
	web := t.TempDir()
	os.WriteFile(filepath.Join(web, "index.html"), []byte("<html>ARPEGIO APP</html>"), 0o644)
	os.WriteFile(filepath.Join(web, "main.dart.js"), []byte("console.log(1)"), 0o644)
	os.MkdirAll(filepath.Join(web, "assets"), 0o755)
	os.WriteFile(filepath.Join(web, "assets", "logo.svg"), []byte("<svg/>"), 0o644)
	e.srv.WebDir = web
	ts := httptest.NewServer(e.srv.Handler())
	defer ts.Close()

	get := func(path string) (int, string) {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	for path, want := range map[string]string{
		"/":                  "ARPEGIO APP",
		"/index.html":        "ARPEGIO APP",
		"/biblioteca/albums": "ARPEGIO APP", // client-side route
		"/main.dart.js":      "console.log(1)",
		"/assets/logo.svg":   "<svg/>",
	} {
		if code, body := get(path); code != 200 || !strings.Contains(body, want) {
			t.Errorf("GET %s = %d %q", path, code, body)
		}
	}
	if code, _ := get("/missing.js"); code != 404 {
		t.Errorf("missing asset = %d (must not fall back to the app shell)", code)
	}
	if code, body := get("/api/v1/nothing"); code != 404 || !strings.Contains(body, "not_found") {
		t.Errorf("unknown API route = %d %q (must stay JSON)", code, body)
	}
	if code, _ := get("/api/v1/library/status"); code != 200 {
		t.Errorf("API still works next to the web app: %d", code)
	}
	if _, body := get("/../../etc/passwd"); strings.Contains(body, "root:") {
		t.Errorf("path traversal: %q", body)
	}
}
