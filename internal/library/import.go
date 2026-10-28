package library

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"arpegio/internal/apperr"
	"arpegio/internal/metadata"
)

// SetImportDir sets the folder uploaded files are stored in; it must be inside a watched root.
func (s *Service) SetImportDir(dir string) { s.importDir = dir }

// SetEnqueuer makes ingestion run in the background worker instead of inline.
func (s *Service) SetEnqueuer(e Enqueuer) { s.enqueue = e }

// Import stores an uploaded audio file in the import folder and queues it for ingestion.
// It returns the final path. The file is written under a temporary name and renamed,
// so the folder watcher never sees a half-written file.
func (s *Service) Import(ctx context.Context, name string, r io.Reader) (string, error) {
	if s.importDir == "" {
		return "", apperr.Invalid("library: imports are disabled (set library.import_dir)")
	}
	base := cleanFileName(name)
	if base == "" || !metadata.IsAudio(base) {
		return "", apperr.Invalid("library: %q is not a supported audio file", name)
	}
	dir, err := filepath.Abs(s.importDir)
	if err != nil {
		return "", fmt.Errorf("library: import dir: %w", err)
	}
	if !s.insideRoot(ctx, dir) {
		return "", apperr.Invalid("library: import_dir %s is not inside a watched folder", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("library: create import dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".import-*.part")
	if err != nil {
		return "", fmt.Errorf("library: import temp file: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return "", fmt.Errorf("library: receive %s: %w", base, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("library: save %s: %w", base, err)
	}
	final := uniquePath(dir, base)
	if err := os.Rename(tmp.Name(), final); err != nil {
		return "", fmt.Errorf("library: place %s: %w", base, err)
	}

	if s.enqueue != nil {
		err = s.enqueue(ctx, final)
	} else {
		err = s.IngestPath(ctx, final)
	}
	if err != nil {
		return final, fmt.Errorf("library: ingest %s: %w", base, err)
	}
	return final, nil
}

// insideRoot reports whether dir is (inside) an enabled library root.
func (s *Service) insideRoot(ctx context.Context, dir string) bool {
	roots, err := s.Roots(ctx)
	if err != nil {
		return false
	}
	d := filepath.ToSlash(dir)
	for _, r := range roots {
		if r.Enabled && (d == r.Path || strings.HasPrefix(d, strings.TrimSuffix(r.Path, "/")+"/")) {
			return true
		}
	}
	return false
}

// cleanFileName keeps only the base name of what a client sent and drops hidden-file dots.
func cleanFileName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = name[strings.LastIndex(name, "/")+1:]
	return strings.TrimLeft(strings.TrimSpace(name), ".")
}

// uniquePath returns dir/name, or dir/name (2).ext etc. when taken.
func uniquePath(dir, name string) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	p := filepath.Join(dir, name)
	for i := 2; ; i++ {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
	}
}

// SetTrackHook registers extra work done inside the ingest transaction of every track
// (the daemon uses it to pick up .lrc lyrics without library knowing about lyrics).
func (s *Service) SetTrackHook(h func(ctx context.Context, tx *sql.Tx, trackID, audioPath string) error) {
	s.afterTrack = h
}
