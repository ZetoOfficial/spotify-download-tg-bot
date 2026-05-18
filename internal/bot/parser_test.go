package bot

import (
	"errors"
	"testing"
)

func TestExtractSpotifyTrackID(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr error
	}{
		{"https full", "https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT", "4cOdK2wGLETKBW3PvgPWqT", nil},
		{"http no scheme", "open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT", "4cOdK2wGLETKBW3PvgPWqT", nil},
		{"with si param", "https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT?si=abc123", "4cOdK2wGLETKBW3PvgPWqT", nil},
		{"with intl segment", "https://open.spotify.com/intl-ru/track/4cOdK2wGLETKBW3PvgPWqT?si=xyz", "4cOdK2wGLETKBW3PvgPWqT", nil},
		{"uri scheme", "spotify:track:4cOdK2wGLETKBW3PvgPWqT", "4cOdK2wGLETKBW3PvgPWqT", nil},
		{"embedded in sentence", "look at this https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT cool huh", "4cOdK2wGLETKBW3PvgPWqT", nil},
		{"empty", "", "", ErrInvalidURL},
		{"random text", "hello world", "", ErrInvalidURL},
		{"playlist not track", "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M", "", ErrInvalidURL},
		{"album not track", "https://open.spotify.com/album/4cOdK2wGLETKBW3PvgPWqT", "", ErrInvalidURL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractSpotifyTrackID(tc.input)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
