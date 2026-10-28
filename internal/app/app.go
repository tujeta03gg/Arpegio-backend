// Package app wires the services together and runs the daemon.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"arpegio/internal/api"
	"arpegio/internal/catalog"
	"arpegio/internal/config"
	"arpegio/internal/events"
	"arpegio/internal/history"
	"arpegio/internal/jobs"
	"arpegio/internal/library"
	"arpegio/internal/lyrics"
	"arpegio/internal/metadata"
	"arpegio/internal/playback"
	"arpegio/internal/playlists"
	"arpegio/internal/queue"
	"arpegio/internal/radio"
	"arpegio/internal/store"
)

// Paths are the XDG locations the daemon uses.
type Paths struct {
	Config, DB, ArtCache, Log, Socket, MPVSocket string
}

// DefaultPaths follows the XDG spec with the usual fallbacks under home.
func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("app: home dir: %w", err)
	}
	xdg := func(env, fallback string) string {
		if v := os.Getenv(env); v != "" {
			return v
		}
		return filepath.Join(home, fallback)
	}
	p := Paths{
		Config:   filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), "arpegio", "config.toml"),
		DB:       filepath.Join(xdg("XDG_DATA_HOME", ".local/share"), "arpegio", "arpegio.db"),
		ArtCache: filepath.Join(xdg("XDG_CACHE_HOME", ".cache"), "arpegio", "art"),
		Log:      filepath.Join(xdg("XDG_STATE_HOME", ".local/state"), "arpegio", "arpegiod.log"),
	}
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		p.Socket = filepath.Join(rt, "arpegio.sock")
		p.MPVSocket = filepath.Join(rt, "arpegio-mpv.sock")
	}
	return p, nil
}

type App struct {
	Cfg     config.Config
	Paths   Paths
	Log     *slog.Logger
	Store   *store.Store
	Bus     *events.Bus
	Catalog *catalog.Service
	Library *library.Service
	Queue   *queue.Service
	History *history.Service
	Lists   *playlists.Service // playlists and likes
	Jobs    *jobs.Service
	Lyrics  *lyrics.Service
	Radio   *radio.Service
	Player  *playback.Service
	API     *api.Server

	mpv     *playback.MPV    // nil when mpv is not installed
	watcher *library.Watcher // nil when library.watch is off
}

// NewLogger writes text to console and JSON to the log file (when path is set).
func NewLogger(console io.Writer, logPath string) (*slog.Logger, func(), error) {
	h := slog.NewTextHandler(console, nil)
	if logPath == "" {
		return slog.New(h), func() {}, nil
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, nil, fmt.Errorf("app: log dir: %w", err)
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("app: open log: %w", err)
	}
	return slog.New(multiHandler{h, slog.NewJSONHandler(f, nil)}), func() { f.Close() }, nil
}

type multiHandler []slog.Handler

func (m multiHandler) Enabled(ctx context.Context, l slog.Level) bool { return m[0].Enabled(ctx, l) }
func (m multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range m {
		_ = h.Handle(ctx, r.Clone())
	}
	return nil
}
func (m multiHandler) WithAttrs(a []slog.Attr) slog.Handler {
	out := make(multiHandler, len(m))
	for i, h := range m {
		out[i] = h.WithAttrs(a)
	}
	return out
}
func (m multiHandler) WithGroup(n string) slog.Handler {
	out := make(multiHandler, len(m))
	for i, h := range m {
		out[i] = h.WithGroup(n)
	}
	return out
}

// New builds every service. dbPath may be ":memory:" for tests.
func New(ctx context.Context, cfg config.Config, paths Paths, log *slog.Logger) (*App, error) {
	if paths.DB != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(paths.DB), 0o755); err != nil {
			return nil, fmt.Errorf("app: data dir: %w", err)
		}
	}
	st, err := store.Open(ctx, paths.DB)
	if err != nil {
		return nil, err
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		log.Warn("ffprobe not found: new files cannot be ingested until ffmpeg is installed")
	}
	bus := events.New()
	cat := catalog.New(st, bus)
	lib := library.New(st, bus, cat, metadata.NewReader(), paths.ArtCache, log)
	a := &App{Cfg: cfg, Paths: paths, Log: log, Store: st, Bus: bus, Catalog: cat, Library: lib}
	a.Queue = queue.New(st, bus)
	a.Lists = playlists.New(st, bus, cat.Exists)
	a.Jobs = jobs.New(st, bus, log)
	a.Lyrics = lyrics.New(st)
	lib.SetTrackHook(a.Lyrics.Attach)
	a.Radio = radio.New(st)
	a.Radio.Queued = func(ctx context.Context) ([]string, error) {
		qs, err := a.Queue.State(ctx)
		ids := make([]string, len(qs.Items))
		for i, it := range qs.Items {
			ids[i] = it.TrackID
		}
		return ids, err
	}
	a.Jobs.Handle("ingest_file", func(ctx context.Context, p json.RawMessage) error {
		var in struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(p, &in); err != nil {
			return err
		}
		return lib.IngestPath(ctx, in.Path)
	})
	enqueueIngest := func(ctx context.Context, path string) error {
		return a.Jobs.Enqueue(ctx, "ingest_file", map[string]string{"path": path})
	}
	lib.SetEnqueuer(enqueueIngest)
	lib.SetImportDir(cfg.Library.ImportDir)
	if cfg.Library.Watch {
		a.watcher = library.NewWatcher(lib, enqueueIngest, library.DefaultSettle)
	}
	a.History = history.New(st, history.Rule{Seconds: cfg.History.MinListenSeconds, Percent: cfg.History.MinListenPercent})

	var engine playback.Engine = idleEngine{}
	if bin, err := exec.LookPath("mpv"); err != nil {
		log.Warn("mpv not found: playback commands will answer 503 until it is installed")
	} else {
		a.mpv = playback.NewMPV(playback.MPVOptions{Binary: bin, Socket: paths.MPVSocket, Args: mpvArgs(cfg.Playback, log), Log: log})
		engine = a.mpv
	}
	a.Player = playback.New(playback.Deps{
		Engine: engine, Queue: a.Queue, Tracks: trackSource{cat, lib}, History: a.History, Radio: a.Radio,
		Contexts: a.contexts(), Store: st, Bus: bus, Output: cfg.Playback.AudioOutput, Log: log,
	})
	a.API = api.New(api.Deps{Ctx: ctx, Catalog: cat, Library: lib, Player: a.Player, Queue: a.Queue, Events: bus, Playlists: a.Lists, History: a.History, Jobs: a.Jobs, Lyrics: a.Lyrics, Log: log})

	for _, root := range cfg.Library.Roots {
		if _, err := lib.AddRoot(ctx, root); err != nil {
			log.Warn("config root skipped", slog.String("path", root), slog.Any("err", err))
		}
	}
	return a, nil
}

// Serve runs the player, mpv and the HTTP API until ctx is cancelled, then waits for them to stop.
func (a *App) Serve(ctx context.Context, listeners ...net.Listener) error {
	var wg sync.WaitGroup
	defer wg.Wait()
	bg, stopBG := context.WithCancel(ctx)
	defer stopBG()
	if err := a.Player.Restore(bg); err != nil {
		a.Log.Warn("could not restore player state", slog.Any("err", err))
	}
	if a.mpv != nil {
		wg.Add(1)
		go func() { defer wg.Done(); a.mpv.Run(bg) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); a.Player.Run(bg) }()
	wg.Add(1)
	go func() { defer wg.Done(); a.Jobs.Run(bg) }()
	if a.watcher != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.watcher.Run(bg); err != nil {
				a.Log.Error("folder watcher stopped", slog.Any("err", err))
			}
		}()
	}
	wg.Add(1)
	go func() { // catch up on whatever changed while the daemon was off
		defer wg.Done()
		if _, err := a.Library.Scan(bg, ""); err != nil && bg.Err() == nil {
			a.Log.Warn("startup scan", slog.Any("err", err))
		}
	}()

	srv := &http.Server{Handler: a.API.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errs := make(chan error, len(listeners))
	for _, l := range listeners {
		go func() { errs <- srv.Serve(l) }()
	}
	select {
	case err := <-errs:
		return fmt.Errorf("app: serve: %w", err)
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("app: shutdown: %w", err)
	}
	return nil
}

// Listeners opens the TCP address and, when enabled and available, the Unix socket.
func (a *App) Listeners() ([]net.Listener, error) {
	var out []net.Listener
	if a.Cfg.API.Listen != "" {
		l, err := net.Listen("tcp", a.Cfg.API.Listen)
		if err != nil {
			return nil, fmt.Errorf("app: listen %s: %w", a.Cfg.API.Listen, err)
		}
		out = append(out, l)
	}
	if a.Cfg.API.Socket && a.Paths.Socket != "" {
		_ = os.Remove(a.Paths.Socket) // stale socket from a previous run
		l, err := net.Listen("unix", a.Paths.Socket)
		if err != nil {
			for _, o := range out {
				o.Close()
			}
			return nil, fmt.Errorf("app: listen %s: %w", a.Paths.Socket, err)
		}
		out = append(out, l)
	}
	if len(out) == 0 {
		return nil, errors.New("app: no listener configured")
	}
	return out, nil
}

func (a *App) Close() error { return a.Store.Close() }

// trackSource gives playback the catalog metadata and the library file paths.
type trackSource struct {
	cat *catalog.Service
	lib *library.Service
}

func (t trackSource) Track(ctx context.Context, id string) (catalog.Track, error) {
	return t.cat.Track(ctx, id)
}
func (t trackSource) TrackPath(ctx context.Context, id string) (string, error) {
	return t.lib.TrackPath(ctx, id)
}

// contexts lists what "play <kind>:<id>" can start.
func (a *App) contexts() *playback.Resolver {
	ids := func(ts []catalog.Track) []string {
		out := make([]string, len(ts))
		for i, t := range ts {
			out[i] = t.ID
		}
		return out
	}
	return &playback.Resolver{Sources: map[string]func(context.Context, string) ([]string, error){
		"track": func(ctx context.Context, id string) ([]string, error) {
			t, err := a.Catalog.Track(ctx, id)
			return []string{t.ID}, err
		},
		"album": func(ctx context.Context, id string) ([]string, error) {
			al, err := a.Catalog.Album(ctx, id)
			return ids(al.Tracks), err
		},
		"artist": func(ctx context.Context, id string) ([]string, error) {
			ar, err := a.Catalog.Artist(ctx, id)
			return ids(ar.Tracks), err
		},
		"genre": func(ctx context.Context, id string) ([]string, error) {
			ts, err := a.Catalog.GenreTracks(ctx, id)
			return ids(ts), err
		},
		"playlist": func(ctx context.Context, id string) ([]string, error) { return a.Lists.TrackIDs(ctx, id) },
		"likes":    func(ctx context.Context, _ string) ([]string, error) { return a.Lists.LikedTrackIDs(ctx) },
		"radio":    func(ctx context.Context, seed string) ([]string, error) { return a.Radio.Start(ctx, seed) },
	}}
}

// mpvArgs turns the [playback] config into mpv options.
func mpvArgs(p config.Playback, log *slog.Logger) []string {
	args := []string{"--ao=" + p.AudioOutput}
	if p.Gapless {
		args = append(args, "--gapless-audio=yes")
	} else {
		args = append(args, "--gapless-audio=no")
	}
	rg := map[string]string{"off": "no", "track": "track", "album": "album"}[p.ReplayGain]
	args = append(args, "--replaygain="+rg)
	if p.CrossfadeSeconds > 0 {
		log.Warn("playback.crossfade_seconds is ignored: mpv has no crossfade")
	}
	return args
}

// idleEngine stands in when mpv is missing so the API can still serve the catalog.
type idleEngine struct{}

func (idleEngine) Load(context.Context, string, int64, bool) error { return playback.ErrNotConnected }
func (idleEngine) Preload(context.Context, string) error           { return playback.ErrNotConnected }
func (idleEngine) ClearPreload(context.Context) error              { return playback.ErrNotConnected }
func (idleEngine) SetPause(context.Context, bool) error            { return playback.ErrNotConnected }
func (idleEngine) Seek(context.Context, int64) error               { return playback.ErrNotConnected }
func (idleEngine) SetVolume(context.Context, int) error            { return playback.ErrNotConnected }
func (idleEngine) Stop(context.Context) error                      { return playback.ErrNotConnected }
func (idleEngine) Events() <-chan playback.EngineEvent             { return nil }
