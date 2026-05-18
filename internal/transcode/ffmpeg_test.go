package transcode

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

func TestToMP3_ArgvAndOutputPath(t *testing.T) {
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("img-bytes"))
	}))
	defer imgSrv.Close()

	var captured []string
	tc := &FFmpeg{
		binary: "ffmpeg",
		http:   imgSrv.Client(),
		execRun: func(ctx context.Context, args []string) ([]byte, error) {
			captured = args
			return nil, nil
		},
	}
	dir := t.TempDir()
	raw := filepath.Join(dir, "in.m4a")
	tr := track.Track{
		SpotifyID: "abc",
		Artist:    "A",
		Title:     "T",
		Album:     "Al",
		CoverURL:  imgSrv.URL + "/cover.jpg",
	}
	out, err := tc.ToMP3(context.Background(), raw, tr, dir)
	if err != nil {
		t.Fatalf("to mp3: %v", err)
	}
	if out != filepath.Join(dir, "abc.mp3") {
		t.Errorf("out %q", out)
	}
	joined := strings.Join(captured, " ")
	for _, want := range []string{
		"-i", raw, "-c:a", "libmp3lame", "-b:a", "320k",
		"-id3v2_version", "3",
		"-metadata", "title=T",
		"-metadata", "artist=A",
		"-metadata", "album=Al",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q in: %s", want, joined)
		}
	}
}

func TestToMP3_CoverFetchFailDoesNotFail(t *testing.T) {
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer badSrv.Close()
	tc := &FFmpeg{
		binary:  "ffmpeg",
		http:    badSrv.Client(),
		execRun: func(ctx context.Context, args []string) ([]byte, error) { return nil, nil },
	}
	dir := t.TempDir()
	tr := track.Track{SpotifyID: "abc", CoverURL: badSrv.URL + "/x.jpg"}
	if _, err := tc.ToMP3(context.Background(), filepath.Join(dir, "in.m4a"), tr, dir); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
}

func TestToMP3_ExecFails(t *testing.T) {
	tc := &FFmpeg{
		binary:  "ffmpeg",
		http:    http.DefaultClient,
		execRun: func(ctx context.Context, args []string) ([]byte, error) { return []byte("bad"), errors.New("boom") },
	}
	dir := t.TempDir()
	_, err := tc.ToMP3(context.Background(), filepath.Join(dir, "in.m4a"), track.Track{SpotifyID: "x"}, dir)
	if !errors.Is(err, ErrTranscode) {
		t.Fatalf("err %v, want ErrTranscode", err)
	}
}
