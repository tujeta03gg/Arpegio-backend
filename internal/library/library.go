// Package library owns the physical files: roots, scanning, hashing, moves and artwork.
package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cespare/xxhash/v2"

	"arpegio/internal/apperr"
	"arpegio/internal/catalog"
	"arpegio/internal/events"
	"arpegio/internal/metadata"
	"arpegio/internal/store"
	"arpegio/internal/store/db"
)

// Reader is the part of metadata.Reader the library uses.
type Reader interface {
	Read(ctx context.Context, path string) (metadata.Info, error)
}

type Service struct {
	st     *store.Store
	bus    *events.Bus
	cat    *catalog.Service
	reader Reader
	artDir string // where cover files are cached; "" disables artwork
	log    *slog.Logger

	afterTrack func(ctx context.Context, tx *sql.Tx, trackID, audioPath string) error // extra per-track ingest work (lyrics)

	importDir string   // where POST /library/import stores files; "" disables imports
	enqueue   Enqueuer // background ingest; nil means ingest inline

	scanMu            sync.Mutex
	scanning          atomic.Bool
	scanDone, scanTot atomic.Int64
}

func New(st *store.Store, bus *events.Bus, cat *catalog.Service, r Reader, artDir string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{st: st, bus: bus, cat: cat, reader: r, artDir: artDir, log: log}
}

type Root struct {
	ID, Path string
	Enabled  bool
	LastScan string
}

// Result summarizes one scan.
type Result struct {
	Added, Updated, Moved, Missing, Skipped, Failed int
	Errors                                          []string
}

// Status is the progress shown by GET /library/status.
type Status struct {
	Scanning    bool
	Done, Total int64
}

func (s *Service) Status() Status {
	return Status{Scanning: s.scanning.Load(), Done: s.scanDone.Load(), Total: s.scanTot.Load()}
}

// ---- roots ----

// AddRoot starts watching a folder. Re-adding a disabled root enables it again.
func (s *Service) AddRoot(ctx context.Context, path string) (Root, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Root{}, apperr.Invalid("library: bad path %q", path)
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return Root{}, apperr.Invalid("library: %s is not a directory", abs)
	}
	abs = filepath.ToSlash(abs)
	var root db.LibraryRoot
	err = s.st.Tx(ctx, func(tx *sql.Tx) error {
		q := db.New(tx)
		r, err := q.GetRootByPath(ctx, abs)
		switch {
		case err == nil:
			root = r
			if r.Enabled == 0 {
				root.Enabled = 1
				return q.SetRootEnabled(ctx, db.SetRootEnabledParams{Enabled: 1, ID: r.ID})
			}
			return nil
		case errors.Is(err, sql.ErrNoRows):
			root, err = q.InsertRoot(ctx, db.InsertRootParams{ID: store.NewID(), Path: abs})
		}
		return err
	})
	if err != nil {
		return Root{}, fmt.Errorf("library: add root %s: %w", abs, err)
	}
	s.bus.Publish(events.Event{Type: "library.roots_changed"})
	return toRoot(root), nil
}

func (s *Service) Roots(ctx context.Context) ([]Root, error) {
	rows, err := db.New(s.st.R).ListRoots(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: list roots: %w", err)
	}
	out := make([]Root, len(rows))
	for i, r := range rows {
		out[i] = toRoot(r)
	}
	return out, nil
}

// RemoveRoot stops watching a folder. Its files are marked missing, never deleted,
// so history, likes and playlists survive if the folder comes back.
func (s *Service) RemoveRoot(ctx context.Context, id string) error {
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		q := db.New(tx)
		if _, err := q.GetRoot(ctx, id); err != nil {
			return apperr.NotFound(err, "library: root "+id)
		}
		if err := q.SetRootEnabled(ctx, db.SetRootEnabledParams{Enabled: 0, ID: id}); err != nil {
			return fmt.Errorf("library: disable root: %w", err)
		}
		return q.MarkRootFilesMissing(ctx, id)
	})
	if err == nil {
		s.bus.Publish(events.Event{Type: "library.roots_changed"})
	}
	return err
}

// ---- scanning ----

// Scan walks every enabled root (or only rootID when given) and ingests what changed.
func (s *Service) Scan(ctx context.Context, rootID string) (Result, error) {
	if !s.scanMu.TryLock() {
		return Result{}, apperr.Conflict("library: a scan is already running")
	}
	defer s.scanMu.Unlock()
	s.scanning.Store(true)
	defer s.scanning.Store(false)

	roots, err := db.New(s.st.R).ListRoots(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("library: list roots: %w", err)
	}
	var total Result
	found := rootID == ""
	for _, r := range roots {
		if r.Enabled == 0 || (rootID != "" && r.ID != rootID) {
			continue
		}
		found = true
		res, err := s.scanRoot(ctx, r)
		total.add(res)
		if err != nil {
			return total, err
		}
	}
	if !found {
		return total, apperr.NotFound(sql.ErrNoRows, "library: root "+rootID)
	}
	s.bus.Publish(events.Event{Type: "library.scan_finished", Data: total})
	return total, nil
}

func (r *Result) add(o Result) {
	r.Added += o.Added
	r.Updated += o.Updated
	r.Moved += o.Moved
	r.Missing += o.Missing
	r.Skipped += o.Skipped
	r.Failed += o.Failed
	r.Errors = append(r.Errors, o.Errors...)
}

type entry struct {
	rel, mtime string
	size       int64
}

func (s *Service) scanRoot(ctx context.Context, root db.LibraryRoot) (Result, error) {
	var res Result
	var entries []entry
	err := filepath.WalkDir(root.Path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			s.log.Warn("library: skip unreadable path", slog.String("path", p), slog.Any("err", err))
			return nil
		}
		if d.IsDir() || !metadata.IsAudio(p) {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root.Path, p)
		entries = append(entries, entry{filepath.ToSlash(rel), fi.ModTime().UTC().Format(time.RFC3339Nano), fi.Size()})
		return ctx.Err()
	})
	if err != nil {
		return res, fmt.Errorf("library: walk %s: %w", root.Path, err)
	}

	existing := map[string]db.File{}
	files, err := db.New(s.st.R).ListFilesByRoot(ctx, root.ID)
	if err != nil {
		return res, fmt.Errorf("library: list files: %w", err)
	}
	for _, f := range files {
		existing[f.RelPath] = f
	}

	s.scanDone.Store(0)
	s.scanTot.Store(int64(len(entries)))
	seen := map[string]bool{}
	moved := map[string]bool{}
	changed := 0
	for i, e := range entries {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		seen[e.rel] = true
		out, id, err := s.processFile(ctx, root, e, existing[e.rel], existing[e.rel].ID != "")
		switch {
		case err != nil:
			res.Failed++
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", e.rel, err))
			s.log.Warn("library: ingest failed", slog.String("path", e.rel), slog.Any("err", err))
		default:
			res.count(out)
			if out != outSkipped {
				changed++
			}
			if out == outMoved {
				moved[id] = true
			}
		}
		s.scanDone.Store(int64(i + 1))
		if i%25 == 24 {
			s.bus.Publish(events.Event{Type: "library.scan_progress", Data: map[string]any{"root": root.ID, "done": i + 1, "total": len(entries)}})
		}
	}

	for _, f := range existing { // files that vanished from disk
		if seen[f.RelPath] || moved[f.ID] || f.Missing == 1 {
			continue
		}
		if err := db.New(s.st.W).SetFileMissing(ctx, db.SetFileMissingParams{Missing: 1, ID: f.ID}); err != nil {
			return res, fmt.Errorf("library: mark missing: %w", err)
		}
		res.Missing++
		changed++
		s.bus.Publish(events.Event{Type: "library.file_removed", Data: map[string]string{"path": f.RelPath}})
	}

	if err := db.New(s.st.W).TouchRootScan(ctx, db.TouchRootScanParams{LastScan: sql.NullString{String: store.Now(), Valid: true}, ID: root.ID}); err != nil {
		return res, fmt.Errorf("library: touch scan: %w", err)
	}
	if changed > 0 {
		s.cat.NotifyUpdated(changed)
	}
	s.bus.Publish(events.Event{Type: "library.scan_progress", Data: map[string]any{"root": root.ID, "done": len(entries), "total": len(entries)}})
	return res, nil
}

type outcome int

const (
	outSkipped outcome = iota
	outAdded
	outUpdated
	outMoved
)

func (r *Result) count(o outcome) {
	switch o {
	case outSkipped:
		r.Skipped++
	case outAdded:
		r.Added++
	case outUpdated:
		r.Updated++
	case outMoved:
		r.Moved++
	}
}

// IngestPath ingests one file (used by the watcher and by imports) without a full scan.
func (s *Service) IngestPath(ctx context.Context, abs string) error {
	abs = filepath.ToSlash(abs)
	roots, err := db.New(s.st.R).ListRoots(ctx)
	if err != nil {
		return fmt.Errorf("library: list roots: %w", err)
	}
	for _, r := range roots {
		rel, ok := strings.CutPrefix(abs, strings.TrimSuffix(r.Path, "/")+"/")
		if !ok || r.Enabled == 0 {
			continue
		}
		fi, err := os.Stat(filepath.FromSlash(abs))
		if err != nil {
			return fmt.Errorf("library: stat %s: %w", abs, err)
		}
		cur, err := db.New(s.st.R).GetFileByPath(ctx, db.GetFileByPathParams{RootID: r.ID, RelPath: rel})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("library: lookup %s: %w", rel, err)
		}
		out, _, err := s.processFile(ctx, r, entry{rel, fi.ModTime().UTC().Format(time.RFC3339Nano), fi.Size()}, cur, err == nil)
		if err != nil {
			return err
		}
		if out != outSkipped {
			s.cat.NotifyUpdated(1)
		}
		return nil
	}
	return apperr.Invalid("library: %s is not inside a watched folder", abs)
}

// MarkMissing flags a file as gone after the watcher saw a delete.
func (s *Service) MarkMissing(ctx context.Context, abs string) error {
	abs = filepath.ToSlash(abs)
	roots, err := db.New(s.st.R).ListRoots(ctx)
	if err != nil {
		return fmt.Errorf("library: list roots: %w", err)
	}
	for _, r := range roots {
		rel, ok := strings.CutPrefix(abs, strings.TrimSuffix(r.Path, "/")+"/")
		if !ok {
			continue
		}
		f, err := db.New(s.st.R).GetFileByPath(ctx, db.GetFileByPathParams{RootID: r.ID, RelPath: rel})
		if err != nil {
			return nil // never indexed: nothing to mark
		}
		if err := db.New(s.st.W).SetFileMissing(ctx, db.SetFileMissingParams{Missing: 1, ID: f.ID}); err != nil {
			return fmt.Errorf("library: mark missing: %w", err)
		}
		s.bus.Publish(events.Event{Type: "library.file_removed", Data: map[string]string{"path": rel}})
		s.cat.NotifyUpdated(1)
	}
	return nil
}

// processFile applies the ingest rules for one file: unchanged, moved, retagged or new.
func (s *Service) processFile(ctx context.Context, root db.LibraryRoot, e entry, cur db.File, known bool) (outcome, string, error) {
	abs := filepath.Join(root.Path, filepath.FromSlash(e.rel))
	if known && cur.Missing == 0 && cur.SizeBytes == e.size && cur.Mtime == e.mtime {
		return outSkipped, cur.ID, nil
	}
	hash, err := hashFile(abs)
	if err != nil {
		return 0, "", err
	}

	if known && cur.ContentHash == hash {
		// same audio: only the timestamp (or the missing flag) changed
		err := db.New(s.st.W).MoveFile(ctx, db.MoveFileParams{RootID: root.ID, RelPath: e.rel, Mtime: e.mtime, ID: cur.ID})
		return outSkipped, cur.ID, err
	}

	if !known { // a known hash whose old path is gone means the file moved
		same, err := db.New(s.st.R).ListFilesByHash(ctx, hash)
		if err != nil {
			return 0, "", fmt.Errorf("library: lookup hash: %w", err)
		}
		for _, f := range same {
			if f.Missing == 0 && pathExists(s, ctx, f) {
				continue
			}
			err := db.New(s.st.W).MoveFile(ctx, db.MoveFileParams{RootID: root.ID, RelPath: e.rel, Mtime: e.mtime, ID: f.ID})
			if err != nil {
				return 0, "", fmt.Errorf("library: move file: %w", err)
			}
			s.bus.Publish(events.Event{Type: "library.file_added", Data: map[string]string{"path": e.rel, "moved": "true"}})
			return outMoved, f.ID, nil
		}
	}

	info, err := s.reader.Read(ctx, abs)
	if err != nil {
		return 0, "", err
	}
	artID, artFile := s.prepareArtwork(abs, info)

	fileID := cur.ID
	err = s.st.Tx(ctx, func(tx *sql.Tx) error {
		q := db.New(tx)
		artworkID := ""
		if artFile != nil {
			var err error
			if artworkID, err = artFile.save(ctx, q, artID); err != nil {
				return err
			}
		}
		if known {
			err := q.UpdateFileContent(ctx, db.UpdateFileContentParams{
				ID: cur.ID, ContentHash: hash, SizeBytes: e.size, Mtime: e.mtime, Format: info.Format,
				BitrateKbps: nullInt(info.BitrateKbps), SampleRate: nullInt(info.SampleRate),
				BitDepth: nullInt(info.BitDepth), Channels: nullInt(info.Channels), DurationMs: info.DurationMS,
			})
			if err != nil {
				return fmt.Errorf("library: update file: %w", err)
			}
		} else {
			fileID = store.NewID()
			err := q.InsertFile(ctx, db.InsertFileParams{
				ID: fileID, RootID: root.ID, RelPath: e.rel, ContentHash: hash, SizeBytes: e.size, Mtime: e.mtime,
				Format: info.Format, BitrateKbps: nullInt(info.BitrateKbps), SampleRate: nullInt(info.SampleRate),
				BitDepth: nullInt(info.BitDepth), Channels: nullInt(info.Channels), DurationMs: info.DurationMS,
			})
			if err != nil {
				return fmt.Errorf("library: insert file: %w", err)
			}
		}
		trackID, err := s.cat.UpsertTrack(ctx, tx, catalog.TrackInput{FileID: fileID, Info: info, ArtworkID: artworkID})
		if err != nil || s.afterTrack == nil {
			return err
		}
		return s.afterTrack(ctx, tx, trackID, abs)
	})
	if err != nil {
		return 0, "", err
	}
	if known {
		return outUpdated, fileID, nil
	}
	s.bus.Publish(events.Event{Type: "library.file_added", Data: map[string]string{"path": e.rel}})
	return outAdded, fileID, nil
}

func pathExists(s *Service, ctx context.Context, f db.File) bool {
	r, err := db.New(s.st.R).GetRoot(ctx, f.RootID)
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(r.Path, filepath.FromSlash(f.RelPath)))
	return err == nil
}

// hashFile returns the xxhash64 of the whole file.
// ponytail: hashes tags too, so retagging changes the hash; hash only the audio stream if that hurts move detection.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("library: open %s: %w", path, err)
	}
	defer f.Close()
	h := xxhash.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("library: hash %s: %w", path, err)
	}
	return fmt.Sprintf("%016x", h.Sum64()), nil
}

func nullInt(n int) sql.NullInt64 { return sql.NullInt64{Int64: int64(n), Valid: n != 0} }

func toRoot(r db.LibraryRoot) Root {
	return Root{ID: r.ID, Path: r.Path, Enabled: r.Enabled != 0, LastScan: r.LastScan.String}
}

// TrackPath returns the absolute audio path of a track, for streaming.
func (s *Service) TrackPath(ctx context.Context, trackID string) (string, error) {
	f, err := db.New(s.st.R).GetTrackFile(ctx, trackID)
	if err != nil {
		return "", apperr.NotFound(err, "library: track "+trackID)
	}
	return filepath.Join(f.RootPath, filepath.FromSlash(f.RelPath)), nil
}
