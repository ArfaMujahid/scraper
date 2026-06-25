// Package job defines the unit of isolation: a Job owned by an OwnerID.
//
// Responsibility: Job, JobID, OwnerID types; Status (queued/running/done/
// failed) via iota; and the per-owner/per-job output-path logic
// (data/scrapes/{owner}/{job}_{ts}.{jsonl,csv}).
package job

// TODO: define JobID, OwnerID, Status, Job, and output-path helpers.
