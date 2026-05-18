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
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/uploader"
)

// Job mirrors queue.Job; redefined here to avoid an import cycle with the
// queue package (queue holds a Handler that calls into Pipeline.Process).
type Job struct {
	ChatID            int64
	UserID            int64
	SpotifyURL        string
	SpotifyID         string
	ReplyMessageID    int
	OriginalMessageID int
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

	tr, err := p.Resolver.Resolve(ctx, j.SpotifyID)
	if err != nil {
		p.handleResolverErr(j, err, log)
		return
	}
	log = log.With("artist", tr.Artist, "title", tr.Title)

	entry, hit, lookupErr := p.Cache.Lookup(ctx, j.SpotifyID)
	if lookupErr != nil {
		log.Warn("cache lookup failed", "err", lookupErr)
	}
	if hit {
		if entry.FileID != "" {
			if sendErr := p.Uploader.SendCached(ctx, j.ChatID, entry.FileID, j.OriginalMessageID); sendErr == nil {
				if touchErr := p.Cache.Touch(ctx, j.SpotifyID); touchErr != nil {
					log.Warn("cache touch failed", "err", touchErr)
				}
				p.Notifier.Done(j.ChatID, j.ReplyMessageID)
				return
			}
		}
		if entry.LocalPath != "" {
			fileID, uploadErr := p.Uploader.Upload(ctx, j.ChatID, entry.LocalPath, tr, j.OriginalMessageID)
			if uploadErr == nil {
				if saveErr := p.Cache.Save(ctx, j.SpotifyID, cache.Entry{FileID: fileID, LocalPath: entry.LocalPath}, tr.Artist, tr.Title, tr.Album, tr.DurationMs); saveErr != nil {
					log.Warn("cache save failed", "err", saveErr)
				}
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
	fileID, err := p.Uploader.Upload(ctx, j.ChatID, mp3, tr, j.OriginalMessageID)
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
