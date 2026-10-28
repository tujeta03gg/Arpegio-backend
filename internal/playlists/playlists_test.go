package playlists

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"arpegio/internal/apperr"
	"arpegio/internal/events"
	"arpegio/internal/store"
)

type env struct {
	t   *testing.T
	ctx context.Context
	svc *Service
	bus *events.Bus
	ch  <-chan events.Event
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.W.Exec(`INSERT INTO library_roots(id, path) VALUES ('r','/m')`)
	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("t%d", i)
		st.W.Exec(`INSERT INTO files(id, root_id, rel_path, content_hash, size_bytes, mtime, format, duration_ms) VALUES (?, 'r', ?, ?, 1, 't', 'mp3', 1000)`, "f"+id, id, id)
		if _, err := st.W.Exec(`INSERT INTO tracks(id, file_id, title, duration_ms, added_at) VALUES (?, ?, ?, 1000, 't')`, id, "f"+id, id); err != nil {
			t.Fatal(err)
		}
	}
	bus := events.New()
	ch, cancel := bus.Subscribe(64)
	t.Cleanup(cancel)
	exists := func(_ context.Context, kind, id string) error {
		if id == "nope" {
			return apperr.NotFound(sql.ErrNoRows, kind+" "+id)
		}
		return nil
	}
	return &env{t: t, ctx: ctx, svc: New(st, bus, exists), bus: bus, ch: ch}
}

func (e *env) create(name string) Playlist {
	e.t.Helper()
	p, err := e.svc.Create(e.ctx, name, "")
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *env) ids(id string) []string {
	e.t.Helper()
	ids, err := e.svc.TrackIDs(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return ids
}

func (e *env) posOf(id string, idx int) float64 {
	_, items, err := e.svc.Get(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return items[idx].Position
}

func (e *env) events() map[string]int {
	out := map[string]int{}
	for len(e.ch) > 0 {
		out[(<-e.ch).Type]++
	}
	return out
}

func TestCreateListUpdateDelete(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Create(e.ctx, "   ", ""); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("blank name: %v", err)
	}
	b := e.create("  road   trip ")
	a := e.create("Ánimo")
	if b.Name != "road trip" {
		t.Errorf("name not cleaned: %q", b.Name)
	}
	list, err := e.svc.List(e.ctx)
	if err != nil || len(list) != 2 || list[0].Name != "Ánimo" && list[0].Name != "road trip" {
		t.Fatalf("list = %+v %v", list, err)
	}
	if list[0].ID != a.ID { // case-insensitive name order: "Ánimo" sorts before "road trip"
		t.Errorf("order = %v", list)
	}

	desc := "for the car"
	renamed := "Road Trip"
	p, err := e.svc.Update(e.ctx, b.ID, &renamed, &desc)
	if err != nil || p.Name != "Road Trip" || p.Description != "for the car" {
		t.Fatalf("update = %+v %v", p, err)
	}
	p, _ = e.svc.Update(e.ctx, b.ID, nil, nil)
	if p.Name != "Road Trip" {
		t.Errorf("nil fields must keep values: %+v", p)
	}
	empty := " "
	if _, err := e.svc.Update(e.ctx, b.ID, &empty, nil); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("blank rename: %v", err)
	}
	if _, err := e.svc.Update(e.ctx, "nope", &renamed, nil); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("update unknown: %v", err)
	}

	if err := e.svc.Delete(e.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(e.ctx, a.ID); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("double delete: %v", err)
	}
	if _, _, err := e.svc.Get(e.ctx, a.ID); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("get deleted: %v", err)
	}
	if ev := e.events(); ev["playlist.changed"] < 5 {
		t.Errorf("events = %v", ev)
	}
}

func TestAddTracksKeepsOrderAndAllowsDuplicates(t *testing.T) {
	e := newEnv(t)
	p := e.create("mix")
	if err := e.svc.AddTracks(e.ctx, p.ID, nil); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("empty add: %v", err)
	}
	if err := e.svc.AddTracks(e.ctx, "nope", []string{"t1"}); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("unknown playlist: %v", err)
	}
	if err := e.svc.AddTracks(e.ctx, p.ID, []string{"ghost"}); err == nil {
		t.Error("unknown track should fail (foreign key)")
	}
	e.svc.AddTracks(e.ctx, p.ID, []string{"t3", "t1"})
	e.svc.AddTracks(e.ctx, p.ID, []string{"t3"})
	if got := e.ids(p.ID); !reflect.DeepEqual(got, []string{"t3", "t1", "t3"}) {
		t.Fatalf("order = %v", got)
	}
	got, items, _ := e.svc.Get(e.ctx, p.ID)
	if got.ItemCount != 3 || len(items) != 3 {
		t.Errorf("count = %d", got.ItemCount)
	}
	list, _ := e.svc.List(e.ctx)
	if list[0].ItemCount != 3 {
		t.Errorf("list count = %d", list[0].ItemCount)
	}
}

func TestMoveItem(t *testing.T) {
	e := newEnv(t)
	p := e.create("mix")
	e.svc.AddTracks(e.ctx, p.ID, []string{"t1", "t2", "t3", "t4"})

	move := func(idx, to int) {
		t.Helper()
		if err := e.svc.MoveItem(e.ctx, p.ID, e.posOf(p.ID, idx), to); err != nil {
			t.Fatal(err)
		}
	}
	move(3, 0)
	if got := e.ids(p.ID); !reflect.DeepEqual(got, []string{"t4", "t1", "t2", "t3"}) {
		t.Fatalf("to front = %v", got)
	}
	move(0, 99)
	if got := e.ids(p.ID); !reflect.DeepEqual(got, []string{"t1", "t2", "t3", "t4"}) {
		t.Fatalf("to end = %v", got)
	}
	move(0, 2)
	if got := e.ids(p.ID); !reflect.DeepEqual(got, []string{"t2", "t3", "t1", "t4"}) {
		t.Fatalf("middle = %v", got)
	}
	if err := e.svc.MoveItem(e.ctx, p.ID, 12345, 0); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("unknown position: %v", err)
	}
	if err := e.svc.MoveItem(e.ctx, "nope", 1, 0); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("unknown playlist: %v", err)
	}
}

func TestRepeatedMovesInTheSameGapRenumber(t *testing.T) {
	e := newEnv(t)
	p := e.create("mix")
	e.svc.AddTracks(e.ctx, p.ID, []string{"t1", "t2", "t3"})
	for i := 0; i < 100; i++ { // squeeze t3 and t2 between t1 and each other until the gap is gone
		if err := e.svc.MoveItem(e.ctx, p.ID, e.posOf(p.ID, 2), 1); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		if err := e.svc.MoveItem(e.ctx, p.ID, e.posOf(p.ID, 2), 1); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
	}
	_, items, _ := e.svc.Get(e.ctx, p.ID)
	if len(items) != 3 || items[0].TrackID != "t1" {
		t.Fatalf("items = %+v", items)
	}
	for i := 1; i < len(items); i++ {
		if items[i].Position <= items[i-1].Position {
			t.Fatalf("positions not increasing: %+v", items)
		}
	}
}

func TestRemoveItem(t *testing.T) {
	e := newEnv(t)
	p := e.create("mix")
	e.svc.AddTracks(e.ctx, p.ID, []string{"t1", "t2", "t3"})
	if err := e.svc.RemoveItem(e.ctx, p.ID, e.posOf(p.ID, 1)); err != nil {
		t.Fatal(err)
	}
	if got := e.ids(p.ID); !reflect.DeepEqual(got, []string{"t1", "t3"}) {
		t.Fatalf("after remove = %v", got)
	}
	if err := e.svc.RemoveItem(e.ctx, p.ID, 999); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("unknown position: %v", err)
	}
	if err := e.svc.RemoveItem(e.ctx, "nope", 1); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("unknown playlist: %v", err)
	}
}

func TestDeletingAPlaylistKeepsTheTracks(t *testing.T) {
	e := newEnv(t)
	p := e.create("mix")
	e.svc.AddTracks(e.ctx, p.ID, []string{"t1", "t2"})
	if err := e.svc.Delete(e.ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	e.svc.st.R.QueryRow(`SELECT count(*) FROM tracks`).Scan(&n)
	var items int
	e.svc.st.R.QueryRow(`SELECT count(*) FROM playlist_items`).Scan(&items)
	if n != 6 || items != 0 {
		t.Errorf("tracks=%d items=%d", n, items)
	}
}

func TestLikes(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.SetLike(e.ctx, "podcast", "x"); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("bad kind: %v", err)
	}
	if err := e.svc.SetLike(e.ctx, "track", "nope"); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("unknown target: %v", err)
	}
	e.events()
	for _, id := range []string{"t1", "t2"} {
		if err := e.svc.SetLike(e.ctx, "track", id); err != nil {
			t.Fatal(err)
		}
	}
	e.svc.SetLike(e.ctx, "track", "t1") // idempotent
	if err := e.svc.SetLike(e.ctx, "artist", "a1"); err != nil {
		t.Fatal(err)
	}

	likes, err := e.svc.Likes(e.ctx, "track")
	if err != nil || len(likes) != 2 {
		t.Fatalf("likes = %+v %v", likes, err)
	}
	if ids, _ := e.svc.LikedTrackIDs(e.ctx); len(ids) != 2 {
		t.Errorf("liked ids = %v", ids)
	}
	if artists, _ := e.svc.Likes(e.ctx, "artist"); len(artists) != 1 {
		t.Errorf("artist likes = %v", artists)
	}
	if _, err := e.svc.Likes(e.ctx, "nope"); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("list bad kind: %v", err)
	}

	e.events()
	e.svc.RemoveLike(e.ctx, "track", "t1")
	e.svc.RemoveLike(e.ctx, "track", "t1") // already gone: no event, no error
	if likes, _ := e.svc.Likes(e.ctx, "track"); len(likes) != 1 || likes[0].ID != "t2" {
		t.Errorf("after unlike = %+v", likes)
	}
	if ev := e.events(); ev["like.changed"] != 1 {
		t.Errorf("events = %v", ev)
	}
	if err := e.svc.RemoveLike(e.ctx, "nope", "x"); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("unlike bad kind: %v", err)
	}
}
