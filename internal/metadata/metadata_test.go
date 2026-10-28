package metadata

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"arpegio/internal/testutil"
)

func fakeProbe(context.Context, string) (Props, error) {
	return Props{DurationMS: 215000, BitrateKbps: 320, SampleRate: 44100, Channels: 2}, nil
}

func TestSortKey(t *testing.T) {
	tests := []struct{ in, want string }{
		{"The Beatles", "beatles"},
		{"  beatles ", "beatles"},
		{"Café  Tacvba", "cafe tacvba"},
		{"Björk", "bjork"},
		{"The The", "the"},
		{"Theory of a Deadman", "theory of a deadman"},
	}
	for _, tt := range tests {
		if got := SortKey(tt.in); got != tt.want {
			t.Errorf("SortKey(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSplitArtists(t *testing.T) {
	tests := []struct {
		in             string
		main, featured []string
	}{
		{"Daft Punk", []string{"Daft Punk"}, nil},
		{"Simon & Garfunkel", []string{"Simon & Garfunkel"}, nil},
		{"A; B", []string{"A", "B"}, nil},
		{"Drake feat. Rihanna", []string{"Drake"}, []string{"Rihanna"}},
		{"Drake FT. Rihanna; Wizkid", []string{"Drake"}, []string{"Rihanna", "Wizkid"}},
		{"", nil, nil},
	}
	for _, tt := range tests {
		main, feat := SplitArtists(tt.in)
		if !reflect.DeepEqual(main, tt.main) || !reflect.DeepEqual(feat, tt.featured) {
			t.Errorf("SplitArtists(%q) = %v, %v; want %v, %v", tt.in, main, feat, tt.main, tt.featured)
		}
	}
}

func TestIsAudio(t *testing.T) {
	for p, want := range map[string]bool{"a.FLAC": true, "b.mp3": true, "c.txt": false, "cover.jpg": false, "d": false} {
		if IsAudio(p) != want {
			t.Errorf("IsAudio(%q) != %v", p, want)
		}
	}
}

func TestParseProbe(t *testing.T) {
	p, err := ParseProbe([]byte(`{"format":{"duration":"215.337","bit_rate":"1011000"},
		"streams":[{"codec_type":"video"},{"codec_type":"audio","sample_rate":"44100","channels":2,"bits_per_raw_sample":"16"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := Props{DurationMS: 215337, BitrateKbps: 1011, SampleRate: 44100, BitDepth: 16, Channels: 2}
	if p != want {
		t.Fatalf("got %+v want %+v", p, want)
	}
	if _, err := ParseProbe([]byte("nope")); err == nil {
		t.Fatal("expected json error")
	}
}

func TestReadTaggedFile(t *testing.T) {
	path := testutil.WriteMP3(t, t.TempDir(), "x.mp3", map[string]string{
		"TIT2": "Canción", "TPE1": "Café Tacvba", "TALB": "Re", "TCON": "Rock;Alternative",
		"TRCK": "3/12", "TPOS": "1/2", "TYER": "1994", "TPE2": "Café Tacvba",
	}, "audio")
	info, err := Reader{Probe: fakeProbe}.Read(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "Canción" || info.Artist != "Café Tacvba" || info.Album != "Re" ||
		info.TrackNo != 3 || info.DiscNo != 1 || info.Year != 1994 || info.Format != "mp3" ||
		!reflect.DeepEqual(info.Genres, []string{"Rock", "Alternative"}) || info.DurationMS != 215000 {
		t.Fatalf("unexpected info: %+v", info)
	}
}

func TestReadUntaggedFallsBackToFilename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Mi Canción.mp3")
	os.WriteFile(path, []byte("no tags here"), 0o644)
	info, err := Reader{Probe: fakeProbe}.Read(context.Background(), path)
	if err != nil || info.Title != "Mi Canción" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
}

func TestReadErrors(t *testing.T) {
	ctx := context.Background()
	path := testutil.WriteMP3(t, t.TempDir(), "x.mp3", map[string]string{"TIT2": "t"}, "a")

	if _, err := (Reader{Probe: fakeProbe}).Read(ctx, filepath.Join(t.TempDir(), "missing.mp3")); err == nil {
		t.Error("missing file should fail")
	}
	if _, err := (Reader{}).Read(ctx, path); !errors.Is(err, ErrNoDuration) {
		t.Errorf("no probe: got %v", err)
	}
	zero := func(context.Context, string) (Props, error) { return Props{}, nil }
	if _, err := (Reader{Probe: zero}).Read(ctx, path); !errors.Is(err, ErrNoDuration) {
		t.Errorf("zero duration: got %v", err)
	}
}
