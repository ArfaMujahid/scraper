// Package ratelimit enforces politeness: a per-host token-bucket rate limit and
// robots.txt rules. One Limiter is shared by every crawler worker; rate limits
// are per host so different sites are scraped in parallel at full speed (FR-5),
// and robots.txt is fetched once per host and cached (FR-4).
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"

	"github.com/temoto/robotstxt"
	"golang.org/x/time/rate"

	"github.com/ArfaMujahid/scraper/internal/fetcher"
)

// defaultBurst is the token-bucket burst. 1 means strictly no faster than the
// configured rate per host — the politest setting (FR-5).
const defaultBurst = 1

// ErrDisallowed is returned by Wait when robots.txt forbids the URL for our
// agent. Callers match it with errors.Is.
var ErrDisallowed = errors.New("ratelimit: blocked by robots.txt")

// Fetcher retrieves a URL. ratelimit depends on this small interface rather
// than the concrete *fetcher.Client so robots.txt fetching can be faked in
// tests (coding-standards §5: define interfaces at the consumer).
type Fetcher interface {
	Fetch(ctx context.Context, url string) (*fetcher.Response, error)
}

// Compile-time check that the production fetcher satisfies our interface.
var _ Fetcher = (*fetcher.Client)(nil)

// robotsEntry caches one host's parsed robots.txt. The once guarantees the file
// is fetched a single time even under concurrent Wait calls for that host.
type robotsEntry struct {
	once sync.Once
	data *robotstxt.RobotsData
}

// Limiter enforces a per-host request rate and robots.txt rules. It is safe for
// concurrent use by all workers.
type Limiter struct {
	mu      sync.Mutex               // guards perHost and robots (map access only)
	perHost map[string]*rate.Limiter // authority -> token bucket
	robots  map[string]*robotsEntry  // origin -> cached robots.txt

	ratePerHost rate.Limit
	burst       int
	respect     bool
	fetcher     Fetcher
	agent       string
}

// New builds a Limiter allowing ratePerHost requests/sec to each host. When
// respectRobots is true, robots.txt is consulted (and cached) per host; agent
// is the token matched against robots.txt User-agent groups.
func New(ratePerHost float64, respectRobots bool, agent string, f Fetcher) *Limiter {
	return &Limiter{
		perHost:     make(map[string]*rate.Limiter),
		robots:      make(map[string]*robotsEntry),
		ratePerHost: rate.Limit(ratePerHost),
		burst:       defaultBurst,
		respect:     respectRobots,
		fetcher:     f,
		agent:       agent,
	}
}

// Wait blocks until a request to rawURL is permitted, respecting both the
// per-host rate limit and ctx cancellation. It returns ErrDisallowed if
// robots.txt forbids the URL, or a wrapped error on a bad URL or cancellation.
func (l *Limiter) Wait(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("ratelimit: parsing url %q: %w", rawURL, err)
	}
	if u.Host == "" {
		return fmt.Errorf("ratelimit: url %q has no host", rawURL)
	}

	// Check robots before spending a rate token — no point waiting to fetch a
	// URL we are not allowed to fetch.
	if l.respect {
		if !l.allowed(ctx, u) {
			return fmt.Errorf("%w: %s", ErrDisallowed, rawURL)
		}
	}

	if err := l.limiterFor(u.Host).Wait(ctx); err != nil {
		return fmt.Errorf("ratelimit: waiting for %s: %w", u.Host, err)
	}
	return nil
}

// allowed reports whether robots.txt permits u for our agent. It fails open: if
// robots.txt cannot be fetched or parsed, the URL is allowed rather than
// blocking the whole host on a transient failure (the fetcher has already
// retried). A 4xx/5xx robots response is handled per the robots spec by
// robotstxt.FromStatusAndBytes.
func (l *Limiter) allowed(ctx context.Context, u *url.URL) bool {
	data := l.robotsFor(ctx, u)
	if data == nil {
		return true
	}
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	return data.TestAgent(path, l.agent)
}

// robotsFor returns the cached robots.txt for u's origin, fetching and parsing
// it exactly once. Returns nil (treated as "allow") when the file cannot be
// fetched or parsed.
func (l *Limiter) robotsFor(ctx context.Context, u *url.URL) *robotstxt.RobotsData {
	origin := u.Scheme + "://" + u.Host

	l.mu.Lock()
	entry, ok := l.robots[origin]
	if !ok {
		entry = &robotsEntry{}
		l.robots[origin] = entry
	}
	l.mu.Unlock()

	entry.once.Do(func() {
		resp, err := l.fetcher.Fetch(ctx, origin+"/robots.txt")
		if err != nil {
			return // fail open: leave data nil
		}
		data, err := robotstxt.FromStatusAndBytes(resp.StatusCode, resp.Body)
		if err != nil {
			return // fail open
		}
		entry.data = data
	})
	return entry.data
}

// limiterFor returns the token bucket for host, creating it on first use. The
// lock is held only for map access, never while a caller waits for a token.
func (l *Limiter) limiterFor(host string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	lim, ok := l.perHost[host]
	if !ok {
		lim = rate.NewLimiter(l.ratePerHost, l.burst)
		l.perHost[host] = lim
	}
	return lim
}
