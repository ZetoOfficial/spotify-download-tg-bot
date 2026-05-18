# Spotify Download Telegram Bot — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go Telegram bot that accepts a Spotify track URL and replies with the mp3 file (with embedded ID3 + cover), backed by a two-level cache (Telegram `file_id` + local disk via sqlc/SQLite).

**Architecture:** Single Go binary, long-polling Telegram, channel-based worker pool (N=2). Four interface seams (`MetadataResolver`, `AudioSource`, `Cache`, `Uploader`) orchestrated by a `pipeline` package. SQLite via sqlc-generated queries. yt-dlp + ffmpeg shell-outs.

**Tech Stack:**
- Go 1.25.5
- `github.com/go-telegram/bot` — Telegram Bot API client (lightweight, idiomatic)
- `modernc.org/sqlite` — pure-Go SQLite driver (no CGO)
- `github.com/sqlc-dev/sqlc` (CLI, dev tool)
- `log/slog` — stdlib structured logging
- `yt-dlp`, `ffmpeg` — external CLI binaries (in Docker image)

**Spec:** [`docs/superpowers/specs/2026-05-18-spotify-download-tg-bot-design.md`](../specs/2026-05-18-spotify-download-tg-bot-design.md)

---

## File Map

Created during the plan:

| Path | Responsibility |
|---|---|
| `.gitignore`, `.golangci.yml`, `Makefile` | Tooling config |
| `sqlc.yaml` | sqlc codegen config |
| `cmd/bot/main.go` | Entrypoint: env, wiring, graceful shutdown |
| `internal/track/track.go` | `Track` DTO (shared) |
| `internal/bot/parser.go` + `_test.go` | Spotify URL parser |
| `internal/bot/handler.go` | Telegram message handler |
| `internal/cache/schema.sql` | DDL, `//go:embed` |
| `internal/cache/queries.sql` | sqlc query definitions |
| `internal/cache/db/*` | sqlc-generated (do not edit) |
| `internal/cache/cache.go` + `_test.go` | `Cache` interface + SQLite impl |
| `internal/metadata/spotify.go` + `_test.go` | `Resolver` for Spotify Web API |
| `internal/audio/ytdlp.go` + `_test.go` | `Source` impl via yt-dlp |
| `internal/transcode/ffmpeg.go` + `_test.go` | ffmpeg wrapper |
| `internal/uploader/telegram.go` + `_test.go` | `Uploader` impl |
| `internal/queue/queue.go` + `_test.go` | Job queue + worker pool + per-user semaphore |
| `internal/pipeline/pipeline.go` + `_test.go` | Orchestrator |
| `Dockerfile`, `docker-compose.yml`, `.env.example` | Deployment |
| `.github/workflows/ci.yml` | CI |

---

## Task 1: Project bootstrap

**Files:**
- Create: `.gitignore`, `.golangci.yml`, `Makefile`, `.env.example`
- Modify: `go.mod` (add deps)

- [ ] **Step 1: Initialize git repo**

```bash
cd /Users/zeto/go/src/github.com/ZetoOfficial/spotify-download-tg-bot
git init
git add go.mod
git commit -m "chore: initial commit"
```

- [ ] **Step 2: Add `.gitignore`**

Create `.gitignore`:
```
/bot
/bot.db
/bot.db-*
/cache/
/.env
*.test
.DS_Store
coverage.out
```

- [ ] **Step 3: Add `.golangci.yml`**

Create `.golangci.yml`:
```yaml
run:
  timeout: 5m
linters:
  enable:
    - govet
    - errcheck
    - staticcheck
    - ineffassign
    - unused
    - gofmt
    - goimports
    - misspell
    - revive
issues:
  exclude-dirs:
    - internal/cache/db
```

- [ ] **Step 4: Add `Makefile`**

Create `Makefile`:
```makefile
.PHONY: build test test-integration lint sqlc tidy run

build:
	go build -o bot ./cmd/bot

test:
	go test ./...

test-integration:
	go test -tags=integration ./...

lint:
	golangci-lint run

sqlc:
	sqlc generate

tidy:
	go mod tidy

run:
	go run ./cmd/bot
```

- [ ] **Step 5: Add `.env.example`**

Create `.env.example`:
```
TELEGRAM_BOT_TOKEN=
SPOTIFY_CLIENT_ID=
SPOTIFY_CLIENT_SECRET=
WORKERS=2
QUEUE_SIZE=64
CACHE_DIR=./cache
MAX_CACHE_MB=2048
SQLITE_PATH=./bot.db
ALLOWED_USER_IDS=
LOG_LEVEL=info
```

- [ ] **Step 6: Pull runtime deps**

```bash
go get github.com/go-telegram/bot
go get modernc.org/sqlite
go mod tidy
```

- [ ] **Step 7: Verify build (no source yet, should succeed with empty module)**

```bash
go build ./...
```
Expected: succeeds with no output (no Go files yet besides what `go mod tidy` may have created).

- [ ] **Step 8: Commit**

```bash
git add .gitignore .golangci.yml Makefile .env.example go.mod go.sum
git commit -m "chore: tooling config and runtime deps"
```

---

## Task 2: Track DTO

**Files:**
- Create: `internal/track/track.go`

- [ ] **Step 1: Create the DTO**

Create `internal/track/track.go`:
```go
package track

// Track is the shared metadata DTO passed between all pipeline stages.
type Track struct {
	SpotifyID  string
	Artist     string
	Title      string
	Album      string
	ISRC       string
	DurationMs int
	CoverURL   string
}
```

- [ ] **Step 2: Build**

```bash
go build ./internal/track
```
Expected: succeeds.

- [ ] **Step 3: Commit**

```bash
git add internal/track
git commit -m "feat: track DTO"
```

---

## Task 3: Spotify URL parser

**Files:**
- Create: `internal/bot/parser.go`, `internal/bot/parser_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/bot/parser_test.go`:
```go
package bot

import (
	"errors"
	"testing"
)

func TestExtractSpotifyTrackID(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr error
	}{
		{"https full", "https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT", "4cOdK2wGLETKBW3PvgPWqT", nil},
		{"http no scheme", "open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT", "4cOdK2wGLETKBW3PvgPWqT", nil},
		{"with si param", "https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT?si=abc123", "4cOdK2wGLETKBW3PvgPWqT", nil},
		{"with intl segment", "https://open.spotify.com/intl-ru/track/4cOdK2wGLETKBW3PvgPWqT?si=xyz", "4cOdK2wGLETKBW3PvgPWqT", nil},
		{"uri scheme", "spotify:track:4cOdK2wGLETKBW3PvgPWqT", "4cOdK2wGLETKBW3PvgPWqT", nil},
		{"embedded in sentence", "look at this https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT cool huh", "4cOdK2wGLETKBW3PvgPWqT", nil},
		{"empty", "", "", ErrInvalidURL},
		{"random text", "hello world", "", ErrInvalidURL},
		{"playlist not track", "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M", "", ErrInvalidURL},
		{"album not track", "https://open.spotify.com/album/4cOdK2wGLETKBW3PvgPWqT", "", ErrInvalidURL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractSpotifyTrackID(tc.input)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test, confirm fail**

```bash
go test ./internal/bot/
```
Expected: fails — undefined `ExtractSpotifyTrackID`, `ErrInvalidURL`.

- [ ] **Step 3: Implement parser**

Create `internal/bot/parser.go`:
```go
package bot

import (
	"errors"
	"regexp"
)

// ErrInvalidURL is returned when the input has no Spotify track URL/URI.
var ErrInvalidURL = errors.New("invalid spotify track url")

var trackRe = regexp.MustCompile(`(?:open\.spotify\.com(?:/intl-[a-z]{2})?/track/|spotify:track:)([A-Za-z0-9]{22})`)

// ExtractSpotifyTrackID returns the 22-char Spotify track ID found in s.
// Accepts open.spotify.com URLs (with optional scheme, intl-XX segment,
// and ?si=... query) and spotify:track:... URIs.
func ExtractSpotifyTrackID(s string) (string, error) {
	m := trackRe.FindStringSubmatch(s)
	if m == nil {
		return "", ErrInvalidURL
	}
	return m[1], nil
}
```

- [ ] **Step 4: Run tests, confirm pass**

```bash
go test ./internal/bot/ -v
```
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bot
git commit -m "feat(bot): spotify URL parser"
```

---

## Task 4: sqlc setup + cache schema/queries

**Files:**
- Create: `sqlc.yaml`, `internal/cache/schema.sql`, `internal/cache/queries.sql`, `internal/cache/db/*` (generated)

- [ ] **Step 1: Install sqlc**

```bash
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
sqlc version
```
Expected: prints version (e.g. `v1.28.0`).

- [ ] **Step 2: Create `sqlc.yaml`**

```yaml
version: "2"
sql:
  - engine: sqlite
    queries: internal/cache/queries.sql
    schema:  internal/cache/schema.sql
    gen:
      go:
        package: db
        out: internal/cache/db
        sql_package: database/sql
        emit_json_tags: false
        emit_pointers_for_null_types: true
```

- [ ] **Step 3: Create `internal/cache/schema.sql`**

```sql
CREATE TABLE IF NOT EXISTS tracks (
  spotify_id   TEXT PRIMARY KEY,
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
```

- [ ] **Step 4: Create `internal/cache/queries.sql`**

```sql
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
```

- [ ] **Step 5: Generate code**

```bash
sqlc generate
```
Expected: creates `internal/cache/db/db.go`, `models.go`, `queries.sql.go` with no errors.

- [ ] **Step 6: Build to verify generated code compiles**

```bash
go build ./internal/cache/db
```
Expected: succeeds.

- [ ] **Step 7: Commit**

```bash
git add sqlc.yaml internal/cache/schema.sql internal/cache/queries.sql internal/cache/db
git commit -m "feat(cache): sqlc setup and generated queries"
```

---

## Task 5: Cache implementation

**Files:**
- Create: `internal/cache/cache.go`, `internal/cache/cache_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/cache/cache_test.go`:
```go
package cache

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestCache(t *testing.T) (*SQLiteCache, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	c, err := NewSQLiteCache(context.Background(), db, dir, 100) // 100 MB
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}
	return c, dir
}

func TestLookup_Miss(t *testing.T) {
	c, _ := newTestCache(t)
	_, ok, err := c.Lookup(context.Background(), "abc")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if ok {
		t.Fatal("expected miss")
	}
}

func TestSave_ThenLookup_FileIDHit(t *testing.T) {
	c, dir := newTestCache(t)
	ctx := context.Background()
	path := filepath.Join(dir, "x.mp3")
	if err := os.WriteFile(path, []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(ctx, "abc", Entry{FileID: "tgid", LocalPath: path}, "Artist", "Title", "Album", 123000); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := c.Lookup(ctx, "abc")
	if err != nil || !ok {
		t.Fatalf("lookup: %v ok=%v", err, ok)
	}
	if got.FileID != "tgid" {
		t.Errorf("file_id = %q, want tgid", got.FileID)
	}
	if got.LocalPath != path {
		t.Errorf("local_path = %q, want %q", got.LocalPath, path)
	}
}

func TestLookup_PartialHit_LocalPathMissingFile(t *testing.T) {
	c, dir := newTestCache(t)
	ctx := context.Background()
	path := filepath.Join(dir, "ghost.mp3")
	// Don't create the file.
	if err := c.Save(ctx, "abc", Entry{LocalPath: path}, "Artist", "Title", "Album", 123000); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := c.Lookup(ctx, "abc")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !ok {
		t.Fatal("expected hit (row exists)")
	}
	if got.LocalPath != "" {
		t.Errorf("local_path should be cleared when file missing, got %q", got.LocalPath)
	}
}

func TestTouch_UpdatesLastUsed(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()
	if err := c.Save(ctx, "abc", Entry{FileID: "tgid"}, "A", "T", "Al", 100); err != nil {
		t.Fatalf("save: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	before := time.Now().Add(-1 * time.Second).Unix()
	if err := c.Touch(ctx, "abc"); err != nil {
		t.Fatalf("touch: %v", err)
	}
	row, err := c.queries.GetTrack(ctx, "abc")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.LastUsedAt <= before {
		t.Errorf("last_used_at not updated: %d <= %d", row.LastUsedAt, before)
	}
}

func TestLRUEviction_DropsOldest(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// 1 MB cap to trigger eviction.
	c, err := NewSQLiteCache(context.Background(), db, dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Write 3 files of ~512KB each → second save triggers eviction.
	mk := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, make([]byte, 512*1024), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	p1 := mk("a.mp3")
	if err := c.Save(ctx, "a", Entry{LocalPath: p1}, "x", "x", "", 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	p2 := mk("b.mp3")
	if err := c.Save(ctx, "b", Entry{LocalPath: p2}, "x", "x", "", 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	p3 := mk("c.mp3")
	if err := c.Save(ctx, "c", Entry{LocalPath: p3}, "x", "x", "", 0); err != nil {
		t.Fatal(err)
	}
	// After three 512KB saves with 1MB cap, oldest (a) must be evicted.
	if _, err := os.Stat(p1); !os.IsNotExist(err) {
		t.Errorf("expected %s evicted, stat err = %v", p1, err)
	}
	row, err := c.queries.GetTrack(ctx, "a")
	if err != nil {
		t.Fatalf("get a: %v", err)
	}
	if row.LocalPath.Valid {
		t.Errorf("local_path not cleared after eviction: %v", row.LocalPath.String)
	}
}
```

- [ ] **Step 2: Run, confirm fail**

```bash
go test ./internal/cache/
```
Expected: build error — `SQLiteCache`, `NewSQLiteCache`, `Entry` undefined.

- [ ] **Step 3: Implement cache**

Create `internal/cache/cache.go`:
```go
package cache

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/cache/db"
)

//go:embed schema.sql
var schemaSQL string

// Entry is the cache lookup result. Either FileID or LocalPath (or both)
// may be set; an empty struct with ok=false means miss.
type Entry struct {
	FileID    string
	LocalPath string
	ExpiresAt time.Time
}

// Cache is the persistent track cache.
type Cache interface {
	Lookup(ctx context.Context, spotifyID string) (Entry, bool, error)
	Save(ctx context.Context, spotifyID string, e Entry, artist, title, album string, durationMs int) error
	Touch(ctx context.Context, spotifyID string) error
}

// SQLiteCache stores cache state in SQLite via sqlc-generated queries,
// with mp3 files on disk under cacheDir. LRU eviction kicks in on Save
// when the on-disk size exceeds maxCacheMB.
type SQLiteCache struct {
	db         *sql.DB
	queries    *db.Queries
	cacheDir   string
	maxCacheMB int
}

// NewSQLiteCache runs the embedded schema and returns a ready-to-use
// cache. cacheDir must be writable; it will be created if absent.
func NewSQLiteCache(ctx context.Context, sqlDB *sql.DB, cacheDir string, maxCacheMB int) (*SQLiteCache, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir cache: %w", err)
	}
	if _, err := sqlDB.ExecContext(ctx, schemaSQL); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &SQLiteCache{
		db:         sqlDB,
		queries:    db.New(sqlDB),
		cacheDir:   cacheDir,
		maxCacheMB: maxCacheMB,
	}, nil
}

func (c *SQLiteCache) Lookup(ctx context.Context, spotifyID string) (Entry, bool, error) {
	row, err := c.queries.GetTrack(ctx, spotifyID)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	e := Entry{}
	if row.FileID.Valid {
		e.FileID = row.FileID.String
	}
	if row.LocalPath.Valid {
		p := row.LocalPath.String
		if _, statErr := os.Stat(p); statErr == nil {
			e.LocalPath = p
		} else {
			// File gone — clear the column so future lookups don't lie.
			_ = c.queries.ClearLocalPath(ctx, spotifyID)
		}
	}
	return e, true, nil
}

func (c *SQLiteCache) Save(ctx context.Context, spotifyID string, e Entry, artist, title, album string, durationMs int) error {
	now := time.Now().Unix()
	params := db.UpsertTrackParams{
		SpotifyID:   spotifyID,
		Artist:      artist,
		Title:       title,
		Album:       album,
		DurationMs:  int64(durationMs),
		FileID:      nullString(e.FileID),
		LocalPath:   nullString(e.LocalPath),
		CreatedAt:   now,
		LastUsedAt:  now,
	}
	if err := c.queries.UpsertTrack(ctx, params); err != nil {
		return fmt.Errorf("upsert: %w", err)
	}
	return c.maybeEvict(ctx)
}

func (c *SQLiteCache) Touch(ctx context.Context, spotifyID string) error {
	return c.queries.TouchLastUsed(ctx, db.TouchLastUsedParams{
		LastUsedAt: time.Now().Unix(),
		SpotifyID:  spotifyID,
	})
}

func (c *SQLiteCache) maybeEvict(ctx context.Context) error {
	size, err := dirSizeBytes(c.cacheDir)
	if err != nil {
		return fmt.Errorf("dir size: %w", err)
	}
	maxBytes := int64(c.maxCacheMB) * 1024 * 1024
	if size <= maxBytes {
		return nil
	}
	// Evict in batches of 16 until under cap.
	for size > maxBytes {
		cands, err := c.queries.ListLRUCandidates(ctx, 16)
		if err != nil {
			return fmt.Errorf("list lru: %w", err)
		}
		if len(cands) == 0 {
			break
		}
		for _, cand := range cands {
			if !cand.LocalPath.Valid {
				continue
			}
			_ = os.Remove(cand.LocalPath.String)
			if err := c.queries.ClearLocalPath(ctx, cand.SpotifyID); err != nil {
				return fmt.Errorf("clear: %w", err)
			}
		}
		size, err = dirSizeBytes(c.cacheDir)
		if err != nil {
			return err
		}
	}
	return nil
}

func dirSizeBytes(dir string) (int64, error) {
	var total int64
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
```

Note: sqlc with `emit_pointers_for_null_types: true` returns `*string` for nullable cols, but the snippet above assumes `sql.NullString`. **Verify by inspecting `internal/cache/db/models.go`**. If pointers are emitted, swap `Valid`/`String` accesses for `!= nil` and `*p`. (The simpler fix is to set `emit_pointers_for_null_types: false` in `sqlc.yaml` and re-generate — recommended; do that and re-run `sqlc generate` before continuing.)

- [ ] **Step 4: Set `emit_pointers_for_null_types: false` and re-generate**

Edit `sqlc.yaml`: set `emit_pointers_for_null_types: false`. Then:
```bash
sqlc generate
```

- [ ] **Step 5: Run tests, confirm pass**

```bash
go test ./internal/cache/ -v
```
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add sqlc.yaml internal/cache
git commit -m "feat(cache): SQLite cache with LRU eviction"
```

---

## Task 6: Spotify metadata resolver

**Files:**
- Create: `internal/metadata/spotify.go`, `internal/metadata/spotify_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/metadata/spotify_test.go`:
```go
package metadata

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolve_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"tok","expires_in":3600,"token_type":"Bearer"}`))
	})
	mux.HandleFunc("/v1/tracks/abc123", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing bearer: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id":"abc123","name":"Song Title","duration_ms":210000,
			"external_ids":{"isrc":"USRC17607839"},
			"artists":[{"name":"Artist One"},{"name":"Artist Two"}],
			"album":{"name":"Album Name","images":[{"url":"https://img/lg.jpg","height":640},{"url":"https://img/sm.jpg","height":300}]}
		}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	r := NewSpotifyResolver("id", "secret",
		WithTokenURL(srv.URL+"/api/token"),
		WithAPIBase(srv.URL+"/v1"),
	)
	tr, err := r.Resolve(context.Background(), "https://open.spotify.com/track/abc123")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tr.SpotifyID != "abc123" {
		t.Errorf("id %q", tr.SpotifyID)
	}
	if tr.Title != "Song Title" {
		t.Errorf("title %q", tr.Title)
	}
	if tr.Artist != "Artist One, Artist Two" {
		t.Errorf("artist %q", tr.Artist)
	}
	if tr.Album != "Album Name" {
		t.Errorf("album %q", tr.Album)
	}
	if tr.ISRC != "USRC17607839" {
		t.Errorf("isrc %q", tr.ISRC)
	}
	if tr.DurationMs != 210000 {
		t.Errorf("duration %d", tr.DurationMs)
	}
	if tr.CoverURL != "https://img/lg.jpg" {
		t.Errorf("cover %q", tr.CoverURL)
	}
}

func TestResolve_NotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"tok","expires_in":3600,"token_type":"Bearer"}`))
	})
	mux.HandleFunc("/v1/tracks/nope", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	r := NewSpotifyResolver("id", "secret",
		WithTokenURL(srv.URL+"/api/token"),
		WithAPIBase(srv.URL+"/v1"),
	)
	_, err := r.Resolve(context.Background(), "spotify:track:nope")
	if !errors.Is(err, ErrSpotifyNotFound) {
		t.Fatalf("err %v, want ErrSpotifyNotFound", err)
	}
}

func TestResolve_AuthFail(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"invalid_client"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	r := NewSpotifyResolver("bad", "bad",
		WithTokenURL(srv.URL+"/api/token"),
		WithAPIBase(srv.URL+"/v1"),
	)
	_, err := r.Resolve(context.Background(), "spotify:track:abc123")
	if !errors.Is(err, ErrSpotifyAuth) {
		t.Fatalf("err %v, want ErrSpotifyAuth", err)
	}
}

func TestResolve_InvalidInput(t *testing.T) {
	r := NewSpotifyResolver("id", "secret")
	_, err := r.Resolve(context.Background(), "garbage")
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("err %v", err)
	}
}
```

- [ ] **Step 2: Run, confirm fail**

```bash
go test ./internal/metadata/
```
Expected: build error.

- [ ] **Step 3: Implement resolver**

Create `internal/metadata/spotify.go`:
```go
package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/bot"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

var (
	ErrSpotifyNotFound = errors.New("spotify track not found")
	ErrSpotifyAuth     = errors.New("spotify auth failed")
)

// Resolver fetches Track metadata for a Spotify URL.
type Resolver interface {
	Resolve(ctx context.Context, spotifyURL string) (track.Track, error)
}

const (
	defaultTokenURL = "https://accounts.spotify.com/api/token"
	defaultAPIBase  = "https://api.spotify.com/v1"
)

type SpotifyResolver struct {
	clientID, clientSecret string
	tokenURL, apiBase      string
	http                   *http.Client

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

type Option func(*SpotifyResolver)

func WithTokenURL(u string) Option { return func(r *SpotifyResolver) { r.tokenURL = u } }
func WithAPIBase(u string) Option  { return func(r *SpotifyResolver) { r.apiBase = u } }
func WithHTTPClient(c *http.Client) Option {
	return func(r *SpotifyResolver) { r.http = c }
}

func NewSpotifyResolver(clientID, clientSecret string, opts ...Option) *SpotifyResolver {
	r := &SpotifyResolver{
		clientID:     clientID,
		clientSecret: clientSecret,
		tokenURL:     defaultTokenURL,
		apiBase:      defaultAPIBase,
		http:         &http.Client{Timeout: 15 * time.Second},
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

func (r *SpotifyResolver) Resolve(ctx context.Context, spotifyURL string) (track.Track, error) {
	id, err := bot.ExtractSpotifyTrackID(spotifyURL)
	if err != nil {
		return track.Track{}, fmt.Errorf("invalid spotify url: %w", err)
	}
	tok, err := r.getToken(ctx)
	if err != nil {
		return track.Track{}, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", r.apiBase+"/tracks/"+id, nil)
	if err != nil {
		return track.Track{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := r.http.Do(req)
	if err != nil {
		return track.Track{}, fmt.Errorf("spotify track request: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 200:
		// fallthrough
	case 401, 403:
		return track.Track{}, ErrSpotifyAuth
	case 404:
		return track.Track{}, ErrSpotifyNotFound
	default:
		body, _ := io.ReadAll(resp.Body)
		return track.Track{}, fmt.Errorf("spotify status %d: %s", resp.StatusCode, string(body))
	}
	var payload struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		DurationMs  int    `json:"duration_ms"`
		ExternalIDs struct {
			ISRC string `json:"isrc"`
		} `json:"external_ids"`
		Artists []struct {
			Name string `json:"name"`
		} `json:"artists"`
		Album struct {
			Name   string `json:"name"`
			Images []struct {
				URL    string `json:"url"`
				Height int    `json:"height"`
			} `json:"images"`
		} `json:"album"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return track.Track{}, fmt.Errorf("decode track: %w", err)
	}
	names := make([]string, 0, len(payload.Artists))
	for _, a := range payload.Artists {
		names = append(names, a.Name)
	}
	cover := ""
	bestH := -1
	for _, im := range payload.Album.Images {
		if im.Height > bestH {
			bestH = im.Height
			cover = im.URL
		}
	}
	return track.Track{
		SpotifyID:  payload.ID,
		Artist:     strings.Join(names, ", "),
		Title:      payload.Name,
		Album:      payload.Album.Name,
		ISRC:       payload.ExternalIDs.ISRC,
		DurationMs: payload.DurationMs,
		CoverURL:   cover,
	}, nil
}

func (r *SpotifyResolver) getToken(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.token != "" && time.Now().Before(r.expiresAt.Add(-60*time.Second)) {
		return r.token, nil
	}
	body := strings.NewReader(url.Values{"grant_type": {"client_credentials"}}.Encode())
	req, err := http.NewRequestWithContext(ctx, "POST", r.tokenURL, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(r.clientID, r.clientSecret)
	resp, err := r.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 400 {
		return "", ErrSpotifyAuth
	}
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token status %d: %s", resp.StatusCode, string(raw))
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", err
	}
	r.token = tr.AccessToken
	r.expiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return r.token, nil
}
```

- [ ] **Step 4: Run tests, confirm pass**

```bash
go test ./internal/metadata/ -v
```
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/metadata
git commit -m "feat(metadata): Spotify resolver with token cache"
```

---

## Task 7: yt-dlp audio source

**Files:**
- Create: `internal/audio/ytdlp.go`, `internal/audio/ytdlp_test.go`, `internal/audio/ytdlp_integration_test.go`

- [ ] **Step 1: Write unit test (mocked command runner)**

Create `internal/audio/ytdlp_test.go`:
```go
package audio

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

func TestFetch_FirstAttemptMatches(t *testing.T) {
	calls := 0
	s := &YtDlpSource{
		workDir: t.TempDir(),
		exec: func(ctx context.Context, args []string) (stdout, stderr []byte, err error) {
			calls++
			meta := struct {
				ID       string `json:"id"`
				Title    string `json:"title"`
				Duration int    `json:"duration"`
				Ext      string `json:"ext"`
			}{ID: "vid1", Title: "x", Duration: 200, Ext: "m4a"}
			b, _ := json.Marshal(meta)
			// Pretend yt-dlp wrote the file at the templated path.
			return b, nil, nil
		},
		writeFile: func(path string, _ []byte) error {
			// Simulate file presence via stat hook.
			return nil
		},
		stat: func(path string) bool {
			return filepath.Ext(path) == ".m4a"
		},
	}
	tr := track.Track{Artist: "A", Title: "T", DurationMs: 200000}
	p, err := s.Fetch(context.Background(), tr)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
	if filepath.Ext(p) != ".m4a" {
		t.Errorf("got %q", p)
	}
}

func TestFetch_FirstWrongDuration_RetriesAndFails(t *testing.T) {
	calls := 0
	s := &YtDlpSource{
		workDir: t.TempDir(),
		exec: func(ctx context.Context, args []string) (stdout, stderr []byte, err error) {
			calls++
			meta := struct {
				ID       string `json:"id"`
				Duration int    `json:"duration"`
				Ext      string `json:"ext"`
			}{ID: "v", Duration: 500, Ext: "m4a"} // 500s vs expected 200s
			b, _ := json.Marshal(meta)
			return b, nil, nil
		},
		stat: func(path string) bool { return true },
	}
	tr := track.Track{Artist: "A", Title: "T", DurationMs: 200000}
	_, err := s.Fetch(context.Background(), tr)
	if !errors.Is(err, ErrAudioNotFound) {
		t.Fatalf("err %v, want ErrAudioNotFound", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 attempts, got %d", calls)
	}
}
```

- [ ] **Step 2: Run, confirm fail**

```bash
go test ./internal/audio/
```
Expected: build errors.

- [ ] **Step 3: Implement**

Create `internal/audio/ytdlp.go`:
```go
package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

var ErrAudioNotFound = errors.New("audio source: no match")

// Source fetches the raw audio for a Track to a local file.
type Source interface {
	Fetch(ctx context.Context, t track.Track) (rawAudioPath string, err error)
}

// YtDlpSource shells out to yt-dlp. The exec hook is overridable for tests.
type YtDlpSource struct {
	binary    string
	workDir   string
	exec      func(ctx context.Context, args []string) (stdout, stderr []byte, err error)
	writeFile func(path string, data []byte) error
	stat      func(path string) bool
}

func NewYtDlpSource(binary, workDir string) *YtDlpSource {
	return &YtDlpSource{
		binary:  binary,
		workDir: workDir,
		exec:    realExec,
		writeFile: func(p string, b []byte) error {
			return os.WriteFile(p, b, 0o644)
		},
		stat: func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		},
	}
}

func realExec(ctx context.Context, args []string) ([]byte, []byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func (s *YtDlpSource) Fetch(ctx context.Context, t track.Track) (string, error) {
	queries := []string{
		fmt.Sprintf("%s - %s", t.Artist, t.Title),
		fmt.Sprintf("%s %s official audio", t.Artist, t.Title),
	}
	for _, q := range queries {
		p, err := s.tryFetch(ctx, q, t)
		if err == nil {
			return p, nil
		}
		if !errors.Is(err, ErrAudioNotFound) {
			return "", err
		}
	}
	return "", ErrAudioNotFound
}

func (s *YtDlpSource) tryFetch(ctx context.Context, query string, t track.Track) (string, error) {
	outTpl := filepath.Join(s.workDir, t.SpotifyID+".%(ext)s")
	args := []string{
		s.binary,
		"--no-playlist",
		"--default-search", "ytsearch1:",
		"-f", "bestaudio[acodec!=opus]/bestaudio",
		"--print-json",
		"--no-progress",
		"-o", outTpl,
		query,
	}
	stdout, stderr, err := s.exec(ctx, args)
	if err != nil {
		return "", fmt.Errorf("yt-dlp: %w (stderr: %s)", err, string(stderr))
	}
	var meta struct {
		ID       string `json:"id"`
		Duration int    `json:"duration"`
		Ext      string `json:"ext"`
	}
	if err := json.Unmarshal(stdout, &meta); err != nil {
		return "", fmt.Errorf("yt-dlp json: %w", err)
	}
	if math.Abs(float64(meta.Duration*1000-t.DurationMs)) > 5000 {
		return "", ErrAudioNotFound
	}
	path := filepath.Join(s.workDir, t.SpotifyID+"."+meta.Ext)
	if !s.stat(path) {
		return "", fmt.Errorf("yt-dlp output missing: %s", path)
	}
	return path, nil
}
```

- [ ] **Step 4: Add an integration test (build-tagged)**

Create `internal/audio/ytdlp_integration_test.go`:
```go
//go:build integration

package audio

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

func TestFetch_RealYtDlp(t *testing.T) {
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		t.Skip("yt-dlp not installed")
	}
	dir := t.TempDir()
	s := NewYtDlpSource("yt-dlp", dir)
	// Public short clip — adjust if it disappears.
	tr := track.Track{
		SpotifyID:  "test-me-at-the-zoo",
		Artist:     "jawed",
		Title:      "Me at the zoo",
		DurationMs: 19000,
	}
	p, err := s.Fetch(context.Background(), tr)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fi, err := os.Stat(p); err != nil || fi.Size() == 0 {
		t.Fatalf("output bad: %v size=%d", err, fi.Size())
	}
}
```

- [ ] **Step 5: Run unit tests, confirm pass**

```bash
go test ./internal/audio/
```
Expected: PASS (integration test skipped without tag).

- [ ] **Step 6: Commit**

```bash
git add internal/audio
git commit -m "feat(audio): yt-dlp source with duration sanity check"
```

---

## Task 8: ffmpeg transcoder

**Files:**
- Create: `internal/transcode/ffmpeg.go`, `internal/transcode/ffmpeg_test.go`, `internal/transcode/ffmpeg_integration_test.go`

- [ ] **Step 1: Write unit test (verifies argv construction)**

Create `internal/transcode/ffmpeg_test.go`:
```go
package transcode

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

func TestToMP3_ArgvAndOutputPath(t *testing.T) {
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("img-bytes"))
	}))
	defer imgSrv.Close()

	var captured []string
	tc := &FFmpeg{
		binary:  "ffmpeg",
		http:    imgSrv.Client(),
		execRun: func(ctx context.Context, args []string) ([]byte, error) {
			captured = args
			return nil, nil
		},
	}
	dir := t.TempDir()
	raw := filepath.Join(dir, "in.m4a")
	tr := track.Track{
		SpotifyID: "abc",
		Artist:    "A",
		Title:     "T",
		Album:     "Al",
		CoverURL:  imgSrv.URL + "/cover.jpg",
	}
	out, err := tc.ToMP3(context.Background(), raw, tr, dir)
	if err != nil {
		t.Fatalf("to mp3: %v", err)
	}
	if out != filepath.Join(dir, "abc.mp3") {
		t.Errorf("out %q", out)
	}
	joined := strings.Join(captured, " ")
	for _, want := range []string{
		"-i", raw, "-c:a", "libmp3lame", "-b:a", "320k",
		"-id3v2_version", "3",
		"-metadata", "title=T",
		"-metadata", "artist=A",
		"-metadata", "album=Al",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q in: %s", want, joined)
		}
	}
}

func TestToMP3_CoverFetchFailDoesNotFail(t *testing.T) {
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer badSrv.Close()
	tc := &FFmpeg{
		binary:  "ffmpeg",
		http:    badSrv.Client(),
		execRun: func(ctx context.Context, args []string) ([]byte, error) { return nil, nil },
	}
	dir := t.TempDir()
	tr := track.Track{SpotifyID: "abc", CoverURL: badSrv.URL + "/x.jpg"}
	if _, err := tc.ToMP3(context.Background(), filepath.Join(dir, "in.m4a"), tr, dir); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
}

func TestToMP3_ExecFails(t *testing.T) {
	tc := &FFmpeg{
		binary:  "ffmpeg",
		http:    http.DefaultClient,
		execRun: func(ctx context.Context, args []string) ([]byte, error) { return []byte("bad"), errors.New("boom") },
	}
	dir := t.TempDir()
	_, err := tc.ToMP3(context.Background(), filepath.Join(dir, "in.m4a"), track.Track{SpotifyID: "x"}, dir)
	if !errors.Is(err, ErrTranscode) {
		t.Fatalf("err %v, want ErrTranscode", err)
	}
}
```

- [ ] **Step 2: Run, confirm fail**

```bash
go test ./internal/transcode/
```
Expected: build error.

- [ ] **Step 3: Implement**

Create `internal/transcode/ffmpeg.go`:
```go
package transcode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

var ErrTranscode = errors.New("transcode failed")

type FFmpeg struct {
	binary  string
	http    *http.Client
	execRun func(ctx context.Context, args []string) ([]byte, error)
}

func NewFFmpeg(binary string) *FFmpeg {
	return &FFmpeg{
		binary: binary,
		http:   &http.Client{Timeout: 15 * time.Second},
		execRun: func(ctx context.Context, args []string) ([]byte, error) {
			var stderr bytes.Buffer
			cmd := exec.CommandContext(ctx, args[0], args[1:]...)
			cmd.Stderr = &stderr
			err := cmd.Run()
			return stderr.Bytes(), err
		},
	}
}

// ToMP3 transcodes raw into outDir/<SpotifyID>.mp3 with ID3 + cover.
// Cover fetch is best-effort: a failure logs nothing and proceeds.
func (f *FFmpeg) ToMP3(ctx context.Context, raw string, t track.Track, outDir string) (string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	out := filepath.Join(outDir, t.SpotifyID+".mp3")

	args := []string{f.binary, "-y", "-i", raw}
	coverPath := ""
	if t.CoverURL != "" {
		if p, err := f.downloadCover(ctx, t); err == nil {
			coverPath = p
			defer os.Remove(coverPath)
			args = append(args, "-i", coverPath, "-map", "0:a", "-map", "1:v", "-disposition:v:0", "attached_pic")
		}
	}
	args = append(args,
		"-vn", "-c:a", "libmp3lame", "-b:a", "320k",
		"-id3v2_version", "3",
		"-metadata", "title="+t.Title,
		"-metadata", "artist="+t.Artist,
		"-metadata", "album="+t.Album,
		out,
	)
	if stderr, err := f.execRun(ctx, args); err != nil {
		return "", fmt.Errorf("%w: %v (stderr: %s)", ErrTranscode, err, string(stderr))
	}
	return out, nil
}

func (f *FFmpeg) downloadCover(ctx context.Context, t track.Track) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", t.CoverURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := f.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("cover status %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp("", "cover-*.jpg")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	tmp.Close()
	return tmp.Name(), nil
}
```

- [ ] **Step 4: Add integration test (build-tagged)**

Create `internal/transcode/ffmpeg_integration_test.go`:
```go
//go:build integration

package transcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

func TestToMP3_RealFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	// Generate 1-second silent WAV with ffmpeg itself.
	wav := filepath.Join(dir, "in.wav")
	if err := exec.Command("ffmpeg", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=stereo", "-t", "1", wav).Run(); err != nil {
		t.Fatalf("gen wav: %v", err)
	}
	f := NewFFmpeg("ffmpeg")
	out, err := f.ToMP3(context.Background(), wav, track.Track{SpotifyID: "x", Title: "t", Artist: "a"}, dir)
	if err != nil {
		t.Fatalf("transcode: %v", err)
	}
	fi, err := os.Stat(out)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("out bad: %v size=%d", err, fi.Size())
	}
}
```

- [ ] **Step 5: Run unit tests**

```bash
go test ./internal/transcode/ -v
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/transcode
git commit -m "feat(transcode): ffmpeg wrapper with cover embed"
```

---

## Task 9: Telegram uploader

**Files:**
- Create: `internal/uploader/telegram.go`, `internal/uploader/telegram_test.go`

The `go-telegram/bot` library hides the HTTP wire, but our `Uploader` interface lets us mock the bot at the pipeline boundary. The uploader itself wraps `bot.Bot.SendAudio`. Tests verify only the retry-on-5xx logic via a stub `audioSender` type.

- [ ] **Step 1: Write failing test**

Create `internal/uploader/telegram_test.go`:
```go
package uploader

import (
	"context"
	"errors"
	"testing"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

func TestUpload_RetriesOn5xx(t *testing.T) {
	attempts := 0
	stub := func(ctx context.Context, chatID int64, path string, t track.Track) (string, error) {
		attempts++
		if attempts < 3 {
			return "", &transientError{}
		}
		return "fileid-ok", nil
	}
	u := &TelegramUploader{send: stub, backoff: 0}
	id, err := u.Upload(context.Background(), 1, "/tmp/x.mp3", track.Track{})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if id != "fileid-ok" {
		t.Errorf("file id %q", id)
	}
	if attempts != 3 {
		t.Errorf("attempts %d", attempts)
	}
}

func TestUpload_GivesUpAfterRetries(t *testing.T) {
	stub := func(ctx context.Context, chatID int64, path string, t track.Track) (string, error) {
		return "", &transientError{}
	}
	u := &TelegramUploader{send: stub, backoff: 0}
	_, err := u.Upload(context.Background(), 1, "/tmp/x.mp3", track.Track{})
	if !errors.Is(err, ErrUpload) {
		t.Fatalf("err %v, want ErrUpload", err)
	}
}

type transientError struct{}

func (t *transientError) Error() string { return "telegram 502" }
func (t *transientError) Transient() bool { return true }
```

- [ ] **Step 2: Run, confirm fail**

```bash
go test ./internal/uploader/
```

- [ ] **Step 3: Implement**

Create `internal/uploader/telegram.go`:
```go
package uploader

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

var ErrUpload = errors.New("upload failed after retries")

// Uploader sends audio to Telegram and returns a reusable file_id.
type Uploader interface {
	Upload(ctx context.Context, chatID int64, mp3Path string, t track.Track) (fileID string, err error)
	SendCached(ctx context.Context, chatID int64, fileID string) error
}

type sendFn func(ctx context.Context, chatID int64, path string, t track.Track) (string, error)

type TelegramUploader struct {
	b       *bot.Bot
	send    sendFn
	backoff time.Duration
}

func NewTelegramUploader(b *bot.Bot) *TelegramUploader {
	u := &TelegramUploader{b: b, backoff: time.Second}
	u.send = u.realSend
	return u
}

func (u *TelegramUploader) Upload(ctx context.Context, chatID int64, path string, t track.Track) (string, error) {
	var lastErr error
	delay := u.backoff
	for attempt := 1; attempt <= 3; attempt++ {
		id, err := u.send(ctx, chatID, path, t)
		if err == nil {
			return id, nil
		}
		lastErr = err
		if !isTransient(err) {
			return "", fmt.Errorf("%w: %v", ErrUpload, err)
		}
		if attempt < 3 && delay > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
			delay *= 4
		}
	}
	return "", fmt.Errorf("%w: %v", ErrUpload, lastErr)
}

func (u *TelegramUploader) SendCached(ctx context.Context, chatID int64, fileID string) error {
	_, err := u.b.SendAudio(ctx, &bot.SendAudioParams{
		ChatID: chatID,
		Audio:  &models.InputFileString{Data: fileID},
	})
	return err
}

func (u *TelegramUploader) realSend(ctx context.Context, chatID int64, path string, t track.Track) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	msg, err := u.b.SendAudio(ctx, &bot.SendAudioParams{
		ChatID:    chatID,
		Audio:     &models.InputFileUpload{Filename: t.Title + ".mp3", Data: f},
		Title:     t.Title,
		Performer: t.Artist,
		Duration:  t.DurationMs / 1000,
	})
	if err != nil {
		return "", err
	}
	if msg.Audio == nil {
		return "", fmt.Errorf("telegram did not return audio")
	}
	return msg.Audio.FileID, nil
}

type transient interface{ Transient() bool }

func isTransient(err error) bool {
	var t transient
	if errors.As(err, &t) {
		return t.Transient()
	}
	s := err.Error()
	// Heuristic for go-telegram/bot lib which returns string-based errors.
	return strings.Contains(s, "502") || strings.Contains(s, "503") ||
		strings.Contains(s, "504") || strings.Contains(s, "timeout")
}
```

- [ ] **Step 4: Run, confirm pass**

```bash
go test ./internal/uploader/ -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/uploader
git commit -m "feat(uploader): telegram audio uploader with retry"
```

---

## Task 10: Job queue + worker pool + per-user semaphore

**Files:**
- Create: `internal/queue/queue.go`, `internal/queue/queue_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/queue/queue_test.go`:
```go
package queue

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnqueue_AndProcess(t *testing.T) {
	var processed atomic.Int64
	q := New(4, 2, func(ctx context.Context, j Job) {
		processed.Add(1)
	})
	q.Start()
	defer q.Stop(context.Background())
	for i := 0; i < 5; i++ {
		if !q.Enqueue(Job{ChatID: int64(i), SpotifyID: "id"}) {
			t.Fatalf("enqueue %d", i)
		}
	}
	deadline := time.Now().Add(time.Second)
	for processed.Load() < 5 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processed.Load() != 5 {
		t.Fatalf("processed = %d", processed.Load())
	}
}

func TestPerUserSemaphore_RejectsConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	q := New(4, 2, func(ctx context.Context, j Job) {
		wg.Wait()
	})
	q.Start()
	defer func() {
		wg.Done()
		q.Stop(context.Background())
	}()
	if !q.TryAcquireUser(42) {
		t.Fatal("first acquire")
	}
	if q.TryAcquireUser(42) {
		t.Fatal("second acquire should fail")
	}
	q.ReleaseUser(42)
	if !q.TryAcquireUser(42) {
		t.Fatal("re-acquire after release")
	}
}

func TestEnqueue_FullReturnsFalse(t *testing.T) {
	block := make(chan struct{})
	q := New(1, 1, func(ctx context.Context, j Job) { <-block })
	q.Start()
	defer func() { close(block); q.Stop(context.Background()) }()
	// First job is picked up by the worker, second sits in the 1-slot buffer,
	// third must be rejected.
	q.Enqueue(Job{ChatID: 1})
	q.Enqueue(Job{ChatID: 2})
	if q.Enqueue(Job{ChatID: 3}) {
		t.Fatal("expected full")
	}
}
```

- [ ] **Step 2: Run, confirm fail**

```bash
go test ./internal/queue/
```

- [ ] **Step 3: Implement**

Create `internal/queue/queue.go`:
```go
package queue

import (
	"context"
	"sync"
	"time"
)

// Job is the unit of work dispatched from the bot handler.
type Job struct {
	ChatID         int64
	UserID         int64
	SpotifyURL     string
	SpotifyID      string
	ReplyMessageID int
}

// Handler processes a Job. It must respect ctx cancellation.
type Handler func(ctx context.Context, j Job)

type Queue struct {
	ch       chan Job
	workers  int
	handler  Handler
	wg       sync.WaitGroup
	cancel   context.CancelFunc
	rootCtx  context.Context
	stopOnce sync.Once

	mu    sync.Mutex
	locks map[int64]struct{}
}

func New(buffer, workers int, h Handler) *Queue {
	return &Queue{
		ch:      make(chan Job, buffer),
		workers: workers,
		handler: h,
		locks:   make(map[int64]struct{}),
	}
}

func (q *Queue) Start() {
	q.rootCtx, q.cancel = context.WithCancel(context.Background())
	for i := 0; i < q.workers; i++ {
		q.wg.Add(1)
		go q.workLoop()
	}
}

func (q *Queue) workLoop() {
	defer q.wg.Done()
	for {
		select {
		case <-q.rootCtx.Done():
			return
		case j, ok := <-q.ch:
			if !ok {
				return
			}
			q.handler(q.rootCtx, j)
			if j.UserID != 0 {
				q.ReleaseUser(j.UserID)
			}
		}
	}
}

// Enqueue tries to insert a job; returns false if the queue is full.
func (q *Queue) Enqueue(j Job) bool {
	select {
	case q.ch <- j:
		return true
	default:
		return false
	}
}

// TryAcquireUser is a non-blocking attempt to claim the per-user slot.
func (q *Queue) TryAcquireUser(userID int64) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, busy := q.locks[userID]; busy {
		return false
	}
	q.locks[userID] = struct{}{}
	return true
}

func (q *Queue) ReleaseUser(userID int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.locks, userID)
}

// Stop drains and shuts down within ctx deadline (caller usually passes a 30s timeout).
func (q *Queue) Stop(ctx context.Context) {
	q.stopOnce.Do(func() {
		close(q.ch)
		done := make(chan struct{})
		go func() { q.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-ctx.Done():
			q.cancel()
			<-done
		}
	})
	_ = time.Second // suppress unused import if go decides
}
```

- [ ] **Step 4: Tidy import**

Remove the unused `time` import — replace the body of `Stop` with the version above and drop `import "time"` if `go vet` complains. Verify with:
```bash
go vet ./internal/queue/
```

If `time` is unused after final code, edit `internal/queue/queue.go` to remove it.

- [ ] **Step 5: Run tests, confirm pass**

```bash
go test ./internal/queue/ -v -race
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/queue
git commit -m "feat(queue): worker pool with per-user semaphore"
```

---

## Task 11: Pipeline orchestrator

**Files:**
- Create: `internal/pipeline/pipeline.go`, `internal/pipeline/pipeline_test.go`

- [ ] **Step 1: Write failing tests with fakes**

Create `internal/pipeline/pipeline_test.go`:
```go
package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/audio"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/cache"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/metadata"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

type fakeResolver struct {
	tr  track.Track
	err error
}

func (f *fakeResolver) Resolve(ctx context.Context, url string) (track.Track, error) {
	return f.tr, f.err
}

type fakeCache struct {
	entry  cache.Entry
	hit    bool
	saved  []cache.Entry
	touch  int
}

func (f *fakeCache) Lookup(ctx context.Context, id string) (cache.Entry, bool, error) {
	return f.entry, f.hit, nil
}
func (f *fakeCache) Save(ctx context.Context, id string, e cache.Entry, artist, title, album string, dur int) error {
	f.saved = append(f.saved, e)
	return nil
}
func (f *fakeCache) Touch(ctx context.Context, id string) error {
	f.touch++
	return nil
}

type fakeAudio struct {
	path string
	err  error
}

func (f *fakeAudio) Fetch(ctx context.Context, t track.Track) (string, error) {
	return f.path, f.err
}

type fakeTranscoder struct{ err error }

func (f *fakeTranscoder) ToMP3(ctx context.Context, raw string, t track.Track, dir string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "/tmp/" + t.SpotifyID + ".mp3", nil
}

type fakeUploader struct {
	fileID    string
	uploaded  []string
	sent      []string
	uploadErr error
}

func (f *fakeUploader) Upload(ctx context.Context, chatID int64, p string, t track.Track) (string, error) {
	f.uploaded = append(f.uploaded, p)
	return f.fileID, f.uploadErr
}
func (f *fakeUploader) SendCached(ctx context.Context, chatID int64, fileID string) error {
	f.sent = append(f.sent, fileID)
	return nil
}

type fakeNotifier struct {
	progress []string
	done     []string
	errs     []string
}

func (f *fakeNotifier) Progress(chatID int64, msgID int, text string)  { f.progress = append(f.progress, text) }
func (f *fakeNotifier) Done(chatID int64, msgID int)                   { f.done = append(f.done, "done") }
func (f *fakeNotifier) Error(chatID int64, msgID int, userMessage string) { f.errs = append(f.errs, userMessage) }

func newPipeline(r *fakeResolver, c *fakeCache, a *fakeAudio, tc *fakeTranscoder, u *fakeUploader, n *fakeNotifier) *Pipeline {
	return &Pipeline{
		Resolver:    r,
		Cache:       c,
		Audio:       a,
		Transcoder:  tc,
		Uploader:    u,
		Notifier:    n,
		CacheDir:    "/tmp",
	}
}

func TestPipeline_CacheHitWithFileID_SendsCached(t *testing.T) {
	c := &fakeCache{entry: cache.Entry{FileID: "fid"}, hit: true}
	n := &fakeNotifier{}
	p := newPipeline(
		&fakeResolver{tr: track.Track{SpotifyID: "x"}},
		c,
		&fakeAudio{}, &fakeTranscoder{}, &fakeUploader{}, n,
	)
	p.Process(context.Background(), Job{ChatID: 1, SpotifyID: "x", SpotifyURL: "u"})
	if c.touch == 0 {
		t.Error("expected Touch on hit")
	}
	if len(n.done) != 1 {
		t.Errorf("done events: %v", n.done)
	}
}

func TestPipeline_FullPath_FetchTranscodeUpload(t *testing.T) {
	c := &fakeCache{hit: false}
	u := &fakeUploader{fileID: "newfid"}
	n := &fakeNotifier{}
	p := newPipeline(
		&fakeResolver{tr: track.Track{SpotifyID: "x", DurationMs: 100000}},
		c, &fakeAudio{path: "/tmp/raw.m4a"}, &fakeTranscoder{}, u, n,
	)
	p.Process(context.Background(), Job{ChatID: 1, SpotifyID: "x", SpotifyURL: "u"})
	if len(u.uploaded) != 1 {
		t.Errorf("uploads: %v", u.uploaded)
	}
	if len(c.saved) != 1 || c.saved[0].FileID != "newfid" {
		t.Errorf("saved: %+v", c.saved)
	}
}

func TestPipeline_ResolverNotFound_EditsErrorReply(t *testing.T) {
	n := &fakeNotifier{}
	p := newPipeline(
		&fakeResolver{err: metadata.ErrSpotifyNotFound},
		&fakeCache{}, &fakeAudio{}, &fakeTranscoder{}, &fakeUploader{}, n,
	)
	p.Process(context.Background(), Job{ChatID: 1, SpotifyID: "x", SpotifyURL: "u"})
	if len(n.errs) != 1 {
		t.Fatalf("errs: %v", n.errs)
	}
}

func TestPipeline_AudioNotFound_EditsErrorReply(t *testing.T) {
	n := &fakeNotifier{}
	p := newPipeline(
		&fakeResolver{tr: track.Track{SpotifyID: "x"}},
		&fakeCache{}, &fakeAudio{err: audio.ErrAudioNotFound},
		&fakeTranscoder{}, &fakeUploader{}, n,
	)
	p.Process(context.Background(), Job{ChatID: 1, SpotifyID: "x", SpotifyURL: "u"})
	if len(n.errs) != 1 {
		t.Fatalf("errs: %v", n.errs)
	}
}

func TestPipeline_TranscodeError_Propagates(t *testing.T) {
	n := &fakeNotifier{}
	p := newPipeline(
		&fakeResolver{tr: track.Track{SpotifyID: "x"}},
		&fakeCache{}, &fakeAudio{path: "/tmp/raw"},
		&fakeTranscoder{err: errors.New("boom")},
		&fakeUploader{}, n,
	)
	p.Process(context.Background(), Job{ChatID: 1, SpotifyID: "x", SpotifyURL: "u"})
	if len(n.errs) != 1 {
		t.Fatalf("errs: %v", n.errs)
	}
}
```

- [ ] **Step 2: Run, confirm fail**

```bash
go test ./internal/pipeline/
```

- [ ] **Step 3: Implement**

Create `internal/pipeline/pipeline.go`:
```go
package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/audio"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/cache"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/metadata"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/transcode"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/uploader"
)

// Job mirrors queue.Job; redefined here to avoid an import cycle with the
// queue package (queue holds a Handler that calls into Pipeline.Process).
type Job struct {
	ChatID         int64
	UserID         int64
	SpotifyURL     string
	SpotifyID      string
	ReplyMessageID int
}

// Notifier edits the "⏳ качаю…" reply with progress / success / error.
type Notifier interface {
	Progress(chatID int64, msgID int, text string)
	Done(chatID int64, msgID int)
	Error(chatID int64, msgID int, userMessage string)
}

// Transcoder isolates the transcode package for testing.
type Transcoder interface {
	ToMP3(ctx context.Context, raw string, t track.Track, outDir string) (string, error)
}

type Pipeline struct {
	Resolver   metadata.Resolver
	Cache      cache.Cache
	Audio      audio.Source
	Transcoder Transcoder
	Uploader   uploader.Uploader
	Notifier   Notifier
	CacheDir   string
	Logger     *slog.Logger
}

func (p *Pipeline) Process(ctx context.Context, j Job) {
	log := p.logger().With("chat_id", j.ChatID, "spotify_id", j.SpotifyID)
	start := time.Now()
	defer func() {
		log.Info("job complete", "duration_ms", time.Since(start).Milliseconds())
	}()

	tr, err := p.Resolver.Resolve(ctx, j.SpotifyURL)
	if err != nil {
		p.handleResolverErr(j, err, log)
		return
	}
	log = log.With("artist", tr.Artist, "title", tr.Title)

	if entry, hit, _ := p.Cache.Lookup(ctx, j.SpotifyID); hit {
		if entry.FileID != "" {
			if err := p.Uploader.SendCached(ctx, j.ChatID, entry.FileID); err == nil {
				_ = p.Cache.Touch(ctx, j.SpotifyID)
				p.Notifier.Done(j.ChatID, j.ReplyMessageID)
				return
			}
		}
		if entry.LocalPath != "" {
			fileID, err := p.Uploader.Upload(ctx, j.ChatID, entry.LocalPath, tr)
			if err == nil {
				_ = p.Cache.Save(ctx, j.SpotifyID, cache.Entry{FileID: fileID, LocalPath: entry.LocalPath}, tr.Artist, tr.Title, tr.Album, tr.DurationMs)
				p.Notifier.Done(j.ChatID, j.ReplyMessageID)
				return
			}
		}
	}

	p.Notifier.Progress(j.ChatID, j.ReplyMessageID, "⏳ ищу аудио…")
	raw, err := p.Audio.Fetch(ctx, tr)
	if err != nil {
		p.handleAudioErr(j, err, log)
		return
	}
	defer os.Remove(raw)

	p.Notifier.Progress(j.ChatID, j.ReplyMessageID, "⏳ конвертирую…")
	mp3, err := p.Transcoder.ToMP3(ctx, raw, tr, p.CacheDir)
	if err != nil {
		log.Error("transcode failed", "err", err)
		p.Notifier.Error(j.ChatID, j.ReplyMessageID, "ошибка конвертации")
		return
	}

	p.Notifier.Progress(j.ChatID, j.ReplyMessageID, "⏳ отправляю…")
	fileID, err := p.Uploader.Upload(ctx, j.ChatID, mp3, tr)
	if err != nil {
		log.Error("upload failed", "err", err)
		p.Notifier.Error(j.ChatID, j.ReplyMessageID, "telegram отвалился, попробуй ещё раз")
		return
	}
	if err := p.Cache.Save(ctx, j.SpotifyID, cache.Entry{FileID: fileID, LocalPath: mp3}, tr.Artist, tr.Title, tr.Album, tr.DurationMs); err != nil {
		log.Warn("cache save failed", "err", err)
	}
	p.Notifier.Done(j.ChatID, j.ReplyMessageID)
}

func (p *Pipeline) logger() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}

func (p *Pipeline) handleResolverErr(j Job, err error, log *slog.Logger) {
	switch {
	case errors.Is(err, metadata.ErrSpotifyNotFound):
		p.Notifier.Error(j.ChatID, j.ReplyMessageID, "трек не найден в Spotify")
	case errors.Is(err, metadata.ErrSpotifyAuth):
		log.Error("spotify auth failed", "err", err)
		p.Notifier.Error(j.ChatID, j.ReplyMessageID, "сервис недоступен, напиши админу")
	default:
		log.Error("resolve failed", "err", err)
		p.Notifier.Error(j.ChatID, j.ReplyMessageID, "не получилось обработать ссылку")
	}
}

func (p *Pipeline) handleAudioErr(j Job, err error, log *slog.Logger) {
	if errors.Is(err, audio.ErrAudioNotFound) {
		p.Notifier.Error(j.ChatID, j.ReplyMessageID, "не получилось скачать аудио")
		return
	}
	log.Error("audio fetch failed", "err", err)
	p.Notifier.Error(j.ChatID, j.ReplyMessageID, "не получилось скачать аудио")
}
```

- [ ] **Step 4: Run, confirm pass**

```bash
go test ./internal/pipeline/ -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/pipeline
git commit -m "feat(pipeline): orchestrator wiring all four seams"
```

---

## Task 12: Bot handler + main wiring

**Files:**
- Create: `internal/bot/handler.go`, `cmd/bot/main.go`

- [ ] **Step 1: Implement handler**

Create `internal/bot/handler.go`:
```go
package bot

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/pipeline"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/queue"
)

type Deps struct {
	Queue        *queue.Queue
	AllowedUsers map[int64]struct{} // empty = allow all
	Logger       *slog.Logger
}

// Notifier implements pipeline.Notifier on top of *bot.Bot.
type Notifier struct {
	B *bot.Bot
}

func (n *Notifier) Progress(chatID int64, msgID int, text string) {
	if msgID == 0 {
		return
	}
	_, _ = n.B.EditMessageText(context.Background(), &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: msgID,
		Text:      text,
	})
}

func (n *Notifier) Done(chatID int64, msgID int) {
	if msgID == 0 {
		return
	}
	_, _ = n.B.DeleteMessage(context.Background(), &bot.DeleteMessageParams{
		ChatID:    chatID,
		MessageID: msgID,
	})
}

func (n *Notifier) Error(chatID int64, msgID int, userMessage string) {
	if msgID == 0 {
		_, _ = n.B.SendMessage(context.Background(), &bot.SendMessageParams{
			ChatID: chatID,
			Text:   userMessage,
		})
		return
	}
	_, _ = n.B.EditMessageText(context.Background(), &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: msgID,
		Text:      "❌ " + userMessage,
	})
}

func Handler(d Deps) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if update.Message == nil || update.Message.Text == "" {
			return
		}
		chatID := update.Message.Chat.ID
		userID := update.Message.From.ID
		text := update.Message.Text

		if strings.HasPrefix(text, "/start") {
			_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: chatID,
				Text:   "Пришли ссылку на трек Spotify — отвечу mp3.",
			})
			return
		}

		if len(d.AllowedUsers) > 0 {
			if _, ok := d.AllowedUsers[userID]; !ok {
				d.Logger.Info("denied", "user_id", userID)
				return
			}
		}

		id, err := ExtractSpotifyTrackID(text)
		if err != nil {
			_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: chatID,
				Text:   "пришли ссылку на трек Spotify",
			})
			return
		}

		if !d.Queue.TryAcquireUser(userID) {
			_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: chatID,
				Text:   "жди, твой прошлый трек ещё качается",
			})
			return
		}

		reply, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   "⏳ качаю…",
		})
		var replyID int
		if err == nil && reply != nil {
			replyID = reply.ID
		}

		ok := d.Queue.Enqueue(queue.Job{
			ChatID:         chatID,
			UserID:         userID,
			SpotifyURL:     text,
			SpotifyID:      id,
			ReplyMessageID: replyID,
		})
		if !ok {
			d.Queue.ReleaseUser(userID)
			_, _ = b.EditMessageText(ctx, &bot.EditMessageTextParams{
				ChatID:    chatID,
				MessageID: replyID,
				Text:      "очередь переполнена, попробуй позже",
			})
		}
	}
}

func ParseAllowedUsers(raw string) map[int64]struct{} {
	out := make(map[int64]struct{})
	for _, part := range strings.Split(raw, ",") {
		s := strings.TrimSpace(part)
		if s == "" {
			continue
		}
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			continue
		}
		out[id] = struct{}{}
	}
	return out
}

// Avoid unused import on builds where pipeline isn't referenced — silenced
// by importing for side-effect via a typed nil.
var _ pipeline.Notifier = (*Notifier)(nil)
```

- [ ] **Step 2: Implement `cmd/bot/main.go`**

Create `cmd/bot/main.go`:
```go
package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	tgbot "github.com/go-telegram/bot"
	_ "modernc.org/sqlite"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/audio"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/bot"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/cache"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/metadata"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/pipeline"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/queue"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/transcode"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/uploader"
)

func envStr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func main() {
	level := slog.LevelInfo
	switch envStr("LOG_LEVEL", "info") {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		logger.Error("TELEGRAM_BOT_TOKEN required")
		os.Exit(1)
	}
	spotifyID := os.Getenv("SPOTIFY_CLIENT_ID")
	spotifySecret := os.Getenv("SPOTIFY_CLIENT_SECRET")
	if spotifyID == "" || spotifySecret == "" {
		logger.Error("SPOTIFY_CLIENT_ID/SECRET required")
		os.Exit(1)
	}

	rootCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	sqlDB, err := sql.Open("sqlite", envStr("SQLITE_PATH", "./bot.db"))
	if err != nil {
		logger.Error("open sqlite", "err", err)
		os.Exit(1)
	}
	defer sqlDB.Close()
	c, err := cache.NewSQLiteCache(rootCtx, sqlDB, envStr("CACHE_DIR", "./cache"), envInt("MAX_CACHE_MB", 2048))
	if err != nil {
		logger.Error("cache init", "err", err)
		os.Exit(1)
	}

	res := metadata.NewSpotifyResolver(spotifyID, spotifySecret)
	ytdlp := audio.NewYtDlpSource(envStr("YTDLP_BIN", "yt-dlp"), os.TempDir())
	ff := transcode.NewFFmpeg(envStr("FFMPEG_BIN", "ffmpeg"))

	b, err := tgbot.New(token)
	if err != nil {
		logger.Error("bot init", "err", err)
		os.Exit(1)
	}
	notifier := &bot.Notifier{B: b}
	up := uploader.NewTelegramUploader(b)

	p := &pipeline.Pipeline{
		Resolver:   res,
		Cache:      c,
		Audio:      ytdlp,
		Transcoder: ff,
		Uploader:   up,
		Notifier:   notifier,
		CacheDir:   envStr("CACHE_DIR", "./cache"),
		Logger:     logger,
	}

	q := queue.New(envInt("QUEUE_SIZE", 64), envInt("WORKERS", 2), func(ctx context.Context, j queue.Job) {
		p.Process(ctx, pipeline.Job{
			ChatID:         j.ChatID,
			UserID:         j.UserID,
			SpotifyURL:     j.SpotifyURL,
			SpotifyID:      j.SpotifyID,
			ReplyMessageID: j.ReplyMessageID,
		})
	})
	q.Start()

	b.RegisterHandler(tgbot.HandlerTypeMessageText, "", tgbot.MatchTypeContains, bot.Handler(bot.Deps{
		Queue:        q,
		AllowedUsers: bot.ParseAllowedUsers(os.Getenv("ALLOWED_USER_IDS")),
		Logger:       logger,
	}))

	logger.Info("starting bot")
	go b.Start(rootCtx)

	<-rootCtx.Done()
	logger.Info("shutdown signal received")
	shutdownCtx, sc := context.WithTimeout(context.Background(), 30*time.Second)
	defer sc()
	q.Stop(shutdownCtx)
	if !errors.Is(rootCtx.Err(), context.Canceled) {
		logger.Error("root ctx", "err", rootCtx.Err())
	}
	logger.Info("bye")
}
```

- [ ] **Step 3: Build everything**

```bash
go build ./...
```
Expected: success.

- [ ] **Step 4: Run unit tests across all packages**

```bash
go test ./... -race
```
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bot/handler.go cmd/bot/main.go
git commit -m "feat: bot handler and main wiring"
```

---

## Task 13: Dockerfile + docker-compose

**Files:**
- Create: `Dockerfile`, `docker-compose.yml`, `.dockerignore`

- [ ] **Step 1: Create `.dockerignore`**

```
.git
.github
bot
bot.db
bot.db-*
cache/
.env
docs/
*.test
coverage.out
```

- [ ] **Step 2: Create `Dockerfile`**

```dockerfile
# syntax=docker/dockerfile:1.7

FROM golang:1.25-alpine AS builder
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bot ./cmd/bot

FROM alpine:3.20
RUN apk add --no-cache ffmpeg python3 py3-pip ca-certificates \
 && pip3 install --break-system-packages --no-cache-dir yt-dlp
WORKDIR /app
COPY --from=builder /out/bot /app/bot
VOLUME ["/app/cache", "/app/data"]
ENV CACHE_DIR=/app/cache SQLITE_PATH=/app/data/bot.db
ENTRYPOINT ["/app/bot"]
```

- [ ] **Step 3: Create `docker-compose.yml`**

```yaml
services:
  bot:
    build: .
    image: spotify-download-tg-bot:local
    restart: unless-stopped
    env_file:
      - .env
    volumes:
      - ./cache:/app/cache
      - ./data:/app/data
```

- [ ] **Step 4: Smoke-build**

```bash
docker build -t spotify-download-tg-bot:local .
```
Expected: image builds.

- [ ] **Step 5: Commit**

```bash
git add Dockerfile docker-compose.yml .dockerignore
git commit -m "chore: dockerfile and docker-compose"
```

---

## Task 14: CI workflow

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Create CI**

Create `.github/workflows/ci.yml`:
```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:
  workflow_dispatch:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25.5'
      - name: install sqlc
        run: go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
      - name: sqlc generate
        run: sqlc generate
      - name: check generated code in sync
        run: git diff --exit-code internal/cache/db
      - name: vet
        run: go vet ./...
      - name: test
        run: go test ./... -race

  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25.5'
      - uses: golangci/golangci-lint-action@v6
        with:
          version: latest

  integration:
    runs-on: ubuntu-latest
    if: github.event_name == 'workflow_dispatch'
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25.5'
      - name: install tools
        run: |
          sudo apt-get update
          sudo apt-get install -y ffmpeg python3-pip
          pip3 install --break-system-packages yt-dlp
      - name: test integration
        run: go test -tags=integration ./...
```

- [ ] **Step 2: Commit**

```bash
git add .github
git commit -m "ci: github actions for test, lint, integration"
```

- [ ] **Step 3: Verify locally that the lint+test path is green**

```bash
go vet ./...
go test ./... -race
```
Expected: all PASS.

---

## Final manual smoke (local)

After all 14 tasks, run the bot against real services to verify end-to-end:

- [ ] **Fill `.env`** with real `TELEGRAM_BOT_TOKEN`, `SPOTIFY_CLIENT_ID`, `SPOTIFY_CLIENT_SECRET`.
- [ ] **Install yt-dlp and ffmpeg locally** (`brew install yt-dlp ffmpeg` on macOS) **or** use Docker (`docker compose up --build`).
- [ ] **Send a Spotify track URL to the bot from Telegram.**
- [ ] **Verify:** progress message updates → mp3 arrives → metadata + cover present in audio player.
- [ ] **Re-send the same URL** → instant resend via cached `file_id` (no progress messages).

If any step fails, the relevant package's logs (slog JSON in stdout) will name the stage.

---

## Self-Review (performed during planning)

**Spec coverage:**
- Scope items (single track URL, yt-dlp, mp3 320 + ID3 + cover, two-level cache, SQLite via sqlc, single Docker, ≤50 users) — all mapped to tasks 2–13.
- 4 interface seams (Resolver, Source, Cache, Uploader) — defined in tasks 5, 6, 7, 9 respectively.
- Worker pool + per-user semaphore + graceful shutdown — task 10 (queue) + task 12 (main).
- Data flow (handler synchronous, worker async, cache hit / partial hit / miss, audio retry, transcode, upload, save) — task 11 (pipeline).
- Error table (`ErrInvalidURL`, `ErrSpotifyNotFound`, `ErrSpotifyAuth`, `ErrAudioNotFound`, `ErrTranscode`, `ErrUpload`) — defined across tasks 3, 6, 7, 8, 9; mapped to user replies in task 11.
- Testing strategy (unit + integration build tag + CI) — tasks 3-11 + task 14.
- Env config — task 1 (.env.example) + task 12 (main reads).
- Deployment — task 13.

**Placeholder scan:** no TBDs, no "implement later" — every code step has full code.

**Type consistency:** `Cache.Save` signature uses `(ctx, id, Entry, artist, title, album, durationMs)` consistently in tasks 5, 11; `Notifier.Progress/Done/Error` consistent across tasks 11 and 12; sqlc column names match between `schema.sql`, `queries.sql`, and Go usage. Generated sqlc types are pinned via `emit_pointers_for_null_types: false` (task 5 step 4).
