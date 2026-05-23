package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/joho/godotenv"
	_ "modernc.org/sqlite"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/audio"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/bot"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/cache"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/metadata"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/pipeline"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/queue"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/source"
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
	_ = godotenv.Load() //nolint:errcheck // .env is optional; missing file is the normal case in Docker

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

	if err := run(logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		return errors.New("TELEGRAM_BOT_TOKEN required")
	}
	spotifyID := os.Getenv("SPOTIFY_CLIENT_ID")
	spotifySecret := os.Getenv("SPOTIFY_CLIENT_SECRET")
	if spotifyID == "" || spotifySecret == "" {
		return errors.New("SPOTIFY_CLIENT_ID/SECRET required")
	}

	rootCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	sqlDB, err := sql.Open("sqlite", envStr("SQLITE_PATH", "./bot.db"))
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	defer sqlDB.Close()
	c, err := cache.NewSQLiteCache(rootCtx, sqlDB, envStr("CACHE_DIR", "./cache"), envInt("MAX_CACHE_MB", 2048))
	if err != nil {
		return fmt.Errorf("cache init: %w", err)
	}

	ytdlpBin := envStr("YTDLP_BIN", "yt-dlp")
	spotifyRes := metadata.NewSpotifyResolver(spotifyID, spotifySecret)
	youtubeRes := metadata.NewYoutubeResolver(ytdlpBin)
	ytdlp := audio.NewYtDlpSource(ytdlpBin, os.TempDir())
	ff := transcode.NewFFmpeg(envStr("FFMPEG_BIN", "ffmpeg"))

	b, err := tgbot.New(token)
	if err != nil {
		return fmt.Errorf("bot init: %w", err)
	}
	notifier := &bot.Notifier{B: b, Logger: logger}
	up := uploader.NewTelegramUploader(b)

	p := &pipeline.Pipeline{
		Resolvers: map[source.Source]metadata.Resolver{
			source.Spotify: spotifyRes,
			source.YouTube: youtubeRes,
		},
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
			ChatID:            j.ChatID,
			UserID:            j.UserID,
			Source:            j.Source,
			SourceID:          j.SourceID,
			SourceURL:         j.SourceURL,
			ReplyMessageID:    j.ReplyMessageID,
			OriginalMessageID: j.OriginalMessageID,
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
	return nil
}
