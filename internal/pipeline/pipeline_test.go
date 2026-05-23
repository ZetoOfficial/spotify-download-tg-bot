package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/audio"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/cache"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/metadata"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/source"
	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

type fakeResolver struct {
	tr     track.Track
	err    error
	gotKey string
	calls  int
}

func (f *fakeResolver) Resolve(ctx context.Context, key string) (track.Track, error) {
	f.calls++
	f.gotKey = key
	return f.tr, f.err
}

type fakeCache struct {
	entry    cache.Entry
	hit      bool
	saved    []cache.Entry
	savedKey []string
	touch    int
	touchKey string
}

func (f *fakeCache) Lookup(ctx context.Context, key string) (cache.Entry, bool, error) {
	return f.entry, f.hit, nil
}

func (f *fakeCache) Save(ctx context.Context, key string, e cache.Entry, artist, title, album string, dur int) error {
	f.saved = append(f.saved, e)
	f.savedKey = append(f.savedKey, key)
	return nil
}

func (f *fakeCache) Touch(ctx context.Context, key string) error {
	f.touch++
	f.touchKey = key
	return nil
}

type fakeAudio struct {
	path string
	err  error
	got  track.Track
}

func (f *fakeAudio) Fetch(ctx context.Context, t track.Track) (string, error) {
	f.got = t
	return f.path, f.err
}

type fakeTranscoder struct{ err error }

func (f *fakeTranscoder) ToMP3(ctx context.Context, raw string, t track.Track, dir string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "/tmp/" + t.SourceID + ".mp3", nil
}

type fakeUploader struct {
	fileID          string
	uploaded        []string
	uploadedReplyTo []int
	sent            []string
	sentReplyTo     []int
	uploadErr       error
}

func (f *fakeUploader) Upload(ctx context.Context, chatID int64, p string, t track.Track, replyToMessageID int) (string, error) {
	f.uploaded = append(f.uploaded, p)
	f.uploadedReplyTo = append(f.uploadedReplyTo, replyToMessageID)
	return f.fileID, f.uploadErr
}

func (f *fakeUploader) SendCached(ctx context.Context, chatID int64, fileID string, replyToMessageID int) error {
	f.sent = append(f.sent, fileID)
	f.sentReplyTo = append(f.sentReplyTo, replyToMessageID)
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
		Resolvers: map[source.Source]metadata.Resolver{
			source.Spotify: r,
			source.YouTube: r,
		},
		Cache:      c,
		Audio:      a,
		Transcoder: tc,
		Uploader:   u,
		Notifier:   n,
		CacheDir:   "/tmp",
	}
}

func spotifyJob(id string) Job {
	return Job{
		ChatID:    1,
		Source:    source.Spotify,
		SourceID:  id,
		SourceURL: "https://open.spotify.com/track/" + id,
	}
}

func youtubeJob(id string) Job {
	url := "https://www.youtube.com/watch?v=" + id
	return Job{
		ChatID:    1,
		Source:    source.YouTube,
		SourceID:  id,
		SourceURL: url,
	}
}

func TestPipeline_CacheHitWithFileID_SendsCached(t *testing.T) {
	c := &fakeCache{entry: cache.Entry{FileID: "fid"}, hit: true}
	n := &fakeNotifier{}
	p := newPipeline(
		&fakeResolver{tr: track.Track{SourceID: "x"}},
		c,
		&fakeAudio{}, &fakeTranscoder{}, &fakeUploader{}, n,
	)
	p.Process(context.Background(), spotifyJob("x"))
	if c.touch == 0 {
		t.Error("expected Touch on hit")
	}
	if c.touchKey != "sp:x" {
		t.Errorf("touchKey = %q, want sp:x", c.touchKey)
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
		&fakeResolver{tr: track.Track{SourceID: "x", DurationMs: 100000}},
		c, &fakeAudio{path: "/tmp/raw.m4a"}, &fakeTranscoder{}, u, n,
	)
	j := spotifyJob("x")
	j.OriginalMessageID = 777
	p.Process(context.Background(), j)
	if len(u.uploaded) != 1 {
		t.Errorf("uploads: %v", u.uploaded)
	}
	if len(u.uploadedReplyTo) != 1 || u.uploadedReplyTo[0] != 777 {
		t.Errorf("expected reply_to=777 on upload, got %v", u.uploadedReplyTo)
	}
	if len(c.saved) != 1 || c.saved[0].FileID != "newfid" {
		t.Errorf("saved: %+v", c.saved)
	}
	if len(c.savedKey) != 1 || c.savedKey[0] != "sp:x" {
		t.Errorf("savedKey = %v, want [sp:x]", c.savedKey)
	}
}

func TestPipeline_CacheHit_ForwardsReplyTo(t *testing.T) {
	c := &fakeCache{entry: cache.Entry{FileID: "fid"}, hit: true}
	u := &fakeUploader{}
	n := &fakeNotifier{}
	p := newPipeline(
		&fakeResolver{tr: track.Track{SourceID: "x"}},
		c, &fakeAudio{}, &fakeTranscoder{}, u, n,
	)
	j := spotifyJob("x")
	j.OriginalMessageID = 555
	p.Process(context.Background(), j)
	if len(u.sentReplyTo) != 1 || u.sentReplyTo[0] != 555 {
		t.Errorf("expected reply_to=555 on SendCached, got %v", u.sentReplyTo)
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
		&fakeResolver{tr: track.Track{SourceID: "x"}},
		c, &fakeAudio{}, &fakeTranscoder{}, u, n,
	)
	p.Process(context.Background(), spotifyJob("x"))
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
	p.Process(context.Background(), spotifyJob("x"))
	if len(n.errs) != 1 {
		t.Fatalf("errs: %v", n.errs)
	}
}

func TestPipeline_AudioNotFound_EditsErrorReply(t *testing.T) {
	n := &fakeNotifier{}
	p := newPipeline(
		&fakeResolver{tr: track.Track{SourceID: "x"}},
		&fakeCache{}, &fakeAudio{err: audio.ErrAudioNotFound},
		&fakeTranscoder{}, &fakeUploader{}, n,
	)
	p.Process(context.Background(), spotifyJob("x"))
	if len(n.errs) != 1 {
		t.Fatalf("errs: %v", n.errs)
	}
}

func TestPipeline_TranscodeError_Propagates(t *testing.T) {
	n := &fakeNotifier{}
	p := newPipeline(
		&fakeResolver{tr: track.Track{SourceID: "x"}},
		&fakeCache{}, &fakeAudio{path: "/tmp/raw"},
		&fakeTranscoder{err: errors.New("boom")},
		&fakeUploader{}, n,
	)
	p.Process(context.Background(), spotifyJob("x"))
	if len(n.errs) != 1 {
		t.Fatalf("errs: %v", n.errs)
	}
}

func TestPipeline_YouTubeFlow_UsesYtCacheKey(t *testing.T) {
	c := &fakeCache{hit: false}
	r := &fakeResolver{tr: track.Track{SourceID: "yt-id-overwritten", Artist: "A", Title: "T", DurationMs: 200000}}
	u := &fakeUploader{fileID: "newfid"}
	n := &fakeNotifier{}
	a := &fakeAudio{path: "/tmp/raw.m4a"}
	p := newPipeline(r, c, a, &fakeTranscoder{}, u, n)
	p.Process(context.Background(), youtubeJob("dQw4w9WgXcQ"))

	if r.gotKey != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Errorf("resolver got %q, want canonical URL", r.gotKey)
	}
	// Pipeline must overwrite SourceID with the Job's value before audio fetch.
	if a.got.SourceID != "dQw4w9WgXcQ" {
		t.Errorf("audio got SourceID %q, want dQw4w9WgXcQ", a.got.SourceID)
	}
	if a.got.Source != source.YouTube {
		t.Errorf("audio got Source %q, want youtube", a.got.Source)
	}
	if len(c.savedKey) != 1 || c.savedKey[0] != "yt:dQw4w9WgXcQ" {
		t.Errorf("savedKey = %v, want [yt:dQw4w9WgXcQ]", c.savedKey)
	}
}

func TestPipeline_TrackTooLong_EditsErrorReply(t *testing.T) {
	n := &fakeNotifier{}
	p := newPipeline(
		&fakeResolver{err: metadata.ErrTrackTooLong},
		&fakeCache{}, &fakeAudio{}, &fakeTranscoder{}, &fakeUploader{}, n,
	)
	p.Process(context.Background(), youtubeJob("dQw4w9WgXcQ"))
	if len(n.errs) != 1 || n.errs[0] != "максимум 5 минут" {
		t.Fatalf("errs = %v", n.errs)
	}
}

func TestPipeline_UnknownSource_EditsErrorReply(t *testing.T) {
	n := &fakeNotifier{}
	p := &Pipeline{
		Resolvers:  map[source.Source]metadata.Resolver{},
		Cache:      &fakeCache{},
		Audio:      &fakeAudio{},
		Transcoder: &fakeTranscoder{},
		Uploader:   &fakeUploader{},
		Notifier:   n,
		CacheDir:   "/tmp",
	}
	p.Process(context.Background(), Job{ChatID: 1, Source: source.Source("weird"), SourceID: "x"})
	if len(n.errs) != 1 {
		t.Fatalf("errs: %v", n.errs)
	}
}
