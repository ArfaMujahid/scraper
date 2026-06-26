// Package crawler is the engine: it runs one scrape job with a bounded worker
// pool, a coordinator that owns the frontier and visited set (so dedup needs no
// lock), per-page fault isolation, progress events, and context cancellation
// throughout.
//
// Termination is the hard part — the number of URLs isn't known up front. A
// coordinator goroutine owns the frontier, the visited set, and an in-flight
// counter; the crawl is done exactly when the frontier is empty and nothing is
// outstanding (implementation-design.md §8).
package crawler

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/ArfaMujahid/scraper/internal/fetcher"
	"github.com/ArfaMujahid/scraper/internal/model"
	"github.com/ArfaMujahid/scraper/internal/output"
	"github.com/ArfaMujahid/scraper/internal/parser"
	"github.com/ArfaMujahid/scraper/internal/ratelimit"
	"github.com/ArfaMujahid/scraper/internal/stats"
)

// Fetcher retrieves one page. Declared at the consumer (coding-standards §5) so
// the crawler can be tested with a fake; *fetcher.Client satisfies it.
type Fetcher interface {
	Fetch(ctx context.Context, url string) (*fetcher.Response, error)
}

var _ Fetcher = (*fetcher.Client)(nil)

// Config is the crawler's slice of configuration.
type Config struct {
	Workers   int
	MaxDepth  int
	Selectors map[string]string
}

// EventType classifies a progress Event.
type EventType int

const (
	// EventPageDone is emitted after a page is successfully scraped.
	EventPageDone EventType = iota
	// EventPageError is emitted after a page fails (recorded, not fatal).
	EventPageError
)

// Event is a progress signal emitted for the stats/UI layer (fan-out). The same
// engine drives the CLI and, later, the dashboard.
type Event struct {
	Type  EventType
	URL   string
	Bytes int
	Err   error
}

// Crawler runs a single scrape job using a bounded worker pool.
type Crawler struct {
	cfg     Config
	fetcher Fetcher
	limiter *ratelimit.Limiter
	writer  output.Writer
	stats   *stats.Stats
	events  chan<- Event // may be nil (headless): emit is then a no-op
}

// New wires a Crawler with its collaborators. A nil events channel disables
// progress emission (headless mode).
func New(cfg Config, f Fetcher, l *ratelimit.Limiter, w output.Writer, s *stats.Stats, events chan<- Event) *Crawler {
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	return &Crawler{cfg: cfg, fetcher: f, limiter: l, writer: w, stats: s, events: events}
}

// item is one URL to fetch, tagged with the depth it was discovered at.
type item struct {
	url   string
	depth int
}

// discoveredMsg reports that one item finished, carrying the links found on it
// so the coordinator can enqueue them (and decrement its in-flight counter).
type discoveredMsg struct {
	links []string
	depth int
}

// Run crawls from seeds until the frontier is exhausted or ctx is cancelled,
// streaming results to the writer. It returns nil on a clean finish, ctx.Err()
// if cancelled, or a write error.
func (c *Crawler) Run(ctx context.Context, seeds []string) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	work := make(chan item)
	discovered := make(chan discoveredMsg, c.cfg.Workers)
	results := make(chan model.Result, c.cfg.Workers)

	// Single writer goroutine owns c.writer, so no file lock is needed. A write
	// error is fatal: record it once and cancel the run.
	var writeErr error
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for r := range results {
			if writeErr != nil {
				continue // already failed; drain so workers don't block
			}
			if err := c.writer.Write(r); err != nil {
				writeErr = err
				cancel()
			}
		}
	}()

	g, gctx := errgroup.WithContext(runCtx)

	// Coordinator owns the frontier + visited set and closes work when done.
	coordDone := make(chan struct{})
	go func() {
		defer close(coordDone)
		c.coordinate(gctx, seeds, work, discovered)
	}()

	for i := 0; i < c.cfg.Workers; i++ {
		g.Go(func() error {
			return c.worker(gctx, work, discovered, results)
		})
	}

	werr := g.Wait()
	<-coordDone
	close(results)
	<-writerDone

	switch {
	case writeErr != nil:
		return writeErr
	case werr != nil:
		return werr
	default:
		return ctx.Err()
	}
}

// coordinate owns the frontier (as a stack), the visited set, and the in-flight
// counter. It feeds work to the pool and consumes discovered links, finishing
// when the frontier is empty and nothing is outstanding. The send case is
// disabled (nil channel) when the frontier is empty so the select can't spin.
func (c *Crawler) coordinate(ctx context.Context, seeds []string, work chan<- item, discovered <-chan discoveredMsg) {
	defer close(work)

	visited := make(map[string]struct{})
	var frontier []item
	inFlight := 0

	for _, s := range seeds {
		if _, ok := visited[s]; ok {
			continue
		}
		visited[s] = struct{}{}
		frontier = append(frontier, item{url: s, depth: 0})
		inFlight++
	}

	for {
		if len(frontier) == 0 && inFlight == 0 {
			return
		}

		var sendCh chan<- item
		var next item
		if n := len(frontier); n > 0 {
			sendCh = work
			next = frontier[n-1]
		}

		select {
		case <-ctx.Done():
			return
		case sendCh <- next:
			frontier = frontier[:len(frontier)-1]
		case d := <-discovered:
			inFlight--
			if d.depth+1 > c.cfg.MaxDepth {
				continue
			}
			for _, link := range d.links {
				if _, ok := visited[link]; ok {
					continue
				}
				visited[link] = struct{}{}
				frontier = append(frontier, item{url: link, depth: d.depth + 1})
				inFlight++
			}
		}
	}
}

// worker pulls items off work, scrapes each, and reports completion (with any
// discovered links) back to the coordinator. It returns only on a fatal error
// (context cancellation); per-page failures are recorded and skipped (NFR-R3).
func (c *Crawler) worker(ctx context.Context, work <-chan item, discovered chan<- discoveredMsg, results chan<- model.Result) error {
	for it := range work {
		links, err := c.scrape(ctx, it, results)
		if err != nil {
			return err // context cancelled
		}
		select {
		case discovered <- discoveredMsg{links: links, depth: it.depth}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// scrape fetches and parses one item, writing a Result. It returns the links to
// enqueue (nil on any per-page error) and a non-nil error only when the context
// was cancelled.
func (c *Crawler) scrape(ctx context.Context, it item, results chan<- model.Result) ([]string, error) {
	c.stats.AddInFlight(1)
	defer c.stats.AddInFlight(-1)
	now := time.Now().UTC()

	if err := c.limiter.Wait(ctx, it.url); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, c.fail(ctx, it, now, 0, err, results)
	}

	resp, err := c.fetcher.Fetch(ctx, it.url)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, c.fail(ctx, it, now, 0, err, results)
	}
	c.stats.AddBytes(int64(len(resp.Body)))

	if resp.StatusCode >= 400 {
		return nil, c.fail(ctx, it, now, resp.StatusCode, fmt.Errorf("http status %d", resp.StatusCode), results)
	}

	doc, err := parser.Parse(resp.Body)
	if err != nil {
		return nil, c.fail(ctx, it, now, resp.StatusCode, err, results)
	}
	links, lerr := doc.Links(it.url)
	if lerr != nil {
		links = nil // base was fetched, so this is unexpected; degrade gracefully
	}

	r := model.Result{
		URL:        it.url,
		StatusCode: resp.StatusCode,
		Title:      doc.Title(),
		Data:       doc.Data(c.cfg.Selectors),
		Links:      links,
		Depth:      it.depth,
		FetchedAt:  now,
	}
	if err := c.send(ctx, results, r); err != nil {
		return nil, err
	}
	c.stats.IncDone()
	c.emit(Event{Type: EventPageDone, URL: it.url, Bytes: len(resp.Body)})
	return links, nil
}

// fail records a per-page error Result and bumps the error counter. It returns
// an error only if the context was cancelled while sending the Result.
func (c *Crawler) fail(ctx context.Context, it item, now time.Time, status int, srcErr error, results chan<- model.Result) error {
	r := model.Result{
		URL:        it.url,
		StatusCode: status,
		Depth:      it.depth,
		FetchedAt:  now,
		Error:      srcErr.Error(),
	}
	if err := c.send(ctx, results, r); err != nil {
		return err
	}
	c.stats.IncError()
	c.emit(Event{Type: EventPageError, URL: it.url, Err: srcErr})
	return nil
}

// send delivers a Result to the writer goroutine, abandoning it if the context
// is cancelled so a shutting-down worker never blocks.
func (c *Crawler) send(ctx context.Context, results chan<- model.Result, r model.Result) error {
	select {
	case results <- r:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// emit publishes a progress Event without ever blocking the hot path: if there
// is no consumer or its buffer is full, the event is dropped (NFR-P3). Stats are
// updated directly, so dropped events never affect the numbers.
func (c *Crawler) emit(ev Event) {
	if c.events == nil {
		return
	}
	select {
	case c.events <- ev:
	default:
	}
}
