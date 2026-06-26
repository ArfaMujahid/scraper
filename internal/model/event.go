package model

// EventType classifies a progress Event.
type EventType int

const (
	// EventPageDone is emitted after a page is successfully scraped.
	EventPageDone EventType = iota
	// EventPageError is emitted after a page fails (recorded, not fatal).
	EventPageError
)

// Event is a progress signal the crawler emits for the stats/UI layer (fan-out).
// It lives in this leaf package so consumers (the web dashboard) can read it
// without importing crawler. It carries enough to drive a live scraped-content
// feed.
type Event struct {
	Type       EventType
	URL        string
	Title      string
	StatusCode int
	Bytes      int
	Err        error
}
