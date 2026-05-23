package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strings"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/source"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

// MaxYouTubeDurationSec is the hard cap for YouTube tracks (5 minutes).
const MaxYouTubeDurationSec = 300

// YoutubeResolver pulls metadata from yt-dlp without downloading the audio.
// The exec hook is overridable for tests.
type YoutubeResolver struct {
	binary string
	exec   func(ctx context.Context, args []string) (stdout, stderr []byte, err error)
}

func NewYoutubeResolver(binary string) *YoutubeResolver {
	return &YoutubeResolver{
		binary: binary,
		exec: func(ctx context.Context, args []string) ([]byte, []byte, error) {
			var stdoutBuf, stderrBuf bytes.Buffer
			cmd := exec.CommandContext(ctx, args[0], args[1:]...) //nolint:gosec // args[0] is the configured yt-dlp binary path
			cmd.Stdout = &stdoutBuf
			cmd.Stderr = &stderrBuf
			err := cmd.Run()
			return stdoutBuf.Bytes(), stderrBuf.Bytes(), err
		},
	}
}

// Resolve fetches metadata for the given canonical YouTube watch URL.
// Returns ErrTrackTooLong if duration > MaxYouTubeDurationSec. Live or
// upcoming streams are rejected with a plain error.
func (r *YoutubeResolver) Resolve(ctx context.Context, videoURL string) (track.Track, error) {
	args := []string{
		r.binary,
		"--skip-download",
		"--dump-json",
		"--no-playlist",
		videoURL,
	}
	stdout, stderr, err := r.exec(ctx, args)
	if err != nil {
		return track.Track{}, fmt.Errorf("yt-dlp metadata: %w (stderr: %s)", err, string(stderr))
	}
	var p struct {
		ID         string  `json:"id"`
		Title      string  `json:"title"`
		Artist     string  `json:"artist"`
		Track      string  `json:"track"`
		Album      string  `json:"album"`
		Channel    string  `json:"channel"`
		Uploader   string  `json:"uploader"`
		Duration   float64 `json:"duration"`
		Thumbnail  string  `json:"thumbnail"`
		IsLive     bool    `json:"is_live"`
		LiveStatus string  `json:"live_status"`
	}
	if err := json.Unmarshal(stdout, &p); err != nil {
		return track.Track{}, fmt.Errorf("yt-dlp json: %w", err)
	}
	if p.IsLive || p.LiveStatus == "is_upcoming" || p.LiveStatus == "is_live" {
		return track.Track{}, fmt.Errorf("youtube live/upcoming not supported")
	}
	durSec := int(math.Round(p.Duration))
	if durSec > MaxYouTubeDurationSec {
		return track.Track{}, ErrTrackTooLong
	}
	artist, title := pickArtistTitle(p.Artist, p.Track, p.Title, p.Channel, p.Uploader)
	return track.Track{
		Source:     source.YouTube,
		SourceID:   p.ID,
		SourceURL:  videoURL,
		Artist:     artist,
		Title:      title,
		Album:      p.Album,
		DurationMs: int(p.Duration * 1000),
		CoverURL:   p.Thumbnail,
	}, nil
}

// pickArtistTitle implements the fallback chain:
//
//	artist: ytArtist → left-of-" - " in title → channel → uploader
//	title:  ytTrack  → right-of-" - " in title → full title
func pickArtistTitle(ytArtist, ytTrack, fullTitle, channel, uploader string) (artist, title string) {
	if ytArtist != "" {
		artist = ytArtist
	}
	if ytTrack != "" {
		title = ytTrack
	}
	if artist == "" || title == "" {
		if i := strings.Index(fullTitle, " - "); i > 0 {
			left := strings.TrimSpace(fullTitle[:i])
			right := strings.TrimSpace(fullTitle[i+3:])
			if artist == "" && left != "" {
				artist = left
			}
			if title == "" && right != "" {
				title = right
			}
		}
	}
	if artist == "" {
		if channel != "" {
			artist = channel
		} else {
			artist = uploader
		}
	}
	if title == "" {
		title = fullTitle
	}
	return artist, title
}
