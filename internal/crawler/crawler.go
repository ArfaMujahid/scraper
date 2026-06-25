// Package crawler is THE ENGINE: it runs one job end to end.
//
// Responsibility: a bounded worker pool reading a URL work queue (channel), a
// mutex-guarded visited-set for dedup, depth tracking, a results channel, and a
// progress-events channel — all threaded with context cancellation. Writes to
// the job's own output path. The crawler does not know the UI exists; consumers
// (terminal, SSE) attach to its event channel.
package crawler

// TODO: worker pool, queue, dedup, depth tracking, results + events channels,
// errgroup coordination, graceful drain on cancellation.
