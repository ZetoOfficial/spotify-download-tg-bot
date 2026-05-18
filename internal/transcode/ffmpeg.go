package transcode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

var ErrTranscode = errors.New("transcode failed")

type FFmpeg struct {
	binary  string
	http    *http.Client
	execRun func(ctx context.Context, args []string) ([]byte, error)
}

func NewFFmpeg(binary string) *FFmpeg {
	return &FFmpeg{
		binary: binary,
		http:   &http.Client{Timeout: 15 * time.Second},
		execRun: func(ctx context.Context, args []string) ([]byte, error) {
			var stderr bytes.Buffer
			cmd := exec.CommandContext(ctx, args[0], args[1:]...) //nolint:gosec // args[0] is the configured ffmpeg binary path
			cmd.Stderr = &stderr
			err := cmd.Run()
			return stderr.Bytes(), err
		},
	}
}

// ToMP3 transcodes raw into outDir/<SpotifyID>.mp3 with ID3 + cover.
// Cover fetch is best-effort: a failure proceeds without the cover.
func (f *FFmpeg) ToMP3(ctx context.Context, raw string, t track.Track, outDir string) (string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	out := filepath.Join(outDir, t.SpotifyID+".mp3")

	args := []string{f.binary, "-y", "-i", raw}
	coverPath := ""
	if t.CoverURL != "" {
		if p, err := f.downloadCover(ctx, t); err == nil {
			coverPath = p
			defer os.Remove(coverPath)
			args = append(args, "-i", coverPath, "-map", "0:a", "-map", "1:v", "-disposition:v:0", "attached_pic")
		}
	}
	args = append(args,
		"-vn", "-c:a", "libmp3lame", "-b:a", "320k",
		"-id3v2_version", "3",
		"-metadata", "title="+t.Title,
		"-metadata", "artist="+t.Artist,
		"-metadata", "album="+t.Album,
		out,
	)
	if stderr, err := f.execRun(ctx, args); err != nil {
		return "", fmt.Errorf("%w: %w (stderr: %s)", ErrTranscode, err, string(stderr))
	}
	return out, nil
}

func (f *FFmpeg) downloadCover(ctx context.Context, t track.Track) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.CoverURL, http.NoBody)
	if err != nil {
		return "", err
	}
	resp, err := f.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cover status %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp("", "cover-*.jpg")
	if err != nil {
		return "", err
	}
	if _, copyErr := io.Copy(tmp, resp.Body); copyErr != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", copyErr
	}
	tmp.Close()
	return tmp.Name(), nil
}
