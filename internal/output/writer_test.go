package output

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ArfaMujahid/scraper/internal/job"
	"github.com/ArfaMujahid/scraper/internal/model"
)

func sampleResults() []model.Result {
	ts := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	return []model.Result{
		{
			URL:        "https://example.com/a",
			StatusCode: 200,
			Title:      "Page A",
			Data:       map[string]string{"price": "$9.99"},
			Links:      []string{"https://example.com/b", "https://example.com/c"},
			Depth:      0,
			FetchedAt:  ts,
		},
		{
			URL:        "https://example.com/b",
			StatusCode: 404,
			Depth:      1,
			FetchedAt:  ts,
			Error:      "not found",
		},
	}
}

func TestJSONLWritesValidLines(t *testing.T) {
	var buf bytes.Buffer
	w := NewJSONL(&buf)
	results := sampleResults()
	for _, r := range results {
		if err := w.Write(r); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != len(results) {
		t.Fatalf("got %d lines, want %d", len(lines), len(results))
	}
	for i, line := range lines {
		var got model.Result
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", i, err)
		}
		if diff := cmp.Diff(results[i], got, cmpopts.EquateApproxTime(time.Second)); diff != "" {
			t.Errorf("line %d mismatch (-want +got):\n%s", i, diff)
		}
	}
}

func TestJSONLOmitsEmptyFields(t *testing.T) {
	var buf bytes.Buffer
	w := NewJSONL(&buf)
	// A bare result: omitempty should drop title/data/links/error.
	if err := w.Write(model.Result{URL: "https://x", StatusCode: 200}); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	line := strings.TrimSpace(buf.String())
	for _, field := range []string{"title", "data", "links", "error"} {
		if strings.Contains(line, `"`+field+`"`) {
			t.Errorf("expected %q to be omitted, line: %s", field, line)
		}
	}
	for _, field := range []string{"url", "status_code", "depth", "fetched_at"} {
		if !strings.Contains(line, `"`+field+`"`) {
			t.Errorf("expected %q to be present, line: %s", field, line)
		}
	}
}

func TestCSVWritesHeaderAndRows(t *testing.T) {
	var buf bytes.Buffer
	w := NewCSV(&buf)
	results := sampleResults()
	for _, r := range results {
		if err := w.Write(r); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatalf("parsing CSV: %v", err)
	}
	if len(rows) != len(results)+1 { // header + rows
		t.Fatalf("got %d rows, want %d", len(rows), len(results)+1)
	}
	if diff := cmp.Diff(csvHeader, rows[0]); diff != "" {
		t.Errorf("header mismatch (-want +got):\n%s", diff)
	}
	// Row 1: links joined with '|', data serialized as JSON.
	if rows[1][4] != "https://example.com/b|https://example.com/c" {
		t.Errorf("links cell = %q", rows[1][4])
	}
	if rows[1][3] != `{"price":"$9.99"}` {
		t.Errorf("data cell = %q", rows[1][3])
	}
	if rows[2][1] != "404" || rows[2][7] != "not found" {
		t.Errorf("error row = %v", rows[2])
	}
}

func TestNewForJobRoundTrip(t *testing.T) {
	for _, format := range []string{"jsonl", "csv"} {
		t.Run(format, func(t *testing.T) {
			dataDir := t.TempDir()
			j := job.New("alice", []string{"https://example.com"}, dataDir, format)

			w, closer, err := NewForJob(j, format)
			if err != nil {
				t.Fatalf("NewForJob: %v", err)
			}
			for _, r := range sampleResults() {
				if err := w.Write(r); err != nil {
					t.Fatalf("Write: %v", err)
				}
			}
			if err := w.Close(); err != nil { // flush first
				t.Fatalf("Writer.Close: %v", err)
			}
			if err := closer.Close(); err != nil { // then close the file
				t.Fatalf("file Close: %v", err)
			}

			data, err := os.ReadFile(j.OutputPath)
			if err != nil {
				t.Fatalf("reading output: %v", err)
			}
			if len(data) == 0 {
				t.Fatal("output file is empty")
			}
			if !strings.Contains(string(data), "https://example.com/a") {
				t.Errorf("output missing expected content:\n%s", data)
			}
		})
	}
}

func TestNewForJobFlushOnCloseIsMandatory(t *testing.T) {
	dataDir := t.TempDir()
	j := job.New("bob", nil, dataDir, "jsonl")

	w, closer, err := NewForJob(j, "jsonl")
	if err != nil {
		t.Fatalf("NewForJob: %v", err)
	}
	t.Cleanup(func() { _ = closer.Close() })

	if err := w.Write(model.Result{URL: "https://x", StatusCode: 200}); err != nil {
		t.Fatal(err)
	}
	// Before Close, the bufio buffer holds the data: the file is still empty.
	if data, _ := os.ReadFile(j.OutputPath); len(data) != 0 {
		t.Fatalf("expected empty file before flush, got %d bytes", len(data))
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if data, _ := os.ReadFile(j.OutputPath); len(data) == 0 {
		t.Fatal("expected data in file after Close (flush)")
	}
}

func TestNewForJobUnknownFormat(t *testing.T) {
	j := job.New("c", nil, t.TempDir(), "xml")
	if _, _, err := NewForJob(j, "xml"); err == nil {
		t.Error("expected error for unknown format")
	}
}
