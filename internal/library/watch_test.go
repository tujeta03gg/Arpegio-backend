package library

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"arpegio/internal/apperr"
	"arpegio/internal/testutil"
)

type capture struct {
	mu    sync.Mutex
	paths []string
	ch    chan string
}

func newCapture() *capture { return &capture{ch: make(chan string, 32)} }

func (c *capture) enqueue(_ context.Context, path string) error {
	c.mu.Lock()
	c.paths = append(c.paths, path)
	c.mu.Unlock()
	c.ch <- filepath.ToSlash(path)
	return nil
}

func (c *capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.paths)
}

// next waits for the next enqueued path.
func (c *capture) next(t *testing.T) string {
	t.Helper()
	select {
	case p := <-c.ch:
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was enqueued")
		return ""
	}
}

func startWatcher(t *testing.T, e *env, c *capture) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	w := NewWatcher(e.svc, c.enqueue, 80*time.Millisecond)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("watcher: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("watcher did not stop")
		}
	})
	time.Sleep(100 * time.Millisecond) // let the watches be registered
}

func TestWatcherEnqueuesNewFilesOnceTheyAreComplete(t *testing.T) {
	e := newEnv(t)
	c := newCapture()
	startWatcher(t, e, c)

	path := filepath.Join(e.dir, "new.mp3")
	f, _ := os.Create(path)
	for i := 0; i < 4; i++ { // a slow copy: the size keeps changing
		f.WriteString(strings.Repeat("x", 100))
		time.Sleep(30 * time.Millisecond)
	}
	f.Close()

	if got := c.next(t); got != filepath.ToSlash(path) {
		t.Fatalf("enqueued %q", got)
	}
	time.Sleep(300 * time.Millisecond)
	if n := c.count(); n != 1 {
		t.Errorf("enqueued %d times, want once", n)
	}
}

func TestWatcherIgnoresNonAudioAndPartFiles(t *testing.T) {
	e := newEnv(t)
	c := newCapture()
	startWatcher(t, e, c)
	os.WriteFile(filepath.Join(e.dir, "cover.jpg"), []byte("img"), 0o644)
	os.WriteFile(filepath.Join(e.dir, "notes.txt"), []byte("hi"), 0o644)
	os.WriteFile(filepath.Join(e.dir, ".import-1.part"), []byte("partial"), 0o644)
	time.Sleep(400 * time.Millisecond)
	if c.count() != 0 {
		t.Errorf("enqueued %v", c.paths)
	}
}

func TestWatcherFollowsNewSubfoldersAndFilesCopiedWithThem(t *testing.T) {
	e := newEnv(t)
	c := newCapture()
	startWatcher(t, e, c)

	// a whole album folder appears at once, files included
	album := filepath.Join(e.dir, "Artist", "Album")
	os.MkdirAll(album, 0o755)
	a := testutil.WriteMP3(t, album, "01.mp3", map[string]string{"TIT2": "One"}, "aaa")
	got := map[string]bool{c.next(t): true}

	// and files added later to that new folder are still seen
	b := testutil.WriteMP3(t, album, "02.mp3", map[string]string{"TIT2": "Two"}, "bbb")
	got[c.next(t)] = true
	if !got[filepath.ToSlash(a)] || !got[filepath.ToSlash(b)] {
		t.Errorf("enqueued = %v", got)
	}
}

func TestWatcherFlagsDeletedFilesMissing(t *testing.T) {
	e := newEnv(t)
	path := e.song("gone.mp3", "Gone", "Band", "LP")
	e.scan()
	if len(e.titles()) != 1 {
		t.Fatal("setup: track not indexed")
	}
	c := newCapture()
	startWatcher(t, e, c)

	os.Remove(path)
	deadline := time.Now().Add(5 * time.Second)
	for len(e.titles()) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("deleted file still listed")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestWatcherPicksUpRootsAddedWhileRunning(t *testing.T) {
	e := newEnv(t)
	c := newCapture()
	startWatcher(t, e, c)

	other := t.TempDir()
	if _, err := e.svc.AddRoot(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	path := testutil.WriteMP3(t, other, "late.mp3", map[string]string{"TIT2": "Late"}, "zzz")
	if got := c.next(t); got != filepath.ToSlash(path) {
		t.Errorf("enqueued %q", got)
	}
}

func TestWatcherStopsWatchingRemovedRoots(t *testing.T) {
	e := newEnv(t)
	c := newCapture()
	startWatcher(t, e, c)
	if err := e.svc.RemoveRoot(context.Background(), e.root.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	testutil.WriteMP3(t, e.dir, "after.mp3", map[string]string{"TIT2": "After"}, "q")
	time.Sleep(400 * time.Millisecond)
	if c.count() != 0 {
		t.Errorf("removed root still watched: %v", c.paths)
	}
}

// ---- import ----

func TestImportStoresFileAndIngestsIt(t *testing.T) {
	e := newEnv(t)
	imp := filepath.Join(e.dir, "_importados")
	e.svc.SetImportDir(imp)
	data := testutil.ID3(map[string]string{"TIT2": "Uploaded", "TPE1": "Someone"}, "audio")

	path, err := e.svc.Import(context.Background(), "../../evil/Uploaded Song.mp3", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != imp || filepath.Base(path) != "Uploaded Song.mp3" {
		t.Errorf("stored at %q (client path must be reduced to a base name)", path)
	}
	if got := e.titles(); len(got) != 1 || got[0] != "Uploaded" {
		t.Errorf("titles = %v (inline ingest expected without an enqueuer)", got)
	}
	if left, _ := filepath.Glob(filepath.Join(imp, ".import-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}

	// same name again does not overwrite
	again, err := e.svc.Import(context.Background(), "Uploaded Song.mp3", bytes.NewReader(append(data, 'x')))
	if err != nil || filepath.Base(again) != "Uploaded Song (2).mp3" {
		t.Errorf("second upload = %q %v", again, err)
	}
}

func TestImportUsesTheBackgroundQueueWhenAvailable(t *testing.T) {
	e := newEnv(t)
	e.svc.SetImportDir(filepath.Join(e.dir, "in"))
	c := newCapture()
	e.svc.SetEnqueuer(c.enqueue)
	path, err := e.svc.Import(context.Background(), "a.flac", strings.NewReader("not really flac"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.next(t); got != filepath.ToSlash(path) {
		t.Errorf("enqueued %q, stored %q", got, path)
	}
	if len(e.titles()) != 0 {
		t.Error("nothing should be ingested inline")
	}
}

func TestImportRejectsBadRequests(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	if _, err := e.svc.Import(ctx, "a.mp3", strings.NewReader("x")); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("no import dir: %v", err)
	}
	e.svc.SetImportDir(filepath.Join(e.dir, "in"))
	for _, name := range []string{"notes.txt", "", "...", "cover.jpg"} {
		if _, err := e.svc.Import(ctx, name, strings.NewReader("x")); !errors.Is(err, apperr.ErrInvalid) {
			t.Errorf("name %q: %v", name, err)
		}
	}
	e.svc.SetImportDir(t.TempDir()) // outside every root
	if _, err := e.svc.Import(ctx, "a.mp3", strings.NewReader("x")); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("outside roots: %v", err)
	}
	if entries, _ := os.ReadDir(e.dir); len(entries) != 0 {
		t.Errorf("rejected imports left files behind: %v", entries)
	}
}

func TestCleanFileNameAndUniquePath(t *testing.T) {
	for in, want := range map[string]string{
		"a.mp3": "a.mp3", "dir/a.mp3": "a.mp3", `C:\Music\a.mp3`: "a.mp3", "../../a.mp3": "a.mp3",
		".hidden.mp3": "hidden.mp3", "  spaced.mp3 ": "spaced.mp3", "": "", "dir/": "",
	} {
		if got := cleanFileName(in); got != want {
			t.Errorf("cleanFileName(%q) = %q, want %q", in, got, want)
		}
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.mp3"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "a (2).mp3"), nil, 0o644)
	if got := filepath.Base(uniquePath(dir, "a.mp3")); got != "a (3).mp3" {
		t.Errorf("uniquePath = %q", got)
	}
	if got := filepath.Base(uniquePath(dir, "b.mp3")); got != "b.mp3" {
		t.Errorf("uniquePath free = %q", got)
	}
}
