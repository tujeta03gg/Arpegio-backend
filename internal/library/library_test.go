package library

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"arpegio/internal/apperr"
	"arpegio/internal/catalog"
	"arpegio/internal/events"
	"arpegio/internal/lyrics"
	"arpegio/internal/metadata"
	"arpegio/internal/store"
	"arpegio/internal/testutil"
)

type env struct {
	t        *testing.T
	svc      *Service
	cat      *catalog.Service
	bus      *events.Bus
	dir      string // music folder
	svcStore *store.Store
	root     Root
}

func probe(_ context.Context, path string) (metadata.Props, error) {
	if strings.Contains(path, "bad") {
		return metadata.Props{}, errors.New("corrupt file")
	}
	return metadata.Props{DurationMS: 1000}, nil
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
	cat := catalog.New(st, bus)
	svc := New(st, bus, cat, metadata.Reader{Probe: probe}, filepath.Join(t.TempDir(), "art"), nil)
	e := &env{t: t, svc: svc, cat: cat, bus: bus, dir: t.TempDir(), svcStore: st}
	e.root, err = svc.AddRoot(ctx, e.dir)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) song(rel, title, artist, album string) string {
	return testutil.WriteMP3(e.t, e.dir, rel, map[string]string{"TIT2": title, "TPE1": artist, "TALB": album}, title+artist)
}

func (e *env) scan() Result {
	e.t.Helper()
	res, err := e.svc.Scan(context.Background(), "")
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *env) titles() []string {
	e.t.Helper()
	p, err := e.cat.Tracks(context.Background(), "", 100)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, tr := range p.Items {
		out = append(out, tr.Title)
	}
	return out
}

func TestAddAndRemoveRoot(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	again, err := e.svc.AddRoot(ctx, e.dir)
	if err != nil || again.ID != e.root.ID {
		t.Fatalf("re-adding must return the same root: %+v %v", again, err)
	}
	if _, err := e.svc.AddRoot(ctx, filepath.Join(e.dir, "nope")); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("missing dir: %v", err)
	}
	file := e.song("a.mp3", "A", "X", "Y")
	if _, err := e.svc.AddRoot(ctx, file); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("file as root: %v", err)
	}

	e.scan()
	if err := e.svc.RemoveRoot(ctx, e.root.ID); err != nil {
		t.Fatal(err)
	}
	if got := e.titles(); len(got) != 0 {
		t.Errorf("removed root still lists %v", got)
	}
	if r, _ := e.svc.AddRoot(ctx, e.dir); !r.Enabled {
		t.Error("re-adding a removed root should enable it")
	}
	if got := e.scan(); got.Skipped+got.Added != 1 || len(e.titles()) != 1 {
		t.Errorf("files should come back after re-adding: %+v %v", got, e.titles())
	}
	if err := e.svc.RemoveRoot(ctx, "nope"); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("unknown root: %v", err)
	}
}

func TestScanAddsThenSkipsUnchanged(t *testing.T) {
	e := newEnv(t)
	ch, cancel := e.bus.Subscribe(64)
	defer cancel()
	e.song("rock/one.mp3", "One", "Band", "LP")
	e.song("two.mp3", "Two", "Band", "LP")
	os.WriteFile(filepath.Join(e.dir, "notes.txt"), []byte("ignore me"), 0o644)

	if r := e.scan(); r.Added != 2 || r.Failed != 0 {
		t.Fatalf("first scan = %+v", r)
	}
	if r := e.scan(); r.Added != 0 || r.Skipped != 2 {
		t.Fatalf("second scan = %+v", r)
	}
	types := map[string]int{}
	for len(ch) > 0 {
		types[(<-ch).Type]++
	}
	if types["library.file_added"] != 2 || types["library.scan_finished"] != 2 || types["catalog.updated"] != 1 {
		t.Errorf("events = %v", types)
	}
	if e.svc.Status().Scanning {
		t.Error("status should not be scanning after Scan returns")
	}
}

func TestRetaggedFileIsUpdatedInPlace(t *testing.T) {
	e := newEnv(t)
	e.song("a.mp3", "Before", "Band", "LP")
	e.scan()
	before, _ := e.cat.Tracks(context.Background(), "", 10)

	e.song("a.mp3", "After retag", "Band", "LP")
	if r := e.scan(); r.Updated != 1 || r.Added != 0 {
		t.Fatalf("scan = %+v", r)
	}
	after, _ := e.cat.Tracks(context.Background(), "", 10)
	if len(after.Items) != 1 || after.Items[0].ID != before.Items[0].ID || after.Items[0].Title != "After retag" {
		t.Fatalf("before=%+v after=%+v", before.Items, after.Items)
	}
}

func TestMovedFileKeepsItsTrack(t *testing.T) {
	e := newEnv(t)
	e.song("old/a.mp3", "Song", "Band", "LP")
	e.scan()
	before, _ := e.cat.Tracks(context.Background(), "", 10)

	os.MkdirAll(filepath.Join(e.dir, "new"), 0o755)
	if err := os.Rename(filepath.Join(e.dir, "old/a.mp3"), filepath.Join(e.dir, "new/b.mp3")); err != nil {
		t.Fatal(err)
	}
	r := e.scan()
	if r.Moved != 1 || r.Added != 0 || r.Missing != 0 {
		t.Fatalf("scan = %+v", r)
	}
	after, _ := e.cat.Tracks(context.Background(), "", 10)
	if len(after.Items) != 1 || after.Items[0].ID != before.Items[0].ID {
		t.Fatalf("track identity lost: before=%+v after=%+v", before.Items, after.Items)
	}
}

func TestDeletedFileIsMarkedMissingAndComesBack(t *testing.T) {
	e := newEnv(t)
	path := e.song("a.mp3", "Song", "Band", "LP")
	keep, _ := os.ReadFile(path)
	e.scan()
	first, _ := e.cat.Tracks(context.Background(), "", 10)
	trackID := first.Items[0].ID

	os.Remove(path)
	if r := e.scan(); r.Missing != 1 {
		t.Fatalf("scan = %+v", r)
	}
	if len(e.titles()) != 0 {
		t.Error("missing file must not be listed")
	}
	if _, err := e.cat.Track(context.Background(), trackID); err != nil {
		t.Errorf("track row must survive: %v", err)
	}
	if r := e.scan(); r.Missing != 0 { // already flagged: no repeated event
		t.Errorf("second scan = %+v", r)
	}

	os.WriteFile(path, keep, 0o644)
	e.scan()
	p, _ := e.cat.Tracks(context.Background(), "", 10)
	if len(p.Items) != 1 || p.Items[0].ID != trackID {
		t.Fatalf("file did not come back with its old track: %+v", p.Items)
	}
}

func TestBadFileDoesNotStopTheScan(t *testing.T) {
	e := newEnv(t)
	e.song("bad.mp3", "Bad", "X", "Y")
	e.song("good.mp3", "Good", "X", "Y")
	r := e.scan()
	if r.Added != 1 || r.Failed != 1 || len(r.Errors) != 1 || !strings.Contains(r.Errors[0], "bad.mp3") {
		t.Fatalf("scan = %+v", r)
	}
}

func TestScanErrors(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Scan(context.Background(), "nope"); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("unknown root: %v", err)
	}
	e.svc.scanMu.Lock()
	if _, err := e.svc.Scan(context.Background(), ""); !errors.Is(err, apperr.ErrConflict) {
		t.Errorf("concurrent scan: %v", err)
	}
	e.svc.scanMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.song("a.mp3", "A", "B", "C")
	if _, err := e.svc.Scan(ctx, ""); err == nil {
		t.Error("cancelled context should abort the scan")
	}
}

func TestIngestPathAndMarkMissing(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	path := e.song("new.mp3", "Fresh", "Band", "LP")

	if err := e.svc.IngestPath(ctx, path); err != nil {
		t.Fatal(err)
	}
	if got := e.titles(); len(got) != 1 || got[0] != "Fresh" {
		t.Fatalf("titles = %v", got)
	}
	if err := e.svc.IngestPath(ctx, path); err != nil || len(e.titles()) != 1 {
		t.Errorf("re-ingest must be idempotent: %v %v", err, e.titles())
	}
	outside := testutil.WriteMP3(t, t.TempDir(), "x.mp3", map[string]string{"TIT2": "x"}, "x")
	if err := e.svc.IngestPath(ctx, outside); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("outside root: %v", err)
	}

	if err := e.svc.MarkMissing(ctx, path); err != nil || len(e.titles()) != 0 {
		t.Errorf("MarkMissing: %v titles=%v", err, e.titles())
	}
	if err := e.svc.MarkMissing(ctx, filepath.Join(e.dir, "never.mp3")); err != nil {
		t.Errorf("unknown file should be a no-op: %v", err)
	}
}

func pngBytes(w, h int) []byte {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)))
	return b.Bytes()
}

func TestArtworkFromFolderCoverAndThumbnails(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.song("alb/a.mp3", "A", "Band", "LP")
	e.song("alb/b.mp3", "B", "Band", "LP")
	os.WriteFile(filepath.Join(e.dir, "alb", "Cover.PNG"), pngBytes(800, 400), 0o644)
	e.scan()

	albums, _ := e.cat.Albums(ctx, "", 10)
	if len(albums.Items) != 1 || albums.Items[0].ArtworkID == "" {
		t.Fatalf("album has no artwork: %+v", albums.Items)
	}
	id := albums.Items[0].ArtworkID

	orig, err := e.svc.Artwork(ctx, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(orig)); len(entries) != 1 {
		t.Errorf("same cover must be stored once, found %d files", len(entries))
	}
	thumb, err := e.svc.Artwork(ctx, id, 300)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(thumb)
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || cfg.Width != 300 || cfg.Height != 150 {
		t.Errorf("thumbnail = %+v err=%v", cfg, err)
	}
	if again, _ := e.svc.Artwork(ctx, id, 300); again != thumb {
		t.Error("thumbnail should be cached")
	}
	if _, err := e.svc.Artwork(ctx, id, 123); !errors.Is(err, apperr.ErrInvalid) {
		t.Errorf("bad size: %v", err)
	}
	if _, err := e.svc.Artwork(ctx, "nope", 0); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("unknown artwork: %v", err)
	}
}

func TestHashFileIsContentBased(t *testing.T) {
	dir := t.TempDir()
	a, b, c := filepath.Join(dir, "a"), filepath.Join(dir, "b"), filepath.Join(dir, "c")
	os.WriteFile(a, []byte("same"), 0o644)
	os.WriteFile(b, []byte("same"), 0o644)
	os.WriteFile(c, []byte("different"), 0o644)
	ha, _ := hashFile(a)
	hb, _ := hashFile(b)
	hc, _ := hashFile(c)
	if ha != hb || ha == hc || len(ha) != 16 {
		t.Fatalf("hashes: %s %s %s", ha, hb, hc)
	}
	if _, err := hashFile(filepath.Join(dir, "missing")); err == nil {
		t.Error("missing file should fail")
	}
}

func TestTrackHookRunsInsideIngest(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	lyr := lyrics.New(e.svcStore)
	e.svc.SetTrackHook(lyr.Attach)
	path := e.song("a/song.mp3", "Song", "Band", "LP")
	os.WriteFile(strings.TrimSuffix(path, ".mp3")+".lrc", []byte("[00:01.00]Hello"), 0o644)
	e.song("a/other.mp3", "Other", "Band", "LP")

	e.scan()
	p, _ := e.cat.Tracks(ctx, "", 10)
	withLyrics := 0
	for _, tr := range p.Items {
		if l, err := lyr.Get(ctx, tr.ID); err == nil && l.Synced && l.Lines[0].Text == "Hello" {
			withLyrics++
			if tr.Title != "Song" {
				t.Errorf("lyrics attached to %q", tr.Title)
			}
		}
	}
	if withLyrics != 1 {
		t.Errorf("tracks with lyrics = %d, want 1", withLyrics)
	}

	// a failing hook rolls the whole file back instead of leaving a half-ingested track
	e.svc.SetTrackHook(func(context.Context, *sql.Tx, string, string) error { return errors.New("hook failed") })
	e.song("a/third.mp3", "Third", "Band", "LP")
	if r := e.scan(); r.Failed != 1 || r.Added != 0 {
		t.Errorf("scan = %+v", r)
	}
	if got := e.titles(); len(got) != 2 {
		t.Errorf("titles = %v", got)
	}
}
