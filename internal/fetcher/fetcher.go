// Package fetcher is the HTTP layer for the scraper: reliable retrieval of a
// single page, isolating every HTTP gotcha (shared client, timeout, retries
// with backoff, a configurable User-Agent, context cancellation, and a bounded
// response size).
//
// It exports the concrete *Client and *Response. Per coding-standards §5 the
// Fetcher *interface* is declared by its consumers (the crawler and rate
// limiter), not here — "accept interfaces, return concrete types".
package fetcher

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"time"
)

// Backoff bounds for the retry loop. baseDelay/maxDelay are also fields on
// Client so tests can shrink them; these are the production defaults.
const (
	defaultBaseDelay = 200 * time.Millisecond
	defaultMaxDelay  = 30 * time.Second
)

// Response is the outcome of fetching one URL. Body is already read and capped
// at the client's maxBytes; URL is the URL that was requested.
type Response struct {
	URL        string
	StatusCode int
	Body       []byte
	Header     http.Header
}

// Client is the production page fetcher: a single shared http.Client (timeout
// set once and reused) plus retry, User-Agent, and response-size policy.
type Client struct {
	http      *http.Client
	userAgent string
	retries   int
	maxBytes  int64
	baseDelay time.Duration
	maxDelay  time.Duration
}

// New builds a Client with one reusable http.Client whose Timeout bounds every
// request. retries is the number of *additional* attempts after the first.
func New(timeout time.Duration, retries int, userAgent string, maxBytes int64) *Client {
	if retries < 0 {
		retries = 0
	}
	return &Client{
		http:      &http.Client{Timeout: timeout},
		userAgent: userAgent,
		retries:   retries,
		maxBytes:  maxBytes,
		baseDelay: defaultBaseDelay,
		maxDelay:  defaultMaxDelay,
	}
}

// Fetch retrieves url, retrying transient failures (network errors and 5xx
// responses) with exponential backoff up to the configured retry count. A 4xx
// is permanent and returned as a *Response (not retried); cancellation, a bad
// URL, or exhausted retries return an error. The other return is invalid when
// err != nil.
func (c *Client) Fetch(ctx context.Context, url string) (*Response, error) {
	attempts := c.retries + 1
	var lastErr error

	for attempt := 1; attempt <= attempts; attempt++ {
		// Wait before every attempt after the first; a cancelled context
		// during the wait aborts promptly instead of sleeping it out.
		if attempt > 1 {
			if err := c.wait(ctx, attempt-1); err != nil {
				return nil, err
			}
		}

		resp, retryable, err := c.try(ctx, url)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !retryable {
			return nil, err
		}
	}

	return nil, fmt.Errorf("fetching %s: gave up after %d attempts: %w", url, attempts, lastErr)
}

// try performs one attempt. It returns a Response on success or a permanent 4xx,
// otherwise an error plus whether that error is worth retrying. Context
// cancellation and malformed requests are never retryable.
func (c *Client) try(ctx context.Context, url string) (resp *Response, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, fmt.Errorf("building request for %s: %w", url, err)
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return nil, false, fmt.Errorf("unsupported URL scheme %q in %s", req.URL.Scheme, url)
	}
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	httpResp, err := c.http.Do(req)
	if err != nil {
		// A cancelled/expired *parent* context is terminal; a per-request
		// client timeout or a transient network error is worth retrying.
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return nil, true, fmt.Errorf("requesting %s: %w", url, err)
	}
	defer c.drainClose(httpResp.Body)

	switch {
	case httpResp.StatusCode >= 500:
		// Server-side and transient — retry.
		return nil, true, fmt.Errorf("requesting %s: server status %s", url, httpResp.Status)
	case httpResp.StatusCode >= 400:
		// Client error — permanent. Surface the status; the body is not needed.
		return &Response{URL: url, StatusCode: httpResp.StatusCode, Header: httpResp.Header}, false, nil
	}

	body, err := io.ReadAll(io.LimitReader(httpResp.Body, c.maxBytes))
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return nil, true, fmt.Errorf("reading body of %s: %w", url, err)
	}
	return &Response{
		URL:        url,
		StatusCode: httpResp.StatusCode,
		Body:       body,
		Header:     httpResp.Header,
	}, false, nil
}

// wait sleeps for an exponentially growing, jittered backoff before retry n
// (n>=1), returning ctx.Err() if the context is cancelled while waiting.
func (c *Client) wait(ctx context.Context, n int) error {
	d := c.baseDelay << (n - 1) // base * 2^(n-1)
	if d <= 0 || d > c.maxDelay {
		d = c.maxDelay // also guards against shift overflow into a non-positive value
	}
	// Full jitter over the lower half avoids a thundering-herd retry burst.
	d = d/2 + time.Duration(rand.Int64N(int64(d/2)+1))

	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// drainClose discards any unread body (up to maxBytes, so a huge response can't
// stall us) to let the keep-alive connection be reused, then closes it. Errors
// are intentionally ignored: the body is being thrown away regardless.
func (c *Client) drainClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, c.maxBytes))
	_ = body.Close()
}
