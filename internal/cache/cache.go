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
		SpotifyID:  spotifyID,
		Artist:     artist,
		Title:      title,
		Album:      album,
		DurationMs: int64(durationMs),
		FileID:     nullString(e.FileID),
		LocalPath:  nullString(e.LocalPath),
		CreatedAt:  now,
		LastUsedAt: now,
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
