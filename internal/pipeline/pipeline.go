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
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/source"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/uploader"
)

// Job mirrors queue.Job; redefined here to avoid an import cycle with the
// queue package (queue holds a Handler that calls into Pipeline.Process).
type Job struct {
	ChatID            int64
	UserID            int64
	Source            source.Source
	SourceID          string
	SourceURL         string
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
	Resolvers  map[source.Source]metadata.Resolver
	Cache      cache.Cache
	Audio      audio.Source
	Transcoder Transcoder
	Uploader   uploader.Uploader
	Notifier   Notifier
	CacheDir   string
	Logger     *slog.Logger
}

func (p *Pipeline) Process(ctx context.Context, j Job) {
	log := p.logger().With("chat_id", j.ChatID, "source", string(j.Source), "source_id", j.SourceID)
	start := time.Now()
	defer func() {
		log.Info("job complete", "duration_ms", time.Since(start).Milliseconds())
	}()

	resolver, ok := p.Resolvers[j.Source]
	if !ok {
		log.Error("no resolver for source")
		p.Notifier.Error(j.ChatID, j.ReplyMessageID, "сервис недоступен, напиши админу")
		return
	}
	tr, err := resolver.Resolve(ctx, resolverKey(j))
	if err != nil {
		p.handleResolverErr(j, err, log)
		return
	}
	// Source / SourceID / SourceURL are authoritative from the Job — overwrite
	// whatever the resolver set, so downstream Cache.Key and file paths match.
	tr.Source = j.Source
	tr.SourceID = j.SourceID
	tr.SourceURL = j.SourceURL
	log = log.With("artist", tr.Artist, "title", tr.Title)

	trackKey := cache.Key(j.Source, j.SourceID)
	entry, hit, lookupErr := p.Cache.Lookup(ctx, trackKey)
	if lookupErr != nil {
		log.Warn("cache lookup failed", "err", lookupErr)
	}
	if hit {
		if entry.FileID != "" {
			if sendErr := p.Uploader.SendCached(ctx, j.ChatID, entry.FileID, j.OriginalMessageID); sendErr == nil {
				if touchErr := p.Cache.Touch(ctx, trackKey); touchErr != nil {
					log.Warn("cache touch failed", "err", touchErr)
				}
				p.Notifier.Done(j.ChatID, j.ReplyMessageID)
				return
			}
		}
		if entry.LocalPath != "" {
			fileID, uploadErr := p.Uploader.Upload(ctx, j.ChatID, entry.LocalPath, tr, j.OriginalMessageID)
			if uploadErr == nil {
				if saveErr := p.Cache.Save(ctx, trackKey, cache.Entry{FileID: fileID, LocalPath: entry.LocalPath}, tr.Artist, tr.Title, tr.Album, tr.DurationMs); saveErr != nil {
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
	if err := p.Cache.Save(ctx, trackKey, cache.Entry{FileID: fileID, LocalPath: mp3}, tr.Artist, tr.Title, tr.Album, tr.DurationMs); err != nil {
		log.Warn("cache save failed", "err", err)
	}
	p.Notifier.Done(j.ChatID, j.ReplyMessageID)
}

// resolverKey returns the argument expected by the source's Resolver:
// Spotify wants the track id, YouTube wants the canonical URL.
func resolverKey(j Job) string {
	if j.Source == source.YouTube {
		return j.SourceURL
	}
	return j.SourceID
}

func (p *Pipeline) logger() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}

func (p *Pipeline) handleResolverErr(j Job, err error, log *slog.Logger) {
	switch {
	case errors.Is(err, metadata.ErrTrackTooLong):
		p.Notifier.Error(j.ChatID, j.ReplyMessageID, "максимум 5 минут")
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
	log.Warn("audio fetch failed", "err", err, "kind", audioErrKind(err))
	p.Notifier.Error(j.ChatID, j.ReplyMessageID, "не получилось скачать аудио")
}

func audioErrKind(err error) string {
	if errors.Is(err, audio.ErrAudioNotFound) {
		return "not_found"
	}
	return "exec_error"
}
