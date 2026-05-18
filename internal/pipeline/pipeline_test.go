package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/audio"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/cache"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/metadata"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

type fakeResolver struct {
	tr  track.Track
	err error
}

func (f *fakeResolver) Resolve(ctx context.Context, spotifyID string) (track.Track, error) {
	return f.tr, f.err
}

type fakeCache struct {
	entry cache.Entry
	hit   bool
	saved []cache.Entry
	touch int
}

func (f *fakeCache) Lookup(ctx context.Context, id string) (cache.Entry, bool, error) {
	return f.entry, f.hit, nil
}

func (f *fakeCache) Save(ctx context.Context, id string, e cache.Entry, artist, title, album string, dur int) error {
	f.saved = append(f.saved, e)
	return nil
}

func (f *fakeCache) Touch(ctx context.Context, id string) error {
	f.touch++
	return nil
}

type fakeAudio struct {
	path string
	err  error
}

func (f *fakeAudio) Fetch(ctx context.Context, t track.Track) (string, error) {
	return f.path, f.err
}

type fakeTranscoder struct{ err error }

func (f *fakeTranscoder) ToMP3(ctx context.Context, raw string, t track.Track, dir string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "/tmp/" + t.SpotifyID + ".mp3", nil
}

type fakeUploader struct {
	fileID    string
	uploaded  []string
	sent      []string
	uploadErr error
}

func (f *fakeUploader) Upload(ctx context.Context, chatID int64, p string, t track.Track) (string, error) {
	f.uploaded = append(f.uploaded, p)
	return f.fileID, f.uploadErr
}

func (f *fakeUploader) SendCached(ctx context.Context, chatID int64, fileID string) error {
	f.sent = append(f.sent, fileID)
	return nil
}

type fakeNotifier struct {
	progress []string
	done     []string
	errs     []string
}

func (f *fakeNotifier) Progress(chatID int64, msgID int, text string) {
	f.progress = append(f.progress, text)
}
func (f *fakeNotifier) Done(chatID int64, msgID int) { f.done = append(f.done, "done") }
func (f *fakeNotifier) Error(chatID int64, msgID int, userMessage string) {
	f.errs = append(f.errs, userMessage)
}

func newPipeline(r *fakeResolver, c *fakeCache, a *fakeAudio, tc *fakeTranscoder, u *fakeUploader, n *fakeNotifier) *Pipeline {
	return &Pipeline{
		Resolver:   r,
		Cache:      c,
		Audio:      a,
		Transcoder: tc,
		Uploader:   u,
		Notifier:   n,
		CacheDir:   "/tmp",
	}
}

func TestPipeline_CacheHitWithFileID_SendsCached(t *testing.T) {
	c := &fakeCache{entry: cache.Entry{FileID: "fid"}, hit: true}
	n := &fakeNotifier{}
	p := newPipeline(
		&fakeResolver{tr: track.Track{SpotifyID: "x"}},
		c,
		&fakeAudio{}, &fakeTranscoder{}, &fakeUploader{}, n,
	)
	p.Process(context.Background(), Job{ChatID: 1, SpotifyID: "x", SpotifyURL: "u"})
	if c.touch == 0 {
		t.Error("expected Touch on hit")
	}
	if len(n.done) != 1 {
		t.Errorf("done events: %v", n.done)
	}
}

func TestPipeline_FullPath_FetchTranscodeUpload(t *testing.T) {
	c := &fakeCache{hit: false}
	u := &fakeUploader{fileID: "newfid"}
	n := &fakeNotifier{}
	p := newPipeline(
		&fakeResolver{tr: track.Track{SpotifyID: "x", DurationMs: 100000}},
		c, &fakeAudio{path: "/tmp/raw.m4a"}, &fakeTranscoder{}, u, n,
	)
	p.Process(context.Background(), Job{ChatID: 1, SpotifyID: "x", SpotifyURL: "u"})
	if len(u.uploaded) != 1 {
		t.Errorf("uploads: %v", u.uploaded)
	}
	if len(c.saved) != 1 || c.saved[0].FileID != "newfid" {
		t.Errorf("saved: %+v", c.saved)
	}
}

func TestPipeline_PartialHitLocalPath_UploadsAndSaves(t *testing.T) {
	c := &fakeCache{
		entry: cache.Entry{LocalPath: "/tmp/cached.mp3"},
		hit:   true,
	}
	u := &fakeUploader{fileID: "fresh-fid"}
	n := &fakeNotifier{}
	p := newPipeline(
		&fakeResolver{tr: track.Track{SpotifyID: "x"}},
		c, &fakeAudio{}, &fakeTranscoder{}, u, n,
	)
	p.Process(context.Background(), Job{ChatID: 1, SpotifyID: "x", SpotifyURL: "u"})
	if len(u.uploaded) != 1 || u.uploaded[0] != "/tmp/cached.mp3" {
		t.Errorf("uploaded: %v", u.uploaded)
	}
	if len(c.saved) != 1 || c.saved[0].FileID != "fresh-fid" {
		t.Errorf("saved: %+v", c.saved)
	}
}

func TestPipeline_ResolverNotFound_EditsErrorReply(t *testing.T) {
	n := &fakeNotifier{}
	p := newPipeline(
		&fakeResolver{err: metadata.ErrSpotifyNotFound},
		&fakeCache{}, &fakeAudio{}, &fakeTranscoder{}, &fakeUploader{}, n,
	)
	p.Process(context.Background(), Job{ChatID: 1, SpotifyID: "x", SpotifyURL: "u"})
	if len(n.errs) != 1 {
		t.Fatalf("errs: %v", n.errs)
	}
}

func TestPipeline_AudioNotFound_EditsErrorReply(t *testing.T) {
	n := &fakeNotifier{}
	p := newPipeline(
		&fakeResolver{tr: track.Track{SpotifyID: "x"}},
		&fakeCache{}, &fakeAudio{err: audio.ErrAudioNotFound},
		&fakeTranscoder{}, &fakeUploader{}, n,
	)
	p.Process(context.Background(), Job{ChatID: 1, SpotifyID: "x", SpotifyURL: "u"})
	if len(n.errs) != 1 {
		t.Fatalf("errs: %v", n.errs)
	}
}

func TestPipeline_TranscodeError_Propagates(t *testing.T) {
	n := &fakeNotifier{}
	p := newPipeline(
		&fakeResolver{tr: track.Track{SpotifyID: "x"}},
		&fakeCache{}, &fakeAudio{path: "/tmp/raw"},
		&fakeTranscoder{err: errors.New("boom")},
		&fakeUploader{}, n,
	)
	p.Process(context.Background(), Job{ChatID: 1, SpotifyID: "x", SpotifyURL: "u"})
	if len(n.errs) != 1 {
		t.Fatalf("errs: %v", n.errs)
	}
}
