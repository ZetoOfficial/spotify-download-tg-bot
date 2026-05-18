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
	// Write 3 files of ~512KB each → third save triggers eviction.
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
