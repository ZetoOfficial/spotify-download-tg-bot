package track

// Track is the shared metadata DTO passed between all pipeline stages.
type Track struct {
	SpotifyID  string
	Artist     string
	Title      string
	Album      string
	ISRC       string
	DurationMs int
	CoverURL   string
}
