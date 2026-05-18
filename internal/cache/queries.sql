-- name: GetTrack :one
SELECT spotify_id, artist, title, album, duration_ms, file_id, local_path, created_at, last_used_at
FROM tracks
WHERE spotify_id = ?;

-- name: UpsertTrack :exec
INSERT INTO tracks (
  spotify_id, artist, title, album, duration_ms, file_id, local_path, created_at, last_used_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(spotify_id) DO UPDATE SET
  artist       = excluded.artist,
  title        = excluded.title,
  album        = excluded.album,
  duration_ms  = excluded.duration_ms,
  file_id      = COALESCE(excluded.file_id, tracks.file_id),
  local_path   = COALESCE(excluded.local_path, tracks.local_path),
  last_used_at = excluded.last_used_at;

-- name: TouchLastUsed :exec
UPDATE tracks SET last_used_at = ? WHERE spotify_id = ?;

-- name: ListLRUCandidates :many
SELECT spotify_id, local_path
FROM tracks
WHERE local_path IS NOT NULL
ORDER BY last_used_at ASC
LIMIT ?;

-- name: ClearLocalPath :exec
UPDATE tracks SET local_path = NULL WHERE spotify_id = ?;
