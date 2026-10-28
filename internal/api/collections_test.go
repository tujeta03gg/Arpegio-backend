package api

import (
	"fmt"
	"testing"
	"time"

	"arpegio/internal/history"
)

// trackIDs seeds two tracks and returns their ids.
func (e *env) trackIDs() (string, string) {
	e.t.Helper()
	e.seed()
	var page Paged[TrackDTO]
	e.do("GET", "/tracks", nil, &page)
	return page.Items[0].ID, page.Items[1].ID
}

func TestPlaylistLifecycle(t *testing.T) {
	e := newEnv(t)
	a, b := e.trackIDs()

	var p PlaylistDTO
	if code := e.do("POST", "/playlists", map[string]string{"name": "Road trip", "description": "car"}, &p); code != 201 || p.ID == "" || p.Name != "Road trip" {
		t.Fatalf("create = %d %+v", code, p)
	}
	if code := e.do("POST", "/playlists", map[string]string{"name": " "}, nil); code != 400 {
		t.Errorf("blank name = %d", code)
	}

	if code := e.do("POST", "/playlists/"+p.ID+"/items", map[string]any{"track_ids": []string{a, b, a}}, &p); code != 200 || p.ItemCount != 3 || len(p.Items) != 3 || p.Items[0].Track.Title == "" {
		t.Fatalf("add = %d %+v", code, p)
	}
	if code := e.do("POST", "/playlists/"+p.ID+"/items", map[string]any{"track_ids": []string{"nope"}}, nil); code != 404 {
		t.Errorf("unknown track = %d", code)
	}
	if code := e.do("POST", "/playlists/nope/items", map[string]any{"track_ids": []string{a}}, nil); code != 404 {
		t.Errorf("unknown playlist = %d", code)
	}
	if code := e.do("POST", "/playlists/"+p.ID+"/items", map[string]any{"track_ids": []string{}}, nil); code != 400 {
		t.Errorf("empty add = %d", code)
	}

	last := p.Items[2].Position
	if code := e.do("PATCH", "/playlists/"+p.ID+"/items", map[string]any{"position": last, "index": 0}, &p); code != 200 || p.Items[0].Track.ID != a || p.Items[0].Position == last && false {
		t.Fatalf("move = %d %+v", code, p)
	}
	if code := e.do("PATCH", "/playlists/"+p.ID+"/items", map[string]any{"position": last}, nil); code != 400 {
		t.Errorf("move without index = %d", code)
	}
	if code := e.do("DELETE", "/playlists/"+p.ID+"/items", map[string]any{"position": p.Items[1].Position}, &p); code != 200 || p.ItemCount != 2 {
		t.Fatalf("remove = %d %+v", code, p)
	}
	if code := e.do("DELETE", "/playlists/"+p.ID+"/items", map[string]any{"position": 999}, nil); code != 404 {
		t.Errorf("remove unknown = %d", code)
	}

	if code := e.do("PATCH", "/playlists/"+p.ID, map[string]string{"name": "Viaje"}, &p); code != 200 || p.Name != "Viaje" || p.Description != "car" {
		t.Fatalf("rename = %d %+v", code, p)
	}
	var list struct{ Items []PlaylistDTO }
	e.do("GET", "/playlists", nil, &list)
	if len(list.Items) != 1 || list.Items[0].ItemCount != 2 || list.Items[0].Items != nil {
		t.Fatalf("list = %+v", list)
	}
	if code := e.do("DELETE", "/playlists/"+p.ID, nil, nil); code != 204 {
		t.Errorf("delete = %d", code)
	}
	if code := e.do("GET", "/playlists/"+p.ID, nil, nil); code != 404 {
		t.Errorf("get deleted = %d", code)
	}
}

func TestLikesEndpoints(t *testing.T) {
	e := newEnv(t)
	a, _ := e.trackIDs()
	var albums Paged[AlbumDTO]
	e.do("GET", "/albums", nil, &albums)
	var artists Paged[ArtistDTO]
	e.do("GET", "/artists", nil, &artists)

	for _, path := range []string{"/likes/track/" + a, "/likes/album/" + albums.Items[0].ID, "/likes/artist/" + artists.Items[0].ID} {
		if code := e.do("PUT", path, nil, nil); code != 204 {
			t.Errorf("PUT %s = %d", path, code)
		}
	}
	if code := e.do("PUT", "/likes/track/nope", nil, nil); code != 404 {
		t.Errorf("like unknown = %d", code)
	}
	if code := e.do("PUT", "/likes/podcast/x", nil, nil); code != 400 {
		t.Errorf("bad kind = %d", code)
	}

	var tracks struct{ Items []TrackDTO }
	e.do("GET", "/likes", nil, &tracks) // default kind
	if len(tracks.Items) != 1 || tracks.Items[0].ID != a || tracks.Items[0].Title == "" {
		t.Fatalf("liked tracks = %+v", tracks)
	}
	var als struct{ Items []AlbumDTO }
	e.do("GET", "/likes?kind=album", nil, &als)
	var ars struct{ Items []ArtistDTO }
	e.do("GET", "/likes?kind=artist", nil, &ars)
	if len(als.Items) != 1 || als.Items[0].Title != "Re" || len(ars.Items) != 1 || ars.Items[0].Name != "Café Tacvba" {
		t.Fatalf("albums=%+v artists=%+v", als, ars)
	}
	if code := e.do("GET", "/likes?kind=nope", nil, nil); code != 400 {
		t.Errorf("list bad kind = %d", code)
	}

	if code := e.do("DELETE", "/likes/track/"+a, nil, nil); code != 204 {
		t.Errorf("unlike = %d", code)
	}
	e.do("GET", "/likes", nil, &tracks)
	if len(tracks.Items) != 0 {
		t.Errorf("still liked: %+v", tracks)
	}
}

func TestHistoryStatsAndHome(t *testing.T) {
	e := newEnv(t)
	a, b := e.trackIDs()
	rec := func(id string, ago time.Duration, listened int64) {
		t.Helper()
		_, err := e.hist.Record(t.Context(), history.Play{TrackID: id, StartedAt: time.Now().Add(-ago), ListenedMS: listened, DurationMS: 100_000, Source: "album"})
		if err != nil {
			t.Fatal(err)
		}
	}
	rec(a, 3*time.Hour, 100_000)
	rec(a, 2*time.Hour, 100_000)
	rec(b, time.Hour, 2_000) // skipped

	var h struct{ Items []HistoryEntryDTO }
	if code := e.do("GET", "/history?limit=2", nil, &h); code != 200 || len(h.Items) != 2 {
		t.Fatalf("history = %d %+v", code, h)
	}
	if h.Items[0].Track.ID != b || !h.Items[0].Skipped || h.Items[1].Track.ID != a || !h.Items[1].Completed || h.Items[1].Source != "album" {
		t.Errorf("entries = %+v", h.Items)
	}
	if code := e.do("GET", "/history?limit=x", nil, nil); code != 400 {
		t.Errorf("bad limit = %d", code)
	}

	var st StatsDTO
	if code := e.do("GET", "/stats?range=7d", nil, &st); code != 200 || st.Range != "7d" || st.Plays != 2 || st.ListenedMS != 202_000 {
		t.Fatalf("stats = %d %+v", code, st)
	}
	if len(st.TopTracks) != 1 || st.TopTracks[0].ID != a || st.TopTracks[0].Plays != 2 || len(st.TopArtists) != 1 || len(st.TopGenres) != 1 {
		t.Errorf("tops = %+v", st)
	}
	e.do("GET", "/stats", nil, &st)
	if st.Range != "30d" {
		t.Errorf("default range = %q", st.Range)
	}
	if code := e.do("GET", "/stats?range=forever", nil, nil); code != 400 {
		t.Errorf("bad range = %d", code)
	}

	var home HomeDTO
	if code := e.do("GET", "/home", nil, &home); code != 200 {
		t.Fatalf("home = %d", code)
	}
	if len(home.RecentlyPlayed) != 1 || home.RecentlyPlayed[0].ID != a { // skips and duplicates are left out
		t.Errorf("recently played = %+v", home.RecentlyPlayed)
	}
	if len(home.MostPlayed) != 1 || home.MostPlayed[0].ID != a || len(home.RecentlyAdded) != 2 {
		t.Errorf("home = %+v", home)
	}
}

func TestHomeIsEmptyButWellFormedOnAFreshInstall(t *testing.T) {
	e := newEnv(t)
	var raw map[string][]any
	if code := e.do("GET", "/home", nil, &raw); code != 200 {
		t.Fatalf("home = %d", code)
	}
	for _, k := range []string{"recently_played", "most_played", "recently_added"} {
		if v, ok := raw[k]; !ok || v == nil || len(v) != 0 {
			t.Errorf("%s = %v (want an empty array, not null)", k, raw[k])
		}
	}
	_ = fmt.Sprint
}
