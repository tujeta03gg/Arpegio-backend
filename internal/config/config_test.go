package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.API.Listen != "127.0.0.1:7600" || cfg.History.MinListenSeconds != 30 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadOverridesOnlyGivenKeys(t *testing.T) {
	cfg, err := Load(write(t, `
[library]
roots = ["/media/music"]
[playback]
replaygain = "track"
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Library.Roots[0] != "/media/music" || cfg.Playback.ReplayGain != "track" {
		t.Fatalf("override not applied: %+v", cfg)
	}
	if !cfg.Playback.Gapless || cfg.API.Listen != "127.0.0.1:7600" {
		t.Fatalf("defaults lost: %+v", cfg)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct{ name, body, want string }{
		{"bad replaygain", "[playback]\nreplaygain = \"loud\"", "replaygain"},
		{"percent out of range", "[history]\nmin_listen_percent = 150", "min_listen_percent"},
		{"negative crossfade", "[playback]\ncrossfade_seconds = -1", "crossfade"},
		{"no api", "[api]\nlisten = \"\"\nsocket = false", "api"},
		{"unknown key", "[library]\nrootz = []", "unknown keys"},
		{"bad toml", "[library", "read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(write(t, tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want error containing %q, got %v", tt.want, err)
			}
		})
	}
}
