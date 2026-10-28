-- name: InsertPlay :exec
INSERT INTO plays (id, track_id, started_at, listened_ms, completed, skipped, source)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListRecentPlays :many
SELECT id, track_id, started_at, listened_ms, completed, skipped, source
FROM plays ORDER BY started_at DESC, id DESC LIMIT ?;

-- name: PlayTotals :one
SELECT CAST(COALESCE(SUM(listened_ms), 0) AS INTEGER) AS listened_ms,
       CAST(COALESCE(SUM(completed), 0) AS INTEGER) AS plays
FROM plays WHERE started_at >= ?;

-- name: TopTracks :many
SELECT p.track_id AS id, t.title AS name, COUNT(*) AS plays
FROM plays p JOIN tracks t ON t.id = p.track_id
WHERE p.completed = 1 AND p.started_at >= ?
GROUP BY p.track_id ORDER BY plays DESC, MAX(p.started_at) DESC LIMIT ?;

-- name: TopArtists :many
SELECT a.id AS id, a.name AS name, COUNT(*) AS plays
FROM plays p
JOIN track_artists ta ON ta.track_id = p.track_id AND ta.role = 'main'
JOIN artists a ON a.id = ta.artist_id
WHERE p.completed = 1 AND p.started_at >= ?
GROUP BY a.id ORDER BY plays DESC, MAX(p.started_at) DESC LIMIT ?;

-- name: TopGenres :many
SELECT g.id AS id, g.name AS name, COUNT(*) AS plays
FROM plays p
JOIN track_genres tg ON tg.track_id = p.track_id
JOIN genres g ON g.id = tg.genre_id
WHERE p.completed = 1 AND p.started_at >= ?
GROUP BY g.id ORDER BY plays DESC, MAX(p.started_at) DESC LIMIT ?;
