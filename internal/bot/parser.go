package bot

import (
	"errors"
	"regexp"
)

// ErrInvalidURL is returned when the input has no Spotify track URL/URI.
var ErrInvalidURL = errors.New("invalid spotify track url")

var trackRe = regexp.MustCompile(`(?:open\.spotify\.com(?:/intl-[a-z]{2})?/track/|spotify:track:)([A-Za-z0-9]{22})`)

// ExtractSpotifyTrackID returns the 22-char Spotify track ID found in s.
// Accepts open.spotify.com URLs (with optional scheme, intl-XX segment,
// and ?si=... query) and spotify:track:... URIs.
func ExtractSpotifyTrackID(s string) (string, error) {
	m := trackRe.FindStringSubmatch(s)
	if m == nil {
		return "", ErrInvalidURL
	}
	return m[1], nil
}
