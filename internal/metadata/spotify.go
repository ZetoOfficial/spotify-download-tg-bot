package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/track"
)

var (
	ErrSpotifyNotFound = errors.New("spotify track not found")
	ErrSpotifyAuth     = errors.New("spotify auth failed")
)

// Resolver fetches Track metadata for a Spotify track ID.
type Resolver interface {
	Resolve(ctx context.Context, spotifyID string) (track.Track, error)
}

const (
	defaultTokenURL = "https://accounts.spotify.com/api/token"
	defaultAPIBase  = "https://api.spotify.com/v1"
)

type SpotifyResolver struct {
	clientID, clientSecret string
	tokenURL, apiBase      string
	http                   *http.Client

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

type Option func(*SpotifyResolver)

func WithTokenURL(u string) Option { return func(r *SpotifyResolver) { r.tokenURL = u } }
func WithAPIBase(u string) Option  { return func(r *SpotifyResolver) { r.apiBase = u } }
func WithHTTPClient(c *http.Client) Option {
	return func(r *SpotifyResolver) { r.http = c }
}

func NewSpotifyResolver(clientID, clientSecret string, opts ...Option) *SpotifyResolver {
	r := &SpotifyResolver{
		clientID:     clientID,
		clientSecret: clientSecret,
		tokenURL:     defaultTokenURL,
		apiBase:      defaultAPIBase,
		http:         &http.Client{Timeout: 15 * time.Second},
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

func (r *SpotifyResolver) Resolve(ctx context.Context, spotifyID string) (track.Track, error) {
	tok, err := r.getToken(ctx)
	if err != nil {
		return track.Track{}, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", r.apiBase+"/tracks/"+spotifyID, nil)
	if err != nil {
		return track.Track{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := r.http.Do(req)
	if err != nil {
		return track.Track{}, fmt.Errorf("spotify track request: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 200:
	case 401, 403:
		return track.Track{}, ErrSpotifyAuth
	case 404:
		return track.Track{}, ErrSpotifyNotFound
	default:
		body, _ := io.ReadAll(resp.Body)
		return track.Track{}, fmt.Errorf("spotify status %d: %s", resp.StatusCode, string(body))
	}
	var payload struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		DurationMs  int    `json:"duration_ms"`
		ExternalIDs struct {
			ISRC string `json:"isrc"`
		} `json:"external_ids"`
		Artists []struct {
			Name string `json:"name"`
		} `json:"artists"`
		Album struct {
			Name   string `json:"name"`
			Images []struct {
				URL    string `json:"url"`
				Height int    `json:"height"`
			} `json:"images"`
		} `json:"album"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return track.Track{}, fmt.Errorf("decode track: %w", err)
	}
	names := make([]string, 0, len(payload.Artists))
	for _, a := range payload.Artists {
		names = append(names, a.Name)
	}
	cover := ""
	bestH := -1
	for _, im := range payload.Album.Images {
		if im.Height > bestH {
			bestH = im.Height
			cover = im.URL
		}
	}
	return track.Track{
		SpotifyID:  payload.ID,
		Artist:     strings.Join(names, ", "),
		Title:      payload.Name,
		Album:      payload.Album.Name,
		ISRC:       payload.ExternalIDs.ISRC,
		DurationMs: payload.DurationMs,
		CoverURL:   cover,
	}, nil
}

func (r *SpotifyResolver) getToken(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.token != "" && time.Now().Before(r.expiresAt.Add(-60*time.Second)) {
		return r.token, nil
	}
	body := strings.NewReader(url.Values{"grant_type": {"client_credentials"}}.Encode())
	req, err := http.NewRequestWithContext(ctx, "POST", r.tokenURL, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(r.clientID, r.clientSecret)
	resp, err := r.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 400 {
		return "", ErrSpotifyAuth
	}
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token status %d: %s", resp.StatusCode, string(raw))
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", err
	}
	r.token = tr.AccessToken
	r.expiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return r.token, nil
}
