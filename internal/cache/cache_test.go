package cache

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/source"
)

func newTestCache(t *testing.T) (cache *SQLiteCache, dir string) {
	t.Helper()
	dir = t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	cache, err = NewSQLiteCache(context.Background(), db, dir, 100) // 100 MB
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}
	return cache, dir
}

func TestKey(t *testing.T) {
	if got := Key(source.Spotify, "x"); got != "sp:x" {
		t.Errorf("spotify key = %q", got)
	}
	if got := Key(source.YouTube, "x"); got != "yt:x" {
		t.Errorf("youtube key = %q", got)
	}
}

func TestLookup_Miss(t *testing.T) {
	c, _ := newTestCache(t)
	_, ok, err := c.Lookup(context.Background(), "sp:abc")
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
	if err := c.Save(ctx, "sp:abc", Entry{FileID: "tgid", LocalPath: path}, "Artist", "Title", "Album", 123000); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := c.Lookup(ctx, "sp:abc")
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
	if err := c.Save(ctx, "sp:abc", Entry{LocalPath: path}, "Artist", "Title", "Album", 123000); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := c.Lookup(ctx, "sp:abc")
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
	if err := c.Save(ctx, "sp:abc", Entry{FileID: "tgid"}, "A", "T", "Al", 100); err != nil {
		t.Fatalf("save: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	before := time.Now().Add(-1 * time.Second).Unix()
	if err := c.Touch(ctx, "sp:abc"); err != nil {
		t.Fatalf("touch: %v", err)
	}
	row, err := c.queries.GetTrack(ctx, "sp:abc")
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
	mk := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, make([]byte, 512*1024), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	p1 := mk("a.mp3")
	if err := c.Save(ctx, "sp:a", Entry{LocalPath: p1}, "x", "x", "", 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	p2 := mk("b.mp3")
	if err := c.Save(ctx, "sp:b", Entry{LocalPath: p2}, "x", "x", "", 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	p3 := mk("c.mp3")
	if err := c.Save(ctx, "sp:c", Entry{LocalPath: p3}, "x", "x", "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p1); !os.IsNotExist(err) {
		t.Errorf("expected %s evicted, stat err = %v", p1, err)
	}
	row, err := c.queries.GetTrack(ctx, "sp:a")
	if err != nil {
		t.Fatalf("get a: %v", err)
	}
	if row.LocalPath.Valid {
		t.Errorf("local_path not cleared after eviction: %v", row.LocalPath.String)
	}
}

// TestMigration_LegacySpotifyIDColumn proves the on-startup migration
// renames the column and prefixes existing rows with "sp:".
func TestMigration_LegacySpotifyIDColumn(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")

	// Seed a DB with the old schema and two existing rows.
	legacy, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	//nolint:dupword // "NULL, NULL" in VALUES is intentional, not a typo
	if _, err := legacy.ExecContext(context.Background(), `
		CREATE TABLE tracks (
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
		INSERT INTO tracks(spotify_id, artist, title, album, duration_ms, file_id, local_path, created_at, last_used_at)
		VALUES ('aaaaaaaaaaaaaaaaaaaaaa', 'A1', 'T1', '', 100, 'fid1', NULL, 1, 1),
		       ('bbbbbbbbbbbbbbbbbbbbbb', 'A2', 'T2', '', 200, NULL, NULL, 2, 2);
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	legacy.Close()

	// Open via NewSQLiteCache → migration must run.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c, err := NewSQLiteCache(context.Background(), db, dir, 100)
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}

	// Old key under new prefix is reachable.
	got, ok, err := c.Lookup(context.Background(), "sp:aaaaaaaaaaaaaaaaaaaaaa")
	if err != nil || !ok {
		t.Fatalf("post-migration lookup: err=%v ok=%v", err, ok)
	}
	if got.FileID != "fid1" {
		t.Errorf("file_id = %q, want fid1", got.FileID)
	}

	// PRAGMA: column must now be track_key, not spotify_id.
	rows, err := db.QueryContext(context.Background(), "PRAGMA table_info(tracks)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	sawTrackKey := false
	for rows.Next() {
		var (
			cid         int
			name, typ   string
			notNull, pk int
			dfltValue   sql.NullString
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "spotify_id" {
			t.Errorf("legacy column spotify_id still present")
		}
		if name == "track_key" {
			sawTrackKey = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !sawTrackKey {
		t.Error("track_key column missing after migration")
	}
}

func TestMigration_FreshDB_NoOp(t *testing.T) {
	// Just ensure NewSQLiteCache on a fresh DB doesn't error out.
	_, _ = newTestCache(t)
}

func TestMigration_AlreadyPrefixed_NotDoublePrefixed(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "x.db")
	legacy, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.ExecContext(context.Background(), `
		CREATE TABLE tracks (
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
		INSERT INTO tracks VALUES ('sp:already', 'A', 'T', '', 1, 'f', NULL, 0, 0);
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	legacy.Close()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c, err := NewSQLiteCache(context.Background(), db, dir, 100)
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}
	_, ok, err := c.Lookup(context.Background(), "sp:already")
	if err != nil || !ok {
		t.Fatalf("lookup: %v ok=%v", err, ok)
	}
	// Make sure 'sp:sp:already' wasn't created.
	_, ok, err = c.Lookup(context.Background(), "sp:sp:already")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("row got double-prefixed")
	}
}
