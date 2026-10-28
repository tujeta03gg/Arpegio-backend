// Package config reads and validates config.toml.
package config

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Library    Library    `toml:"library"`
	API        API        `toml:"api"`
	Playback   Playback   `toml:"playback"`
	History    History    `toml:"history"`
	Enrichment Enrichment `toml:"enrichment"`
	Hardware   Hardware   `toml:"hardware"`
}

type Library struct {
	Roots     []string `toml:"roots"`
	ImportDir string   `toml:"import_dir"`
	Watch     bool     `toml:"watch"`
}

type API struct {
	Listen string `toml:"listen"`
	Socket bool   `toml:"socket"`
}

type Playback struct {
	AudioOutput      string `toml:"audio_output"`
	Gapless          bool   `toml:"gapless"`
	ReplayGain       string `toml:"replaygain"` // off, track, album
	CrossfadeSeconds int    `toml:"crossfade_seconds"`
}

type History struct {
	MinListenSeconds int `toml:"min_listen_seconds"`
	MinListenPercent int `toml:"min_listen_percent"`
}

type Enrichment struct {
	MusicBrainz bool `toml:"musicbrainz"`
}

type Hardware struct {
	GPIO bool `toml:"gpio"`
}

// Default returns the configuration used when no file or key overrides it.
func Default() Config {
	return Config{
		Library:  Library{Watch: true},
		API:      API{Listen: "127.0.0.1:7600", Socket: true},
		Playback: Playback{AudioOutput: "pipewire", Gapless: true, ReplayGain: "album"},
		History:  History{MinListenSeconds: 30, MinListenPercent: 50},
	}
}

// Load reads path over the defaults. A missing file is not an error.
func Load(path string) (Config, error) {
	cfg := Default()
	md, err := toml.DecodeFile(path, &cfg)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, cfg.Validate()
	}
	if err != nil {
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		return Config{}, fmt.Errorf("config: unknown keys %v", u)
	}
	return cfg, cfg.Validate()
}

// Validate rejects values the daemon cannot work with.
func (c Config) Validate() error {
	switch c.Playback.ReplayGain {
	case "off", "track", "album":
	default:
		return fmt.Errorf("config: playback.replaygain %q must be off, track or album", c.Playback.ReplayGain)
	}
	if c.Playback.CrossfadeSeconds < 0 {
		return errors.New("config: playback.crossfade_seconds must be >= 0")
	}
	if p := c.History.MinListenPercent; p < 1 || p > 100 {
		return fmt.Errorf("config: history.min_listen_percent %d must be 1-100", p)
	}
	if c.History.MinListenSeconds < 1 {
		return errors.New("config: history.min_listen_seconds must be >= 1")
	}
	if c.API.Listen == "" && !c.API.Socket {
		return errors.New("config: api needs listen or socket enabled")
	}
	return nil
}
