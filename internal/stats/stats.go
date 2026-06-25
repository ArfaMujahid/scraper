// Package stats holds thread-safe live metrics for a running job.
//
// Responsibility: counters updated by workers (done, in-flight, errors, bytes,
// throughput) per running job, aggregated from events for the UI and terminal.
package stats

// TODO: thread-safe counters (atomics/mutex) + throughput calculation.
