// Package config defines the Config struct and fail-fast validation for the
// scraper.
//
// Responsibility: the Config struct (seeds, workers, depth, rate, timeout,
// format, user-agent, respect-robots, mode, owner, ui-addr, data-dir,
// retention, sweep-interval) plus Validate(), which reports every problem at
// once before any crawling begins (FR-17).
//
// Flag parsing lives in cmd/scraper/main.go; this package stays pure and
// unit-testable. Default() supplies the baseline values main overrides with
// flags.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Mode selects how the scraper runs.
type Mode int

const (
	// ModeHeadless runs a single job from --seeds and exits (cron-friendly).
	ModeHeadless Mode = iota
	// ModeUI serves the embedded dashboard for multiple anonymous users.
	ModeUI
)

func (m Mode) String() string {
	switch m {
	case ModeHeadless:
		return "headless"
	case ModeUI:
		return "ui"
	default:
		return fmt.Sprintf("Mode(%d)", int(m))
	}
}

func (m Mode) valid() bool { return m == ModeHeadless || m == ModeUI }

// Format selects the on-disk output encoding for a job.
type Format int

const (
	// FormatJSONL writes one JSON object per line (streamable, partial-safe).
	FormatJSONL Format = iota
	// FormatCSV writes comma-separated rows.
	FormatCSV
)

func (f Format) String() string {
	switch f {
	case FormatJSONL:
		return "jsonl"
	case FormatCSV:
		return "csv"
	default:
		return fmt.Sprintf("Format(%d)", int(f))
	}
}

// Ext returns the file extension (no dot) used for this format's output file.
func (f Format) Ext() string {
	switch f {
	case FormatJSONL:
		return "jsonl"
	case FormatCSV:
		return "csv"
	default:
		return ""
	}
}

func (f Format) valid() bool { return f == FormatJSONL || f == FormatCSV }

// ParseFormat converts a flag value ("jsonl" or "csv") into a Format.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "jsonl":
		return FormatJSONL, nil
	case "csv":
		return FormatCSV, nil
	default:
		return 0, fmt.Errorf(`%w: %q (want "jsonl" or "csv")`, ErrFormat, s)
	}
}

// Default values, exported so cmd/scraper can advertise them in --help.
const (
	DefaultOwner         = "local"
	DefaultDepth         = 2
	DefaultWorkers       = 10
	DefaultRatePerSecond = 1.0 // polite: one request per second per host
	DefaultTimeout       = 30 * time.Second
	DefaultRetries       = 3
	DefaultUserAgent     = "scraper/0.1 (+https://github.com/ArfaMujahid/scraper)"
	DefaultMaxBodyBytes  = 10 << 20 // 10 MiB (NFR-S2: bounded responses)
	DefaultDataDir       = "data"
	DefaultUIAddr        = "127.0.0.1:8080" // localhost only by default (NFR-S4)
	DefaultRetention     = 7 * 24 * time.Hour
	DefaultSweepInterval = time.Hour
)

// Config is the validated input shape for a scraper run. It is mode-agnostic:
// the same struct drives headless and UI runs, differing only in how the owner
// and seeds are supplied.
type Config struct {
	// Mode & identity.
	Mode  Mode   // headless or ui
	Owner string // headless owner id; in UI mode the owner comes from the session cookie

	// Crawl inputs.
	Seeds    []string // seed URLs (required in headless mode)
	Depth    int      // max crawl depth; 0 = seeds only (FR-2)
	Selector string   // optional CSS selector for data extraction (FR-6)

	// Concurrency & politeness.
	Workers       int     // bounded worker-pool size (FR-3)
	RatePerSecond float64 // per-host request rate (FR-5)
	RespectRobots bool    // obey robots.txt unless explicitly overridden (FR-4)

	// Fetch behaviour.
	Timeout      time.Duration // per-request timeout
	Retries      int           // retries for transient failures (FR-8)
	UserAgent    string        // identifiable agent string (NFR-S3)
	MaxBodyBytes int64         // response-body cap (NFR-S2)

	// Output.
	Format  Format // jsonl or csv (FR-9)
	DataDir string // output root; jobs live under {DataDir}/scrapes/{owner}/

	// UI mode.
	UIAddr string // listen address for the dashboard server

	// Cleanup janitor.
	Retention     time.Duration // delete completed jobs older than this (FR-15)
	SweepInterval time.Duration // janitor tick interval
}

// Default returns a Config populated with safe, polite defaults. Callers (main)
// override fields from flags, then call Validate.
func Default() Config {
	return Config{
		Mode:          ModeHeadless,
		Owner:         DefaultOwner,
		Depth:         DefaultDepth,
		Workers:       DefaultWorkers,
		RatePerSecond: DefaultRatePerSecond,
		RespectRobots: true,
		Timeout:       DefaultTimeout,
		Retries:       DefaultRetries,
		UserAgent:     DefaultUserAgent,
		MaxBodyBytes:  DefaultMaxBodyBytes,
		Format:        FormatJSONL,
		DataDir:       DefaultDataDir,
		UIAddr:        DefaultUIAddr,
		Retention:     DefaultRetention,
		SweepInterval: DefaultSweepInterval,
	}
}

// Sentinel errors, exported so callers and tests can match with errors.Is.
var (
	ErrMode          = errors.New("config: invalid mode")
	ErrNoSeeds       = errors.New("config: at least one seed URL is required in headless mode")
	ErrEmptyOwner    = errors.New("config: owner must not be empty in headless mode")
	ErrUIAddr        = errors.New("config: ui address must not be empty in UI mode")
	ErrWorkers       = errors.New("config: workers must be greater than 0")
	ErrDepth         = errors.New("config: depth must be 0 or greater")
	ErrRate          = errors.New("config: rate must be greater than 0")
	ErrTimeout       = errors.New("config: timeout must be greater than 0")
	ErrRetries       = errors.New("config: retries must be 0 or greater")
	ErrUserAgent     = errors.New("config: user-agent must not be empty")
	ErrMaxBodyBytes  = errors.New("config: max body size must be greater than 0")
	ErrFormat        = errors.New("config: invalid output format")
	ErrDataDir       = errors.New("config: data directory must not be empty")
	ErrRetention     = errors.New("config: retention must be greater than 0")
	ErrSweepInterval = errors.New("config: sweep interval must be greater than 0")
)

// Validate checks the configuration and returns every problem found, joined
// into a single error (nil when valid). It performs no I/O; use EnsureDataDir
// for the filesystem writability check.
func (c Config) Validate() error {
	var errs []error

	if !c.Mode.valid() {
		errs = append(errs, fmt.Errorf("%w: %d", ErrMode, int(c.Mode)))
	}
	switch c.Mode {
	case ModeHeadless:
		if len(c.Seeds) == 0 {
			errs = append(errs, ErrNoSeeds)
		}
		if strings.TrimSpace(c.Owner) == "" {
			errs = append(errs, ErrEmptyOwner)
		}
	case ModeUI:
		if strings.TrimSpace(c.UIAddr) == "" {
			errs = append(errs, ErrUIAddr)
		}
	}

	if c.Workers <= 0 {
		errs = append(errs, ErrWorkers)
	}
	if c.Depth < 0 {
		errs = append(errs, ErrDepth)
	}
	if c.RatePerSecond <= 0 {
		errs = append(errs, ErrRate)
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
	if !c.Format.valid() {
		errs = append(errs, ErrFormat)
	}
	if strings.TrimSpace(c.DataDir) == "" {
		errs = append(errs, ErrDataDir)
	}
	if c.Retention <= 0 {
		errs = append(errs, ErrRetention)
	}
	if c.SweepInterval <= 0 {
		errs = append(errs, ErrSweepInterval)
	}

	return errors.Join(errs...)
}

// EnsureDataDir creates the data directory (if needed) and verifies it is
// writable, so an unwritable path fails fast at startup rather than mid-crawl
// (FR-17). Call after Validate, before crawling begins.
func (c Config) EnsureDataDir() error {
	if strings.TrimSpace(c.DataDir) == "" {
		return ErrDataDir
	}
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return fmt.Errorf("config: create data dir %q: %w", c.DataDir, err)
	}
	probe, err := os.CreateTemp(c.DataDir, ".writecheck-*")
	if err != nil {
		return fmt.Errorf("config: data dir %q is not writable: %w", c.DataDir, err)
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return nil
}
