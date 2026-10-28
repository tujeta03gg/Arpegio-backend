package playback

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeMPV is the other end of the IPC socket: it records commands and answers like mpv.
type fakeMPV struct {
	mu   sync.Mutex
	cmds []string
	nc   net.Conn
	// fileLoaded makes it announce "file-loaded" after every loadfile replace
	fileLoaded bool
	// loadDelay postpones that announcement; until it happens the fake behaves like a real
	// mpv that is still opening the file and rejects seeks with "error running command"
	loadDelay time.Duration
	opened    atomic.Bool
	// seekFailures makes the first N seeks fail the same way regardless of load state
	seekFailures atomic.Int32
}

func (f *fakeMPV) serve(nc net.Conn) {
	sc := bufio.NewScanner(nc)
	for sc.Scan() {
		var m struct {
			Command   []any `json:"command"`
			RequestID int   `json:"request_id"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		parts := make([]string, len(m.Command))
		for i, p := range m.Command {
			parts[i] = fmt.Sprint(p)
		}
		line := strings.Join(parts, " ")
		f.mu.Lock()
		f.cmds = append(f.cmds, line)
		f.mu.Unlock()
		reply := `{"request_id":%d,"error":"success","data":null}`
		if parts[0] == "badcmd" {
			reply = `{"request_id":%d,"error":"invalid parameter"}`
		}
		if parts[0] == "silent" {
			continue
		}
		isLoad := parts[0] == "loadfile" && parts[2] == "replace"
		if isLoad {
			f.opened.Store(f.loadDelay == 0)
		}
		if parts[0] == "seek" && (f.seekFailures.Add(-1) >= 0 || (f.loadDelay > 0 && !f.opened.Load())) {
			reply = `{"request_id":%d,"error":"error running command"}`
		}
		fmt.Fprintf(nc, reply+"\n", m.RequestID)
		if f.fileLoaded && isLoad {
			announce := func() {
				f.opened.Store(true)
				fmt.Fprintln(nc, `{"event":"file-loaded"}`)
			}
			if f.loadDelay > 0 {
				time.AfterFunc(f.loadDelay, announce)
			} else {
				announce()
			}
		}
	}
}

func (f *fakeMPV) push(line string) {
	f.mu.Lock()
	nc := f.nc
	f.mu.Unlock()
	fmt.Fprintln(nc, line)
}

func (f *fakeMPV) commands() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.cmds, " | ")
}

func (f *fakeMPV) reset() {
	f.mu.Lock()
	f.cmds = nil
	f.mu.Unlock()
}

func newPipeConn(t *testing.T) (*conn, *fakeMPV, chan EngineEvent) {
	t.Helper()
	client, server := net.Pipe()
	f := &fakeMPV{nc: server}
	go f.serve(server)
	out := make(chan EngineEvent, 16)
	c := newConn(client, out)
	t.Cleanup(c.close)
	return c, f, out
}

func TestConnCommandAnswers(t *testing.T) {
	c, f, _ := newPipeConn(t)
	ctx := context.Background()
	if _, err := c.command(ctx, "set_property", "volume", 50); err != nil {
		t.Fatal(err)
	}
	if got := f.commands(); got != "set_property volume 50" {
		t.Errorf("sent %q", got)
	}
	if _, err := c.command(ctx, "badcmd"); err == nil || !strings.Contains(err.Error(), "invalid parameter") {
		t.Errorf("mpv error not reported: %v", err)
	}
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := c.command(short, "silent"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("unanswered command: %v", err)
	}
}

func TestConnTranslatesEvents(t *testing.T) {
	_, f, out := newPipeConn(t)
	for _, line := range []string{
		`{"event":"property-change","id":1,"name":"time-pos","data":12.3456}`,
		`{"event":"property-change","id":1,"name":"time-pos","data":null}`, // idle: ignored
		`{"event":"property-change","id":2,"name":"pause","data":true}`,    // other property: ignored
		`{"event":"end-file","reason":"eof"}`,
		`{"event":"end-file","reason":"error"}`,
		`{"event":"end-file","reason":"stop"}`,
		`{"event":"end-file","reason":"redirect"}`,
		`{"event":"idle"}`,
	} {
		f.push(line)
	}
	want := []EngineEvent{
		{Kind: EvPosition, PositionMS: 12346},
		{Kind: EvEnded, Reason: ReasonEOF},
		{Kind: EvEnded, Reason: ReasonError},
		{Kind: EvEnded, Reason: ReasonStop},
		{Kind: EvEnded, Reason: ReasonStop},
	}
	for i, w := range want {
		select {
		case got := <-out:
			if got != w {
				t.Errorf("event %d = %+v, want %+v", i, got, w)
			}
		case <-time.After(time.Second):
			t.Fatalf("event %d never arrived", i)
		}
	}
}

func TestConnEndEventSurvivesAFullChannel(t *testing.T) {
	c := &conn{out: make(chan EngineEvent, 1)}
	c.send(EngineEvent{Kind: EvPosition, PositionMS: 1})
	c.send(EngineEvent{Kind: EvPosition, PositionMS: 2}) // dropped
	c.send(EngineEvent{Kind: EvEnded, Reason: ReasonEOF})
	if ev := <-c.out; ev.Kind != EvEnded {
		t.Errorf("end event lost: %+v", ev)
	}
}

func TestConnClosedFailsPendingCommands(t *testing.T) {
	c, f, _ := newPipeConn(t)
	errc := make(chan error, 1)
	go func() { _, err := c.command(context.Background(), "silent"); errc <- err }()
	for !strings.Contains(f.commands(), "silent") {
		time.Sleep(time.Millisecond)
	}
	f.nc.Close()
	select {
	case err := <-errc:
		if err == nil {
			t.Error("expected an error")
		}
	case <-time.After(time.Second):
		t.Fatal("pending command was never released")
	}
}

// ---- supervised engine ----

type fakeProc struct {
	die  chan struct{}
	once sync.Once
}

func (p *fakeProc) Wait() error { <-p.die; return errors.New("killed") }
func (p *fakeProc) Kill()       { p.once.Do(func() { close(p.die) }) }

type mpvHarness struct {
	m      *MPV
	starts atomic.Int32
	procs  chan *fakeProc
	fakes  chan *fakeMPV
	cancel context.CancelFunc
	done   chan struct{}
}

func startMPV(t *testing.T, fileLoaded bool) *mpvHarness { return startMPVWith(t, fileLoaded, nil) }

// startMPVWith is startMPV plus a hook to tune each fake mpv before it starts serving.
func startMPVWith(t *testing.T, fileLoaded bool, tune func(*fakeMPV)) *mpvHarness {
	t.Helper()
	h := &mpvHarness{procs: make(chan *fakeProc, 8), fakes: make(chan *fakeMPV, 8), done: make(chan struct{})}
	var current *fakeMPV
	var mu sync.Mutex
	h.m = NewMPV(MPVOptions{
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		MinBackoff: 5 * time.Millisecond,
		Start: func(context.Context) (process, error) {
			h.starts.Add(1)
			p := &fakeProc{die: make(chan struct{})}
			h.procs <- p
			return p, nil
		},
		Dial: func(context.Context) (net.Conn, error) {
			mu.Lock()
			defer mu.Unlock()
			client, server := net.Pipe()
			current = &fakeMPV{nc: server, fileLoaded: fileLoaded}
			if tune != nil {
				tune(current)
			}
			go current.serve(server)
			h.fakes <- current
			return client, nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { h.m.Run(ctx); close(h.done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-h.done:
		case <-time.After(2 * time.Second):
			t.Error("MPV.Run did not stop")
		}
	})
	return h
}

func (h *mpvHarness) waitRestartEvent(t *testing.T) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-h.m.Events():
			if ev.Kind == EvRestarted {
				return
			}
		case <-deadline:
			t.Fatal("no EvRestarted")
		}
	}
}

func TestMPVCommands(t *testing.T) {
	h := startMPV(t, true)
	h.waitRestartEvent(t)
	f := <-h.fakes
	ctx := context.Background()
	if got := f.commands(); got != "observe_property 1 time-pos" {
		t.Fatalf("handshake = %q", got)
	}
	f.reset()

	steps := []struct {
		name string
		run  func() error
		want string
	}{
		{"load", func() error { return h.m.Load(ctx, "/m/a.flac", 0, false) }, "set_property pause false | loadfile /m/a.flac replace"},
		{"load paused at 12.5s", func() error { return h.m.Load(ctx, "/m/b.flac", 12500, true) }, "set_property pause true | loadfile /m/b.flac replace | seek 12.5 absolute+exact"},
		{"preload", func() error { return h.m.Preload(ctx, "/m/c.flac") }, "loadfile /m/c.flac append"},
		{"clear preload", func() error { return h.m.ClearPreload(ctx) }, "playlist-clear"},
		{"pause", func() error { return h.m.SetPause(ctx, true) }, "set_property pause true"},
		{"seek", func() error { return h.m.Seek(ctx, 90000) }, "seek 90 absolute+exact"},
		{"volume", func() error { return h.m.SetVolume(ctx, 35) }, "set_property volume 35"},
		{"stop", func() error { return h.m.Stop(ctx) }, "stop"},
	}
	for _, s := range steps {
		f.reset()
		if err := s.run(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if got := f.commands(); got != s.want {
			t.Errorf("%s sent %q, want %q", s.name, got, s.want)
		}
	}
}

func TestMPVNotConnectedBeforeRun(t *testing.T) {
	m := NewMPV(MPVOptions{})
	if err := m.SetVolume(context.Background(), 10); !errors.Is(err, ErrNotConnected) {
		t.Errorf("err = %v", err)
	}
}

func TestMPVForwardsEventsAndRestartsWhenItDies(t *testing.T) {
	h := startMPV(t, false)
	h.waitRestartEvent(t)
	f := <-h.fakes
	f.push(`{"event":"end-file","reason":"eof"}`)
	select {
	case ev := <-h.m.Events():
		if ev.Kind != EvEnded || ev.Reason != ReasonEOF {
			t.Errorf("event = %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("event not forwarded")
	}

	// mpv crashes: a new process is started and the engine announces itself again
	p := <-h.procs
	p.Kill()
	h.waitRestartEvent(t)
	if h.starts.Load() != 2 {
		t.Errorf("starts = %d", h.starts.Load())
	}
	<-h.fakes
	if err := h.m.SetVolume(context.Background(), 20); err != nil {
		t.Errorf("commands should work again after the restart: %v", err)
	}
}

func TestMPVBacksOffWhenStartFails(t *testing.T) {
	var starts atomic.Int32
	m := NewMPV(MPVOptions{
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		MinBackoff: 2 * time.Millisecond,
		Start: func(context.Context) (process, error) {
			starts.Add(1)
			return nil, errors.New("mpv not installed")
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	time.Sleep(60 * time.Millisecond)
	cancel()
	<-done
	if n := starts.Load(); n < 2 || n > 20 {
		t.Errorf("start attempts = %d, want a few spaced out retries", n)
	}
}

// shorten makes the engine's patience small for a test and restores it afterwards.
func shorten(t *testing.T, command, load, retryEvery time.Duration, retries int) {
	t.Helper()
	oc, ol, oe, or := commandTimeout, loadWait, seekRetryEvery, seekRetries
	commandTimeout, loadWait, seekRetryEvery, seekRetries = command, load, retryEvery, retries
	t.Cleanup(func() { commandTimeout, loadWait, seekRetryEvery, seekRetries = oc, ol, oe, or })
}

// The bug: dragging the progress bar right after a track change made mpv answer
// "error running command" because the file was still opening, and the API returned 500.
func TestSeekRightAfterLoadWaitsForTheFileToOpen(t *testing.T) {
	shorten(t, 2*time.Second, 2*time.Second, 100*time.Millisecond, 0) // no retries: only the wait can save it
	h := startMPVWith(t, true, func(f *fakeMPV) { f.loadDelay = 250 * time.Millisecond })
	h.waitRestartEvent(t)
	f := <-h.fakes
	ctx := context.Background()

	if err := h.m.Load(ctx, "/m/a.flac", 0, false); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := h.m.Seek(ctx, 30000); err != nil { // immediately, while the file is still opening
		t.Fatalf("seek right after load: %v", err)
	}
	if waited := time.Since(start); waited < 150*time.Millisecond {
		t.Errorf("seek did not wait for the file (%v)", waited)
	}
	if !strings.HasSuffix(f.commands(), "seek 30 absolute+exact") {
		t.Errorf("commands = %q", f.commands())
	}

	// once the file is open there is no waiting at all
	start = time.Now()
	if err := h.m.Seek(ctx, 40000); err != nil || time.Since(start) > 100*time.Millisecond {
		t.Errorf("second seek: %v after %v", err, time.Since(start))
	}
}

func TestSeekRetriesWhileMPVSaysNotReady(t *testing.T) {
	shorten(t, 2*time.Second, time.Second, 10*time.Millisecond, 5)
	h := startMPVWith(t, true, func(f *fakeMPV) { f.seekFailures.Store(3) })
	h.waitRestartEvent(t)
	f := <-h.fakes
	if err := h.m.Seek(context.Background(), 5000); err != nil {
		t.Fatalf("seek should succeed on the 4th attempt: %v", err)
	}
	if n := strings.Count(f.commands(), "seek 5 absolute+exact"); n != 4 {
		t.Errorf("seek attempts = %d, want 4", n)
	}
}

func TestSeekGivesUpWithABusyError(t *testing.T) {
	shorten(t, 2*time.Second, time.Second, 5*time.Millisecond, 3)
	h := startMPVWith(t, true, func(f *fakeMPV) { f.seekFailures.Store(1000) })
	h.waitRestartEvent(t)
	err := h.m.Seek(context.Background(), 5000)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestLoadWithStartPositionSeeksAfterTheFileOpens(t *testing.T) {
	shorten(t, 2*time.Second, 2*time.Second, 50*time.Millisecond, 0)
	h := startMPVWith(t, true, func(f *fakeMPV) { f.loadDelay = 150 * time.Millisecond })
	h.waitRestartEvent(t)
	f := <-h.fakes
	if err := h.m.Load(context.Background(), "/m/a.flac", 12500, true); err != nil {
		t.Fatalf("load with start position: %v", err)
	}
	if !strings.HasSuffix(f.commands(), "loadfile /m/a.flac replace | seek 12.5 absolute+exact") {
		t.Errorf("commands = %q", f.commands())
	}
}

func TestLoadDoesNotWaitForeverIfMPVNeverAnnouncesTheFile(t *testing.T) {
	shorten(t, 2*time.Second, 80*time.Millisecond, 10*time.Millisecond, 1)
	h := startMPV(t, false) // never sends file-loaded; the fake accepts the seek
	h.waitRestartEvent(t)
	start := time.Now()
	if err := h.m.Load(context.Background(), "/m/a.flac", 5000, false); err != nil {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("load took %v", time.Since(start))
	}
}

// A call that mpv never answers must come back (as "busy") instead of freezing the player.
func TestUnansweredCommandFailsFastAsBusy(t *testing.T) {
	shorten(t, 80*time.Millisecond, time.Second, 10*time.Millisecond, 0)
	c, _, _ := newPipeConn(t)
	start := time.Now()
	_, err := c.command(context.Background(), "silent") // background ctx: only our own timeout can end this
	if !errors.Is(err, ErrBusy) || time.Since(start) > time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
	// a caller that cancels is not "busy"
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	if _, err := c.command(ctx, "silent"); !errors.Is(err, context.Canceled) || errors.Is(err, ErrBusy) {
		t.Errorf("cancelled call: %v", err)
	}
	// and the connection still works afterwards
	if _, err := c.command(context.Background(), "set_property", "volume", 1); err != nil {
		t.Errorf("connection unusable after a timeout: %v", err)
	}
}
