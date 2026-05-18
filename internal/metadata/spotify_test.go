package metadata

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolve_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"tok","expires_in":3600,"token_type":"Bearer"}`))
	})
	mux.HandleFunc("/v1/tracks/abc123", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing bearer: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id":"abc123","name":"Song Title","duration_ms":210000,
			"external_ids":{"isrc":"USRC17607839"},
			"artists":[{"name":"Artist One"},{"name":"Artist Two"}],
			"album":{"name":"Album Name","images":[{"url":"https://img/lg.jpg","height":640},{"url":"https://img/sm.jpg","height":300}]}
		}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	r := NewSpotifyResolver("id", "secret",
		WithTokenURL(srv.URL+"/api/token"),
		WithAPIBase(srv.URL+"/v1"),
	)
	tr, err := r.Resolve(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tr.SpotifyID != "abc123" {
		t.Errorf("id %q", tr.SpotifyID)
	}
	if tr.Title != "Song Title" {
		t.Errorf("title %q", tr.Title)
	}
	if tr.Artist != "Artist One, Artist Two" {
		t.Errorf("artist %q", tr.Artist)
	}
	if tr.Album != "Album Name" {
		t.Errorf("album %q", tr.Album)
	}
	if tr.ISRC != "USRC17607839" {
		t.Errorf("isrc %q", tr.ISRC)
	}
	if tr.DurationMs != 210000 {
		t.Errorf("duration %d", tr.DurationMs)
	}
	if tr.CoverURL != "https://img/lg.jpg" {
		t.Errorf("cover %q", tr.CoverURL)
	}
}

func TestResolve_NotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"tok","expires_in":3600,"token_type":"Bearer"}`))
	})
	mux.HandleFunc("/v1/tracks/nope", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	r := NewSpotifyResolver("id", "secret",
		WithTokenURL(srv.URL+"/api/token"),
		WithAPIBase(srv.URL+"/v1"),
	)
	_, err := r.Resolve(context.Background(), "nope")
	if !errors.Is(err, ErrSpotifyNotFound) {
		t.Fatalf("err %v, want ErrSpotifyNotFound", err)
	}
}

func TestResolve_AuthFail(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid_client"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	r := NewSpotifyResolver("bad", "bad",
		WithTokenURL(srv.URL+"/api/token"),
		WithAPIBase(srv.URL+"/v1"),
	)
	_, err := r.Resolve(context.Background(), "abc123")
	if !errors.Is(err, ErrSpotifyAuth) {
		t.Fatalf("err %v, want ErrSpotifyAuth", err)
	}
}

func TestResolve_TokenCached(t *testing.T) {
	tokenCalls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		tokenCalls++
		w.Write([]byte(`{"access_token":"tok","expires_in":3600,"token_type":"Bearer"}`))
	})
	mux.HandleFunc("/v1/tracks/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"x","name":"x","duration_ms":1,"external_ids":{},"artists":[],"album":{}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	r := NewSpotifyResolver("id", "secret",
		WithTokenURL(srv.URL+"/api/token"),
		WithAPIBase(srv.URL+"/v1"),
	)
	for i := range 3 {
		if _, err := r.Resolve(context.Background(), "x"); err != nil {
			t.Fatalf("resolve %d: %v", i, err)
		}
	}
	if tokenCalls != 1 {
		t.Errorf("token endpoint hit %d times, want 1", tokenCalls)
	}
}
