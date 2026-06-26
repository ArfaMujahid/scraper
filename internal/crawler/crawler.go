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
	MaxPages  int // cap on total pages fetched; 0 = unlimited
	Selectors map[string]string
}

// Crawler runs a single scrape job using a bounded worker pool.
type Crawler struct {
	cfg     Config
	fetcher Fetcher
	limiter *ratelimit.Limiter
	writer  output.Writer
	stats   *stats.Stats
	events  chan<- model.Event // may be nil (headless): emit is then a no-op
}

// New wires a Crawler with its collaborators. A nil events channel disables
// progress emission (headless mode).
func New(cfg Config, f Fetcher, l *ratelimit.Limiter, w output.Writer, s *stats.Stats, events chan<- model.Event) *Crawler {
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

// coordinate owns the frontier (as a stack) and the visited set. It dispatches
// work and consumes discovered links, finishing when nothing is in flight and
// either the frontier is empty or the page cap is reached.
//
//   - pending  = pages dispatched to workers but not yet reported back
//   - dispatched = total pages handed out (the page-count cap, MaxPages)
//
// Dispatch is disabled (nil send channel) when the frontier is empty or the cap
// is reached, so the select never spins and an infinite-link trap is bounded by
// MaxPages and MaxDepth.
func (c *Crawler) coordinate(ctx context.Context, seeds []string, work chan<- item, discovered <-chan discoveredMsg) {
	defer close(work)

	visited := make(map[string]struct{})
	var frontier []item
	pending := 0
	dispatched := 0

	for _, s := range seeds {
		if _, ok := visited[s]; ok {
			continue
		}
		visited[s] = struct{}{}
		frontier = append(frontier, item{url: s, depth: 0})
	}

	for {
		capReached := c.cfg.MaxPages > 0 && dispatched >= c.cfg.MaxPages
		if pending == 0 && (len(frontier) == 0 || capReached) {
			return
		}

		var sendCh chan<- item
		var next item
		if n := len(frontier); n > 0 && !capReached {
			sendCh = work
			next = frontier[n-1]
		}

		select {
		case <-ctx.Done():
			return
		case sendCh <- next:
			frontier = frontier[:len(frontier)-1]
			pending++
			dispatched++
		case d := <-discovered:
			pending--
			if d.depth+1 > c.cfg.MaxDepth {
				continue
			}
			for _, link := range d.links {
				if _, ok := visited[link]; ok {
					continue
				}
				visited[link] = struct{}{}
				frontier = append(frontier, item{url: link, depth: d.depth + 1})
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

	title := doc.Title()
	r := model.Result{
		URL:        it.url,
		StatusCode: resp.StatusCode,
		Title:      title,
		Data:       doc.Data(c.cfg.Selectors),
		Links:      links,
		Depth:      it.depth,
		FetchedAt:  now,
	}
	if err := c.send(ctx, results, r); err != nil {
		return nil, err
	}
	c.stats.IncDone()
	c.emit(model.Event{Type: model.EventPageDone, URL: it.url, Title: title, StatusCode: resp.StatusCode, Bytes: len(resp.Body)})
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
	c.emit(model.Event{Type: model.EventPageError, URL: it.url, StatusCode: status, Err: srcErr})
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
func (c *Crawler) emit(ev model.Event) {
	if c.events == nil {
		return
	}
	select {
	case c.events <- ev:
	default:
	}
}
