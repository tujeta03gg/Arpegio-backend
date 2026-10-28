-- name: ListQueueItems :many
SELECT position, track_id, source, source_id, priority FROM queue_items ORDER BY position;

-- name: InsertQueueItem :exec
INSERT INTO queue_items (position, track_id, source, source_id, priority) VALUES (?, ?, ?, ?, ?);

-- name: DeleteQueueItem :execrows
DELETE FROM queue_items WHERE position = ?;

-- name: ClearQueueItems :exec
DELETE FROM queue_items;

-- name: GetQueueState :one
SELECT * FROM queue_state WHERE id = 1;

-- name: SetCurrentPos :exec
UPDATE queue_state SET current_pos = ? WHERE id = 1;

-- name: SetShuffleState :exec
UPDATE queue_state SET shuffle = ?, shuffle_seed = ? WHERE id = 1;

-- name: SetRepeatMode :exec
UPDATE queue_state SET repeat_mode = ? WHERE id = 1;

-- name: GetPlayerState :one
SELECT * FROM player_state WHERE id = 1;

-- name: SavePlayerState :exec
UPDATE player_state SET track_id = ?, position_ms = ?, volume = ?, output = ?, updated_at = ? WHERE id = 1;
