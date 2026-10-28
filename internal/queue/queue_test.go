package queue

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"arpegio/internal/apperr"
	"arpegio/internal/events"
	"arpegio/internal/store"
)

type env struct {
	t   *testing.T
	svc *Service
	bus *events.Bus
	ctx context.Context
}

// newEnv creates a store with tracks t1..t20 and a queue service with a fixed shuffle seed.
func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.W.Exec(`INSERT INTO library_roots(id, path) VALUES ('r','/m')`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 20; i++ {
		id := fmt.Sprintf("t%d", i)
		st.W.Exec(`INSERT INTO files(id, root_id, rel_path, content_hash, size_bytes, mtime, format, duration_ms) VALUES (?, 'r', ?, ?, 1, 't', 'mp3', 1000)`, "f"+id, id, id)
		if _, err := st.W.Exec(`INSERT INTO tracks(id, file_id, title, duration_ms, added_at) VALUES (?, ?, ?, 1000, 't')`, id, "f"+id, id); err != nil {
			t.Fatal(err)
		}
	}
	bus := events.New()
	svc := New(st, bus)
	seed := int64(41)
	svc.newSeed = func() int64 { seed++; return seed }
	return &env{t: t, svc: svc, bus: bus, ctx: ctx}
}

func (e *env) state() State {
	e.t.Helper()
	st, err := e.svc.State(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

func (e *env) order() []string {
	var out []string
	for _, it := range e.state().Items {
		out = append(out, it.TrackID)
	}
	return out
}

func (e *env) current() string {
	it, ok := e.state().Current()
	if !ok {
		return ""
	}
	return it.TrackID
}

func (e *env) replace(start int, ids ...string) {
	e.t.Helper()
	if _, err := e.svc.Replace(e.ctx, ids, "album", "A1", start); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) posOf(id string) float64 {
	for _, it := range e.state().Items {
		if it.TrackID == id {
			return it.Position
		}
	}
	e.t.Fatalf("%s not in queue", id)
	return 0
}

func eq(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestReplaceSelectsStartAndKeepsContextOrder(t *testing.T) {
	e := newEnv(t)
	e.replace(1, "t1", "t2", "t3", "t4")
	eq(t, e.order(), []string{"t1", "t2", "t3", "t4"})
	eq(t, e.current(), "t2")
	st := e.state()
	if st.Items[0].Source != "album" || st.Items[0].SourceID != "A1" {
		t.Errorf("source lost: %+v", st.Items[0])
	}
	if n, _ := e.svc.Remaining(e.ctx); n != 2 {
		t.Errorf("remaining = %d", n)
	}
}

func TestReplaceRejectsBadInput(t *testing.T) {
	e := newEnv(t)
	for _, tt := range []struct {
		ids   []string
		start int
	}{{nil, 0}, {[]string{"t1"}, 1}, {[]string{"t1"}, -1}} {
		if _, err := e.svc.Replace(e.ctx, tt.ids, "user", "", tt.start); !errors.Is(err, apperr.ErrInvalid) {
			t.Errorf("Replace(%v,%d) = %v", tt.ids, tt.start, err)
		}
	}
}

func TestReplaceKeepsPlayNextButDropsOldContext(t *testing.T) {
	e := newEnv(t)
	e.replace(0, "t1", "t2", "t3")
	e.svc.Add(e.ctx, []string{"t9"}, true)
	eq(t, e.order(), []string{"t1", "t9", "t2", "t3"})

	e.replace(1, "t5", "t6", "t7")
	eq(t, e.order(), []string{"t5", "t6", "t9", "t7"})
	eq(t, e.current(), "t6")

	// once the "play next" item has played, replacing again drops it
	e.svc.Next(e.ctx, false)
	eq(t, e.current(), "t9")
	e.replace(0, "t10", "t11")
	eq(t, e.order(), []string{"t10", "t11"})
}

func TestShuffleIsReproducibleFromSavedSeed(t *testing.T) {
	ids := []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8"}
	run := func() []string {
		e := newEnv(t)
		if err := e.svc.SetShuffle(e.ctx, true); err != nil {
			t.Fatal(err)
		}
		e.replace(2, ids...)
		eq(t, e.current(), "t3")
		return e.order()
	}
	a, b := run(), run()
	eq(t, a, b)
	if a[0] != "t3" || reflect.DeepEqual(a, ids) || len(a) != len(ids) {
		t.Errorf("shuffled order = %v", a)
	}
	seen := map[string]bool{}
	for _, id := range a {
		seen[id] = true
	}
	if len(seen) != len(ids) {
		t.Errorf("tracks lost or duplicated: %v", a)
	}
}

func TestSetShuffleKeepsCurrentAndPlayNextFirst(t *testing.T) {
	e := newEnv(t)
	e.replace(1, "t1", "t2", "t3", "t4", "t5", "t6")
	e.svc.Add(e.ctx, []string{"t9"}, true)
	if err := e.svc.SetShuffle(e.ctx, true); err != nil {
		t.Fatal(err)
	}
	order := e.order()
	if order[0] != "t1" || order[1] != "t2" || order[2] != "t9" || e.current() != "t2" || len(order) != 7 {
		t.Fatalf("order = %v current = %s", order, e.current())
	}
	if !e.state().Shuffle {
		t.Error("shuffle flag not saved")
	}
	e.svc.SetShuffle(e.ctx, false)
	if e.state().Shuffle {
		t.Error("shuffle flag not cleared")
	}
}

func TestAddAtEndAndPlayNextOrdering(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.Add(e.ctx, nil, false); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("empty add: %v", err)
	}
	e.svc.Add(e.ctx, []string{"t1", "t2"}, false)
	eq(t, e.order(), []string{"t1", "t2"})
	eq(t, e.current(), "") // adding does not select anything

	e.replace(0, "t1", "t2", "t3")
	e.svc.Add(e.ctx, []string{"t10"}, false)
	e.svc.Add(e.ctx, []string{"t11", "t12"}, true)
	e.svc.Add(e.ctx, []string{"t13"}, true)
	// FIFO among "play next" items, all before the rest of the context
	eq(t, e.order(), []string{"t1", "t11", "t12", "t13", "t2", "t3", "t10"})
	for _, it := range e.state().Items[1:4] {
		if it.Priority != 1 {
			t.Errorf("%s should be priority", it.TrackID)
		}
	}
}

func TestPlayNextWithNothingSelectedGoesFirst(t *testing.T) {
	e := newEnv(t)
	e.svc.Add(e.ctx, []string{"t1", "t2"}, false)
	e.svc.Add(e.ctx, []string{"t5"}, true)
	eq(t, e.order(), []string{"t5", "t1", "t2"})
}

func TestNextAndPreviousAcrossRepeatModes(t *testing.T) {
	e := newEnv(t)
	e.replace(0, "t1", "t2", "t3")

	step := func(auto bool) (string, bool) {
		it, ok, err := e.svc.Next(e.ctx, auto)
		if err != nil {
			t.Fatal(err)
		}
		return it.TrackID, ok
	}
	if id, ok := step(true); id != "t2" || !ok {
		t.Fatalf("next = %s %v", id, ok)
	}
	step(true)
	if id, ok := step(true); ok || e.current() != "t3" {
		t.Fatalf("end of queue: %s %v current=%s", id, ok, e.current())
	}

	e.svc.SetRepeat(e.ctx, "all")
	if id, ok := step(true); id != "t1" || !ok {
		t.Errorf("repeat all wrap = %s %v", id, ok)
	}
	if it, ok, _ := e.svc.Previous(e.ctx); it.TrackID != "t3" || !ok {
		t.Errorf("previous wrap = %s %v", it.TrackID, ok)
	}

	e.svc.SetRepeat(e.ctx, "one")
	if id, _ := step(true); id != "t3" {
		t.Errorf("repeat one auto = %s", id)
	}
	if id, _ := step(false); id != "t1" { // manual next leaves the repeated track
		t.Errorf("repeat one manual = %s", id)
	}

	e.svc.SetRepeat(e.ctx, "off")
	if it, ok, _ := e.svc.Previous(e.ctx); it.TrackID != "t1" || !ok {
		t.Errorf("previous at start restarts current: %s %v", it.TrackID, ok)
	}
	if err := e.svc.SetRepeat(e.ctx, "sometimes"); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("bad repeat: %v", err)
	}
}

func TestNextOnEmptyAndUnselected(t *testing.T) {
	e := newEnv(t)
	if _, ok, err := e.svc.Next(e.ctx, true); ok || err != nil {
		t.Errorf("empty queue next: %v %v", ok, err)
	}
	if _, ok, _ := e.svc.Previous(e.ctx); ok {
		t.Error("empty queue previous")
	}
	e.svc.Add(e.ctx, []string{"t4", "t5"}, false)
	if it, ok, _ := e.svc.Peek(e.ctx); !ok || it.TrackID != "t4" {
		t.Errorf("peek with no selection = %v %v", it, ok)
	}
	if it, ok, _ := e.svc.Next(e.ctx, false); !ok || it.TrackID != "t4" {
		t.Errorf("next with no selection = %v %v", it, ok)
	}
	if it, ok, _ := e.svc.Peek(e.ctx); !ok || it.TrackID != "t5" || e.current() != "t4" {
		t.Errorf("peek must not move: %v %v current=%s", it, ok, e.current())
	}
}

func TestMove(t *testing.T) {
	e := newEnv(t)
	e.replace(1, "t1", "t2", "t3", "t4", "t5")

	move := func(id string, to int) {
		t.Helper()
		if err := e.svc.Move(e.ctx, e.posOf(id), to); err != nil {
			t.Fatal(err)
		}
	}
	move("t5", 0)
	eq(t, e.order(), []string{"t5", "t1", "t2", "t3", "t4"})
	move("t5", 99) // clamped to the end
	eq(t, e.order(), []string{"t1", "t2", "t3", "t4", "t5"})
	move("t4", 2)
	eq(t, e.order(), []string{"t1", "t2", "t4", "t3", "t5"})
	eq(t, e.current(), "t2")

	move("t2", 4) // moving the selected item keeps it selected
	eq(t, e.order(), []string{"t1", "t4", "t3", "t5", "t2"})
	eq(t, e.current(), "t2")

	if err := e.svc.Move(e.ctx, 12345, 0); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("unknown position: %v", err)
	}
}

func TestManyMovesBetweenSameNeighboursRenumber(t *testing.T) {
	e := newEnv(t)
	e.replace(0, "t1", "t2", "t3")
	// keep dropping t3 between t1 and t2 / t2 and t1: positions would underflow without renumbering
	for i := 0; i < 80; i++ {
		to := 1
		if err := e.svc.Move(e.ctx, e.posOf("t3"), to); err != nil {
			t.Fatalf("move %d: %v", i, err)
		}
		if err := e.svc.Move(e.ctx, e.posOf("t2"), 1); err != nil {
			t.Fatalf("move %d: %v", i, err)
		}
	}
	got := e.order()
	if len(got) != 3 {
		t.Fatalf("items = %v", got)
	}
	st := e.state()
	for i := 1; i < len(st.Items); i++ {
		if st.Items[i].Position <= st.Items[i-1].Position {
			t.Fatalf("positions not strictly increasing: %+v", st.Items)
		}
	}
	eq(t, e.current(), "t1")
}

func TestRemoveAndClear(t *testing.T) {
	e := newEnv(t)
	e.replace(1, "t1", "t2", "t3", "t4")
	if err := e.svc.Remove(e.ctx, e.posOf("t2")); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("removing current: %v", err)
	}
	if err := e.svc.Remove(e.ctx, 999); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("removing unknown: %v", err)
	}
	if err := e.svc.Remove(e.ctx, e.posOf("t3")); err != nil {
		t.Fatal(err)
	}
	eq(t, e.order(), []string{"t1", "t2", "t4"})

	if err := e.svc.Clear(e.ctx); err != nil {
		t.Fatal(err)
	}
	eq(t, e.order(), []string{"t2"})
	eq(t, e.current(), "t2")

	e2 := newEnv(t)
	e2.svc.Add(e2.ctx, []string{"t1"}, false)
	e2.svc.Clear(e2.ctx)
	eq(t, e2.order(), []string(nil))
}

func TestMutationsPublishQueueChanged(t *testing.T) {
	e := newEnv(t)
	ch, cancel := e.bus.Subscribe(16)
	defer cancel()
	e.replace(0, "t1", "t2")
	e.svc.Next(e.ctx, false)
	e.svc.Add(e.ctx, []string{"t3"}, false)
	e.svc.Clear(e.ctx)
	if len(ch) != 4 {
		t.Fatalf("events = %d, want 4", len(ch))
	}
	if ev := <-ch; ev.Type != "queue.changed" {
		t.Errorf("event = %v", ev)
	}
	// reads and failed writes publish nothing
	for len(ch) > 0 {
		<-ch
	}
	e.state()
	e.svc.Remove(e.ctx, 999)
	e.svc.SetRepeat(e.ctx, "bogus")
	if len(ch) != 0 {
		t.Errorf("unexpected events: %d", len(ch))
	}
}

func TestStateSurvivesANewService(t *testing.T) {
	e := newEnv(t)
	e.replace(2, "t1", "t2", "t3")
	e.svc.SetRepeat(e.ctx, "all")
	again := New(e.svc.st, e.bus) // what happens after a daemon restart
	st, err := again.State(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := st.Current()
	if cur.TrackID != "t3" || st.Repeat != "all" || len(st.Items) != 3 {
		t.Errorf("restored state = %+v", st)
	}
	if !strings.HasPrefix(fmt.Sprint(cur.Position), "3") {
		t.Errorf("position = %v", cur.Position)
	}
}
