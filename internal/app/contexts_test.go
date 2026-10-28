package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"arpegio/internal/apperr"
	"arpegio/internal/catalog"
	"arpegio/internal/config"
	"arpegio/internal/metadata"
	"arpegio/internal/store"
)

func TestContextsResolveCatalogEntities(t *testing.T) {
	ctx := context.Background()
	a, err := New(ctx, config.Default(), Paths{DB: ":memory:"}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.Store.W.Exec(`INSERT INTO library_roots(id, path) VALUES ('r','/m')`)
	add := func(title, artist, album, genre string, no int) {
		fid := store.NewID()
		a.Store.W.Exec(`INSERT INTO files(id, root_id, rel_path, content_hash, size_bytes, mtime, format, duration_ms) VALUES (?, 'r', ?, ?, 1, 't', 'mp3', 1000)`, fid, fid, fid)
		err := a.Store.Tx(ctx, func(tx *sql.Tx) error {
			_, err := a.Catalog.UpsertTrack(ctx, tx, catalog.TrackInput{FileID: fid, Info: metadata.Info{
				Title: title, Artist: artist, Album: album, Genres: []string{genre}, TrackNo: no, Props: metadata.Props{DurationMS: 1000}}})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	add("Second", "Band", "LP", "Rock", 2)
	add("First", "Band", "LP", "Rock", 1)
	add("Other", "Solo", "EP", "Jazz", 1)

	titles := func(spec string) []string {
		t.Helper()
		pc, err := a.contexts().Resolve(ctx, spec)
		if err != nil {
			t.Fatalf("%s: %v", spec, err)
		}
		var out []string
		for _, id := range pc.TrackIDs {
			tr, _ := a.Catalog.Track(ctx, id)
			out = append(out, tr.Title)
		}
		return out
	}
	albums, _ := a.Catalog.Albums(ctx, "", 10)
	var lp catalog.Album
	for _, al := range albums.Items {
		if al.Title == "LP" {
			lp = al
		}
	}
	artists, _ := a.Catalog.Artists(ctx, "", 10)
	genres, _ := a.Catalog.Genres(ctx)

	if got := titles("album:" + lp.ID); !reflect.DeepEqual(got, []string{"First", "Second"}) {
		t.Errorf("album order = %v (must follow track numbers)", got)
	}
	if got := titles("artist:" + lp.ArtistID); len(got) != 2 {
		t.Errorf("artist = %v", got)
	}
	for _, g := range genres {
		if g.Name == "Jazz" {
			if got := titles("genre:" + g.ID); !reflect.DeepEqual(got, []string{"Other"}) {
				t.Errorf("genre = %v", got)
			}
		}
	}
	_ = artists
	pc, _ := a.contexts().Resolve(ctx, "album:"+lp.ID)
	if got := titles("track:" + pc.TrackIDs[0]); len(got) != 1 {
		t.Errorf("track = %v", got)
	}
	if pc.Source != "album" || pc.SourceID != lp.ID {
		t.Errorf("source = %s/%s", pc.Source, pc.SourceID)
	}
	pl, err := a.Lists.Create(ctx, "mix", "")
	if err != nil {
		t.Fatal(err)
	}
	a.Lists.AddTracks(ctx, pl.ID, []string{pc.TrackIDs[1], pc.TrackIDs[0]})
	if got := titles("playlist:" + pl.ID); !reflect.DeepEqual(got, []string{"Second", "First"}) {
		t.Errorf("playlist = %v (must keep its own order)", got)
	}
	if _, err := a.contexts().Resolve(ctx, "likes"); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("no likes yet: %v", err)
	}
	a.Lists.SetLike(ctx, "track", pc.TrackIDs[0])
	if got := titles("likes"); len(got) != 1 {
		t.Errorf("likes = %v", got)
	}
	for _, bad := range []string{"album:nope", "track:nope", "artist:nope", "playlist:nope"} {
		if _, err := a.contexts().Resolve(ctx, bad); !errors.Is(err, apperr.ErrNotFound) {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

func TestMPVArgsFollowConfig(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	got := mpvArgs(config.Playback{AudioOutput: "pipewire", Gapless: true, ReplayGain: "album"}, log)
	want := []string{"--ao=pipewire", "--gapless-audio=yes", "--replaygain=album"}
	if !reflect.DeepEqual(got, want) || logs.Len() != 0 {
		t.Errorf("args = %v logs=%q", got, logs.String())
	}
	got = mpvArgs(config.Playback{AudioOutput: "alsa", Gapless: false, ReplayGain: "off", CrossfadeSeconds: 3}, log)
	want = []string{"--ao=alsa", "--gapless-audio=no", "--replaygain=no"}
	if !reflect.DeepEqual(got, want) || !strings.Contains(logs.String(), "crossfade") {
		t.Errorf("args = %v logs=%q", got, logs.String())
	}
}

func TestIdleEngineAnswersNotConnected(t *testing.T) {
	var e idleEngine
	ctx := context.Background()
	if err := e.Load(ctx, "x", 0, false); err == nil || e.Events() != nil || e.SetVolume(ctx, 1) == nil {
		t.Error("idle engine must refuse commands")
	}
}
