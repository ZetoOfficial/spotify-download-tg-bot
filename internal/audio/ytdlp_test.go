package audio

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/source"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

func TestFetch_Spotify_FirstAttemptMatches(t *testing.T) {
	calls := 0
	s := &YtDlpSource{
		workDir: t.TempDir(),
		exec: func(ctx context.Context, args []string) (stdout, stderr []byte, err error) {
			calls++
			meta := struct {
				ID       string `json:"id"`
				Title    string `json:"title"`
				Duration int    `json:"duration"`
				Ext      string `json:"ext"`
			}{ID: "vid1", Title: "x", Duration: 200, Ext: "m4a"}
			b, _ := json.Marshal(meta)
			return b, nil, nil
		},
		stat: func(path string) bool {
			return filepath.Ext(path) == ".m4a"
		},
	}
	tr := track.Track{Source: source.Spotify, SourceID: "sp1", Artist: "A", Title: "T", DurationMs: 200000}
	p, err := s.Fetch(context.Background(), tr)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
	if filepath.Ext(p) != ".m4a" {
		t.Errorf("got %q", p)
	}
}

func TestFetch_Spotify_WrongDuration_RetriesAndFails(t *testing.T) {
	calls := 0
	s := &YtDlpSource{
		workDir: t.TempDir(),
		exec: func(ctx context.Context, args []string) (stdout, stderr []byte, err error) {
			calls++
			meta := struct {
				ID       string `json:"id"`
				Duration int    `json:"duration"`
				Ext      string `json:"ext"`
			}{ID: "v", Duration: 500, Ext: "m4a"} // 500s vs expected 200s
			b, _ := json.Marshal(meta)
			return b, nil, nil
		},
		stat: func(path string) bool { return true },
	}
	tr := track.Track{Source: source.Spotify, SourceID: "sp1", Artist: "A", Title: "T", DurationMs: 200000}
	_, err := s.Fetch(context.Background(), tr)
	if !errors.Is(err, ErrAudioNotFound) {
		t.Fatalf("err %v, want ErrAudioNotFound", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 attempts, got %d", calls)
	}
}

func TestFetch_YouTube_DownloadsByURL_NoSearch_NoDurationCheck(t *testing.T) {
	var capturedArgs []string
	s := &YtDlpSource{
		workDir: t.TempDir(),
		exec: func(ctx context.Context, args []string) (stdout, stderr []byte, err error) {
			capturedArgs = args
			meta := struct {
				ID  string `json:"id"`
				Ext string `json:"ext"`
			}{ID: "ytid", Ext: "m4a"}
			b, _ := json.Marshal(meta)
			return b, nil, nil
		},
		stat: func(path string) bool { return true },
	}
	// Wildly mismatched DurationMs to confirm duration check is skipped.
	tr := track.Track{
		Source:     source.YouTube,
		SourceID:   "dQw4w9WgXcQ",
		SourceURL:  "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		DurationMs: 1,
	}
	p, err := s.Fetch(context.Background(), tr)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !strings.HasSuffix(p, "dQw4w9WgXcQ.m4a") {
		t.Errorf("path %q", p)
	}
	joined := strings.Join(capturedArgs, " ")
	if strings.Contains(joined, "ytsearch1:") {
		t.Errorf("YouTube path must not use ytsearch1, got args: %s", joined)
	}
	if !strings.Contains(joined, tr.SourceURL) {
		t.Errorf("expected URL in args, got: %s", joined)
	}
}

func TestFetch_YouTube_VideoUnavailable_MapsToErrAudioNotFound(t *testing.T) {
	s := &YtDlpSource{
		workDir: t.TempDir(),
		exec: func(ctx context.Context, args []string) (stdout, stderr []byte, err error) {
			return nil, []byte("ERROR: [youtube] xyz: Video unavailable"), errors.New("exit status 1")
		},
		stat: func(path string) bool { return true },
	}
	tr := track.Track{Source: source.YouTube, SourceID: "x", SourceURL: "https://www.youtube.com/watch?v=x"}
	_, err := s.Fetch(context.Background(), tr)
	if !errors.Is(err, ErrAudioNotFound) {
		t.Fatalf("err %v, want ErrAudioNotFound", err)
	}
}
