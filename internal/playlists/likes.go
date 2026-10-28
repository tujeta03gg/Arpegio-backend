package playlists

import (
	"context"
	"fmt"

	"arpegio/internal/apperr"
	"arpegio/internal/events"
	"arpegio/internal/store"
	"arpegio/internal/store/db"
)

type Like struct {
	Kind, ID, LikedAt string
}

func checkKind(kind string) error {
	switch kind {
	case "track", "album", "artist":
		return nil
	}
	return apperr.Invalid("likes: kind must be track, album or artist")
}

func (s *Service) likeChanged(kind, id string, liked bool) {
	s.bus.Publish(events.Event{Type: "like.changed", Data: map[string]any{"kind": kind, "id": id, "liked": liked}})
}

// SetLike marks a track, album or artist as liked. Liking twice is fine.
func (s *Service) SetLike(ctx context.Context, kind, id string) error {
	if err := checkKind(kind); err != nil {
		return err
	}
	if err := s.exists(ctx, kind, id); err != nil {
		return err
	}
	if err := db.New(s.st.W).InsertLike(ctx, db.InsertLikeParams{TargetKind: kind, TargetID: id, LikedAt: store.Now()}); err != nil {
		return fmt.Errorf("likes: like %s %s: %w", kind, id, err)
	}
	s.likeChanged(kind, id, true)
	return nil
}

// RemoveLike removes a like. Removing one that is not there is fine.
func (s *Service) RemoveLike(ctx context.Context, kind, id string) error {
	if err := checkKind(kind); err != nil {
		return err
	}
	n, err := db.New(s.st.W).DeleteLike(ctx, db.DeleteLikeParams{TargetKind: kind, TargetID: id})
	if err != nil {
		return fmt.Errorf("likes: unlike %s %s: %w", kind, id, err)
	}
	if n > 0 {
		s.likeChanged(kind, id, false)
	}
	return nil
}

// Likes lists liked items of a kind, most recent first.
func (s *Service) Likes(ctx context.Context, kind string) ([]Like, error) {
	if err := checkKind(kind); err != nil {
		return nil, err
	}
	rows, err := db.New(s.st.R).ListLikes(ctx, kind)
	if err != nil {
		return nil, fmt.Errorf("likes: list %s: %w", kind, err)
	}
	out := make([]Like, len(rows))
	for i, r := range rows {
		out[i] = Like{Kind: kind, ID: r.TargetID, LikedAt: r.LikedAt}
	}
	return out, nil
}

// LikedTrackIDs is the "likes" playback context.
func (s *Service) LikedTrackIDs(ctx context.Context) ([]string, error) {
	likes, err := s.Likes(ctx, "track")
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(likes))
	for i, l := range likes {
		ids[i] = l.ID
	}
	return ids, nil
}
