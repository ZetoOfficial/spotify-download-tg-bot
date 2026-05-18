//go:build integration

package transcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

func TestToMP3_RealFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	wav := filepath.Join(dir, "in.wav")
	if err := exec.Command("ffmpeg", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=stereo", "-t", "1", wav).Run(); err != nil {
		t.Fatalf("gen wav: %v", err)
	}
	f := NewFFmpeg("ffmpeg")
	out, err := f.ToMP3(context.Background(), wav, track.Track{SpotifyID: "x", Title: "t", Artist: "a"}, dir)
	if err != nil {
		t.Fatalf("transcode: %v", err)
	}
	fi, err := os.Stat(out)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("out bad: %v size=%d", err, fi.Size())
	}
}
