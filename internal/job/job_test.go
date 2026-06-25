package job

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestNew(t *testing.T) {
	dataDir := "/tmp/data"
	seeds := []string{"https://a.example", "https://b.example"}

	j := New("alice", seeds, dataDir, "jsonl")

	if j.ID == "" {
		t.Error("New must generate a non-empty ID")
	}
	if j.Owner != "alice" {
		t.Errorf("Owner = %q, want %q", j.Owner, "alice")
	}
	if j.Status != StatusQueued {
		t.Errorf("Status = %v, want %v", j.Status, StatusQueued)
	}
	if diff := cmp.Diff(seeds, j.Seeds); diff != "" {
		t.Errorf("Seeds mismatch (-want +got):\n%s", diff)
	}
	if !j.StartedAt.IsZero() || !j.EndedAt.IsZero() {
		t.Error("a fresh job must have zero StartedAt/EndedAt")
	}
}

func TestNewGeneratesUniqueIDs(t *testing.T) {
	a := New("o", nil, "/d", "jsonl")
	b := New("o", nil, "/d", "jsonl")
	if a.ID == b.ID {
		t.Errorf("expected unique IDs, both were %q", a.ID)
	}
}

func TestOutputPathLayout(t *testing.T) {
	dataDir := filepath.Join("var", "data")
	j := New("alice", nil, dataDir, "csv")

	wantDir := filepath.Join(dataDir, scrapesDir, "alice")
	if got := filepath.Dir(j.OutputPath); got != wantDir {
		t.Errorf("output dir = %q, want %q", got, wantDir)
	}

	base := filepath.Base(j.OutputPath)
	if !strings.HasPrefix(base, string(j.ID)+"_") {
		t.Errorf("filename %q should start with %q", base, string(j.ID)+"_")
	}
	if !strings.HasSuffix(base, ".csv") {
		t.Errorf("filename %q should end with .csv", base)
	}
}

func TestOutputPathSanitizesOwner(t *testing.T) {
	tests := []struct {
		name      string
		owner     OwnerID
		wantOwner string // the sanitized path component we expect
	}{
		{"plain", "alice", "alice"},
		{"uuid-like", "9f1c-44_a", "9f1c-44_a"},
		{"path traversal", "../../etc", "______etc"},
		{"separators", "a/b\\c", "a_b_c"},
		{"dotdot", "..", "__"},
		{"empty", "", "default"},
		{"unicode", "café", "caf_"},
	}

	dataDir := filepath.Join("var", "data")
	scrapesRoot := filepath.Join(dataDir, scrapesDir)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := outputPath(dataDir, tt.owner, "id123", "jsonl")

			// The owner component must be exactly the sanitized value.
			ownerDir := filepath.Dir(p)
			if got := filepath.Base(ownerDir); got != tt.wantOwner {
				t.Errorf("owner component = %q, want %q", got, tt.wantOwner)
			}
			// Defense-in-depth: the path must stay under data/scrapes and
			// contain no traversal segment after cleaning.
			clean := filepath.Clean(p)
			if !strings.HasPrefix(clean, scrapesRoot+string(filepath.Separator)) {
				t.Errorf("path %q escaped scrapes root %q", clean, scrapesRoot)
			}
			if strings.Contains(clean, ".."+string(filepath.Separator)) {
				t.Errorf("path %q still contains a traversal segment", clean)
			}
		})
	}
}

func TestStatusString(t *testing.T) {
	tests := []struct {
		status Status
		want   string
	}{
		{StatusQueued, "queued"},
		{StatusRunning, "running"},
		{StatusDone, "done"},
		{StatusFailed, "failed"},
		{Status(99), "Status(99)"},
	}
	for _, tt := range tests {
		if got := tt.status.String(); got != tt.want {
			t.Errorf("Status(%d).String() = %q, want %q", int(tt.status), got, tt.want)
		}
	}
}
