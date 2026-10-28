-- name: RadioTracks :many
SELECT t.id AS id, CAST(COALESCE(al.year, 0) AS INTEGER) AS year, CAST(COALESCE(t.bpm, 0) AS INTEGER) AS bpm
FROM tracks t
JOIN files f ON f.id = t.file_id
LEFT JOIN albums al ON al.id = t.album_id
WHERE f.missing = 0;

-- name: RadioTrackArtists :many
SELECT track_id, artist_id FROM track_artists WHERE role = 'main';

-- name: RadioTrackGenres :many
SELECT track_id, genre_id FROM track_genres;

-- name: RadioPlayStats :many
SELECT track_id, COUNT(*) AS plays, CAST(COALESCE(SUM(skipped), 0) AS INTEGER) AS skips,
  CAST(MAX(started_at) AS TEXT) AS last_played
FROM plays GROUP BY track_id;

-- name: RadioSessionPlays :many
SELECT p.started_at AS started_at, ta.artist_id AS artist_id
FROM plays p JOIN track_artists ta ON ta.track_id = p.track_id AND ta.role = 'main'
WHERE p.completed = 1 AND p.started_at >= ?
ORDER BY p.started_at;
