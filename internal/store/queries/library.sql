-- name: InsertRoot :one
INSERT INTO library_roots (id, path) VALUES (?, ?) RETURNING *;

-- name: GetRoot :one
SELECT * FROM library_roots WHERE id = ?;

-- name: ListRoots :many
SELECT * FROM library_roots ORDER BY path;

-- name: SetRootEnabled :exec
UPDATE library_roots SET enabled = ? WHERE id = ?;

-- name: TouchRootScan :exec
UPDATE library_roots SET last_scan = ? WHERE id = ?;

-- name: GetFileByPath :one
SELECT * FROM files WHERE root_id = ? AND rel_path = ?;

-- name: ListFilesByHash :many
SELECT * FROM files WHERE content_hash = ?;

-- name: ListFilesByRoot :many
SELECT * FROM files WHERE root_id = ?;

-- name: InsertFile :exec
INSERT INTO files (id, root_id, rel_path, content_hash, size_bytes, mtime, format,
  bitrate_kbps, sample_rate, bit_depth, channels, duration_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateFileContent :exec
UPDATE files SET content_hash = ?, size_bytes = ?, mtime = ?, format = ?,
  bitrate_kbps = ?, sample_rate = ?, bit_depth = ?, channels = ?, duration_ms = ?, missing = 0
WHERE id = ?;

-- name: MoveFile :exec
UPDATE files SET root_id = ?, rel_path = ?, mtime = ?, missing = 0 WHERE id = ?;

-- name: SetFileMissing :exec
UPDATE files SET missing = ? WHERE id = ?;

-- name: MarkRootFilesMissing :exec
UPDATE files SET missing = 1 WHERE root_id = ?;

-- name: GetArtworkByHash :one
SELECT * FROM artwork WHERE hash = ?;

-- name: InsertArtwork :exec
INSERT INTO artwork (id, hash, cache_path, width, height) VALUES (?, ?, ?, ?, ?);

-- name: GetArtwork :one
SELECT * FROM artwork WHERE id = ?;

-- name: GetRootByPath :one
SELECT * FROM library_roots WHERE path = ?;
