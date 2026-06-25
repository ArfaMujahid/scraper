package ratelimit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ArfaMujahid/scraper/internal/fetcher"
)

// fakeFetcher serves canned robots.txt bodies and records how often each URL is
// fetched, so tests can assert robots.txt is fetched once per host.
type fakeFetcher struct {
	mu     sync.Mutex
	bodies map[string]string // robots URL -> body
	status int               // status to return (0 => 200)
	err    error             // if set, Fetch returns this error
	calls  map[string]int
}

func newFakeFetcher() *fakeFetcher {
	return &fakeFetcher{bodies: map[string]string{}, calls: map[string]int{}}
}

func (f *fakeFetcher) Fetch(_ context.Context, url string) (*fetcher.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[url]++
	if f.err != nil {
		return nil, f.err
	}
	st := f.status
	if st == 0 {
		st = 200
	}
	return &fetcher.Response{URL: url, StatusCode: st, Body: []byte(f.bodies[url])}, nil
}

func (f *fakeFetcher) callCount(url string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[url]
}

func TestWaitRateLimitsPerHost(t *testing.T) {
	// rate=20/sec => ~50ms between requests; burst 1 lets the first through.
	l := New(20, false, "test-agent", newFakeFetcher())

	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := l.Wait(context.Background(), "http://example.com/p"); err != nil {
			t.Fatalf("Wait %d: %v", i, err)
		}
	}
	// 3 requests at 20/sec: first immediate, then 2 gaps of ~50ms => >= ~100ms.
	if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
		t.Errorf("3 requests took %v, expected the rate limiter to space them out", elapsed)
	}
}

func TestWaitDifferentHostsDoNotBlockEachOther(t *testing.T) {
	l := New(1, false, "test-agent", newFakeFetcher()) // 1/sec is very slow per host

	start := time.Now()
	// First hit on each of two hosts consumes that host's initial burst token,
	// so both should return immediately despite the slow per-host rate.
	if err := l.Wait(context.Background(), "http://a.example/x"); err != nil {
		t.Fatal(err)
	}
	if err := l.Wait(context.Background(), "http://b.example/x"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("different hosts blocked each other (took %v)", elapsed)
	}
}

func TestWaitRobotsDisallowed(t *testing.T) {
	ff := newFakeFetcher()
	ff.bodies["http://example.com/robots.txt"] = "User-agent: *\nDisallow: /private\n"
	l := New(1000, true, "test-agent", ff)

	if err := l.Wait(context.Background(), "http://example.com/private/page"); !errors.Is(err, ErrDisallowed) {
		t.Errorf("expected ErrDisallowed for /private, got: %v", err)
	}
	if err := l.Wait(context.Background(), "http://example.com/public/page"); err != nil {
		t.Errorf("/public should be allowed, got: %v", err)
	}
}

func TestWaitRobotsAllowsWhenNoRules(t *testing.T) {
	ff := newFakeFetcher() // empty body, 200
	l := New(1000, true, "test-agent", ff)

	if err := l.Wait(context.Background(), "http://example.com/anything"); err != nil {
		t.Errorf("empty robots.txt should allow everything, got: %v", err)
	}
}

func TestWaitRobotsFetchedOncePerHost(t *testing.T) {
	ff := newFakeFetcher()
	ff.bodies["http://example.com/robots.txt"] = "User-agent: *\nAllow: /\n"
	l := New(100000, true, "test-agent", ff) // effectively no rate delay

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = l.Wait(context.Background(), "http://example.com/page")
		}()
	}
	wg.Wait()

	if n := ff.callCount("http://example.com/robots.txt"); n != 1 {
		t.Errorf("robots.txt fetched %d times, want exactly 1", n)
	}
}

func TestWaitRespectDisabledSkipsRobots(t *testing.T) {
	ff := newFakeFetcher()
	ff.bodies["http://example.com/robots.txt"] = "User-agent: *\nDisallow: /\n" // would block all
	l := New(1000, false, "test-agent", ff)                                     // but respect is off

	if err := l.Wait(context.Background(), "http://example.com/private"); err != nil {
		t.Errorf("robots should be ignored when respect is off, got: %v", err)
	}
	if n := ff.callCount("http://example.com/robots.txt"); n != 0 {
		t.Errorf("robots.txt fetched %d times with respect off, want 0", n)
	}
}

func TestWaitRobotsFailsOpenOnFetchError(t *testing.T) {
	ff := newFakeFetcher()
	ff.err = errors.New("network down")
	l := New(1000, true, "test-agent", ff)

	// Robots unreachable => allow rather than block the whole host.
	if err := l.Wait(context.Background(), "http://example.com/page"); err != nil {
		t.Errorf("expected fail-open allow on robots fetch error, got: %v", err)
	}
}

func TestWaitContextCancelled(t *testing.T) {
	l := New(1, false, "test-agent", newFakeFetcher()) // slow rate

	// Consume the initial token so the next Wait must block on refill.
	if err := l.Wait(context.Background(), "http://example.com/x"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.Wait(ctx, "http://example.com/y"); err == nil {
		t.Error("expected an error from a cancelled context")
	}
}

func TestWaitInvalidURL(t *testing.T) {
	l := New(1000, false, "test-agent", newFakeFetcher())
	tests := []string{"://bad", "/relative-no-host", ""}
	for _, raw := range tests {
		if err := l.Wait(context.Background(), raw); err == nil {
			t.Errorf("expected error for %q", raw)
		}
	}
}
