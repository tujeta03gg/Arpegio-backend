package library

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"arpegio/internal/metadata"
)

// DefaultSettle is how long a file must keep the same size before it counts as fully copied.
const DefaultSettle = 2 * time.Second

// Enqueuer hands a file to the background worker (jobs.Service in the daemon).
type Enqueuer func(ctx context.Context, path string) error

// Watcher turns file system events in the library folders into ingest jobs and missing flags.
// It uses inotify through fsnotify, so it costs nothing while the folders are quiet.
type Watcher struct {
	lib     *Service
	enqueue Enqueuer
	settle  time.Duration
}

func NewWatcher(lib *Service, enqueue Enqueuer, settle time.Duration) *Watcher {
	if settle <= 0 {
		settle = DefaultSettle
	}
	return &Watcher{lib: lib, enqueue: enqueue, settle: settle}
}

type pending struct {
	size  int64
	timer *time.Timer
}

// Run watches every enabled root until ctx ends, following roots added or removed at runtime.
func (w *Watcher) Run(ctx context.Context) error {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("library: start watcher: %w", err)
	}
	defer fw.Close()
	roots, cancelRoots := w.lib.bus.Subscribe(8)
	defer cancelRoots()

	log := w.lib.log
	watched := map[string]bool{} // root path -> watched
	ready := make(chan string, 64)
	waiting := map[string]*pending{}
	defer func() {
		for _, p := range waiting {
			p.timer.Stop()
		}
	}()

	// check (re)starts the "wait until the size stops changing" timer for a file.
	check := func(path string, size int64) {
		p := waiting[path]
		if p == nil {
			p = &pending{}
			waiting[path] = p
		}
		p.size = size
		if p.timer != nil {
			p.timer.Stop()
		}
		p.timer = time.AfterFunc(w.settle, func() {
			select {
			case ready <- path:
			case <-ctx.Done():
			}
		})
	}
	schedule := func(path string) {
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			check(path, fi.Size())
		}
	}
	addTree := func(dir string) {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			switch {
			case d.IsDir():
				if err := fw.Add(p); err != nil {
					log.Warn("library: cannot watch folder (inotify limit?)", slog.String("path", p), slog.Any("err", err))
				}
			case metadata.IsAudio(p):
				schedule(p) // arrived before the watch was in place
			}
			return nil
		})
	}
	syncRoots := func() {
		list, err := w.lib.Roots(ctx)
		if err != nil {
			log.Error("library: list roots for watcher", slog.Any("err", err))
			return
		}
		active := map[string]bool{}
		for _, r := range list {
			if !r.Enabled {
				continue
			}
			active[r.Path] = true
			if !watched[r.Path] {
				watched[r.Path] = true
				addTree(filepath.FromSlash(r.Path))
			}
		}
		for path := range watched {
			if !active[path] {
				delete(watched, path)
				for _, wp := range fw.WatchList() {
					if strings.HasPrefix(filepath.ToSlash(wp), path) {
						_ = fw.Remove(wp)
					}
				}
			}
		}
	}
	syncRoots()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-roots:
			if ev.Type == "library.roots_changed" {
				syncRoots()
			}
		case err := <-fw.Errors:
			log.Warn("library: watcher error", slog.Any("err", err))
		case ev := <-fw.Events:
			w.onEvent(ctx, ev, fw, addTree, schedule, waiting)
		case path := <-ready:
			p := waiting[path]
			if p == nil {
				continue
			}
			fi, err := os.Stat(path)
			switch {
			case err != nil: // vanished while settling
				delete(waiting, path)
			case fi.Size() != p.size: // still being written
				check(path, fi.Size())
			default:
				delete(waiting, path)
				if err := w.enqueue(ctx, path); err != nil {
					log.Error("library: enqueue ingest", slog.String("path", path), slog.Any("err", err))
				}
			}
		}
	}
}

func (w *Watcher) onEvent(ctx context.Context, ev fsnotify.Event, fw *fsnotify.Watcher, addTree func(string), schedule func(string), waiting map[string]*pending) {
	path := ev.Name
	switch {
	case ev.Has(fsnotify.Create):
		if fi, err := os.Stat(path); err == nil && fi.IsDir() {
			addTree(path)
			return
		}
		fallthrough
	case ev.Has(fsnotify.Write):
		if metadata.IsAudio(path) {
			schedule(path)
		}
	case ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename):
		if p := waiting[path]; p != nil {
			p.timer.Stop()
			delete(waiting, path)
		}
		if metadata.IsAudio(path) {
			if err := w.lib.MarkMissing(ctx, path); err != nil {
				w.lib.log.Warn("library: mark missing", slog.String("path", path), slog.Any("err", err))
			}
		}
	}
}

// ponytail: a folder renamed or moved out of a watched tree only reports the folder itself,
// so its files stay "present" until the next scan (`arpegio scan`) flags them missing.
