package track

import "github.com/ZetoOfficial/spotify-download-tg-bot/internal/source"

// Track is the shared metadata DTO passed between all pipeline stages.
type Track struct {
	Source     source.Source
	SourceID   string // spotify id or youtube video id
	SourceURL  string // canonical source URL (used by yt-dlp for YouTube and for logs)
	Artist     string
	Title      string
	Album      string
	ISRC       string // populated only for Spotify
	DurationMs int
	CoverURL   string
}
