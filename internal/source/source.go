// Package source defines the audio source tag shared across track,
// pipeline, queue, and bot packages. Lives in its own package so that
// neither track nor bot needs to import the other.
package source

type Source string

const (
	Spotify Source = "spotify"
	YouTube Source = "youtube"
)
