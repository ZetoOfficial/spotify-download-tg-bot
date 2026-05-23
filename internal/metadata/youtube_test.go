package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/source"
)

type ytPayload struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Artist     string  `json:"artist,omitempty"`
	Track      string  `json:"track,omitempty"`
	Album      string  `json:"album,omitempty"`
	Channel    string  `json:"channel,omitempty"`
	Uploader   string  `json:"uploader,omitempty"`
	Duration   float64 `json:"duration"`
	Thumbnail  string  `json:"thumbnail,omitempty"`
	IsLive     bool    `json:"is_live,omitempty"`
	LiveStatus string  `json:"live_status,omitempty"`
}

func newFakeResolver(p ytPayload) *YoutubeResolver {
	r := NewYoutubeResolver("yt-dlp")
	r.exec = func(ctx context.Context, args []string) ([]byte, []byte, error) {
		b, _ := json.Marshal(p)
		return b, nil, nil
	}
	return r
}

func TestYoutube_PrefersArtistTrack(t *testing.T) {
	r := newFakeResolver(ytPayload{
		ID: "abc", Title: "Random video name (Official)",
		Artist: "Real Artist", Track: "Real Title",
		Album: "Real Album", Duration: 200, Thumbnail: "http://img/x.jpg",
	})
	tr, err := r.Resolve(context.Background(), "https://www.youtube.com/watch?v=abc")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tr.Artist != "Real Artist" {
		t.Errorf("artist %q", tr.Artist)
	}
	if tr.Title != "Real Title" {
		t.Errorf("title %q", tr.Title)
	}
	if tr.Album != "Real Album" {
		t.Errorf("album %q", tr.Album)
	}
	if tr.Source != source.YouTube {
		t.Errorf("source %q", tr.Source)
	}
	if tr.SourceID != "abc" {
		t.Errorf("source id %q", tr.SourceID)
	}
	if tr.DurationMs != 200000 {
		t.Errorf("duration %d", tr.DurationMs)
	}
	if tr.CoverURL != "http://img/x.jpg" {
		t.Errorf("cover %q", tr.CoverURL)
	}
}

func TestYoutube_TitleDashFallback(t *testing.T) {
	r := newFakeResolver(ytPayload{
		ID: "abc", Title: "Sting - Englishman in New York", Channel: "Sting", Duration: 250,
	})
	tr, err := r.Resolve(context.Background(), "url")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tr.Artist != "Sting" {
		t.Errorf("artist %q, want Sting", tr.Artist)
	}
	if tr.Title != "Englishman in New York" {
		t.Errorf("title %q", tr.Title)
	}
}

func TestYoutube_ChannelFallbackWhenNoDash(t *testing.T) {
	r := newFakeResolver(ytPayload{
		ID: "abc", Title: "some video", Channel: "ChannelName", Duration: 60,
	})
	tr, err := r.Resolve(context.Background(), "url")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tr.Artist != "ChannelName" {
		t.Errorf("artist %q", tr.Artist)
	}
	if tr.Title != "some video" {
		t.Errorf("title %q", tr.Title)
	}
}

func TestYoutube_UploaderFallbackWhenNoChannel(t *testing.T) {
	r := newFakeResolver(ytPayload{
		ID: "abc", Title: "x", Uploader: "U", Duration: 10,
	})
	tr, _ := r.Resolve(context.Background(), "url")
	if tr.Artist != "U" {
		t.Errorf("artist %q", tr.Artist)
	}
}

func TestYoutube_TooLong(t *testing.T) {
	r := newFakeResolver(ytPayload{ID: "abc", Title: "x", Duration: 301, Channel: "c"})
	_, err := r.Resolve(context.Background(), "url")
	if !errors.Is(err, ErrTrackTooLong) {
		t.Fatalf("err %v, want ErrTrackTooLong", err)
	}
}

func TestYoutube_BoundaryExactly300_OK(t *testing.T) {
	r := newFakeResolver(ytPayload{ID: "abc", Title: "x", Duration: 300, Channel: "c"})
	if _, err := r.Resolve(context.Background(), "url"); err != nil {
		t.Fatalf("expected ok at 300s, got %v", err)
	}
}

func TestYoutube_LiveStream_Rejected(t *testing.T) {
	r := newFakeResolver(ytPayload{ID: "abc", Title: "x", Duration: 10, IsLive: true, Channel: "c"})
	_, err := r.Resolve(context.Background(), "url")
	if err == nil || !strings.Contains(err.Error(), "live") {
		t.Fatalf("err %v, want live rejection", err)
	}
}

func TestYoutube_ExecError_Wrapped(t *testing.T) {
	r := NewYoutubeResolver("yt-dlp")
	r.exec = func(ctx context.Context, args []string) ([]byte, []byte, error) {
		return nil, []byte("boom"), errors.New("exit 1")
	}
	_, err := r.Resolve(context.Background(), "url")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "yt-dlp metadata") {
		t.Errorf("err %v", err)
	}
}
