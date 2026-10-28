package api

import (
	"context"
	"testing"

	"arpegio/internal/apperr"
	"arpegio/internal/lyrics"
	"arpegio/internal/playback"
)

type fakeLyrics map[string]lyrics.Lyrics

func (f fakeLyrics) Get(_ context.Context, id string) (lyrics.Lyrics, error) {
	l, ok := f[id]
	if !ok {
		return lyrics.Lyrics{}, apperr.ErrNotFound
	}
	return l, nil
}

func TestLyricsEndpoint(t *testing.T) {
	e := newEnv(t)
	e.srv.Lyrics = fakeLyrics{
		"synced": lyrics.Parse("[00:01.50]Hello\n[00:05.00]World"),
		"plain":  lyrics.Parse("Just words\nMore words"),
	}

	var l LyricsDTO
	if code := e.do("GET", "/tracks/synced/lyrics", nil, &l); code != 200 || !l.Synced || len(l.Lines) != 2 ||
		l.Lines[0].TimeMS == nil || *l.Lines[0].TimeMS != 1500 || l.Lines[1].Text != "World" {
		t.Fatalf("synced = %d %+v", code, l)
	}
	var raw map[string]any
	if code := e.do("GET", "/tracks/plain/lyrics", nil, &raw); code != 200 || raw["synced"] != false {
		t.Fatalf("plain = %d %v", code, raw)
	}
	for _, line := range raw["lines"].([]any) {
		if _, has := line.(map[string]any)["time_ms"]; has {
			t.Errorf("plain lyrics must not carry timestamps: %v", line)
		}
	}
	if code := e.do("GET", "/tracks/none/lyrics", nil, nil); code != 404 {
		t.Errorf("no lyrics = %d", code)
	}
}

func TestRadioEndpoint(t *testing.T) {
	e := newEnv(t)
	e.player.state = playback.State{Volume: 60}
	var p PlayerDTO
	if code := e.do("POST", "/radio", map[string]string{"seed": "track:T1"}, &p); code != 200 || p.Volume != 60 {
		t.Fatalf("radio = %d %+v", code, p)
	}
	if got := e.player.take(); got != `play "radio:track:T1" ""` {
		t.Errorf("player called with %q", got)
	}
	e.player.err = apperr.Invalid("radio: seed must look like track:ID")
	if code := e.do("POST", "/radio", map[string]string{"seed": "nonsense"}, nil); code != 400 {
		t.Errorf("bad seed = %d", code)
	}
	if code := e.do("POST", "/radio", map[string]string{"x": "y"}, nil); code != 400 {
		t.Errorf("unknown field = %d", code)
	}
}
