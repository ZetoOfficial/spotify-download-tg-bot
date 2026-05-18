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

func TestUpload_NonTransientFailsImmediately(t *testing.T) {
	attempts := 0
	stub := func(ctx context.Context, chatID int64, path string, t track.Track) (string, error) {
		attempts++
		return "", errors.New("permission denied")
	}
	u := &TelegramUploader{send: stub, backoff: 0}
	_, err := u.Upload(context.Background(), 1, "/tmp/x.mp3", track.Track{})
	if !errors.Is(err, ErrUpload) {
		t.Fatalf("err %v, want ErrUpload", err)
	}
	if attempts != 1 {
		t.Errorf("attempts %d, want 1", attempts)
	}
}

type transientError struct{}

func (t *transientError) Error() string   { return "telegram 502" }
func (t *transientError) Transient() bool { return true }
