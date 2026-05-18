//go:build integration

package audio

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

func TestFetch_RealYtDlp(t *testing.T) {
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		t.Skip("yt-dlp not installed")
	}
	dir := t.TempDir()
	s := NewYtDlpSource("yt-dlp", dir)
	// Public short clip — adjust if it disappears.
	tr := track.Track{
		SpotifyID:  "test-me-at-the-zoo",
		Artist:     "jawed",
		Title:      "Me at the zoo",
		DurationMs: 19000,
	}
	p, err := s.Fetch(context.Background(), tr)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fi, err := os.Stat(p); err != nil || fi.Size() == 0 {
		t.Fatalf("output bad: %v size=%d", err, fi.Size())
	}
}
