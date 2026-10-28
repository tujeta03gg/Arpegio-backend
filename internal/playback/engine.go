// Package playback drives the audio engine (mpv) and keeps the player state.
package playback

import "context"

// Engine is the audio backend. The real one is mpv over its JSON IPC; tests use a fake.
type Engine interface {
	// Load plays path, replacing whatever is playing, optionally starting at startMS and paused.
	Load(ctx context.Context, path string, startMS int64, paused bool) error
	// Preload queues path to start right after the current file, with no gap.
	Preload(ctx context.Context, path string) error
	// ClearPreload drops a preloaded file, keeping the current one.
	ClearPreload(ctx context.Context) error
	SetPause(ctx context.Context, paused bool) error
	Seek(ctx context.Context, ms int64) error
	SetVolume(ctx context.Context, level int) error
	Stop(ctx context.Context) error
	// Events delivers what the engine reports; the channel is owned by the engine.
	Events() <-chan EngineEvent
}

type EventKind int

const (
	EvPosition  EventKind = iota // PositionMS changed
	EvEnded                      // a file finished; see Reason
	EvRestarted                  // the engine (re)started and is empty: volume and track must be set again
)

// End reasons reported with EvEnded.
const (
	ReasonEOF   = "eof"   // played to the end
	ReasonError = "error" // could not be played
	ReasonStop  = "stop"  // stopped or replaced by a command (ignored by the service)
)

type EngineEvent struct {
	Kind       EventKind
	PositionMS int64
	Reason     string
}
