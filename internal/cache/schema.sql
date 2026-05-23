CREATE TABLE IF NOT EXISTS tracks (
  track_key    TEXT PRIMARY KEY,  -- 'sp:<spotify_id>' | 'yt:<youtube_video_id>'
  artist       TEXT NOT NULL,
  title        TEXT NOT NULL,
  album        TEXT NOT NULL DEFAULT '',
  duration_ms  INTEGER NOT NULL,
  file_id      TEXT,
  local_path   TEXT,
  created_at   INTEGER NOT NULL,
  last_used_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tracks_last_used ON tracks(last_used_at);
