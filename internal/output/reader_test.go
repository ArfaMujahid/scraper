package output

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestRoundTrip(t *testing.T) {
	for _, format := range []string{"jsonl", "csv"} {
		t.Run(format, func(t *testing.T) {
			in := sampleResults()

			var buf bytes.Buffer
			if err := WriteAll(&buf, format, in); err != nil {
				t.Fatalf("WriteAll: %v", err)
			}
			got, err := ReadAll(&buf, format)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			// CSV stores second-precision timestamps; allow a second of slack.
			if diff := cmp.Diff(in, got, cmpopts.EquateApproxTime(time.Second)); diff != "" {
				t.Errorf("%s round-trip mismatch (-want +got):\n%s", format, diff)
			}
		})
	}
}

func TestReadAllUnknownFormat(t *testing.T) {
	if _, err := ReadAll(bytes.NewReader(nil), "xml"); err == nil {
		t.Error("expected error for unknown format")
	}
}
