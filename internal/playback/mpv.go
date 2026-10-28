package playback

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"os/exec"
	"sync"
	"time"
)

// ErrNotConnected means mpv is not running (yet, or it is being restarted).
var ErrNotConnected = errors.New("playback: mpv is not connected")

// ---- JSON IPC connection ----

type response struct {
	Err  string          `json:"error"`
	Data json.RawMessage `json:"data"`
}

type message struct {
	RequestID int             `json:"request_id"`
	Err       string          `json:"error"`
	Data      json.RawMessage `json:"data"`
	Event     string          `json:"event"`
	Reason    string          `json:"reason"`
	Name      string          `json:"name"`
}

// conn speaks mpv's line-based JSON IPC over one socket connection.
type conn struct {
	nc      net.Conn
	out     chan EngineEvent
	done    chan struct{} // closed when the reader stops
	wmu     sync.Mutex
	mu      sync.Mutex
	nextID  int
	pending map[int]chan response
	waiters map[string][]chan struct{}
}

func newConn(nc net.Conn, out chan EngineEvent) *conn {
	c := &conn{nc: nc, out: out, done: make(chan struct{}), pending: map[int]chan response{}, waiters: map[string][]chan struct{}{}}
	go c.read()
	return c
}

func (c *conn) read() {
	defer close(c.done)
	sc := bufio.NewScanner(c.nc)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var m message
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch {
		case m.Event != "":
			c.onEvent(m)
		case m.RequestID != 0:
			c.mu.Lock()
			ch := c.pending[m.RequestID]
			delete(c.pending, m.RequestID)
			c.mu.Unlock()
			if ch != nil {
				ch <- response{Err: m.Err, Data: m.Data}
			}
		}
	}
	c.mu.Lock()
	for id, ch := range c.pending { // fail everything still waiting
		ch <- response{Err: "connection closed"}
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

func (c *conn) onEvent(m message) {
	c.mu.Lock()
	for _, w := range c.waiters[m.Event] {
		close(w)
	}
	delete(c.waiters, m.Event)
	c.mu.Unlock()

	switch m.Event {
	case "end-file":
		reason := ReasonStop
		switch m.Reason {
		case "eof":
			reason = ReasonEOF
		case "error":
			reason = ReasonError
		}
		c.send(EngineEvent{Kind: EvEnded, Reason: reason})
	case "property-change":
		var secs *float64
		if m.Name == "time-pos" && json.Unmarshal(m.Data, &secs) == nil && secs != nil {
			c.send(EngineEvent{Kind: EvPosition, PositionMS: int64(math.Round(*secs * 1000))})
		}
	}
}

// send never blocks the reader: position ticks are droppable, and the service drains quickly.
func (c *conn) send(ev EngineEvent) {
	select {
	case c.out <- ev:
	default:
		if ev.Kind == EvEnded { // an end must not be lost; make room by dropping a position tick
			select {
			case <-c.out:
			default:
			}
			select {
			case c.out <- ev:
			default:
			}
		}
	}
}

// expect returns a channel closed when the named event arrives next.
func (c *conn) expect(event string) <-chan struct{} {
	ch := make(chan struct{})
	c.mu.Lock()
	c.waiters[event] = append(c.waiters[event], ch)
	c.mu.Unlock()
	return ch
}

// command sends one mpv command and waits for its answer.
func (c *conn) command(ctx context.Context, args ...any) (json.RawMessage, error) {
	ch := make(chan response, 1)
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.pending[id] = ch
	c.mu.Unlock()

	line, err := json.Marshal(map[string]any{"command": args, "request_id": id})
	if err != nil {
		return nil, fmt.Errorf("mpv: encode %v: %w", args, err)
	}
	c.wmu.Lock()
	_, err = c.nc.Write(append(line, '\n'))
	c.wmu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("mpv: write %v: %w", args[0], err)
	}
	select {
	case r := <-ch:
		if r.Err != "success" {
			return nil, fmt.Errorf("mpv: %v: %s", args[0], r.Err)
		}
		return r.Data, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("mpv: %v: %w", args[0], ctx.Err())
	}
}

func (c *conn) close() { c.nc.Close() }

// ---- supervised mpv engine ----

type process interface {
	Wait() error
	Kill()
}

// MPVOptions configure the real engine. Start and Dial can be replaced in tests.
type MPVOptions struct {
	Binary     string   // default "mpv"
	Socket     string   // IPC socket path
	Args       []string // extra mpv arguments (audio device, replaygain, ...)
	Log        *slog.Logger
	Start      func(ctx context.Context) (process, error)
	Dial       func(ctx context.Context) (net.Conn, error)
	MinBackoff time.Duration // restart delay, doubles up to 10s (default 500ms)
}

// MPV runs mpv as a child process, talks to it over IPC and restarts it if it dies.
type MPV struct {
	opts   MPVOptions
	events chan EngineEvent
	mu     sync.Mutex
	c      *conn
}

func NewMPV(o MPVOptions) *MPV {
	if o.Binary == "" {
		o.Binary = "mpv"
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.MinBackoff == 0 {
		o.MinBackoff = 500 * time.Millisecond
	}
	m := &MPV{opts: o, events: make(chan EngineEvent, 64)}
	if o.Start == nil {
		m.opts.Start = m.startProcess
	}
	if o.Dial == nil {
		m.opts.Dial = func(ctx context.Context) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", o.Socket)
		}
	}
	return m
}

type execProcess struct{ cmd *exec.Cmd }

func (p execProcess) Wait() error { return p.cmd.Wait() }
func (p execProcess) Kill()       { _ = p.cmd.Process.Kill() }

func (m *MPV) startProcess(ctx context.Context) (process, error) {
	args := append([]string{"--idle=yes", "--no-video", "--no-terminal", "--input-ipc-server=" + m.opts.Socket}, m.opts.Args...)
	cmd := exec.CommandContext(ctx, m.opts.Binary, args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mpv: start %s: %w", m.opts.Binary, err)
	}
	return execProcess{cmd}, nil
}

func (m *MPV) Events() <-chan EngineEvent { return m.events }

// Run keeps mpv alive until ctx ends. After every restart it emits EvRestarted so the
// player can put the current track back.
func (m *MPV) Run(ctx context.Context) {
	backoff := m.opts.MinBackoff
	for ctx.Err() == nil {
		began := time.Now()
		err := m.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(began) > 30*time.Second {
			backoff = m.opts.MinBackoff
		}
		m.opts.Log.Warn("mpv stopped, restarting", slog.Any("err", err), slog.Duration("in", backoff))
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 10*time.Second)
	}
}

// session runs one mpv process until it or its connection dies.
func (m *MPV) session(ctx context.Context) error {
	proc, err := m.opts.Start(ctx)
	if err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- proc.Wait() }()
	defer proc.Kill()

	nc, err := m.dialRetry(ctx, exited)
	if err != nil {
		return err
	}
	c := newConn(nc, m.events)
	defer c.close()
	if _, err := c.command(ctx, "observe_property", 1, "time-pos"); err != nil {
		return err
	}
	m.mu.Lock()
	m.c = c
	m.mu.Unlock()
	m.events <- EngineEvent{Kind: EvRestarted}
	defer func() {
		m.mu.Lock()
		m.c = nil
		m.mu.Unlock()
	}()

	select {
	case err := <-exited:
		return fmt.Errorf("mpv exited: %w", errOrNil(err))
	case <-c.done:
		return errors.New("mpv connection lost")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func errOrNil(err error) error {
	if err == nil {
		return errors.New("exit status 0")
	}
	return err
}

// dialRetry waits for mpv to create its socket.
func (m *MPV) dialRetry(ctx context.Context, exited <-chan error) (net.Conn, error) {
	deadline := time.After(10 * time.Second)
	for {
		nc, err := m.opts.Dial(ctx)
		if err == nil {
			return nc, nil
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case err := <-exited:
			return nil, fmt.Errorf("mpv exited before opening its socket: %w", errOrNil(err))
		case <-deadline:
			return nil, fmt.Errorf("mpv socket never appeared: %w", err)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (m *MPV) conn() (*conn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.c == nil {
		return nil, ErrNotConnected
	}
	return m.c, nil
}

func (m *MPV) cmd(ctx context.Context, args ...any) error {
	c, err := m.conn()
	if err != nil {
		return err
	}
	_, err = c.command(ctx, args...)
	return err
}

// ---- Engine ----

func (m *MPV) Load(ctx context.Context, path string, startMS int64, paused bool) error {
	c, err := m.conn()
	if err != nil {
		return err
	}
	if _, err := c.command(ctx, "set_property", "pause", paused); err != nil {
		return err
	}
	var loaded <-chan struct{}
	if startMS > 0 {
		loaded = c.expect("file-loaded")
	}
	if _, err := c.command(ctx, "loadfile", path, "replace"); err != nil {
		return err
	}
	if startMS > 0 { // loadfile options differ between mpv versions, so seek once the file is open
		select {
		case <-loaded:
		case <-time.After(3 * time.Second):
			return errors.New("mpv: file did not open in time")
		case <-ctx.Done():
			return ctx.Err()
		}
		return m.Seek(ctx, startMS)
	}
	return nil
}

func (m *MPV) Preload(ctx context.Context, path string) error {
	return m.cmd(ctx, "loadfile", path, "append")
}

func (m *MPV) ClearPreload(ctx context.Context) error { return m.cmd(ctx, "playlist-clear") }

func (m *MPV) SetPause(ctx context.Context, paused bool) error {
	return m.cmd(ctx, "set_property", "pause", paused)
}

func (m *MPV) Seek(ctx context.Context, ms int64) error {
	return m.cmd(ctx, "seek", float64(ms)/1000, "absolute+exact")
}

func (m *MPV) SetVolume(ctx context.Context, level int) error {
	return m.cmd(ctx, "set_property", "volume", level)
}

func (m *MPV) Stop(ctx context.Context) error { return m.cmd(ctx, "stop") }
