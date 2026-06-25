// Package output provides streaming result writers (JSON Lines / CSV).
//
// Responsibility: JSONL & CSV writers built on io.Writer, wrapped in bufio with
// flush-on-close; stream results to the job's file without buffering everything
// in memory. A Writer interface keeps formats pluggable.
package output

// TODO: Writer interface + JSONL and CSV implementations (bufio, flush on close).
