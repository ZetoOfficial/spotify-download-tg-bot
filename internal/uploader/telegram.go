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

// ErrUpload is the sentinel error returned after retries are exhausted or on
// non-transient failures.
var ErrUpload = errors.New("upload failed after retries")

// Uploader sends audio to Telegram and returns a reusable file_id.
type Uploader interface {
	Upload(ctx context.Context, chatID int64, mp3Path string, t track.Track) (fileID string, err error)
	SendCached(ctx context.Context, chatID int64, fileID string) error
}

type sendFn func(ctx context.Context, chatID int64, path string, t track.Track) (string, error)

// TelegramUploader implements Uploader using github.com/go-telegram/bot.
type TelegramUploader struct {
	b       *bot.Bot
	send    sendFn
	backoff time.Duration
}

// NewTelegramUploader constructs a TelegramUploader backed by the given bot
// client. The retry backoff starts at 1s and grows by 4x between attempts.
func NewTelegramUploader(b *bot.Bot) *TelegramUploader {
	u := &TelegramUploader{b: b, backoff: time.Second}
	u.send = u.realSend
	return u
}

// Upload sends the file at path to the chat and returns the resulting file_id.
// It retries up to three times on transient (5xx/timeout) errors with
// exponential backoff. Non-transient errors fail immediately. All failures are
// wrapped in ErrUpload.
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

// SendCached sends a previously uploaded audio file by its Telegram file_id.
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
	return strings.Contains(s, "502") || strings.Contains(s, "503") ||
		strings.Contains(s, "504") || strings.Contains(s, "timeout")
}
