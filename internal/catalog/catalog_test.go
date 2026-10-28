package catalog

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"arpegio/internal/apperr"
	"arpegio/internal/events"
	"arpegio/internal/metadata"
	"arpegio/internal/store"
)

type env struct {
	t   *testing.T
	st  *store.Store
	svc *Service
	n   int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.W.Exec(`INSERT INTO library_roots(id, path) VALUES ('r', '/m')`); err != nil {
		t.Fatal(err)
	}
	return &env{t: t, st: st, svc: New(st, events.New())}
}

// add inserts a fake file row and upserts a track for it.
func (e *env) add(info metadata.Info) string {
	e.t.Helper()
	e.n++
	fid := store.NewID()
	_, err := e.st.W.Exec(`INSERT INTO files(id, root_id, rel_path, content_hash, size_bytes, mtime, format, bitrate_kbps, sample_rate, bit_depth, duration_ms)
		VALUES (?, 'r', ?, ?, 1, 't', 'flac', 1011, 44100, 16, 1000)`, fid, info.Title+string(rune('a'+e.n)), fid)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.upsert(fid, info)
}

func (e *env) upsert(fid string, info metadata.Info) string {
	e.t.Helper()
	var id string
	err := e.st.Tx(context.Background(), func(tx *sql.Tx) (err error) {
		id, err = e.svc.UpsertTrack(context.Background(), tx, TrackInput{FileID: fid, Info: info})
		return
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func info(title, artist, album string, genres ...string) metadata.Info {
	return metadata.Info{Title: title, Artist: artist, Album: album, Genres: genres, TrackNo: 1, Props: metadata.Props{DurationMS: 1000}}
}

func TestUpsertDedupesArtistsAndAlbums(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.add(info("One", "The Beatles", "Abbey Road", "Rock"))
	e.add(info("Two", "beatles", "abbey road", "rock"))

	artists, _ := e.svc.Artists(ctx, "", 10)
	albums, _ := e.svc.Albums(ctx, "", 10)
	genres, _ := e.svc.Genres(ctx)
	if len(artists.Items) != 1 || len(albums.Items) != 1 || len(genres) != 1 {
		t.Fatalf("artists=%d albums=%d genres=%d, want 1 each", len(artists.Items), len(albums.Items), len(genres))
	}
	if artists.Items[0].Name != "The Beatles" {
		t.Errorf("first-seen name should win, got %q", artists.Items[0].Name)
	}
}

func TestUpsertCreditsAndUnknownArtist(t *testing.T) {
	e := newEnv(t)
	id := e.add(info("Song", "Drake feat. Rihanna", "Album"))
	tr, err := e.svc.Track(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range tr.Artists {
		got = append(got, a.Role+":"+a.Name)
	}
	if !reflect.DeepEqual(got, []string{"main:Drake", "featured:Rihanna"}) || tr.Artist != "Drake" {
		t.Fatalf("credits = %v artist=%q", got, tr.Artist)
	}

	id = e.add(info("Anon", "", ""))
	tr, _ = e.svc.Track(context.Background(), id)
	if tr.Artist != "Unknown Artist" || tr.AlbumID != "" {
		t.Fatalf("unknown artist track = %+v", tr)
	}
}

func TestUpsertSameFileUpdatesInPlace(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.add(info("Old", "A", "Al"))
	var fid string
	e.st.R.QueryRow(`SELECT file_id FROM tracks WHERE id = ?`, id).Scan(&fid)

	id2 := e.upsert(fid, info("New", "A", "Al"))
	if id2 != id {
		t.Fatalf("track id changed: %s -> %s", id, id2)
	}
	tr, _ := e.svc.Track(ctx, id)
	if tr.Title != "New" {
		t.Fatalf("title = %q", tr.Title)
	}
	page, _ := e.svc.Tracks(ctx, "", 10)
	if len(page.Items) != 1 {
		t.Fatalf("tracks = %d", len(page.Items))
	}
}

func TestOverridesSurviveRescan(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.add(info("Typo", "A", "Al"))
	var fid string
	e.st.R.QueryRow(`SELECT file_id FROM tracks WHERE id = ?`, id).Scan(&fid)

	title, no := "Fixed", 7
	if _, err := e.svc.UpdateTrack(ctx, id, TrackPatch{Title: &title, TrackNo: &no}); err != nil {
		t.Fatal(err)
	}
	e.upsert(fid, info("Typo", "A", "Al")) // rescan reads the original tags again
	tr, _ := e.svc.Track(ctx, id)
	if tr.Title != "Fixed" || tr.TrackNo != 7 {
		t.Fatalf("override lost: %+v", tr)
	}
	hits, _ := e.svc.Search(ctx, "fixed", []string{"track"}, 10)
	if len(hits["track"]) != 1 {
		t.Fatalf("search index not updated: %v", hits)
	}
}

func TestUpdateTrackErrors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	empty, neg := "  ", -1
	if _, err := e.svc.UpdateTrack(ctx, "nope", TrackPatch{}); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("missing track: %v", err)
	}
	id := e.add(info("T", "A", "Al"))
	if _, err := e.svc.UpdateTrack(ctx, id, TrackPatch{Title: &empty}); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("empty title: %v", err)
	}
	if _, err := e.svc.UpdateTrack(ctx, id, TrackPatch{TrackNo: &neg}); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("negative number: %v", err)
	}
}

func TestSearch(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.add(info("Canción de cuna", "Café Tacvba", "Re"))
	e.add(info("Other", "Someone", "Else"))

	tests := []struct {
		q    string
		kind string
		want int
	}{
		{"cancion", "track", 1},
		{"can", "track", 1}, // prefix
		{"CAFE tac", "artist", 1},
		{"re", "album", 1},
		{"zzz", "track", 0},
		{`cancion" OR "x`, "track", 0}, // FTS syntax is neutralized: no error, OR is just another term
	}
	for _, tt := range tests {
		hits, err := e.svc.Search(ctx, tt.q, []string{tt.kind}, 10)
		if err != nil || len(hits[tt.kind]) != tt.want {
			t.Errorf("Search(%q,%s) = %v, %v; want %d hits", tt.q, tt.kind, hits[tt.kind], err, tt.want)
		}
	}
	if _, err := e.svc.Search(ctx, "  !! ", nil, 10); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("empty query: %v", err)
	}
	if _, err := e.svc.Search(ctx, "x", []string{"podcast"}, 10); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("bad kind: %v", err)
	}
	all, _ := e.svc.Search(ctx, "cancion", nil, 10)
	if len(all) != 3 {
		t.Errorf("default kinds = %d groups", len(all))
	}
}

func TestPaginationCursor(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		e.add(info(n, "A", ""))
	}
	var seen []string
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		p, err := e.svc.Tracks(ctx, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, tr := range p.Items {
			seen = append(seen, tr.Title)
		}
		if p.Next == "" {
			break
		}
		cursor = p.Next
	}
	if !reflect.DeepEqual(seen, []string{"a", "b", "c", "d", "e"}) {
		t.Fatalf("seen = %v", seen)
	}
}

func TestDetailsAndMissingFilesHidden(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	t1 := e.add(info("S1", "Band", "LP", "Rock"))
	e.add(info("S2", "Band", "LP", "Rock"))

	al, _ := e.svc.Albums(ctx, "", 10)
	detail, err := e.svc.Album(ctx, al.Items[0].ID)
	if err != nil || len(detail.Tracks) != 2 || detail.Artist != "Band" {
		t.Fatalf("album detail = %+v err=%v", detail, err)
	}
	ar, _ := e.svc.Artists(ctx, "", 10)
	ad, err := e.svc.Artist(ctx, ar.Items[0].ID)
	if err != nil || len(ad.Albums) != 1 || len(ad.Tracks) != 2 {
		t.Fatalf("artist detail = %+v err=%v", ad, err)
	}
	g, _ := e.svc.Genres(ctx)
	gt, _ := e.svc.GenreTracks(ctx, g[0].ID)
	if len(gt) != 2 {
		t.Fatalf("genre tracks = %d", len(gt))
	}

	// a missing file disappears from listings but the track row stays (history/likes keep working)
	e.st.W.Exec(`UPDATE files SET missing = 1 WHERE id = (SELECT file_id FROM tracks WHERE id = ?)`, t1)
	p, _ := e.svc.Tracks(ctx, "", 10)
	if len(p.Items) != 1 {
		t.Fatalf("missing file still listed: %d", len(p.Items))
	}
	if _, err := e.svc.Track(ctx, t1); err != nil {
		t.Fatalf("missing track must stay readable: %v", err)
	}
	if _, err := e.svc.Album(ctx, "nope"); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("album not found: %v", err)
	}
}

func TestTrackCarriesTechnicalInfoCoverAndMissingFlag(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.st.W.Exec(`INSERT INTO artwork(id, hash, cache_path) VALUES ('art1', 'h', '/c/h.jpg')`)
	fid := store.NewID()
	e.st.W.Exec(`INSERT INTO files(id, root_id, rel_path, content_hash, size_bytes, mtime, format, bitrate_kbps, sample_rate, bit_depth, duration_ms)
		VALUES (?, 'r', 'x', 'hx', 1, 't', 'flac', 1011, 96000, 24, 1000)`, fid)
	var id string
	err := e.st.Tx(ctx, func(tx *sql.Tx) (err error) {
		id, err = e.svc.UpsertTrack(ctx, tx, TrackInput{FileID: fid, ArtworkID: "art1", Info: info("Hi-Res", "Band", "LP")})
		return
	})
	if err != nil {
		t.Fatal(err)
	}

	tr, err := e.svc.Track(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if tr.ArtworkID != "art1" || tr.Format != "flac" || tr.BitrateKbps != 1011 || tr.SampleRate != 96000 || tr.BitDepth != 24 || tr.Missing {
		t.Fatalf("track = %+v", tr)
	}
	page, _ := e.svc.Tracks(ctx, "", 10)
	if len(page.Items) != 1 || page.Items[0].ArtworkID != "art1" || page.Items[0].SampleRate != 96000 {
		t.Errorf("list rows must carry the same fields: %+v", page.Items)
	}
	al, _ := e.svc.Album(ctx, tr.AlbumID)
	if al.Tracks[0].Format != "flac" || al.Tracks[0].ArtworkID != "art1" {
		t.Errorf("album tracks = %+v", al.Tracks)
	}

	// a vanished file keeps its track readable, flagged as missing
	e.st.W.Exec(`UPDATE files SET missing = 1 WHERE id = ?`, fid)
	tr, _ = e.svc.Track(ctx, id)
	if !tr.Missing {
		t.Error("missing flag not reported")
	}

	// a track without any album has no cover
	plain := e.add(info("Loose", "Solo", ""))
	if tr, _ := e.svc.Track(ctx, plain); tr.ArtworkID != "" || tr.Format != "flac" {
		t.Errorf("loose track = %+v", tr)
	}
}

func TestListsReportTheirTotal(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		e.add(info(n, "Artist "+n, "Album "+n))
	}
	tracks, _ := e.svc.Tracks(ctx, "", 2)
	artists, _ := e.svc.Artists(ctx, "", 2)
	albums, _ := e.svc.Albums(ctx, "", 2)
	if len(tracks.Items) != 2 || tracks.Total != 5 || artists.Total != 5 || albums.Total != 5 {
		t.Errorf("totals = %d/%d/%d (page of %d)", tracks.Total, artists.Total, albums.Total, len(tracks.Items))
	}
	// a missing file leaves the track total, like it leaves the list
	e.st.W.Exec(`UPDATE files SET missing = 1 WHERE id = (SELECT file_id FROM tracks LIMIT 1)`)
	if p, _ := e.svc.Tracks(ctx, "", 10); p.Total != 4 || len(p.Items) != 4 {
		t.Errorf("after a file went missing: total=%d items=%d", p.Total, len(p.Items))
	}
}

func TestSearchHitsCarryAlbumDurationAndCover(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.st.W.Exec(`INSERT INTO artwork(id, hash, cache_path) VALUES ('art1', 'h', '/c/h.jpg')`)
	fid := store.NewID()
	e.st.W.Exec(`INSERT INTO files(id, root_id, rel_path, content_hash, size_bytes, mtime, format, duration_ms) VALUES (?, 'r', 'x', 'hx', 1, 't', 'mp3', 215000)`, fid)
	err := e.st.Tx(ctx, func(tx *sql.Tx) error {
		in := info("Ingrata", "Café Tacvba", "Re")
		in.DurationMS = 215000
		_, err := e.svc.UpsertTrack(ctx, tx, TrackInput{FileID: fid, ArtworkID: "art1", Info: in})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	hits, err := e.svc.Search(ctx, "ingrata", []string{"track"}, 5)
	if err != nil || len(hits["track"]) != 1 {
		t.Fatalf("hits = %v %v", hits, err)
	}
	h := hits["track"][0]
	if h.AlbumID == "" || h.DurationMS != 215000 || h.ArtworkID != "art1" {
		t.Errorf("track hit = %+v", h)
	}
	albums, _ := e.svc.Search(ctx, "re", []string{"album"}, 5)
	if len(albums["album"]) != 1 || albums["album"][0].ArtworkID != "art1" {
		t.Errorf("album hit = %+v", albums["album"])
	}
}

func TestRecentAlbumsOrderedByNewestTrack(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.add(info("a1", "X", "Old album"))
	e.add(info("b1", "Y", "New album"))
	e.add(info("a2", "X", "Old album")) // a later track bumps its album to the top
	for title, at := range map[string]string{"a1": "2026-01-01T00:00:01Z", "b1": "2026-01-01T00:00:02Z", "a2": "2026-01-01T00:00:03Z"} {
		e.st.W.Exec(`UPDATE tracks SET added_at = ? WHERE title = ?`, at, title)
	}
	got, err := e.svc.RecentAlbums(ctx, 10)
	if err != nil || len(got) != 2 || got[0].Title != "Old album" || got[1].Title != "New album" || got[0].Artist != "X" {
		t.Fatalf("recent albums = %+v %v", got, err)
	}
	if got, _ := e.svc.RecentAlbums(ctx, 1); len(got) != 1 {
		t.Errorf("limit not applied: %d", len(got))
	}
	// albums whose files are all gone drop out
	e.st.W.Exec(`UPDATE files SET missing = 1`)
	if got, _ := e.svc.RecentAlbums(ctx, 10); len(got) != 0 {
		t.Errorf("missing albums listed: %+v", got)
	}
}
