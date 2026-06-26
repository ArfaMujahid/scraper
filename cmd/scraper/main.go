// Command scraper is the entry point and composition root for the concurrent
// web scraper. It parses flags, builds and validates config, wires the
// fetcher/limiter/stats/crawler together, installs signal-based graceful
// shutdown, and runs a headless scrape job. (UI mode is a later phase.)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ArfaMujahid/scraper/internal/config"
	"github.com/ArfaMujahid/scraper/internal/crawler"
	"github.com/ArfaMujahid/scraper/internal/fetcher"
	"github.com/ArfaMujahid/scraper/internal/job"
	"github.com/ArfaMujahid/scraper/internal/output"
	"github.com/ArfaMujahid/scraper/internal/ratelimit"
	"github.com/ArfaMujahid/scraper/internal/stats"
)

// main runs the CLI and exits with the returned status code.
func main() {
	os.Exit(run())
}

// run parses flags, validates config, and dispatches to the chosen mode,
// returning a process exit code (0 success, non-zero failure).
func run() int {
	cfg := config.Default()
	var seedsCSV string
	var selectors selectorsFlag

	flag.StringVar(&seedsCSV, "seeds", "", "comma-separated seed URLs (or pass as positional args)")
	flag.IntVar(&cfg.MaxDepth, "depth", cfg.MaxDepth, "max crawl depth (0 = seeds only)")
	flag.IntVar(&cfg.Workers, "workers", cfg.Workers, "number of concurrent workers")
	flag.Float64Var(&cfg.RatePerHost, "rate", cfg.RatePerHost, "max requests per second per host")
	flag.DurationVar(&cfg.Timeout, "timeout", cfg.Timeout, "per-request timeout")
	flag.IntVar(&cfg.Retries, "retries", cfg.Retries, "retries for transient failures")
	flag.BoolVar(&cfg.RespectRobots, "respect-robots", cfg.RespectRobots, "obey robots.txt")
	flag.StringVar(&cfg.UserAgent, "user-agent", cfg.UserAgent, "User-Agent header")
	flag.StringVar(&cfg.Format, "format", cfg.Format, "output format: jsonl or csv")
	flag.StringVar(&cfg.Owner, "owner", cfg.Owner, "owner id for output isolation")
	flag.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "output root directory")
	flag.Int64Var(&cfg.MaxBodyBytes, "max-body", cfg.MaxBodyBytes, "max response body bytes")
	flag.BoolVar(&cfg.UIEnabled, "ui", cfg.UIEnabled, "run the web dashboard (not yet implemented)")
	flag.StringVar(&cfg.UIAddr, "ui-addr", cfg.UIAddr, "dashboard listen address")
	flag.DurationVar(&cfg.Retention, "retention", cfg.Retention, "delete completed jobs older than this")
	flag.DurationVar(&cfg.SweepInterval, "sweep-interval", cfg.SweepInterval, "janitor sweep interval")
	flag.Var(&selectors, "select", "CSS field to extract as name=selector (repeatable)")
	flag.Parse()

	cfg.Selectors = selectors.m
	cfg.Seeds = parseSeeds(seedsCSV, flag.Args())

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	if err := cfg.Validate(); err != nil {
		logger.Error("invalid configuration", "err", err)
		return 1
	}

	if cfg.UIEnabled {
		logger.Error("ui mode is not implemented yet; run headless with --seeds")
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runHeadless(ctx, cfg, logger); err != nil {
		logger.Error("scrape failed", "err", err)
		return 1
	}
	return 0
}

// runHeadless wires the dependencies, runs one job to completion (or graceful
// cancellation), flushes output, and prints a summary.
func runHeadless(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	f := fetcher.New(cfg.Timeout, cfg.Retries, cfg.UserAgent, cfg.MaxBodyBytes)
	limiter := ratelimit.New(cfg.RatePerHost, cfg.RespectRobots, cfg.UserAgent, f)
	st := stats.New()

	j := job.New(job.OwnerID(cfg.Owner), cfg.Seeds, cfg.DataDir, cfg.Format)
	w, file, err := output.NewForJob(j, cfg.Format)
	if err != nil {
		return fmt.Errorf("opening output: %w", err)
	}
	// Flush then close: graceful shutdown preserves already-scraped results.
	defer func() {
		if cerr := w.Close(); cerr != nil {
			logger.Error("flushing output", "err", cerr)
		}
		if cerr := file.Close(); cerr != nil {
			logger.Error("closing output file", "err", cerr)
		}
	}()

	cr := crawler.New(crawler.Config{
		Workers:   cfg.Workers,
		MaxDepth:  cfg.MaxDepth,
		Selectors: cfg.Selectors,
	}, f, limiter, w, st, nil)

	logger.Info("scrape started",
		"job", j.ID, "owner", j.Owner, "seeds", len(cfg.Seeds),
		"workers", cfg.Workers, "depth", cfg.MaxDepth, "output", j.OutputPath)

	runErr := cr.Run(ctx, cfg.Seeds)

	printSummary(j, st.Snapshot(), runErr)

	// A cancelled run (Ctrl-C / SIGTERM) is a graceful stop, not a failure.
	if runErr != nil && !errors.Is(runErr, context.Canceled) && !errors.Is(runErr, context.DeadlineExceeded) {
		return runErr
	}
	return nil
}

// printSummary writes the human-readable final report (FR-18) to stdout.
func printSummary(j *job.Job, s stats.Snapshot, runErr error) {
	status := "completed"
	if runErr != nil {
		status = "interrupted (" + runErr.Error() + ")"
	}
	fmt.Printf("\nScrape %s\n", status)
	fmt.Printf("  pages scraped : %d\n", s.Done)
	fmt.Printf("  errors        : %d\n", s.Errors)
	fmt.Printf("  bytes         : %d\n", s.Bytes)
	fmt.Printf("  elapsed       : %s\n", s.Elapsed.Round(time.Millisecond))
	fmt.Printf("  throughput    : %.1f pages/sec\n", s.PagesPerSec)
	fmt.Printf("  output        : %s\n", j.OutputPath)
}

// parseSeeds combines comma-separated --seeds values with positional args,
// dropping blanks.
func parseSeeds(csv string, args []string) []string {
	var seeds []string
	for _, s := range strings.Split(csv, ",") {
		if s = strings.TrimSpace(s); s != "" {
			seeds = append(seeds, s)
		}
	}
	seeds = append(seeds, args...)
	return seeds
}

// selectorsFlag collects repeated -select name=selector flags into a map.
type selectorsFlag struct{ m map[string]string }

// String renders the collected selectors (for flag's default display).
func (s *selectorsFlag) String() string { return fmt.Sprintf("%v", s.m) }

// Set parses one "name=selector" pair and adds it to the map.
func (s *selectorsFlag) Set(v string) error {
	name, sel, ok := strings.Cut(v, "=")
	if !ok || name == "" || sel == "" {
		return fmt.Errorf("expected name=selector, got %q", v)
	}
	if s.m == nil {
		s.m = make(map[string]string)
	}
	s.m[name] = sel
	return nil
}
