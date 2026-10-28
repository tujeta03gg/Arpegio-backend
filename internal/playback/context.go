package playback

import (
	"context"
	"strings"

	"arpegio/internal/apperr"
)

// PlayContext is a resolved "album:ID"-style spec: the tracks to queue and where they came from.
type PlayContext struct {
	TrackIDs []string
	Source   string // user, album, playlist, radio
	SourceID string
}

// Resolver turns context specs into track lists. Sources maps a kind ("album", "playlist", ...)
// to a function returning the track ids for an id (kinds without ids, like "likes", get "").
type Resolver struct {
	Sources map[string]func(ctx context.Context, id string) ([]string, error)
}

// queueSources are the kinds the queue table distinguishes; everything else is "user".
var queueSources = map[string]bool{"album": true, "playlist": true, "radio": true}

func (r *Resolver) Resolve(ctx context.Context, spec string) (PlayContext, error) {
	kind, id, _ := strings.Cut(spec, ":")
	fn, ok := r.Sources[kind]
	if !ok {
		return PlayContext{}, apperr.Invalid("playback: unknown context %q", spec)
	}
	ids, err := fn(ctx, id)
	if err != nil {
		return PlayContext{}, err
	}
	if len(ids) == 0 {
		return PlayContext{}, apperr.Invalid("playback: %s has no playable tracks", spec)
	}
	src := "user"
	if queueSources[kind] {
		src = kind
	}
	return PlayContext{TrackIDs: ids, Source: src, SourceID: id}, nil
}
