package audio

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

func TestFetch_FirstAttemptMatches(t *testing.T) {
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
	tr := track.Track{Artist: "A", Title: "T", DurationMs: 200000}
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

func TestFetch_FirstWrongDuration_RetriesAndFails(t *testing.T) {
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
	tr := track.Track{Artist: "A", Title: "T", DurationMs: 200000}
	_, err := s.Fetch(context.Background(), tr)
	if !errors.Is(err, ErrAudioNotFound) {
		t.Fatalf("err %v, want ErrAudioNotFound", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 attempts, got %d", calls)
	}
}
