package uploader

import (
	"context"
	"errors"
	"testing"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

func TestUpload_RetriesOn5xx(t *testing.T) {
	attempts := 0
	var seenReply int
	stub := func(ctx context.Context, chatID int64, path string, t track.Track, replyToMessageID int) (string, error) {
		attempts++
		seenReply = replyToMessageID
		if attempts < 3 {
			return "", &transientError{}
		}
		return "fileid-ok", nil
	}
	u := &TelegramUploader{send: stub, backoff: 0}
	id, err := u.Upload(context.Background(), 1, "/tmp/x.mp3", track.Track{}, 42)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if id != "fileid-ok" {
		t.Errorf("file id %q", id)
	}
	if attempts != 3 {
		t.Errorf("attempts %d", attempts)
	}
	if seenReply != 42 {
		t.Errorf("replyTo propagated: got %d, want 42", seenReply)
	}
}

func TestUpload_GivesUpAfterRetries(t *testing.T) {
	stub := func(ctx context.Context, chatID int64, path string, t track.Track, replyToMessageID int) (string, error) {
		return "", &transientError{}
	}
	u := &TelegramUploader{send: stub, backoff: 0}
	_, err := u.Upload(context.Background(), 1, "/tmp/x.mp3", track.Track{}, 0)
	if !errors.Is(err, ErrUpload) {
		t.Fatalf("err %v, want ErrUpload", err)
	}
}

func TestUpload_NonTransientFailsImmediately(t *testing.T) {
	attempts := 0
	stub := func(ctx context.Context, chatID int64, path string, t track.Track, replyToMessageID int) (string, error) {
		attempts++
		return "", errors.New("permission denied")
	}
	u := &TelegramUploader{send: stub, backoff: 0}
	_, err := u.Upload(context.Background(), 1, "/tmp/x.mp3", track.Track{}, 0)
	if !errors.Is(err, ErrUpload) {
		t.Fatalf("err %v, want ErrUpload", err)
	}
	if attempts != 1 {
		t.Errorf("attempts %d, want 1", attempts)
	}
}

func TestReplyParams_ZeroReturnsNil(t *testing.T) {
	if replyParams(0) != nil {
		t.Error("replyParams(0) should be nil")
	}
	if rp := replyParams(123); rp == nil || rp.MessageID != 123 {
		t.Errorf("replyParams(123) = %+v, want MessageID=123", rp)
	}
}

type transientError struct{}

func (t *transientError) Error() string   { return "telegram 502" }
func (t *transientError) Transient() bool { return true }
