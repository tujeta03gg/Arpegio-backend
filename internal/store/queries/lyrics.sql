-- name: UpsertLyrics :exec
INSERT INTO lyrics (track_id, synced, body) VALUES (?, ?, ?)
ON CONFLICT (track_id) DO UPDATE SET synced = excluded.synced, body = excluded.body;

-- name: DeleteLyrics :exec
DELETE FROM lyrics WHERE track_id = ?;

-- name: GetLyrics :one
SELECT * FROM lyrics WHERE track_id = ?;
