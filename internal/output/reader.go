package output

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/ArfaMujahid/scraper/internal/model"
)

// maxLine bounds a single JSONL line so a malformed file can't exhaust memory.
const maxLine = 16 << 20 // 16 MiB

// ReadAll parses previously-written output (jsonl or csv) back into Results, so a
// job's file can be re-encoded into a different format on download. It is the
// inverse of the Writers and must track their schema.
func ReadAll(r io.Reader, format string) ([]model.Result, error) {
	switch format {
	case formatJSONL:
		return readJSONL(r)
	case formatCSV:
		return readCSV(r)
	default:
		return nil, fmt.Errorf("output: unknown format %q", format)
	}
}

// readJSONL parses one Result per non-empty line.
func readJSONL(r io.Reader) ([]model.Result, error) {
	var out []model.Result
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var res model.Result
		if err := json.Unmarshal(line, &res); err != nil {
			return nil, fmt.Errorf("output: parsing jsonl: %w", err)
		}
		out = append(out, res)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("output: reading jsonl: %w", err)
	}
	return out, nil
}

// readCSV reverses the CSV writer's flattening (header skipped, links split on
// '|', the data cell parsed as JSON).
func readCSV(r io.Reader) ([]model.Result, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // tolerate ragged rows
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("output: parsing csv: %w", err)
	}

	out := make([]model.Result, 0, len(rows))
	for i, row := range rows {
		if i == 0 || len(row) < len(csvHeader) {
			continue // skip header / malformed rows
		}
		res := model.Result{
			URL:        row[0],
			StatusCode: atoi(row[1]),
			Title:      row[2],
			Depth:      atoi(row[5]),
			Error:      row[7],
		}
		if row[3] != "" {
			if err := json.Unmarshal([]byte(row[3]), &res.Data); err != nil {
				return nil, fmt.Errorf("output: parsing csv data cell: %w", err)
			}
		}
		if row[4] != "" {
			res.Links = strings.Split(row[4], "|")
		}
		if row[6] != "" {
			if t, err := time.Parse(time.RFC3339, row[6]); err == nil {
				res.FetchedAt = t
			}
		}
		out = append(out, res)
	}
	return out, nil
}

// atoi parses an int, returning 0 on error (fields are best-effort on re-read).
func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
