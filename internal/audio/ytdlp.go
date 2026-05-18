package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

var ErrAudioNotFound = errors.New("audio source: no match")

// Source fetches the raw audio for a Track to a local file.
type Source interface {
	Fetch(ctx context.Context, t track.Track) (rawAudioPath string, err error)
}

// YtDlpSource shells out to yt-dlp. The exec hook is overridable for tests.
type YtDlpSource struct {
	binary  string
	workDir string
	exec    func(ctx context.Context, args []string) (stdout, stderr []byte, err error)
	stat    func(path string) bool
}

func NewYtDlpSource(binary, workDir string) *YtDlpSource {
	return &YtDlpSource{
		binary:  binary,
		workDir: workDir,
		exec:    realExec,
		stat: func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		},
	}
}

func realExec(ctx context.Context, args []string) ([]byte, []byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func (s *YtDlpSource) Fetch(ctx context.Context, t track.Track) (string, error) {
	queries := []string{
		fmt.Sprintf("%s - %s", t.Artist, t.Title),
		fmt.Sprintf("%s %s official audio", t.Artist, t.Title),
	}
	for _, q := range queries {
		p, err := s.tryFetch(ctx, q, t)
		if err == nil {
			return p, nil
		}
		if !errors.Is(err, ErrAudioNotFound) {
			return "", err
		}
	}
	return "", ErrAudioNotFound
}

func (s *YtDlpSource) tryFetch(ctx context.Context, query string, t track.Track) (string, error) {
	outTpl := filepath.Join(s.workDir, t.SpotifyID+".%(ext)s")
	args := []string{
		s.binary,
		"--no-playlist",
		"--default-search", "ytsearch1:",
		"-f", "bestaudio[acodec!=opus]/bestaudio",
		"--print-json",
		"--no-progress",
		"-o", outTpl,
		query,
	}
	stdout, stderr, err := s.exec(ctx, args)
	if err != nil {
		return "", fmt.Errorf("yt-dlp: %w (stderr: %s)", err, string(stderr))
	}
	var meta struct {
		ID       string `json:"id"`
		Duration int    `json:"duration"`
		Ext      string `json:"ext"`
	}
	if err := json.Unmarshal(stdout, &meta); err != nil {
		return "", fmt.Errorf("yt-dlp json: %w", err)
	}
	if math.Abs(float64(meta.Duration*1000-t.DurationMs)) > 5000 {
		return "", ErrAudioNotFound
	}
	path := filepath.Join(s.workDir, t.SpotifyID+"."+meta.Ext)
	if !s.stat(path) {
		return "", fmt.Errorf("yt-dlp output missing: %s", path)
	}
	return path, nil
}
