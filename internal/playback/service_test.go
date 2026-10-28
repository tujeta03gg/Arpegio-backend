package playback

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"arpegio/internal/apperr"
	"arpegio/internal/catalog"
	"arpegio/internal/events"
	"arpegio/internal/history"
	"arpegio/internal/queue"
	"arpegio/internal/store"
)

// ---- fakes ----

type fakeEngine struct {
	calls   []string
	events  chan EngineEvent
	failOn  map[string]error // path -> error returned by Load
	loadErr error
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{events: make(chan EngineEvent, 16), failOn: map[string]error{}}
}

func (f *fakeEngine) rec(format string, a ...any) {
	f.calls = append(f.calls, fmt.Sprintf(format, a...))
}
func (f *fakeEngine) Load(_ context.Context, path string, start int64, paused bool) error {
	if err := f.failOn[path]; err != nil {
		return err
	}
	f.rec("load:%s@%d paused=%v", name(path), start, paused)
	return nil
}
func (f *fakeEngine) Preload(_ context.Context, path string) error {
	f.rec("preload:%s", name(path))
	return nil
}
func (f *fakeEngine) ClearPreload(context.Context) error       { f.rec("clearpreload"); return nil }
func (f *fakeEngine) SetPause(_ context.Context, p bool) error { f.rec("pause:%v", p); return nil }
func (f *fakeEngine) Seek(_ context.Context, ms int64) error   { f.rec("seek:%d", ms); return nil }
func (f *fakeEngine) SetVolume(_ context.Context, l int) error { f.rec("volume:%d", l); return nil }
func (f *fakeEngine) Stop(context.Context) error               { f.rec("stop"); return nil }
func (f *fakeEngine) Events() <-chan EngineEvent               { return f.events }

func name(path string) string { return strings.TrimSuffix(strings.TrimPrefix(path, "/music/"), ".mp3") }

// take returns the recorded calls and clears them.
func (f *fakeEngine) take() string {
	s := strings.Join(f.calls, " | ")
	f.calls = nil
	return s
}

type fakeTracks struct{ duration int64 }

func (f fakeTracks) Track(_ context.Context, id string) (catalog.Track, error) {
	if id == "ghost" {
		return catalog.Track{}, apperr.ErrNotFound
	}
	return catalog.Track{ID: id, Title: id, DurationMS: f.duration}, nil
}
func (f fakeTracks) TrackPath(_ context.Context, id string) (string, error) {
	return "/music/" + id + ".mp3", nil
}

type fakeRadio struct {
	asked int
	ids   []string
}

func (r *fakeRadio) More(_ context.Context, _ string, n int) ([]string, error) {
	r.asked++
	return r.ids, nil
}

type env struct {
	t      *testing.T
	ctx    context.Context
	svc    *Service
	eng    *fakeEngine
	queue  *queue.Service
	hist   *history.Service
	st     *store.Store
	bus    *events.Bus
	events chan events.Event
	now    time.Time
	radio  *fakeRadio
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
	for i := 1; i <= 12; i++ {
		id := fmt.Sprintf("t%d", i)
		st.W.Exec(`INSERT INTO files(id, root_id, rel_path, content_hash, size_bytes, mtime, format, duration_ms) VALUES (?, 'r', ?, ?, 1, 't', 'mp3', 100000)`, "f"+id, id, id)
		if _, err := st.W.Exec(`INSERT INTO tracks(id, file_id, title, duration_ms, added_at) VALUES (?, ?, ?, 100000, 't')`, id, "f"+id, id); err != nil {
			t.Fatal(err)
		}
	}
	e := &env{t: t, ctx: ctx, st: st, bus: events.New(), eng: newFakeEngine(), now: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC), radio: &fakeRadio{}}
	e.queue = queue.New(st, e.bus)
	e.hist = history.New(st, history.Rule{Seconds: 30, Percent: 50})
	sub, _ := e.bus.Subscribe(256)
	e.events = make(chan events.Event, 256)
	go func() {
		for ev := range sub {
			e.events <- ev
		}
	}()
	e.svc = e.newService()
	return e
}

func (e *env) newService() *Service {
	list := func(ids ...string) func(context.Context, string) ([]string, error) {
		return func(context.Context, string) ([]string, error) { return ids, nil }
	}
	return New(Deps{
		Engine: e.eng, Queue: e.queue, Tracks: fakeTracks{duration: 100000}, History: e.hist, Radio: e.radio,
		Contexts: &Resolver{Sources: map[string]func(context.Context, string) ([]string, error){
			"album": list("t1", "t2", "t3", "t4", "t5", "t6"),
			"radio": list("t1", "t2", "t3"),
			"empty": list(),
			"track": func(_ context.Context, id string) ([]string, error) {
				if id == "ghost" {
					return nil, apperr.NotFound(sql.ErrNoRows, "track ghost")
				}
				return []string{id}, nil
			},
			"broken": func(context.Context, string) ([]string, error) { return nil, errors.New("db down") },
		}},
		Store: e.st, Bus: e.bus, Output: "pipewire", Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return e.now },
	})
}

func (e *env) play(spec string) {
	e.t.Helper()
	if err := e.svc.Play(e.ctx, PlayRequest{Context: spec}); err != nil {
		e.t.Fatal(err)
	}
}

// listen feeds position ticks of 1s up to ms, as mpv would while playing.
func (e *env) listen(from, to int64) {
	for p := from + 1000; p <= to; p += 1000 {
		e.svc.handle(e.ctx, EngineEvent{Kind: EvPosition, PositionMS: p})
	}
}

func (e *env) end(reason string) { e.svc.handle(e.ctx, EngineEvent{Kind: EvEnded, Reason: reason}) }

func (e *env) state() State {
	e.t.Helper()
	st, err := e.svc.State(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

func (e *env) drain() map[string]int {
	time.Sleep(20 * time.Millisecond) // the forwarding goroutine in newEnv
	out := map[string]int{}
	for {
		select {
		case ev := <-e.events:
			out[ev.Type]++
		default:
			return out
		}
	}
}

func (e *env) plays() []history.Entry {
	e.t.Helper()
	p, err := e.hist.Recent(e.ctx, 50)
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

func is(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// ---- tests ----

func TestPlayContextLoadsFirstAndPreloadsNext(t *testing.T) {
	e := newEnv(t)
	e.play("album:A1")
	is(t, e.eng.take(), "load:t1@0 paused=false | preload:t2")
	st := e.state()
	if st.TrackID != "t1" || !st.Playing || st.DurationMS != 100000 || st.Volume != 70 {
		t.Fatalf("state = %+v", st)
	}
	ev := e.drain()
	if ev["player.track_started"] != 1 || ev["player.state_changed"] < 1 || ev["queue.changed"] < 1 {
		t.Errorf("events = %v", ev)
	}
}

func TestPlayWithStartTrack(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.Play(e.ctx, PlayRequest{Context: "album:A1", Start: "track:t3"}); err != nil {
		t.Fatal(err)
	}
	is(t, e.eng.take(), "load:t3@0 paused=false | preload:t4")

	// a bare track starts a one-track context
	if err := e.svc.Play(e.ctx, PlayRequest{Start: "track:t9"}); err != nil {
		t.Fatal(err)
	}
	is(t, e.eng.take(), "load:t9@0 paused=false")
}

func TestPlayRejectsBadRequests(t *testing.T) {
	e := newEnv(t)
	for _, req := range []PlayRequest{
		{Context: "nonsense:1"},
		{Context: "empty:x"},
		{Context: "album:A1", Start: "track:t99"},
		{Context: "album:A1", Start: "album:A1"},
	} {
		if err := e.svc.Play(e.ctx, req); !errors.Is(err, apperr.ErrInvalid) {
			t.Errorf("Play(%+v) = %v, want ErrInvalid", req, err)
		}
	}
	if err := e.svc.Play(e.ctx, PlayRequest{Context: "broken:1"}); err == nil || errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("resolver failure should pass through: %v", err)
	}
	if err := e.svc.Play(e.ctx, PlayRequest{Context: "track:ghost"}); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("unknown track: %v", err)
	}
	if len(e.eng.calls) != 0 {
		t.Errorf("engine touched on failures: %v", e.eng.calls)
	}
}

func TestPauseResume(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.Pause(e.ctx); err != nil { // nothing playing: no-op
		t.Fatal(err)
	}
	if err := e.svc.Play(e.ctx, PlayRequest{}); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("resume with empty queue: %v", err)
	}
	e.play("album:A1")
	e.eng.take()
	e.svc.Pause(e.ctx)
	if st := e.state(); st.Playing {
		t.Error("should be paused")
	}
	e.svc.Play(e.ctx, PlayRequest{})
	is(t, e.eng.take(), "pause:true | pause:false")
	if !e.state().Playing {
		t.Error("should be playing")
	}
}

func TestResumeStartsQueueSelectionAfterStop(t *testing.T) {
	e := newEnv(t)
	e.play("album:A1")
	e.svc.Stop(e.ctx)
	if st := e.state(); st.TrackID != "" || st.Playing {
		t.Fatalf("after stop = %+v", st)
	}
	e.eng.take()
	if err := e.svc.Play(e.ctx, PlayRequest{}); err != nil {
		t.Fatal(err)
	}
	is(t, e.eng.take(), "load:t1@0 paused=false | preload:t2")
}

func TestNaturalEndIsGaplessAndRecordsCountedPlay(t *testing.T) {
	e := newEnv(t)
	e.play("album:A1")
	e.listen(0, 100000)
	e.eng.take()
	e.end(ReasonEOF)

	is(t, e.eng.take(), "preload:t3") // no load: mpv already moved on to t2
	if st := e.state(); st.TrackID != "t2" || st.PositionMS != 0 {
		t.Fatalf("state = %+v", st)
	}
	plays := e.plays()
	if len(plays) != 1 || plays[0].TrackID != "t1" || !plays[0].Completed || plays[0].ListenedMS < 99000 {
		t.Fatalf("plays = %+v", plays)
	}
	ev := e.drain()
	if ev["player.track_ended"] != 1 || ev["player.track_started"] != 2 {
		t.Errorf("events = %v", ev)
	}
}

func TestManualNextCountsAsSkip(t *testing.T) {
	e := newEnv(t)
	e.play("album:A1")
	e.listen(0, 5000)
	e.eng.take()
	if err := e.svc.Next(e.ctx); err != nil {
		t.Fatal(err)
	}
	is(t, e.eng.take(), "load:t2@0 paused=false | preload:t3")
	plays := e.plays()
	if len(plays) != 1 || !plays[0].Skipped || plays[0].Completed || plays[0].ListenedMS != 5000 {
		t.Fatalf("plays = %+v", plays)
	}
	if e.state().TrackID != "t2" {
		t.Error("should be on t2")
	}
}

func TestSeekingIsNotListeningTime(t *testing.T) {
	e := newEnv(t)
	e.play("album:A1")
	e.listen(0, 10000)
	e.svc.Seek(e.ctx, 90000) // jump forward
	e.svc.handle(e.ctx, EngineEvent{Kind: EvPosition, PositionMS: 91000})
	e.svc.Next(e.ctx)
	plays := e.plays()
	if len(plays) != 1 || plays[0].ListenedMS != 11000 {
		t.Fatalf("listened = %+v", plays)
	}
}

func TestPausedTimeIsNotListeningTime(t *testing.T) {
	e := newEnv(t)
	e.play("album:A1")
	e.listen(0, 4000)
	e.svc.Pause(e.ctx)
	e.svc.handle(e.ctx, EngineEvent{Kind: EvPosition, PositionMS: 5000}) // stray tick while paused
	e.svc.Next(e.ctx)
	if p := e.plays(); len(p) != 1 || p[0].ListenedMS != 4000 {
		t.Fatalf("plays = %+v", p)
	}
}

func TestEndOfQueueStops(t *testing.T) {
	e := newEnv(t)
	e.play("track:t1")
	e.end(ReasonEOF)
	if st := e.state(); st.TrackID != "" || st.Playing {
		t.Fatalf("state = %+v", st)
	}
	if ev := e.drain(); ev["player.track_ended"] != 1 {
		t.Errorf("events = %v", ev)
	}
}

func TestRepeatAllWrapsAndRepeatOneReloads(t *testing.T) {
	e := newEnv(t)
	e.svc.SetRepeat(e.ctx, "all")
	e.play("radio:r") // 3 tracks
	e.end(ReasonEOF)
	e.end(ReasonEOF)
	e.eng.take()
	e.end(ReasonEOF) // t3 ends, queue wraps to t1
	if st := e.state(); st.TrackID != "t1" {
		t.Fatalf("after wrap: %+v", st)
	}

	e.svc.SetRepeat(e.ctx, "one")
	e.eng.take()
	e.end(ReasonEOF)
	is(t, e.eng.take(), "load:t1@0 paused=false") // reloaded, nothing preloaded
	if e.state().TrackID != "t1" {
		t.Error("repeat one should stay on t1")
	}
}

func TestPrevious(t *testing.T) {
	e := newEnv(t)
	e.svc.Play(e.ctx, PlayRequest{Context: "album:A1", Start: "track:t3"})
	e.listen(0, 10000)
	e.eng.take()
	e.svc.Previous(e.ctx) // past 3s: restart
	is(t, e.eng.take(), "seek:0")
	if e.state().TrackID != "t3" {
		t.Error("restart must stay on t3")
	}
	e.svc.Previous(e.ctx) // at the start: go back
	is(t, e.eng.take(), "load:t2@0 paused=false | preload:t3")
}

func TestUnplayableTracksAreSkippedThenGiveUp(t *testing.T) {
	e := newEnv(t)
	e.play("album:A1")
	e.end(ReasonEOF) // gapless: the service follows the preloaded t2
	e.eng.take()
	e.eng.failOn["/music/t3.mp3"] = errors.New("gone")
	e.end(ReasonError) // t2 failed to play; t3 cannot be loaded either; t4 plays
	is(t, e.eng.take(), "load:t4@0 paused=false | preload:t5")
	if e.state().TrackID != "t4" {
		t.Fatalf("state = %+v", e.state())
	}

	// three failures in a row stop the player
	e2 := newEnv(t)
	e2.play("album:A1")
	for _, p := range []string{"t2", "t3", "t4", "t5", "t6"} {
		e2.eng.failOn["/music/"+p+".mp3"] = errors.New("gone")
	}
	e2.eng.take()
	e2.end(ReasonError)
	if got := e2.eng.take(); !strings.HasSuffix(got, "stop") {
		t.Errorf("calls = %q", got)
	}
	if st := e2.state(); st.TrackID != "" || st.Playing {
		t.Errorf("state = %+v", st)
	}
}

func TestEngineStopEventsAreIgnored(t *testing.T) {
	e := newEnv(t)
	e.play("album:A1")
	e.eng.take()
	e.end(ReasonStop) // old file replaced by our own Load
	if e.state().TrackID != "t1" || e.eng.take() != "" || len(e.plays()) != 0 {
		t.Error("stop event must not change anything")
	}
}

func TestSeekAndVolumeValidation(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.Seek(e.ctx, 1000); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("seek with nothing playing: %v", err)
	}
	e.play("album:A1")
	for _, ms := range []int64{-1, 100001} {
		if err := e.svc.Seek(e.ctx, ms); !errors.Is(err, apperr.ErrInvalid) {
			t.Errorf("seek %d: %v", ms, err)
		}
	}
	if err := e.svc.Seek(e.ctx, 30000); err != nil || e.state().PositionMS != 30000 {
		t.Errorf("seek: %v %+v", err, e.state())
	}
	for _, v := range []int{-1, 101} {
		if err := e.svc.SetVolume(e.ctx, v); !errors.Is(err, apperr.ErrInvalid) {
			t.Errorf("volume %d: %v", v, err)
		}
	}
	e.eng.take()
	if err := e.svc.SetVolume(e.ctx, 35); err != nil {
		t.Fatal(err)
	}
	is(t, e.eng.take(), "volume:35")
	if e.state().Volume != 35 {
		t.Error("volume not stored")
	}
}

func TestQueueChangesRefreshThePreload(t *testing.T) {
	e := newEnv(t)
	e.play("album:A1")
	e.eng.take()
	e.queue.Add(e.ctx, []string{"t9"}, true)
	e.svc.mu.Lock()
	e.svc.refreshPreload(e.ctx)
	e.svc.mu.Unlock()
	is(t, e.eng.take(), "clearpreload | preload:t9")

	e.svc.mu.Lock() // nothing changed: no engine traffic
	e.svc.refreshPreload(e.ctx)
	e.svc.mu.Unlock()
	is(t, e.eng.take(), "")

	e.svc.SetRepeat(e.ctx, "one") // repeat one needs no preload
	is(t, e.eng.take(), "clearpreload")
}

func TestShuffleReordersWhatIsPreloaded(t *testing.T) {
	e := newEnv(t)
	e.play("album:A1")
	e.eng.take()
	if err := e.svc.SetShuffle(e.ctx, true); err != nil {
		t.Fatal(err)
	}
	if !e.state().Shuffle {
		t.Error("shuffle flag")
	}
	st, _ := e.queue.State(e.ctx)
	next, _, _ := e.queue.Peek(e.ctx)
	want := "preload:" + next.TrackID
	if got := e.eng.take(); got != want && got != "" { // "" only if the shuffle kept t2 next
		t.Errorf("calls = %q, want %q (queue %v)", got, want, st.Items)
	}
}

func TestRadioIsToppedUpWhenLow(t *testing.T) {
	e := newEnv(t)
	e.radio.ids = []string{"t7", "t8", "t9"}
	e.play("radio:seed") // 3 tracks
	e.end(ReasonEOF)
	if e.radio.asked == 0 {
		t.Fatal("radio was not asked for more")
	}
	st, _ := e.queue.State(e.ctx)
	if len(st.Items) != 6 || st.Items[5].Source != "radio" || st.Items[5].SourceID != "seed" {
		t.Fatalf("queue = %+v", st.Items)
	}

	e2 := newEnv(t) // non-radio queues are left alone
	e2.play("album:A1")
	e2.end(ReasonEOF)
	if e2.radio.asked != 0 {
		t.Error("album queue must not trigger radio")
	}
}

func TestPositionEventsAreThrottled(t *testing.T) {
	e := newEnv(t)
	e.play("album:A1")
	e.drain()
	for i := int64(1); i <= 6; i++ { // six ticks within the same second
		e.svc.handle(e.ctx, EngineEvent{Kind: EvPosition, PositionMS: i * 100})
	}
	if n := e.drain()["player.position"]; n != 1 {
		t.Errorf("position events = %d, want 1", n)
	}
	e.now = e.now.Add(1500 * time.Millisecond)
	e.svc.handle(e.ctx, EngineEvent{Kind: EvPosition, PositionMS: 2000})
	if n := e.drain()["player.position"]; n != 1 {
		t.Errorf("after a second = %d, want 1", n)
	}
}

func TestStateIsRestoredAfterRestart(t *testing.T) {
	e := newEnv(t)
	e.svc.SetVolume(e.ctx, 42)
	e.play("album:A1")
	e.listen(0, 12000)
	e.svc.Pause(e.ctx) // saves the position

	// "restart": a fresh service and engine over the same database
	e.eng = newFakeEngine()
	again := e.newService()
	if err := again.Restore(e.ctx); err != nil {
		t.Fatal(err)
	}
	is(t, e.eng.take(), "") // nothing is sent until the engine announces itself
	again.handle(e.ctx, EngineEvent{Kind: EvRestarted})
	is(t, e.eng.take(), "volume:42 | load:t1@12000 paused=true | preload:t2")
	st, _ := again.State(e.ctx)
	if st.TrackID != "t1" || st.Playing || st.Volume != 42 || st.PositionMS != 12000 {
		t.Fatalf("restored = %+v", st)
	}
	// resuming continues the same track
	e.svc = again
	e.svc.Play(e.ctx, PlayRequest{})
	is(t, e.eng.take(), "pause:false")
}

func TestRestoreWithEmptyQueueOnlySetsVolume(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.Restore(e.ctx); err != nil {
		t.Fatal(err)
	}
	e.svc.handle(e.ctx, EngineEvent{Kind: EvRestarted})
	is(t, e.eng.take(), "volume:70")
}

func TestRunLoopHandlesEngineEventsAndSavesOnExit(t *testing.T) {
	e := newEnv(t)
	e.play("album:A1")
	e.listen(0, 7000)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.svc.Run(ctx); close(done) }()

	e.eng.events <- EngineEvent{Kind: EvEnded, Reason: ReasonEOF}
	deadline := time.Now().Add(2 * time.Second)
	for e.state().TrackID != "t2" {
		if time.Now().After(deadline) {
			t.Fatal("Run did not process the engine event")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
	ps, err := e.st.R.Query(`SELECT track_id FROM player_state WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	var id string
	ps.Next()
	ps.Scan(&id)
	if id != "t2" {
		t.Errorf("saved track = %q", id)
	}
}

func TestResolver(t *testing.T) {
	r := &Resolver{Sources: map[string]func(context.Context, string) ([]string, error){
		"album": func(_ context.Context, id string) ([]string, error) { return []string{id + "1"}, nil },
		"genre": func(_ context.Context, id string) ([]string, error) { return []string{"g"}, nil },
		"likes": func(_ context.Context, id string) ([]string, error) { return []string{"l"}, nil },
	}}
	pc, err := r.Resolve(context.Background(), "album:X")
	if err != nil || pc.Source != "album" || pc.SourceID != "X" || pc.TrackIDs[0] != "X1" {
		t.Errorf("album = %+v %v", pc, err)
	}
	if pc, _ := r.Resolve(context.Background(), "genre:rock"); pc.Source != "user" {
		t.Errorf("genre source = %q", pc.Source)
	}
	if pc, err := r.Resolve(context.Background(), "likes"); err != nil || pc.SourceID != "" {
		t.Errorf("likes = %+v %v", pc, err)
	}
	if _, err := r.Resolve(context.Background(), "podcast:1"); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("unknown kind: %v", err)
	}
}

func TestEngineRestartPutsTheCurrentTrackBack(t *testing.T) {
	e := newEnv(t)
	e.svc.SetVolume(e.ctx, 55)
	e.play("album:A1")
	e.listen(0, 20000)
	e.eng.take()
	e.svc.handle(e.ctx, EngineEvent{Kind: EvRestarted}) // mpv crashed and came back empty
	is(t, e.eng.take(), "volume:55 | load:t1@20000 paused=false | preload:t2")
	if st := e.state(); st.TrackID != "t1" || !st.Playing {
		t.Errorf("state = %+v", st)
	}
}
