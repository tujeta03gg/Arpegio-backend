-- +goose Up

-- Physical library
CREATE TABLE library_roots (
  id          TEXT PRIMARY KEY,
  path        TEXT NOT NULL UNIQUE,
  enabled     INTEGER NOT NULL DEFAULT 1,
  last_scan   TEXT
);

CREATE TABLE files (
  id          TEXT PRIMARY KEY,
  root_id     TEXT NOT NULL REFERENCES library_roots(id),
  rel_path    TEXT NOT NULL,
  content_hash TEXT NOT NULL,          -- xxhash64 of the audio
  size_bytes  INTEGER NOT NULL,
  mtime       TEXT NOT NULL,
  format      TEXT NOT NULL,           -- flac, mp3, opus, m4a
  bitrate_kbps INTEGER,
  sample_rate INTEGER,
  bit_depth   INTEGER,
  channels    INTEGER,
  duration_ms INTEGER NOT NULL,
  missing     INTEGER NOT NULL DEFAULT 0,
  UNIQUE (root_id, rel_path)
);
CREATE INDEX idx_files_hash ON files(content_hash);

CREATE TABLE artwork (
  id          TEXT PRIMARY KEY,
  hash        TEXT NOT NULL UNIQUE,
  cache_path  TEXT NOT NULL,
  width       INTEGER,
  height      INTEGER
);

-- Music catalog
CREATE TABLE artists (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  sort_name   TEXT NOT NULL,
  mbid        TEXT,
  UNIQUE (sort_name)
);

CREATE TABLE albums (
  id          TEXT PRIMARY KEY,
  title       TEXT NOT NULL,
  album_artist_id TEXT REFERENCES artists(id),
  year        INTEGER,
  album_type  TEXT,                    -- album, ep, single, compilation
  artwork_id  TEXT REFERENCES artwork(id),
  mbid        TEXT
);

CREATE TABLE tracks (
  id          TEXT PRIMARY KEY,
  file_id     TEXT NOT NULL UNIQUE REFERENCES files(id),
  album_id    TEXT REFERENCES albums(id),
  title       TEXT NOT NULL,
  disc_no     INTEGER,
  track_no    INTEGER,
  duration_ms INTEGER NOT NULL,
  replaygain_track_db REAL,
  replaygain_album_db REAL,
  bpm         INTEGER,
  added_at    TEXT NOT NULL,
  overrides   TEXT                     -- JSON with hand-edited metadata
);

CREATE TABLE track_artists (
  track_id    TEXT NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
  artist_id   TEXT NOT NULL REFERENCES artists(id),
  role        TEXT NOT NULL DEFAULT 'main', -- main, featured, composer
  position    INTEGER NOT NULL,
  PRIMARY KEY (track_id, artist_id, role)
);

CREATE TABLE genres (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL UNIQUE
);

CREATE TABLE track_genres (
  track_id    TEXT NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
  genre_id    TEXT NOT NULL REFERENCES genres(id),
  PRIMARY KEY (track_id, genre_id)
);

CREATE VIRTUAL TABLE search_index USING fts5(
  kind, ref_id UNINDEXED, title, artist, album,
  tokenize = 'unicode61 remove_diacritics 2'
);

-- User collections
CREATE TABLE playlists (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  description TEXT,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

CREATE TABLE playlist_items (
  playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
  position    REAL NOT NULL,           -- REAL allows reordering without renumbering
  track_id    TEXT NOT NULL REFERENCES tracks(id),
  added_at    TEXT NOT NULL,
  PRIMARY KEY (playlist_id, position)
);

CREATE TABLE likes (
  target_kind TEXT NOT NULL,           -- track, album, artist
  target_id   TEXT NOT NULL,
  liked_at    TEXT NOT NULL,
  PRIMARY KEY (target_kind, target_id)
);

-- Playback
CREATE TABLE queue_items (
  position    REAL PRIMARY KEY,
  track_id    TEXT NOT NULL REFERENCES tracks(id),
  source      TEXT NOT NULL,           -- user, album, playlist, radio
  source_id   TEXT,
  priority    INTEGER NOT NULL DEFAULT 0 -- 1 = play next
);

CREATE TABLE queue_state (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  current_pos REAL,
  shuffle     INTEGER NOT NULL DEFAULT 0,
  repeat_mode TEXT NOT NULL DEFAULT 'off', -- off, all, one
  shuffle_seed INTEGER
);

CREATE TABLE player_state (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  track_id    TEXT,
  position_ms INTEGER NOT NULL DEFAULT 0,
  volume      INTEGER NOT NULL DEFAULT 70,
  output      TEXT,
  updated_at  TEXT NOT NULL
);

CREATE TABLE plays (
  id          TEXT PRIMARY KEY,
  track_id    TEXT NOT NULL REFERENCES tracks(id),
  started_at  TEXT NOT NULL,
  listened_ms INTEGER NOT NULL,
  completed   INTEGER NOT NULL,        -- 1 = met the counted-play rule
  skipped     INTEGER NOT NULL,
  source      TEXT NOT NULL
);
CREATE INDEX idx_plays_track ON plays(track_id, started_at);

CREATE TABLE lyrics (
  track_id    TEXT PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,
  synced      INTEGER NOT NULL,
  body        TEXT NOT NULL
);

-- Infrastructure
CREATE TABLE jobs (
  id          TEXT PRIMARY KEY,
  kind        TEXT NOT NULL,           -- ingest_file, extract_art, rebuild_fts
  payload     TEXT NOT NULL,           -- JSON
  status      TEXT NOT NULL,           -- pending, running, done, failed
  attempts    INTEGER NOT NULL DEFAULT 0,
  run_after   TEXT NOT NULL,
  last_error  TEXT
);
CREATE INDEX idx_jobs_ready ON jobs(status, run_after);

-- +goose Down
-- Initial schema is not reversible on purpose: the daemon only migrates forward.
SELECT 1;
