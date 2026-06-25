package fetcher

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fast returns a Client with tiny backoff so retry tests don't sleep for real.
func fast(timeout time.Duration, retries int, maxBytes int64) *Client {
	c := New(timeout, retries, "test-agent/1.0", maxBytes)
	c.baseDelay = time.Millisecond
	c.maxDelay = 5 * time.Millisecond
	return c
}

func TestFetchSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello world"))
	}))
	defer srv.Close()

	resp, err := fast(2*time.Second, 2, 1<<20).Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if string(resp.Body) != "hello world" {
		t.Errorf("Body = %q, want %q", resp.Body, "hello world")
	}
	if resp.URL != srv.URL {
		t.Errorf("URL = %q, want %q", resp.URL, srv.URL)
	}
}

func TestFetchSendsUserAgent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.UserAgent()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if _, err := fast(2*time.Second, 0, 1<<20).Fetch(context.Background(), srv.URL); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "test-agent/1.0" {
		t.Errorf("User-Agent = %q, want %q", got, "test-agent/1.0")
	}
}

func TestFetchRetriesOn5xxThenSucceeds(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) < 3 { // fail twice, succeed on the third attempt
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	resp, err := fast(2*time.Second, 3, 1<<20).Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if n := hits.Load(); n != 3 {
		t.Errorf("server hit %d times, want 3", n)
	}
}

func TestFetchExhaustsRetriesOn5xx(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	resp, err := fast(2*time.Second, 2, 1<<20).Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatalf("expected an error after exhausting retries, got resp=%+v", resp)
	}
	if n := hits.Load(); n != 3 { // 1 initial + 2 retries
		t.Errorf("server hit %d times, want 3", n)
	}
}

func TestFetchDoesNotRetryOn4xx(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	resp, err := fast(2*time.Second, 3, 1<<20).Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("a 4xx should return a Response, not an error: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", resp.StatusCode)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("server hit %d times, want 1 (no retry on 4xx)", n)
	}
}

func TestFetchCapsBodySize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 1000)))
	}))
	defer srv.Close()

	const cap = 10
	resp, err := fast(2*time.Second, 0, cap).Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Body) != cap {
		t.Errorf("Body length = %d, want %d (capped)", len(resp.Body), cap)
	}
}

func TestFetchContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before the call

	_, err := fast(2*time.Second, 3, 1<<20).Fetch(ctx, srv.URL)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestFetchCancelDuringBackoffReturnsPromptly(t *testing.T) {
	// Always 5xx so Fetch wants to retry; cancel mid-flight and confirm the
	// backoff wait is interrupted rather than slept out.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := New(2*time.Second, 5, "test-agent/1.0", 1<<20)
	c.baseDelay = time.Hour // a retry wait we would never sit through
	c.maxDelay = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := c.Fetch(ctx, srv.URL)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Fetch did not abort during backoff (took %v)", elapsed)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestFetchTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Per-request timeout shorter than the handler; no retries so we fail fast.
	_, err := fast(30*time.Millisecond, 0, 1<<20).Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
}

func TestFetchRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"unsupported scheme", "ftp://example.com/file"},
		{"malformed url", "http://[::1]:namedport/"},
		{"empty url", ""},
	}
	c := fast(time.Second, 3, 1<<20)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := c.Fetch(context.Background(), tt.url); err == nil {
				t.Errorf("expected an error for %q", tt.url)
			}
		})
	}
}
