package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// validHeadless returns a valid headless config rooted at a writable temp dir,
// which tests mutate per case.
func validHeadless(t *testing.T) Config {
	t.Helper()
	c := Default()
	c.Seeds = []string{"https://example.com"}
	c.DataDir = t.TempDir()
	return c
}

// validUI returns a valid UI-mode config rooted at a writable temp dir.
func validUI(t *testing.T) Config {
	t.Helper()
	c := Default()
	c.UIEnabled = true
	c.DataDir = t.TempDir()
	return c
}

func TestValidateAcceptsValidHeadless(t *testing.T) {
	c := validHeadless(t)
	if err := c.Validate(); err != nil {
		t.Fatalf("expected valid config, got: %v", err)
	}
}

func TestValidateAcceptsValidUI(t *testing.T) {
	c := validUI(t)
	if err := c.Validate(); err != nil {
		t.Fatalf("expected valid UI config, got: %v", err)
	}
}

func TestValidateUIModeNeedsAddr(t *testing.T) {
	c := validUI(t)
	c.UIAddr = "   "
	if err := c.Validate(); !errors.Is(err, ErrUIAddr) {
		t.Fatalf("blank UIAddr in UI mode should fail with ErrUIAddr, got: %v", err)
	}
}

func TestValidateSeedsNotRequiredInUIMode(t *testing.T) {
	// UI mode mints jobs per request, so seeds at startup are not required.
	c := validUI(t)
	c.Seeds = nil
	if err := c.Validate(); err != nil {
		t.Fatalf("UI mode without seeds should be valid, got: %v", err)
	}
}

func TestValidateRules(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr error // nil means the mutation should still be valid
	}{
		{"no seeds headless", func(c *Config) { c.Seeds = nil }, ErrNoSeeds},
		{"empty owner headless", func(c *Config) { c.Owner = "  " }, ErrEmptyOwner},
		{"zero workers", func(c *Config) { c.Workers = 0 }, ErrWorkers},
		{"negative workers", func(c *Config) { c.Workers = -1 }, ErrWorkers},
		{"negative depth", func(c *Config) { c.MaxDepth = -1 }, ErrMaxDepth},
		{"zero depth ok", func(c *Config) { c.MaxDepth = 0 }, nil},
		{"zero rate", func(c *Config) { c.RatePerHost = 0 }, ErrRatePerHost},
		{"zero timeout", func(c *Config) { c.Timeout = 0 }, ErrTimeout},
		{"negative retries", func(c *Config) { c.Retries = -1 }, ErrRetries},
		{"zero retries ok", func(c *Config) { c.Retries = 0 }, nil},
		{"empty user-agent", func(c *Config) { c.UserAgent = "" }, ErrUserAgent},
		{"zero max body", func(c *Config) { c.MaxBodyBytes = 0 }, ErrMaxBodyBytes},
		{"bad format", func(c *Config) { c.Format = "xml" }, ErrFormat},
		{"csv format ok", func(c *Config) { c.Format = "csv" }, nil},
		{"empty data dir", func(c *Config) { c.DataDir = "" }, ErrDataDir},
		{"zero retention", func(c *Config) { c.Retention = 0 }, ErrRetention},
		{"zero sweep", func(c *Config) { c.SweepInterval = 0 }, ErrSweepInterval},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validHeadless(t)
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
	c := validHeadless(t)
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

func TestValidateCreatesDataDir(t *testing.T) {
	c := validHeadless(t)
	c.DataDir = filepath.Join(t.TempDir(), "nested", "data")

	if err := c.Validate(); err != nil {
		t.Fatalf("Validate should create a nested data dir, got: %v", err)
	}
	info, err := os.Stat(c.DataDir)
	if err != nil || !info.IsDir() {
		t.Fatalf("data dir not created: err=%v", err)
	}
	// The write probe must not leave a leftover file behind.
	entries, err := os.ReadDir(c.DataDir)
	if err != nil {
		t.Fatalf("read data dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty data dir after probe, found %d entries", len(entries))
	}
}

func TestValidateUnwritableDataDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root bypasses permission checks")
	}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil { // read+execute, no write
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	c := validHeadless(t)
	c.DataDir = filepath.Join(parent, "cannot-create")
	if err := c.Validate(); err == nil {
		t.Fatal("expected Validate to fail on an unwritable data dir")
	}
}

func TestDefaultRetentionIsSevenDays(t *testing.T) {
	if Default().Retention != 7*24*time.Hour {
		t.Errorf("default retention = %v, want 168h", Default().Retention)
	}
}
