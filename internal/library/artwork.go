package library

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // register the PNG decoder
	"os"
	"path/filepath"
	"strings"

	"github.com/cespare/xxhash/v2"
	xdraw "golang.org/x/image/draw"

	"arpegio/internal/apperr"
	"arpegio/internal/metadata"
	"arpegio/internal/store"
	"arpegio/internal/store/db"
)

type artFile struct {
	hash, ext string
	data      []byte
	w, h      int
	dir       string
}

var coverNames = map[string]bool{"cover.jpg": true, "cover.png": true, "folder.jpg": true, "folder.png": true}

// prepareArtwork finds the cover for a file (embedded first, then cover/folder image next to it).
// It returns the content hash and the pending file, or nil when there is no usable image.
func (s *Service) prepareArtwork(audioPath string, info metadata.Info) (string, *artFile) {
	if s.artDir == "" {
		return "", nil
	}
	data := info.Picture
	if len(data) == 0 {
		entries, _ := os.ReadDir(filepath.Dir(audioPath))
		for _, e := range entries {
			if !e.IsDir() && coverNames[strings.ToLower(e.Name())] {
				data, _ = os.ReadFile(filepath.Join(filepath.Dir(audioPath), e.Name()))
				break
			}
		}
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") {
		return "", nil
	}
	ext := map[string]string{"jpeg": "jpg", "png": "png"}[format]
	hash := fmt.Sprintf("%016x", xxhash.Sum64(data))
	return hash, &artFile{hash: hash, ext: ext, data: data, w: cfg.Width, h: cfg.Height, dir: s.artDir}
}

// save stores the image (once per hash) and returns the artwork row id. Runs in the ingest transaction.
func (a *artFile) save(ctx context.Context, q *db.Queries, hash string) (string, error) {
	if row, err := q.GetArtworkByHash(ctx, hash); err == nil {
		return row.ID, nil
	}
	if err := os.MkdirAll(a.dir, 0o755); err != nil {
		return "", fmt.Errorf("library: artwork dir: %w", err)
	}
	path := filepath.Join(a.dir, hash+"."+a.ext)
	if err := os.WriteFile(path, a.data, 0o644); err != nil {
		return "", fmt.Errorf("library: write artwork: %w", err)
	}
	id := store.NewID()
	err := q.InsertArtwork(ctx, db.InsertArtworkParams{
		ID: id, Hash: hash, CachePath: path,
		Width: sql.NullInt64{Int64: int64(a.w), Valid: true}, Height: sql.NullInt64{Int64: int64(a.h), Valid: true},
	})
	if err != nil {
		return "", fmt.Errorf("library: save artwork: %w", err)
	}
	return id, nil
}

// ArtworkSizes are the thumbnail widths the API serves.
var ArtworkSizes = map[int]bool{96: true, 300: true, 600: true}

// Artwork returns the path of the cover file; size 0 is the original, otherwise a cached thumbnail.
func (s *Service) Artwork(ctx context.Context, id string, size int) (string, error) {
	if size != 0 && !ArtworkSizes[size] {
		return "", apperr.Invalid("library: size must be 96, 300 or 600")
	}
	row, err := db.New(s.st.R).GetArtwork(ctx, id)
	if err != nil {
		return "", apperr.NotFound(err, "library: artwork "+id)
	}
	if size == 0 || int(row.Width.Int64) <= size {
		return row.CachePath, nil
	}
	thumb := filepath.Join(filepath.Dir(row.CachePath), fmt.Sprintf("%s_%d.jpg", row.Hash, size))
	if _, err := os.Stat(thumb); err == nil {
		return thumb, nil
	}
	data, err := os.ReadFile(row.CachePath)
	if err != nil {
		return "", fmt.Errorf("library: read artwork: %w", err)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("library: decode artwork: %w", err)
	}
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, size, b.Dy()*size/b.Dx()))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, xdraw.Src, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 85}); err != nil {
		return "", fmt.Errorf("library: encode thumbnail: %w", err)
	}
	if err := os.WriteFile(thumb, out.Bytes(), 0o644); err != nil {
		return "", fmt.Errorf("library: write thumbnail: %w", err)
	}
	return thumb, nil
}
