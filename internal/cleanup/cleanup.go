// Package cleanup is the background janitor.
//
// Responsibility: on a ticker, sweep data/scrapes/{owner}/, delete job files
// whose modtime is past retention (only done/failed jobs, only our extensions),
// evict registry entries, and remove empty owner dirs. Exits on ctx.Done().
// Defensive: a failure to delete one file is logged and skipped, never fatal,
// and never touches a running job's file.
package cleanup

// TODO: ticker-driven janitor with retention-based, job-aware deletion.
