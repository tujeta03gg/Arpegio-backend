-- name: GetArtistBySortName :one
SELECT * FROM artists WHERE sort_name = ?;

-- name: InsertArtist :exec
INSERT INTO artists (id, name, sort_name) VALUES (?, ?, ?);

-- name: GetArtist :one
SELECT * FROM artists WHERE id = ?;

-- name: ListArtists :many
SELECT * FROM artists WHERE id > ? ORDER BY id LIMIT ?;

-- name: FindAlbum :one
SELECT * FROM albums
WHERE title = sqlc.arg(title) COLLATE NOCASE
  AND ((album_artist_id = sqlc.narg(artist_id)) OR (album_artist_id IS NULL AND sqlc.narg(artist_id) IS NULL));

-- name: InsertAlbum :exec
INSERT INTO albums (id, title, album_artist_id, year, album_type) VALUES (?, ?, ?, ?, ?);

-- name: SetAlbumArtwork :exec
UPDATE albums SET artwork_id = ? WHERE id = ? AND artwork_id IS NULL;

-- name: GetAlbum :one
SELECT * FROM albums WHERE id = ?;

-- name: ListAlbums :many
SELECT * FROM albums WHERE id > ? ORDER BY id LIMIT ?;

-- name: ListAlbumsByArtist :many
SELECT * FROM albums WHERE album_artist_id = ? ORDER BY year, title;

-- name: GetGenreByName :one
SELECT * FROM genres WHERE name = ? COLLATE NOCASE;

-- name: InsertGenre :exec
INSERT INTO genres (id, name) VALUES (?, ?);

-- name: ListGenres :many
SELECT * FROM genres ORDER BY name;

-- name: GetTrackIDByFile :one
SELECT id FROM tracks WHERE file_id = ?;

-- name: InsertTrack :exec
INSERT INTO tracks (id, file_id, album_id, title, disc_no, track_no, duration_ms,
  replaygain_track_db, replaygain_album_db, bpm, added_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateTrackFromTags :exec
UPDATE tracks SET album_id = ?, title = ?, disc_no = ?, track_no = ?, duration_ms = ?,
  replaygain_track_db = ?, replaygain_album_db = ?, bpm = ?
WHERE id = ?;

-- name: GetTrackOverrides :one
SELECT overrides FROM tracks WHERE id = ?;

-- name: SetTrackOverrides :exec
UPDATE tracks SET title = ?, disc_no = ?, track_no = ?, overrides = ? WHERE id = ?;

-- name: DeleteTrackArtists :exec
DELETE FROM track_artists WHERE track_id = ?;

-- name: InsertTrackArtist :exec
INSERT INTO track_artists (track_id, artist_id, role, position) VALUES (?, ?, ?, ?);

-- name: DeleteTrackGenres :exec
DELETE FROM track_genres WHERE track_id = ?;

-- name: InsertTrackGenre :exec
INSERT OR IGNORE INTO track_genres (track_id, genre_id) VALUES (?, ?);

-- name: ListTrackArtists :many
SELECT a.id, a.name, ta.role FROM track_artists ta
JOIN artists a ON a.id = ta.artist_id
WHERE ta.track_id = ? ORDER BY ta.position;

-- name: GetTrack :one
SELECT t.id, t.file_id, t.album_id, t.title, t.disc_no, t.track_no, t.duration_ms,
  t.bpm, t.added_at, t.replaygain_track_db, t.replaygain_album_db,
  COALESCE(al.title, '') AS album_title,
  COALESCE(al.artwork_id, '') AS artwork_id,
  f.format AS format,
  CAST(COALESCE(f.bitrate_kbps, 0) AS INTEGER) AS bitrate_kbps,
  CAST(COALESCE(f.sample_rate, 0) AS INTEGER) AS sample_rate,
  CAST(COALESCE(f.bit_depth, 0) AS INTEGER) AS bit_depth,
  f.missing AS missing,
  CAST(COALESCE((SELECT group_concat(a.name, ', ') FROM track_artists ta JOIN artists a ON a.id = ta.artist_id
    WHERE ta.track_id = t.id AND ta.role = 'main'), '') AS TEXT) AS artist_names
FROM tracks t
JOIN files f ON f.id = t.file_id
LEFT JOIN albums al ON al.id = t.album_id
WHERE t.id = ?;

-- name: ListTracks :many
SELECT t.id, t.file_id, t.album_id, t.title, t.disc_no, t.track_no, t.duration_ms,
  t.bpm, t.added_at, t.replaygain_track_db, t.replaygain_album_db,
  COALESCE(al.title, '') AS album_title,
  COALESCE(al.artwork_id, '') AS artwork_id,
  f.format AS format,
  CAST(COALESCE(f.bitrate_kbps, 0) AS INTEGER) AS bitrate_kbps,
  CAST(COALESCE(f.sample_rate, 0) AS INTEGER) AS sample_rate,
  CAST(COALESCE(f.bit_depth, 0) AS INTEGER) AS bit_depth,
  f.missing AS missing,
  CAST(COALESCE((SELECT group_concat(a.name, ', ') FROM track_artists ta JOIN artists a ON a.id = ta.artist_id
    WHERE ta.track_id = t.id AND ta.role = 'main'), '') AS TEXT) AS artist_names
FROM tracks t
JOIN files f ON f.id = t.file_id
LEFT JOIN albums al ON al.id = t.album_id
WHERE t.id > sqlc.arg(after) AND f.missing = 0
ORDER BY t.id LIMIT sqlc.arg(lim);

-- name: ListTracksByAlbum :many
SELECT t.id, t.file_id, t.album_id, t.title, t.disc_no, t.track_no, t.duration_ms,
  t.bpm, t.added_at, t.replaygain_track_db, t.replaygain_album_db,
  COALESCE(al.title, '') AS album_title,
  COALESCE(al.artwork_id, '') AS artwork_id,
  f.format AS format,
  CAST(COALESCE(f.bitrate_kbps, 0) AS INTEGER) AS bitrate_kbps,
  CAST(COALESCE(f.sample_rate, 0) AS INTEGER) AS sample_rate,
  CAST(COALESCE(f.bit_depth, 0) AS INTEGER) AS bit_depth,
  f.missing AS missing,
  CAST(COALESCE((SELECT group_concat(a.name, ', ') FROM track_artists ta JOIN artists a ON a.id = ta.artist_id
    WHERE ta.track_id = t.id AND ta.role = 'main'), '') AS TEXT) AS artist_names
FROM tracks t
JOIN files f ON f.id = t.file_id
LEFT JOIN albums al ON al.id = t.album_id
WHERE t.album_id = ? AND f.missing = 0
ORDER BY COALESCE(t.disc_no, 1), COALESCE(t.track_no, 0), t.title;

-- name: ListTracksByGenre :many
SELECT t.id, t.file_id, t.album_id, t.title, t.disc_no, t.track_no, t.duration_ms,
  t.bpm, t.added_at, t.replaygain_track_db, t.replaygain_album_db,
  COALESCE(al.title, '') AS album_title,
  COALESCE(al.artwork_id, '') AS artwork_id,
  f.format AS format,
  CAST(COALESCE(f.bitrate_kbps, 0) AS INTEGER) AS bitrate_kbps,
  CAST(COALESCE(f.sample_rate, 0) AS INTEGER) AS sample_rate,
  CAST(COALESCE(f.bit_depth, 0) AS INTEGER) AS bit_depth,
  f.missing AS missing,
  CAST(COALESCE((SELECT group_concat(a.name, ', ') FROM track_artists ta JOIN artists a ON a.id = ta.artist_id
    WHERE ta.track_id = t.id AND ta.role = 'main'), '') AS TEXT) AS artist_names
FROM tracks t
JOIN track_genres tg ON tg.track_id = t.id
JOIN files f ON f.id = t.file_id
LEFT JOIN albums al ON al.id = t.album_id
WHERE tg.genre_id = ? AND f.missing = 0
ORDER BY t.title;

-- name: ListTracksByArtist :many
SELECT t.id, t.file_id, t.album_id, t.title, t.disc_no, t.track_no, t.duration_ms,
  t.bpm, t.added_at, t.replaygain_track_db, t.replaygain_album_db,
  COALESCE(al.title, '') AS album_title,
  COALESCE(al.artwork_id, '') AS artwork_id,
  f.format AS format,
  CAST(COALESCE(f.bitrate_kbps, 0) AS INTEGER) AS bitrate_kbps,
  CAST(COALESCE(f.sample_rate, 0) AS INTEGER) AS sample_rate,
  CAST(COALESCE(f.bit_depth, 0) AS INTEGER) AS bit_depth,
  f.missing AS missing,
  CAST(COALESCE((SELECT group_concat(a.name, ', ') FROM track_artists ta JOIN artists a ON a.id = ta.artist_id
    WHERE ta.track_id = t.id AND ta.role = 'main'), '') AS TEXT) AS artist_names
FROM tracks t
JOIN track_artists tar ON tar.track_id = t.id AND tar.role = 'main'
JOIN files f ON f.id = t.file_id
LEFT JOIN albums al ON al.id = t.album_id
WHERE tar.artist_id = ? AND f.missing = 0
ORDER BY al.year, al.title, COALESCE(t.track_no, 0);

-- name: GetTrackFile :one
SELECT f.rel_path, r.path AS root_path, f.format FROM tracks t
JOIN files f ON f.id = t.file_id JOIN library_roots r ON r.id = f.root_id
WHERE t.id = ?;

-- name: DeleteSearchRef :exec
DELETE FROM search_index WHERE kind = ? AND ref_id = ?;

-- name: InsertSearch :exec
INSERT INTO search_index (kind, ref_id, title, artist, album) VALUES (?, ?, ?, ?, ?);


-- name: ListRecentTracks :many
SELECT t.id, t.file_id, t.album_id, t.title, t.disc_no, t.track_no, t.duration_ms,
  t.bpm, t.added_at, t.replaygain_track_db, t.replaygain_album_db,
  COALESCE(al.title, '') AS album_title,
  COALESCE(al.artwork_id, '') AS artwork_id,
  f.format AS format,
  CAST(COALESCE(f.bitrate_kbps, 0) AS INTEGER) AS bitrate_kbps,
  CAST(COALESCE(f.sample_rate, 0) AS INTEGER) AS sample_rate,
  CAST(COALESCE(f.bit_depth, 0) AS INTEGER) AS bit_depth,
  f.missing AS missing,
  CAST(COALESCE((SELECT group_concat(a.name, ', ') FROM track_artists ta JOIN artists a ON a.id = ta.artist_id
    WHERE ta.track_id = t.id AND ta.role = 'main'), '') AS TEXT) AS artist_names
FROM tracks t
JOIN files f ON f.id = t.file_id
LEFT JOIN albums al ON al.id = t.album_id
WHERE f.missing = 0
ORDER BY t.added_at DESC, t.id DESC LIMIT ?;

-- name: CountTracks :one
SELECT COUNT(*) FROM tracks t JOIN files f ON f.id = t.file_id WHERE f.missing = 0;

-- name: CountArtists :one
SELECT COUNT(*) FROM artists;

-- name: CountAlbums :one
SELECT COUNT(*) FROM albums;

-- name: ListRecentAlbums :many
SELECT al.* FROM albums al
JOIN tracks t ON t.album_id = al.id
JOIN files f ON f.id = t.file_id
WHERE f.missing = 0
GROUP BY al.id
ORDER BY MAX(t.added_at) DESC, al.id DESC
LIMIT ?;
