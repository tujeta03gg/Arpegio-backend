// Package lyrics finds .lrc files next to the audio and serves synchronized lyrics.
package lyrics

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"arpegio/internal/apperr"
	"arpegio/internal/store"
	"arpegio/internal/store/db"
)

// Line is one lyric line. TimeMS is -1 for lyrics without timestamps.
type Line struct {
	TimeMS int64
	Text   string
}

type Lyrics struct {
	Synced bool
	Lines  []Line
}

var (
	timestamp = regexp.MustCompile(`\[(\d{1,3}):(\d{2})(?:[.:](\d{1,3}))?\]`)
	tag       = regexp.MustCompile(`^\[([a-zA-Z]+):(.*)\]$`)
)

// Parse reads LRC text. Lines may carry several timestamps ("[00:12.00][01:30.50]chorus");
// the [offset:±ms] tag shifts them (positive = earlier). Text without timestamps is returned as plain lines.
func Parse(body string) Lyrics {
	body = strings.TrimPrefix(body, "\xef\xbb\xbf") // UTF-8 byte order mark
	var lines []Line
	offset := int64(0)
	synced := false
	for _, raw := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		raw = strings.TrimRight(raw, "\r ")
		stamps := timestamp.FindAllStringSubmatch(raw, -1)
		if len(stamps) == 0 {
			if m := tag.FindStringSubmatch(strings.TrimSpace(raw)); m != nil {
				if strings.EqualFold(m[1], "offset") {
					offset, _ = strconv.ParseInt(strings.TrimSpace(m[2]), 10, 64)
				}
				continue // ar, ti, al, by...
			}
			if strings.TrimSpace(raw) != "" {
				lines = append(lines, Line{TimeMS: -1, Text: strings.TrimSpace(raw)})
			}
			continue
		}
		synced = true
		text := strings.TrimSpace(timestamp.ReplaceAllString(raw, ""))
		for _, s := range stamps {
			min, _ := strconv.ParseInt(s[1], 10, 64)
			sec, _ := strconv.ParseInt(s[2], 10, 64)
			lines = append(lines, Line{TimeMS: min*60_000 + sec*1000 + fraction(s[3]), Text: text})
		}
	}
	if synced {
		kept := lines[:0]
		for _, l := range lines {
			if l.TimeMS >= 0 { // text without a timestamp inside a synced file is a stray comment
				l.TimeMS = max(0, l.TimeMS-offset)
				kept = append(kept, l)
			}
		}
		lines = kept
		sort.SliceStable(lines, func(i, j int) bool { return lines[i].TimeMS < lines[j].TimeMS })
	}
	return Lyrics{Synced: synced, Lines: lines}
}

// fraction converts the digits after the dot into milliseconds: ".5" = 500, ".50" = 500, ".500" = 500.
func fraction(digits string) int64 {
	if digits == "" {
		return 0
	}
	n, _ := strconv.ParseInt(digits, 10, 64)
	switch len(digits) {
	case 1:
		return n * 100
	case 2:
		return n * 10
	}
	return n
}

// CurrentLine returns the index of the line to show at posMS, or -1 before the first line.
func (l Lyrics) CurrentLine(posMS int64) int {
	if !l.Synced {
		return -1
	}
	i := sort.Search(len(l.Lines), func(i int) bool { return l.Lines[i].TimeMS > posMS })
	return i - 1
}

type Service struct{ st *store.Store }

func New(st *store.Store) *Service { return &Service{st: st} }

// Attach looks for <audio file name>.lrc next to the audio and stores it for the track
// (or removes the stored lyrics when the file is gone). It runs inside the ingest transaction.
func (s *Service) Attach(ctx context.Context, tx *sql.Tx, trackID, audioPath string) error {
	q := db.New(tx)
	lrc := strings.TrimSuffix(audioPath, filepath.Ext(audioPath)) + ".lrc"
	data, err := os.ReadFile(lrc)
	if errors.Is(err, os.ErrNotExist) {
		if err := q.DeleteLyrics(ctx, trackID); err != nil {
			return fmt.Errorf("lyrics: clear for %s: %w", trackID, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("lyrics: read %s: %w", lrc, err)
	}
	body := string(bytes.TrimSpace(data))
	if body == "" {
		return q.DeleteLyrics(ctx, trackID)
	}
	synced := int64(0)
	if Parse(body).Synced {
		synced = 1
	}
	if err := q.UpsertLyrics(ctx, db.UpsertLyricsParams{TrackID: trackID, Synced: synced, Body: body}); err != nil {
		return fmt.Errorf("lyrics: save for %s: %w", trackID, err)
	}
	return nil
}

// Get returns the parsed lyrics of a track.
func (s *Service) Get(ctx context.Context, trackID string) (Lyrics, error) {
	row, err := db.New(s.st.R).GetLyrics(ctx, trackID)
	if err != nil {
		return Lyrics{}, apperr.NotFound(err, "lyrics: track "+trackID)
	}
	return Parse(row.Body), nil
}
