package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// headless returns a valid headless config that tests can mutate per case.
func headless() Config {
	c := Default()
	c.Seeds = []string{"https://example.com"}
	return c
}

// ui returns a valid UI-mode config.
func ui() Config {
	c := Default()
	c.Mode = ModeUI
	return c
}

func TestDefaultIsValidHeadlessWithSeeds(t *testing.T) {
	if err := headless().Validate(); err != nil {
		t.Fatalf("expected valid config, got: %v", err)
	}
}

func TestValidateUIModeNeedsAddr(t *testing.T) {
	c := ui()
	if err := c.Validate(); err != nil {
		t.Fatalf("valid UI config should pass, got: %v", err)
	}
	c.UIAddr = "   "
	if err := c.Validate(); !errors.Is(err, ErrUIAddr) {
		t.Fatalf("blank UIAddr in UI mode should fail with ErrUIAddr, got: %v", err)
	}
}

func TestValidateSeedsNotRequiredInUIMode(t *testing.T) {
	// UI mode mints jobs per-request, so seeds at startup are not required.
	c := ui()
	c.Seeds = nil
	if err := c.Validate(); err != nil {
		t.Fatalf("UI mode without seeds should be valid, got: %v", err)
	}
}

func TestValidateRules(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr error
	}{
		{"no seeds headless", func(c *Config) { c.Seeds = nil }, ErrNoSeeds},
		{"empty owner headless", func(c *Config) { c.Owner = "  " }, ErrEmptyOwner},
		{"zero workers", func(c *Config) { c.Workers = 0 }, ErrWorkers},
		{"negative workers", func(c *Config) { c.Workers = -1 }, ErrWorkers},
		{"negative depth", func(c *Config) { c.Depth = -1 }, ErrDepth},
		{"zero depth ok", func(c *Config) { c.Depth = 0 }, nil},
		{"zero rate", func(c *Config) { c.RatePerSecond = 0 }, ErrRate},
		{"zero timeout", func(c *Config) { c.Timeout = 0 }, ErrTimeout},
		{"negative retries", func(c *Config) { c.Retries = -1 }, ErrRetries},
		{"zero retries ok", func(c *Config) { c.Retries = 0 }, nil},
		{"empty user-agent", func(c *Config) { c.UserAgent = "" }, ErrUserAgent},
		{"zero max body", func(c *Config) { c.MaxBodyBytes = 0 }, ErrMaxBodyBytes},
		{"bad format", func(c *Config) { c.Format = Format(99) }, ErrFormat},
		{"empty data dir", func(c *Config) { c.DataDir = "" }, ErrDataDir},
		{"zero retention", func(c *Config) { c.Retention = 0 }, ErrRetention},
		{"zero sweep", func(c *Config) { c.SweepInterval = 0 }, ErrSweepInterval},
		{"bad mode", func(c *Config) { c.Mode = Mode(42) }, ErrMode},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := headless()
			tt.mutate(&c)
			err := c.Validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("expected valid, got: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected error %v, got: %v", tt.wantErr, err)
			}
		})
	}
}

func TestValidateReportsAllProblems(t *testing.T) {
	c := headless()
	c.Seeds = nil
	c.Workers = 0
	c.Timeout = 0

	err := c.Validate()
	for _, want := range []error{ErrNoSeeds, ErrWorkers, ErrTimeout} {
		if !errors.Is(err, want) {
			t.Errorf("aggregated error missing %v; got: %v", want, err)
		}
	}
}

func TestParseFormat(t *testing.T) {
	tests := []struct {
		in      string
		want    Format
		wantErr bool
	}{
		{"jsonl", FormatJSONL, false},
		{"JSONL", FormatJSONL, false},
		{" csv ", FormatCSV, false},
		{"xml", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseFormat(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("ParseFormat(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestFormatStringAndExt(t *testing.T) {
	if FormatJSONL.String() != "jsonl" || FormatJSONL.Ext() != "jsonl" {
		t.Errorf("jsonl string/ext mismatch: %q / %q", FormatJSONL.String(), FormatJSONL.Ext())
	}
	if FormatCSV.String() != "csv" || FormatCSV.Ext() != "csv" {
		t.Errorf("csv string/ext mismatch: %q / %q", FormatCSV.String(), FormatCSV.Ext())
	}
}

func TestModeString(t *testing.T) {
	if ModeHeadless.String() != "headless" {
		t.Errorf("ModeHeadless.String() = %q", ModeHeadless.String())
	}
	if ModeUI.String() != "ui" {
		t.Errorf("ModeUI.String() = %q", ModeUI.String())
	}
}

func TestEnsureDataDirCreatesAndProbes(t *testing.T) {
	c := headless()
	c.DataDir = filepath.Join(t.TempDir(), "nested", "data")

	if err := c.EnsureDataDir(); err != nil {
		t.Fatalf("EnsureDataDir failed: %v", err)
	}
	info, err := os.Stat(c.DataDir)
	if err != nil || !info.IsDir() {
		t.Fatalf("data dir not created: err=%v", err)
	}

	// No leftover probe files.
	entries, err := os.ReadDir(c.DataDir)
	if err != nil {
		t.Fatalf("read data dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty data dir after probe, found %d entries", len(entries))
	}
}

func TestEnsureDataDirUnwritable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root bypasses permission checks")
	}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil { // read+execute, no write
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	c := headless()
	c.DataDir = filepath.Join(parent, "cannot-create")
	if err := c.EnsureDataDir(); err == nil {
		t.Fatal("expected EnsureDataDir to fail on unwritable parent")
	}
}

func TestDefaultRetentionIsSevenDays(t *testing.T) {
	if Default().Retention != 7*24*time.Hour {
		t.Errorf("default retention = %v, want 168h", Default().Retention)
	}
}
