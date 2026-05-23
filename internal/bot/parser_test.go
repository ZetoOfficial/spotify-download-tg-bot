package bot

import (
	"errors"
	"testing"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/source"
)

func TestParseLink(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantSrc source.Source
		wantID  string
		wantURL string
		wantErr error
	}{
		// Spotify
		{"sp https full", "https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT", source.Spotify, "4cOdK2wGLETKBW3PvgPWqT", "https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT", nil},
		{"sp no scheme", "open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT", source.Spotify, "4cOdK2wGLETKBW3PvgPWqT", "https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT", nil},
		{"sp si param", "https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT?si=abc123", source.Spotify, "4cOdK2wGLETKBW3PvgPWqT", "https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT", nil},
		{"sp intl segment", "https://open.spotify.com/intl-ru/track/4cOdK2wGLETKBW3PvgPWqT?si=xyz", source.Spotify, "4cOdK2wGLETKBW3PvgPWqT", "https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT", nil},
		{"sp uri", "spotify:track:4cOdK2wGLETKBW3PvgPWqT", source.Spotify, "4cOdK2wGLETKBW3PvgPWqT", "https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT", nil},
		{"sp embedded", "look at this https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT cool", source.Spotify, "4cOdK2wGLETKBW3PvgPWqT", "https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT", nil},

		// YouTube
		{"yt watch", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", source.YouTube, "dQw4w9WgXcQ", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", nil},
		{"yt watch no www", "https://youtube.com/watch?v=dQw4w9WgXcQ", source.YouTube, "dQw4w9WgXcQ", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", nil},
		{"yt watch si before v", "https://youtube.com/watch?si=abc&v=dQw4w9WgXcQ&t=10s", source.YouTube, "dQw4w9WgXcQ", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", nil},
		{"yt short", "https://youtu.be/dQw4w9WgXcQ", source.YouTube, "dQw4w9WgXcQ", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", nil},
		{"yt short with query", "https://youtu.be/dQw4w9WgXcQ?si=xxx", source.YouTube, "dQw4w9WgXcQ", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", nil},
		{"yt with dashes/underscores", "https://www.youtube.com/watch?v=ab-cd_EF123", source.YouTube, "ab-cd_EF123", "https://www.youtube.com/watch?v=ab-cd_EF123", nil},

		// Unsupported
		{"yt music", "https://music.youtube.com/watch?v=dQw4w9WgXcQ", "", "", "", ErrInvalidURL},
		{"yt shorts", "https://youtube.com/shorts/dQw4w9WgXcQ", "", "", "", ErrInvalidURL},
		{"yt playlist", "https://youtube.com/playlist?list=PL12345", "", "", "", ErrInvalidURL},
		{"empty", "", "", "", "", ErrInvalidURL},
		{"random text", "hello world", "", "", "", ErrInvalidURL},
		{"sp playlist", "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M", "", "", "", ErrInvalidURL},
		{"sp album", "https://open.spotify.com/album/4cOdK2wGLETKBW3PvgPWqT", "", "", "", ErrInvalidURL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseLink(tc.input)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Source != tc.wantSrc {
				t.Errorf("source = %q, want %q", got.Source, tc.wantSrc)
			}
			if got.ID != tc.wantID {
				t.Errorf("id = %q, want %q", got.ID, tc.wantID)
			}
			if got.URL != tc.wantURL {
				t.Errorf("url = %q, want %q", got.URL, tc.wantURL)
			}
		})
	}
}
