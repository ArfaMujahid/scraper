// Package output streams scrape Results to a job's file as JSON Lines or CSV,
// in constant memory (results are written as they arrive, never buffered in
// full). One Writer serves one job and is owned by a single goroutine, so it
// needs no internal locking (the crawler's single-writer design).
package output

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ArfaMujahid/scraper/internal/job"
	"github.com/ArfaMujahid/scraper/internal/model"
)

// Output format identifiers, matching config's accepted values.
const (
	formatJSONL = "jsonl"
	formatCSV   = "csv"
)

// Writer serializes Results to a destination. Not safe for concurrent use: one
// Writer per job, written by a single goroutine. Close MUST be called — it
// flushes buffered data that would otherwise be lost.
type Writer interface {
	Write(r model.Result) error
	Close() error
}

// flusher is implemented by buffered writers (e.g. *bufio.Writer) so Close can
// push pending bytes to the underlying file. A plain io.Writer (a test buffer)
// simply doesn't implement it.
type flusher interface {
	Flush() error
}

// flush flushes w if it buffers; otherwise it is a no-op.
func flush(w io.Writer) error {
	if f, ok := w.(flusher); ok {
		if err := f.Flush(); err != nil {
			return fmt.Errorf("flushing buffer: %w", err)
		}
	}
	return nil
}

// jsonlWriter emits one JSON object per line.
type jsonlWriter struct {
	w   io.Writer
	enc *json.Encoder
}

// NewJSONL returns a Writer that emits one JSON object per line to w. JSON Lines
// is streamable and partial-file-safe: a crash leaves every completed line
// valid (unlike one big JSON array).
func NewJSONL(w io.Writer) Writer {
	return &jsonlWriter{w: w, enc: json.NewEncoder(w)}
}

// Write encodes r as a single JSON line (Encoder.Encode appends the newline).
func (jw *jsonlWriter) Write(r model.Result) error {
	if err := jw.enc.Encode(r); err != nil {
		return fmt.Errorf("writing jsonl record: %w", err)
	}
	return nil
}

// Close flushes any buffered bytes to the underlying writer.
func (jw *jsonlWriter) Close() error {
	return flush(jw.w)
}

// csvHeader is the column order; the row builder in Write must match it.
var csvHeader = []string{"url", "status_code", "title", "data", "links", "depth", "fetched_at", "error"}

// csvWriter emits a header row, then one row per Result.
type csvWriter struct {
	w           io.Writer
	csv         *csv.Writer
	wroteHeader bool
}

// NewCSV returns a Writer that emits a header row followed by one row per
// Result to w.
func NewCSV(w io.Writer) Writer {
	return &csvWriter{w: w, csv: csv.NewWriter(w)}
}

// Write emits the header on first call, then r as a CSV row. Nested fields are
// flattened: links joined by '|', the selector map serialized as JSON.
func (cw *csvWriter) Write(r model.Result) error {
	if !cw.wroteHeader {
		if err := cw.csv.Write(csvHeader); err != nil {
			return fmt.Errorf("writing csv header: %w", err)
		}
		cw.wroteHeader = true
	}
	row := []string{
		r.URL,
		strconv.Itoa(r.StatusCode),
		r.Title,
		flattenData(r.Data),
		strings.Join(r.Links, "|"),
		strconv.Itoa(r.Depth),
		r.FetchedAt.Format(time.RFC3339),
		r.Error,
	}
	if err := cw.csv.Write(row); err != nil {
		return fmt.Errorf("writing csv record: %w", err)
	}
	return nil
}

// Close flushes the csv writer's buffer and then the underlying writer.
func (cw *csvWriter) Close() error {
	cw.csv.Flush()
	if err := cw.csv.Error(); err != nil {
		return fmt.Errorf("flushing csv: %w", err)
	}
	return flush(cw.w)
}

// flattenData serializes the selector-match map to a compact JSON string for a
// single CSV cell, or "" when empty. map[string]string never fails to marshal.
func flattenData(data map[string]string) string {
	if len(data) == 0 {
		return ""
	}
	b, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	return string(b)
}

// NewForJob creates the job's owner directory (deferred here from job.New so the
// error is reported), opens the output file buffered, and returns the
// format-appropriate Writer plus the file as an io.Closer.
//
// Close order matters: call Writer.Close() (flushes buffered data) BEFORE
// closing the returned io.Closer (the file). With defers, defer the file close
// first so it runs last.
func NewForJob(j *job.Job, format string) (Writer, io.Closer, error) {
	dir := filepath.Dir(j.OutputPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("output: creating dir %q: %w", dir, err)
	}
	f, err := os.Create(j.OutputPath)
	if err != nil {
		return nil, nil, fmt.Errorf("output: creating file %q: %w", j.OutputPath, err)
	}
	buf := bufio.NewWriter(f)

	var w Writer
	switch format {
	case formatCSV:
		w = NewCSV(buf)
	case formatJSONL:
		w = NewJSONL(buf)
	default:
		_ = f.Close()
		return nil, nil, fmt.Errorf("output: unknown format %q", format)
	}
	return w, f, nil
}

// WriteAll encodes results to w in the given format (jsonl or csv), flushing on
// completion. Used to serve a job's records in a format other than the one it
// was written in on disk.
func WriteAll(w io.Writer, format string, results []model.Result) error {
	var ow Writer
	switch format {
	case formatCSV:
		ow = NewCSV(w)
	case formatJSONL:
		ow = NewJSONL(w)
	default:
		return fmt.Errorf("output: unknown format %q", format)
	}
	for _, r := range results {
		if err := ow.Write(r); err != nil {
			return err
		}
	}
	return ow.Close()
}
