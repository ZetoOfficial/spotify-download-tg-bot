package bot

import (
	"errors"
	"regexp"

	"github.com/ZetoOfficial/spotify-download-tg-bot/internal/source"
)

// ErrInvalidURL is returned when the input has neither a Spotify track URL/URI
// nor a supported YouTube URL.
var ErrInvalidURL = errors.New("invalid track url")

// ParsedLink is the result of recognizing a supported source link.
type ParsedLink struct {
	Source source.Source
	ID     string // spotify track id or youtube video id
	URL    string // canonical URL — for YouTube, https://www.youtube.com/watch?v=<id>
}

var (
	spotifyRe = regexp.MustCompile(`(?:open\.spotify\.com(?:/intl-[a-z]{2})?/track/|spotify:track:)([A-Za-z0-9]{22})`)
	// Anchor youtube.com / youtu.be at a non-letter-non-dot boundary so that
	// music.youtube.com (a different unsupported host) doesn't match.
	youtubeRe = regexp.MustCompile(`(?:^|[\s/])(?:www\.|m\.)?(?:youtube\.com/watch\?(?:[^\s&]+&)*v=|youtu\.be/)([A-Za-z0-9_-]{11})`)
)

// ParseLink returns the first supported track link found in s.
// Spotify is tried before YouTube.
func ParseLink(s string) (ParsedLink, error) {
	if m := spotifyRe.FindStringSubmatch(s); m != nil {
		return ParsedLink{
			Source: source.Spotify,
			ID:     m[1],
			URL:    "https://open.spotify.com/track/" + m[1],
		}, nil
	}
	if m := youtubeRe.FindStringSubmatch(s); m != nil {
		return ParsedLink{
			Source: source.YouTube,
			ID:     m[1],
			URL:    "https://www.youtube.com/watch?v=" + m[1],
		}, nil
	}
	return ParsedLink{}, ErrInvalidURL
}
