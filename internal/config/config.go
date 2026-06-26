// Package config holds all runtime tunables and validates them at startup so a
// misconfigured run fails fast and loudly rather than corrupting output midway.
//
// Field shape follows implementation-design.md §1. Flag parsing lives in
// cmd/scraper/main.go; this package stays focused on the input shape, defaults,
// and validation.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Default values, exported so cmd/scraper can advertise them in --help.
const (
	DefaultMaxDepth      = 2
	DefaultMaxPages      = 10 // cap on total pages per job (0 = unlimited)
	DefaultWorkers       = 10
	DefaultRatePerHost   = 1.0 // requests/sec per host — polite by default
	DefaultTimeout       = 30 * time.Second
	DefaultRetries       = 3
	DefaultUserAgent     = "scraper/0.1 (+https://github.com/ArfaMujahid/scraper)"
	DefaultMaxBodyBytes  = 10 << 20 // 10 MiB cap on a response body (NFR-S2)
	DefaultUIAddr        = "127.0.0.1:8080"
	DefaultOwner         = "local"
	DefaultDataDir       = "data"
	DefaultFormat        = "jsonl"
	DefaultRetention     = 7 * 24 * time.Hour
	DefaultSweepInterval = time.Hour
)

// formatJSONL and formatCSV are the only accepted output formats.
const (
	formatJSONL = "jsonl"
	formatCSV   = "csv"
)

// Config holds all runtime configuration, populated from flags/env and
// validated before any work begins.
type Config struct {
	// crawl
	Seeds         []string
	MaxDepth      int               // 0 = seeds only (FR-2)
	MaxPages      int               // cap on total pages per job; 0 = unlimited
	Workers       int               // bounded worker-pool size (FR-3)
	RatePerHost   float64           // requests/sec per host (FR-5)
	Timeout       time.Duration     // per-request timeout
	Retries       int               // retries for transient failures (FR-8)
	RespectRobots bool              // obey robots.txt unless overridden (FR-4)
	UserAgent     string            // identifiable agent string (NFR-S3)
	Selectors     map[string]string // name -> CSS selector for data extraction (FR-6)
	MaxBodyBytes  int64             // response-body cap; required by fetcher (NFR-S2)

	// identity & mode
	UIEnabled bool   // false = headless single job; true = dashboard server
	UIAddr    string // listen address for the dashboard, e.g. "127.0.0.1:8080"
	Owner     string // headless owner id; UI mode derives the owner from the session cookie

	// storage & cleanup
	DataDir       string        // output root; jobs live under {DataDir}/scrapes/{owner}/
	Format        string        // "jsonl" | "csv" (FR-9)
	Retention     time.Duration // delete completed jobs older than this (FR-15)
	SweepInterval time.Duration // janitor tick interval
}

// Default returns a Config populated with safe, polite defaults that
// cmd/scraper overrides from flags before calling Validate.
func Default() Config {
	return Config{
		MaxDepth:      DefaultMaxDepth,
		MaxPages:      DefaultMaxPages,
		Workers:       DefaultWorkers,
		RatePerHost:   DefaultRatePerHost,
		Timeout:       DefaultTimeout,
		Retries:       DefaultRetries,
		RespectRobots: true,
		UserAgent:     DefaultUserAgent,
		MaxBodyBytes:  DefaultMaxBodyBytes,
		UIEnabled:     false,
		UIAddr:        DefaultUIAddr,
		Owner:         DefaultOwner,
		DataDir:       DefaultDataDir,
		Format:        DefaultFormat,
		Retention:     DefaultRetention,
		SweepInterval: DefaultSweepInterval,
	}
}

// Sentinel errors, exported so callers and tests can match with errors.Is.
var (
	ErrNoSeeds       = errors.New("config: at least one seed URL is required in headless mode")
	ErrEmptyOwner    = errors.New("config: owner must not be empty in headless mode")
	ErrUIAddr        = errors.New("config: ui address must not be empty when the UI is enabled")
	ErrWorkers       = errors.New("config: workers must be greater than 0")
	ErrMaxDepth      = errors.New("config: max depth must be 0 or greater")
	ErrMaxPages      = errors.New("config: max pages must be 0 or greater")
	ErrRatePerHost   = errors.New("config: rate per host must be greater than 0")
	ErrTimeout       = errors.New("config: timeout must be greater than 0")
	ErrRetries       = errors.New("config: retries must be 0 or greater")
	ErrUserAgent     = errors.New("config: user-agent must not be empty")
	ErrMaxBodyBytes  = errors.New("config: max body size must be greater than 0")
	ErrFormat        = errors.New(`config: format must be "jsonl" or "csv"`)
	ErrDataDir       = errors.New("config: data directory must not be empty")
	ErrRetention     = errors.New("config: retention must be greater than 0")
	ErrSweepInterval = errors.New("config: sweep interval must be greater than 0")
)

// Validate reports every configuration problem at once (joined into one error,
// nil when valid) so the user sees all issues in a single run rather than
// fixing them one at a time. It also confirms the data directory is writable
// now, not at first write, per implementation-design.md §1.
func (c *Config) Validate() error {
	var errs []error

	// Mode-specific requirements.
	if c.UIEnabled {
		if strings.TrimSpace(c.UIAddr) == "" {
			errs = append(errs, ErrUIAddr)
		}
	} else {
		if len(c.Seeds) == 0 {
			errs = append(errs, ErrNoSeeds)
		}
		if strings.TrimSpace(c.Owner) == "" {
			errs = append(errs, ErrEmptyOwner)
		}
	}

	if c.Workers <= 0 {
		errs = append(errs, ErrWorkers)
	}
	if c.MaxDepth < 0 {
		errs = append(errs, ErrMaxDepth)
	}
	if c.MaxPages < 0 {
		errs = append(errs, ErrMaxPages)
	}
	if c.RatePerHost <= 0 {
		errs = append(errs, ErrRatePerHost)
	}
	if c.Timeout <= 0 {
		errs = append(errs, ErrTimeout)
	}
	if c.Retries < 0 {
		errs = append(errs, ErrRetries)
	}
	if strings.TrimSpace(c.UserAgent) == "" {
		errs = append(errs, ErrUserAgent)
	}
	if c.MaxBodyBytes <= 0 {
		errs = append(errs, ErrMaxBodyBytes)
	}
	if c.Format != formatJSONL && c.Format != formatCSV {
		errs = append(errs, fmt.Errorf("%w (got %q)", ErrFormat, c.Format))
	}
	if strings.TrimSpace(c.DataDir) == "" {
		errs = append(errs, ErrDataDir)
	} else if err := checkDataDir(c.DataDir); err != nil {
		errs = append(errs, err)
	}
	if c.Retention <= 0 {
		errs = append(errs, ErrRetention)
	}
	if c.SweepInterval <= 0 {
		errs = append(errs, ErrSweepInterval)
	}

	return errors.Join(errs...)
}

// checkDataDir creates dir if needed and verifies it is writable, so an
// unwritable output path fails fast at startup rather than mid-crawl (FR-17).
func checkDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("config: create data dir %q: %w", dir, err)
	}
	probe, err := os.CreateTemp(dir, ".writecheck-*")
	if err != nil {
		return fmt.Errorf("config: data dir %q is not writable: %w", dir, err)
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return nil
}
