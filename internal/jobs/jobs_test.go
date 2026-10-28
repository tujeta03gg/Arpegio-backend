package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"arpegio/internal/events"
	"arpegio/internal/store"
)

type env struct {
	t     *testing.T
	ctx   context.Context
	svc   *Service
	bus   *events.Bus
	ch    <-chan events.Event
	clock time.Time
	st    *store.Store
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bus := events.New()
	ch, cancel := bus.Subscribe(64)
	t.Cleanup(cancel)
	e := &env{t: t, ctx: ctx, bus: bus, ch: ch, st: st, clock: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	e.svc = New(st, bus, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.svc.now = func() time.Time { return e.clock }
	e.svc.backoff = func(attempt int) time.Duration { return time.Duration(attempt) * time.Minute }
	return e
}

func (e *env) status() map[string]int {
	rows, err := e.st.R.Query(`SELECT status, count(*) FROM jobs GROUP BY status`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var s string
		var n int
		rows.Scan(&s, &n)
		out[s] = n
	}
	return out
}

func (e *env) finished() []map[string]string {
	var out []map[string]string
	for len(e.ch) > 0 {
		if ev := <-e.ch; ev.Type == "job.finished" {
			out = append(out, ev.Data.(map[string]string))
		}
	}
	return out
}

func TestEnqueueRunsHandlerWithPayload(t *testing.T) {
	e := newEnv(t)
	var got []string
	e.svc.Handle("ingest_file", func(_ context.Context, p json.RawMessage) error {
		var in struct{ Path string }
		if err := json.Unmarshal(p, &in); err != nil {
			return err
		}
		got = append(got, in.Path)
		return nil
	})
	e.svc.Enqueue(e.ctx, "ingest_file", map[string]string{"path": "/m/a.flac"})
	e.svc.Enqueue(e.ctx, "ingest_file", map[string]string{"path": "/m/b.flac"})
	if n, _ := e.svc.Open(e.ctx); n != 2 {
		t.Fatalf("open = %d", n)
	}
	e.svc.drain(e.ctx)
	if len(got) != 2 || got[0] != "/m/a.flac" || got[1] != "/m/b.flac" {
		t.Fatalf("handled = %v", got)
	}
	if n, _ := e.svc.Open(e.ctx); n != 0 || len(e.status()) != 0 {
		t.Errorf("finished jobs should be pruned: open=%d status=%v", n, e.status())
	}
	if ev := e.finished(); len(ev) != 2 || ev[0]["status"] != "done" || ev[0]["kind"] != "ingest_file" {
		t.Errorf("events = %v", ev)
	}
}

func TestFailedJobsRetryWithBackoffThenGiveUp(t *testing.T) {
	e := newEnv(t)
	e.svc.maxAttempts = 3
	calls := 0
	e.svc.Handle("flaky", func(context.Context, json.RawMessage) error { calls++; return errors.New("disk busy") })
	e.svc.Enqueue(e.ctx, "flaky", nil)

	e.svc.drain(e.ctx)
	if calls != 1 || e.status()["pending"] != 1 {
		t.Fatalf("after 1st run: calls=%d status=%v", calls, e.status())
	}
	e.svc.drain(e.ctx) // not due yet
	if calls != 1 {
		t.Fatalf("ran before the backoff: calls=%d", calls)
	}
	e.clock = e.clock.Add(61 * time.Second)
	e.svc.drain(e.ctx)
	e.clock = e.clock.Add(3 * time.Minute)
	e.svc.drain(e.ctx)
	if calls != 3 || e.status()["failed"] != 1 {
		t.Fatalf("calls=%d status=%v", calls, e.status())
	}
	if f, _ := e.svc.Failed(e.ctx); f != 1 {
		t.Errorf("failed = %d", f)
	}
	if n, _ := e.svc.Open(e.ctx); n != 0 {
		t.Errorf("failed jobs are not open: %d", n)
	}
	var lastErr string
	e.st.R.QueryRow(`SELECT last_error FROM jobs`).Scan(&lastErr)
	if lastErr != "disk busy" {
		t.Errorf("last_error = %q", lastErr)
	}
	if ev := e.finished(); len(ev) != 1 || ev[0]["status"] != "failed" {
		t.Errorf("events = %v (retries must not announce)", ev)
	}
}

func TestRetrySucceedsLater(t *testing.T) {
	e := newEnv(t)
	attempt := 0
	e.svc.Handle("k", func(context.Context, json.RawMessage) error {
		attempt++
		if attempt < 2 {
			return errors.New("not yet")
		}
		return nil
	})
	e.svc.Enqueue(e.ctx, "k", nil)
	e.svc.drain(e.ctx)
	e.clock = e.clock.Add(2 * time.Minute)
	e.svc.drain(e.ctx)
	if attempt != 2 || len(e.status()) != 0 {
		t.Errorf("attempt=%d status=%v", attempt, e.status())
	}
}

func TestPanicAndUnknownKindAreContained(t *testing.T) {
	e := newEnv(t)
	e.svc.maxAttempts = 1
	e.svc.Handle("boom", func(context.Context, json.RawMessage) error { panic("kaboom") })
	e.svc.Enqueue(e.ctx, "boom", nil)
	e.svc.Enqueue(e.ctx, "nobody-handles-this", nil)
	e.svc.drain(e.ctx) // must not panic
	if e.status()["failed"] != 2 {
		t.Errorf("status = %v", e.status())
	}
	var msg string
	e.st.R.QueryRow(`SELECT last_error FROM jobs WHERE kind = 'boom'`).Scan(&msg)
	if msg != "handler panicked: kaboom" {
		t.Errorf("last_error = %q", msg)
	}
}

func TestUnknownKindFailsWithoutRetries(t *testing.T) {
	e := newEnv(t)
	e.svc.Enqueue(e.ctx, "mystery", nil)
	e.svc.drain(e.ctx)
	if e.status()["failed"] != 1 {
		t.Errorf("status = %v", e.status())
	}
}

func TestRunWakesOnEnqueueAndOnRetryTime(t *testing.T) {
	e := newEnv(t)
	e.svc.now = time.Now
	e.svc.backoff = func(int) time.Duration { return 40 * time.Millisecond }
	var mu sync.Mutex
	var runs []int
	done := make(chan struct{}, 4)
	e.svc.Handle("k", func(_ context.Context, p json.RawMessage) error {
		mu.Lock()
		runs = append(runs, len(runs)+1)
		n := len(runs)
		mu.Unlock()
		defer func() { done <- struct{}{} }()
		if n == 1 {
			return errors.New("first try fails")
		}
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { e.svc.Run(ctx); close(stopped) }()

	time.Sleep(30 * time.Millisecond) // the worker is idle now, asleep
	e.svc.Enqueue(ctx, "k", nil)
	for i := 0; i < 2; i++ { // the first run, then the retry fired by the timer
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatalf("run %d never happened", i+1)
		}
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRunRecoversJobsLeftRunningByACrash(t *testing.T) {
	e := newEnv(t)
	e.svc.now = time.Now
	_, err := e.st.W.Exec(`INSERT INTO jobs(id, kind, payload, status, attempts, run_after) VALUES ('j1', 'k', '{}', 'running', 1, '2020-01-01T00:00:00.000000000Z')`)
	if err != nil {
		t.Fatal(err)
	}
	ran := make(chan struct{}, 1)
	e.svc.Handle("k", func(context.Context, json.RawMessage) error { ran <- struct{}{}; return nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.svc.Run(ctx)
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("orphaned job was not picked up")
	}
}

func TestEnqueueRejectsUnencodablePayload(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.Enqueue(e.ctx, "k", make(chan int)); err == nil {
		t.Error("expected an encoding error")
	}
}
