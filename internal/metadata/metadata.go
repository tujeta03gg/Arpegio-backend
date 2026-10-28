// Package metadata reads tags and technical properties from audio files. It is a pure service: no DB.
package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dhowden/tag"
)

// Info is everything the library needs to know about one audio file.
type Info struct {
	Title       string
	Artist      string // raw artist tag; see SplitArtists
	AlbumArtist string
	Album       string
	Genres      []string
	Year        int
	TrackNo     int
	DiscNo      int
	BPM         int
	Picture     []byte // embedded cover, if any
	Format      string // flac, mp3, opus, m4a, ogg, wav
	Props
}

// Props are the technical properties ffprobe provides.
type Props struct {
	DurationMS  int64
	BitrateKbps int
	SampleRate  int
	BitDepth    int
	Channels    int
}

var audioExt = map[string]bool{".flac": true, ".mp3": true, ".opus": true, ".m4a": true, ".ogg": true, ".wav": true}

// IsAudio reports whether path has a supported audio extension.
func IsAudio(path string) bool { return audioExt[strings.ToLower(filepath.Ext(path))] }

// ErrNoDuration means the duration could not be determined (ffprobe missing or failed).
var ErrNoDuration = errors.New("metadata: duration unavailable")

// Reader reads files. Probe is injectable so tests do not need ffprobe.
type Reader struct {
	Probe func(ctx context.Context, path string) (Props, error)
}

// NewReader returns a Reader backed by the ffprobe binary.
func NewReader() Reader { return Reader{Probe: FFProbe} }

// Read extracts tags with dhowden/tag and technical properties with Probe.
// Files without tags still work: the title falls back to the file name.
func (r Reader) Read(ctx context.Context, path string) (Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return Info{}, fmt.Errorf("metadata: open %s: %w", path, err)
	}
	defer f.Close()

	info := Info{Format: strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")}
	// Missing or corrupt tags are not fatal: the title falls back to the file name
	// and the probe step decides whether the file is playable.
	m, err := tag.ReadFrom(f)
	if err == nil {
		info.Title, info.Artist, info.AlbumArtist = CleanName(m.Title()), CleanName(m.Artist()), CleanName(m.AlbumArtist())
		info.Album, info.Year = CleanName(m.Album()), m.Year()
		info.TrackNo, _ = m.Track()
		info.DiscNo, _ = m.Disc()
		info.Genres = splitGenres(m.Genre())
		if p := m.Picture(); p != nil {
			info.Picture = p.Data
		}
		if bpm, ok := m.Raw()["TBPM"]; ok {
			info.BPM = atoi(fmt.Sprint(bpm))
		}
	}
	if info.Title == "" {
		info.Title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}

	if r.Probe == nil {
		return Info{}, ErrNoDuration
	}
	props, err := r.Probe(ctx, path)
	if err != nil {
		return Info{}, fmt.Errorf("metadata: probe %s: %w", path, err)
	}
	if props.DurationMS <= 0 {
		return Info{}, fmt.Errorf("metadata: probe %s: %w", path, ErrNoDuration)
	}
	info.Props = props
	return info, nil
}

func splitGenres(s string) []string {
	var out []string
	for _, g := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == '/' }) {
		if g = CleanName(g); g != "" {
			out = append(out, g)
		}
	}
	return out
}

func atoi(s string) int { n, _ := strconv.Atoi(strings.TrimSpace(s)); return n }

// FFProbe runs the ffprobe binary on path.
func FFProbe(ctx context.Context, path string) (Props, error) {
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return Props{}, fmt.Errorf("%w: ffprobe not installed", ErrNoDuration)
		}
		return Props{}, fmt.Errorf("ffprobe: %w", err)
	}
	return ParseProbe(out.Bytes())
}

// ParseProbe parses ffprobe's JSON output.
func ParseProbe(data []byte) (Props, error) {
	var raw struct {
		Format struct {
			Duration string `json:"duration"`
			BitRate  string `json:"bit_rate"`
		} `json:"format"`
		Streams []struct {
			CodecType     string `json:"codec_type"`
			SampleRate    string `json:"sample_rate"`
			Channels      int    `json:"channels"`
			BitsPerSample int    `json:"bits_per_sample"`
			BitsPerRaw    string `json:"bits_per_raw_sample"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Props{}, fmt.Errorf("ffprobe: parse json: %w", err)
	}
	secs, _ := strconv.ParseFloat(raw.Format.Duration, 64)
	p := Props{
		DurationMS:  int64(math.Round(secs * 1000)),
		BitrateKbps: atoi(raw.Format.BitRate) / 1000,
	}
	for _, s := range raw.Streams {
		if s.CodecType != "audio" {
			continue
		}
		p.SampleRate, p.Channels = atoi(s.SampleRate), s.Channels
		p.BitDepth = s.BitsPerSample
		if p.BitDepth == 0 {
			p.BitDepth = atoi(s.BitsPerRaw)
		}
		break
	}
	return p, nil
}
