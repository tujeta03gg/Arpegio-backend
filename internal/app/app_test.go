package app

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"arpegio/internal/config"
)

func TestDefaultPathsHonorXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/cfg")
	t.Setenv("XDG_DATA_HOME", "/data")
	t.Setenv("XDG_CACHE_HOME", "/cache")
	t.Setenv("XDG_STATE_HOME", "/state")
	t.Setenv("XDG_RUNTIME_DIR", "/run")
	p, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		p.Config: "/cfg/arpegio/config.toml", p.DB: "/data/arpegio/arpegio.db", p.ArtCache: "/cache/arpegio/art",
		p.Log: "/state/arpegio/arpegiod.log", p.Socket: "/run/arpegio.sock", p.MPVSocket: "/run/arpegio-mpv.sock",
	}
	for got, exp := range want {
		if filepath.ToSlash(got) != filepath.ToSlash(filepath.Join(exp)) && !strings.HasSuffix(filepath.ToSlash(got), exp) {
			t.Errorf("path %q, want %q", got, exp)
		}
	}
}

func TestDefaultPathsWithoutRuntimeDirHaveNoSockets(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	p, _ := DefaultPaths()
	if p.Socket != "" || p.MPVSocket != "" {
		t.Errorf("sockets should be empty: %+v", p)
	}
}

func TestDaemonServesAndShutsDown(t *testing.T) {
	cfg := config.Default()
	cfg.API.Listen, cfg.API.Socket = "127.0.0.1:0", false
	cfg.Library.Roots = []string{t.TempDir(), filepath.Join(t.TempDir(), "missing")} // second is skipped with a warning
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := New(ctx, cfg, Paths{DB: ":memory:"}, log)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ls, err := a.Listeners()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx, ls...) }()

	res, err := http.Get("http://" + ls[0].Addr().String() + "/api/v1/library/roots")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status = %d", res.StatusCode)
	}
	roots, _ := a.Library.Roots(ctx)
	if len(roots) != 1 || !strings.Contains(logs.String(), "config root skipped") {
		t.Errorf("roots=%v logs=%s", roots, logs.String())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not stop after cancel")
	}
}

func TestListenersNeedsOne(t *testing.T) {
	cfg := config.Default()
	cfg.API.Listen, cfg.API.Socket = "", false
	a := &App{Cfg: cfg}
	if _, err := a.Listeners(); err == nil {
		t.Error("expected error with no listener configured")
	}
	cfg.API.Listen = "256.0.0.1:99999"
	if _, err := (&App{Cfg: cfg}).Listeners(); err == nil {
		t.Error("expected error for bad address")
	}
}

func TestNewLoggerWritesJSONFile(t *testing.T) {
	var console bytes.Buffer
	path := filepath.Join(t.TempDir(), "sub", "a.log")
	log, closeLog, err := NewLogger(&console, path)
	if err != nil {
		t.Fatal(err)
	}
	log.Info("hello", slog.String("track_id", "t1"))
	closeLog()
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"track_id":"t1"`) || !strings.Contains(console.String(), "hello") {
		t.Errorf("file=%q console=%q", data, console.String())
	}
}
