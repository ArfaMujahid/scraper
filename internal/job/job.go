// Package job defines a scrape's identity, status, and the per-owner/per-job
// output-path rule. A Job is the unit of isolation: its output lives under
// data/scrapes/{owner}/{id}_{ts}.{ext} and no two jobs ever share a file.
//
// Types and signatures follow implementation-design.md §2 and its shared-types
// section.
package job

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// scrapesDir is the fixed sub-directory under the data root that holds all job
// output. Exposed via the path helpers so cleanup reuses the exact same layout.
const scrapesDir = "scrapes"

// tsLayout is a sortable, filesystem-safe UTC timestamp (no colons, so it is
// valid on Windows too — NFR-D2).
const tsLayout = "20060102T150405Z"

// OwnerID identifies who a job belongs to. Opaque to the rest of the system;
// derived from --owner (CLI) or a session cookie (UI).
type OwnerID string

// ID uniquely identifies one scrape job (a UUID string).
type ID string

// Status is the lifecycle state of a job.
type Status int

const (
	StatusQueued  Status = iota // created, not yet started
	StatusRunning               // crawling in progress
	StatusDone                  // finished successfully
	StatusFailed                // finished with a fatal error
)

// String returns the lowercase status name, used in logs and the dashboard.
func (s Status) String() string {
	switch s {
	case StatusQueued:
		return "queued"
	case StatusRunning:
		return "running"
	case StatusDone:
		return "done"
	case StatusFailed:
		return "failed"
	default:
		return fmt.Sprintf("Status(%d)", int(s))
	}
}

// Job is one scrape: its identity, owner, seeds, live status, and isolated
// output path. It is not safe for concurrent use; the registry serializes
// access to a running job's mutable fields (see implementation-design.md §9).
type Job struct {
	ID         ID
	Owner      OwnerID
	Seeds      []string
	Status     Status
	OutputPath string    // set once at creation; the only file this job writes
	StartedAt  time.Time // zero until running
	EndedAt    time.Time // zero until terminal
	Err        string    // set when Status == StatusFailed
}

// New creates a queued job for the given owner and seeds, generating a fresh
// UUID and computing its isolated output path under dataDir.
//
// New is pure: it computes the path but does not touch the filesystem. The
// owner directory is created (and any error reported) by the output writer when
// the file is opened, which keeps New's no-error signature honest under the
// errcheck rule.
func New(owner OwnerID, seeds []string, dataDir, format string) *Job {
	id := ID(uuid.NewString())
	return &Job{
		ID:         id,
		Owner:      owner,
		Seeds:      seeds,
		Status:     StatusQueued,
		OutputPath: outputPath(dataDir, owner, id, format),
	}
}

// ScrapesDir returns the root directory holding all job output under dataDir.
// The cleanup janitor sweeps exactly this directory, so the path scheme has a
// single source of truth here.
func ScrapesDir(dataDir string) string {
	return filepath.Join(dataDir, scrapesDir)
}

// OwnerDir returns the directory holding one owner's job output. The owner is
// sanitized so untrusted input can never escape the data directory.
func OwnerDir(dataDir string, owner OwnerID) string {
	return filepath.Join(ScrapesDir(dataDir), sanitize(string(owner)))
}

// outputPath returns data/scrapes/{owner}/{id}_{ts}.{ext}, the file this job
// (and only this job) writes to.
func outputPath(dataDir string, owner OwnerID, id ID, format string) string {
	ts := time.Now().UTC().Format(tsLayout)
	name := fmt.Sprintf("%s_%s.%s", id, ts, format)
	return filepath.Join(OwnerDir(dataDir, owner), name)
}

// sanitize maps any character outside [A-Za-z0-9_-] to '_', producing a single
// filesystem-safe path component. This neutralizes path separators and ".." so
// a user-supplied --owner can never traverse out of the data directory — the
// one place untrusted input reaches the filesystem. Empty input collapses to a
// stable fallback rather than an empty path segment.
func sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "default"
	}
	return b.String()
}
