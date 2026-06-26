// Package stats holds thread-safe live counters for one running job, surfaced to
// the terminal summary and (later) the dashboard.
package stats

import (
	"sync/atomic"
	"time"
)

// Stats holds live, concurrently-updated counters for one job. Use the pointer
// from New and never copy a Stats — it holds atomics.
type Stats struct {
	done     atomic.Int64
	errors   atomic.Int64
	inFlight atomic.Int64
	bytes    atomic.Int64
	start    time.Time
}

// New returns a Stats whose elapsed time is measured from now.
func New() *Stats {
	return &Stats{start: time.Now()}
}

// IncDone records one successfully scraped page.
func (s *Stats) IncDone() { s.done.Add(1) }

// IncError records one page that failed to scrape.
func (s *Stats) IncError() { s.errors.Add(1) }

// AddInFlight adjusts the in-flight request count by delta (+1 before a fetch,
// -1 after).
func (s *Stats) AddInFlight(delta int64) { s.inFlight.Add(delta) }

// AddBytes adds n to the running total of downloaded bytes.
func (s *Stats) AddBytes(n int64) { s.bytes.Add(n) }

// Snapshot is an immutable view of the counters plus derived throughput, safe
// to pass around and serialize.
type Snapshot struct {
	Done        int64
	Errors      int64
	InFlight    int64
	Bytes       int64
	Elapsed     time.Duration
	PagesPerSec float64
}

// Snapshot reads each counter atomically and computes throughput. Because the
// counters are independent atomics the view is near-instantaneous rather than a
// single locked instant — which is fine for live metrics.
func (s *Stats) Snapshot() Snapshot {
	done := s.done.Load()
	elapsed := time.Since(s.start)
	var pps float64
	if secs := elapsed.Seconds(); secs > 0 {
		pps = float64(done) / secs
	}
	return Snapshot{
		Done:        done,
		Errors:      s.errors.Load(),
		InFlight:    s.inFlight.Load(),
		Bytes:       s.bytes.Load(),
		Elapsed:     elapsed,
		PagesPerSec: pps,
	}
}
