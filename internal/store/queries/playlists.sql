-- name: InsertPlaylist :exec
INSERT INTO playlists (id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?);

-- name: GetPlaylist :one
SELECT * FROM playlists WHERE id = ?;

-- name: ListPlaylists :many
SELECT p.id, p.name, p.description, p.created_at, p.updated_at,
  (SELECT COUNT(*) FROM playlist_items i WHERE i.playlist_id = p.id) AS item_count
FROM playlists p ORDER BY p.id;

-- name: UpdatePlaylist :exec
UPDATE playlists SET name = ?, description = ?, updated_at = ? WHERE id = ?;

-- name: TouchPlaylist :exec
UPDATE playlists SET updated_at = ? WHERE id = ?;

-- name: DeletePlaylist :execrows
DELETE FROM playlists WHERE id = ?;

-- name: ListPlaylistItems :many
SELECT position, track_id, added_at FROM playlist_items WHERE playlist_id = ? ORDER BY position;

-- name: InsertPlaylistItem :exec
INSERT INTO playlist_items (playlist_id, position, track_id, added_at) VALUES (?, ?, ?, ?);

-- name: DeletePlaylistItem :execrows
DELETE FROM playlist_items WHERE playlist_id = ? AND position = ?;

-- name: DeletePlaylistItems :exec
DELETE FROM playlist_items WHERE playlist_id = ?;

-- name: InsertLike :exec
INSERT INTO likes (target_kind, target_id, liked_at) VALUES (?, ?, ?) ON CONFLICT (target_kind, target_id) DO NOTHING;

-- name: DeleteLike :execrows
DELETE FROM likes WHERE target_kind = ? AND target_id = ?;

-- name: ListLikes :many
SELECT target_id, liked_at FROM likes WHERE target_kind = ? ORDER BY liked_at DESC, target_id;

-- name: IsLiked :one
SELECT COUNT(*) FROM likes WHERE target_kind = ? AND target_id = ?;
