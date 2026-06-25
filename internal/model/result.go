// Package model holds the data types that cross package boundaries — the
// crawler produces them; output, stats, and the web layer consume them. They
// live in this leaf package (imported by everyone, importing no internal
// package) to avoid the import cycle that would arise if they sat in crawler
// (coding-standards §7; implementation-design.md shared-types note).
package model

import "time"

// Result is one scraped page's output record: one JSONL line or one CSV row.
// The json tags define the on-disk JSONL schema.
type Result struct {
	URL        string            `json:"url"`
	StatusCode int               `json:"status_code"`
	Title      string            `json:"title,omitempty"`
	Data       map[string]string `json:"data,omitempty"` // CSS-selector matches
	Links      []string          `json:"links,omitempty"`
	Depth      int               `json:"depth"`
	FetchedAt  time.Time         `json:"fetched_at"`
	Error      string            `json:"error,omitempty"` // recorded per-page, not fatal
}
