package crawler

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/ArfaMujahid/scraper/internal/fetcher"
	"github.com/ArfaMujahid/scraper/internal/model"
	"github.com/ArfaMujahid/scraper/internal/ratelimit"
	"github.com/ArfaMujahid/scraper/internal/stats"
)

// fakeFetcher serves canned HTML per URL and canned errors. It satisfies both
// crawler.Fetcher and ratelimit.Fetcher.
type fakeFetcher struct {
	pages map[string]string
	errs  map[string]error
}

func (f *fakeFetcher) Fetch(_ context.Context, url string) (*fetcher.Response, error) {
	if err := f.errs[url]; err != nil {
		return nil, err
	}
	html, ok := f.pages[url]
	if !ok {
		return &fetcher.Response{URL: url, StatusCode: 404}, nil
	}
	return &fetcher.Response{URL: url, StatusCode: 200, Body: []byte(html)}, nil
}

// fakeWriter collects results; safe for the single writer goroutine plus the
// test reading after Run returns.
type fakeWriter struct {
	mu      sync.Mutex
	results []model.Result
}

func (w *fakeWriter) Write(r model.Result) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.results = append(w.results, r)
	return nil
}
func (w *fakeWriter) Close() error { return nil }
func (w *fakeWriter) all() []model.Result {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]model.Result(nil), w.results...)
}

func link(href string) string { return `<a href="` + href + `">x</a>` }

// newCrawler wires a crawler with a real (but unthrottled, robots-off) limiter.
func newCrawler(t *testing.T, cfg Config, f *fakeFetcher, w *fakeWriter, events chan<- model.Event) *Crawler {
	t.Helper()
	lim := ratelimit.New(1e6, false, "test", f)
	return New(cfg, f, lim, w, stats.New(), events)
}

// resultURLs returns the set of URLs that appear in the collected results.
func resultURLs(rs []model.Result) map[string]bool {
	set := make(map[string]bool, len(rs))
	for _, r := range rs {
		set[r.URL] = true
	}
	return set
}

func TestCrawlDepthZeroOnlySeeds(t *testing.T) {
	f := &fakeFetcher{pages: map[string]string{
		"http://site/seed": link("http://site/a") + link("http://site/b"),
		"http://site/a":    "",
		"http://site/b":    "",
	}}
	w := &fakeWriter{}
	c := newCrawler(t, Config{Workers: 3, MaxDepth: 0}, f, w, nil)

	if err := c.Run(context.Background(), []string{"http://site/seed"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := resultURLs(w.all())
	if len(got) != 1 || !got["http://site/seed"] {
		t.Errorf("depth 0 should fetch only the seed, got %v", got)
	}
}

func TestCrawlFollowsLinksToDepth(t *testing.T) {
	f := &fakeFetcher{pages: map[string]string{
		"http://site/seed": link("http://site/a") + link("http://site/b"),
		"http://site/a":    link("http://site/c"),
		"http://site/b":    "",
		"http://site/c":    "",
	}}

	// depth 1: seed, a, b (c is at depth 2)
	w1 := &fakeWriter{}
	if err := newCrawler(t, Config{Workers: 4, MaxDepth: 1}, f, w1, nil).Run(context.Background(), []string{"http://site/seed"}); err != nil {
		t.Fatal(err)
	}
	if got := resultURLs(w1.all()); len(got) != 3 || got["http://site/c"] {
		t.Errorf("depth 1 expected {seed,a,b}, got %v", got)
	}

	// depth 2: seed, a, b, c
	w2 := &fakeWriter{}
	if err := newCrawler(t, Config{Workers: 4, MaxDepth: 2}, f, w2, nil).Run(context.Background(), []string{"http://site/seed"}); err != nil {
		t.Fatal(err)
	}
	if got := resultURLs(w2.all()); len(got) != 4 || !got["http://site/c"] {
		t.Errorf("depth 2 expected {seed,a,b,c}, got %v", got)
	}
}

func TestCrawlDedupsCycle(t *testing.T) {
	// a <-> b cycle; each should be fetched exactly once.
	f := &fakeFetcher{pages: map[string]string{
		"http://site/a": link("http://site/b"),
		"http://site/b": link("http://site/a"),
	}}
	w := &fakeWriter{}
	c := newCrawler(t, Config{Workers: 5, MaxDepth: 10}, f, w, nil)
	if err := c.Run(context.Background(), []string{"http://site/a"}); err != nil {
		t.Fatal(err)
	}
	all := w.all()
	if len(all) != 2 {
		t.Errorf("cycle should yield 2 results, got %d: %v", len(all), resultURLs(all))
	}
}

func TestCrawlRecordsFetchErrors(t *testing.T) {
	f := &fakeFetcher{
		pages: map[string]string{"http://site/seed": link("http://site/bad") + link("http://site/missing")},
		errs:  map[string]error{"http://site/bad": errors.New("boom")},
	}
	w := &fakeWriter{}
	c := newCrawler(t, Config{Workers: 3, MaxDepth: 1}, f, w, nil)
	if err := c.Run(context.Background(), []string{"http://site/seed"}); err != nil {
		t.Fatal(err)
	}

	var errCount int
	byURL := map[string]model.Result{}
	for _, r := range w.all() {
		byURL[r.URL] = r
		if r.Error != "" {
			errCount++
		}
	}
	if errCount != 2 { // network error + 404
		t.Errorf("expected 2 error results, got %d (%v)", errCount, w.all())
	}
	if byURL["http://site/bad"].Error == "" {
		t.Error("expected the network-failing URL to be recorded with an error")
	}
	if byURL["http://site/missing"].StatusCode != 404 {
		t.Errorf("expected 404 recorded for missing page, got %d", byURL["http://site/missing"].StatusCode)
	}
}

func TestCrawlExtractsData(t *testing.T) {
	f := &fakeFetcher{pages: map[string]string{
		"http://site/p": `<html><head><title>Hi</title></head><body><span class="price">$5</span></body></html>`,
	}}
	w := &fakeWriter{}
	c := newCrawler(t, Config{Workers: 1, MaxDepth: 0, Selectors: map[string]string{"price": ".price"}}, f, w, nil)
	if err := c.Run(context.Background(), []string{"http://site/p"}); err != nil {
		t.Fatal(err)
	}
	all := w.all()
	if len(all) != 1 {
		t.Fatalf("want 1 result, got %d", len(all))
	}
	if all[0].Title != "Hi" {
		t.Errorf("title = %q, want Hi", all[0].Title)
	}
	if all[0].Data["price"] != "$5" {
		t.Errorf("data[price] = %q, want $5", all[0].Data["price"])
	}
}

func TestCrawlEmitsEvents(t *testing.T) {
	f := &fakeFetcher{pages: map[string]string{
		"http://site/seed": link("http://site/a"),
		"http://site/a":    "",
	}}
	w := &fakeWriter{}
	events := make(chan model.Event, 100)
	c := newCrawler(t, Config{Workers: 2, MaxDepth: 1}, f, w, events)
	if err := c.Run(context.Background(), []string{"http://site/seed"}); err != nil {
		t.Fatal(err)
	}
	close(events)

	var done int
	for ev := range events {
		if ev.Type == model.EventPageDone {
			done++
		}
	}
	if done != 2 {
		t.Errorf("expected 2 PageDone events, got %d", done)
	}
}

func TestCrawlRespectsMaxPages(t *testing.T) {
	// A trap: every page links to two fresh, distinct URLs forever.
	f := &fakeFetcher{pages: trapPages(200)}
	w := &fakeWriter{}
	c := newCrawler(t, Config{Workers: 4, MaxDepth: 100, MaxPages: 10}, f, w, nil)
	if err := c.Run(context.Background(), []string{"http://trap/0"}); err != nil {
		t.Fatal(err)
	}
	if n := len(w.all()); n != 10 {
		t.Errorf("MaxPages=10 should fetch exactly 10 pages, got %d", n)
	}
}

func TestCrawlMaxPagesZeroIsUnlimited(t *testing.T) {
	f := &fakeFetcher{pages: map[string]string{
		"http://site/seed": link("http://site/a") + link("http://site/b"),
		"http://site/a":    "",
		"http://site/b":    "",
	}}
	w := &fakeWriter{}
	c := newCrawler(t, Config{Workers: 3, MaxDepth: 5, MaxPages: 0}, f, w, nil)
	if err := c.Run(context.Background(), []string{"http://site/seed"}); err != nil {
		t.Fatal(err)
	}
	if n := len(w.all()); n != 3 {
		t.Errorf("MaxPages=0 (unlimited) should fetch all 3 pages, got %d", n)
	}
}

// trapPages builds n pages where page i links to two distinct deeper pages, a
// stand-in for an infinite-link trap.
func trapPages(n int) map[string]string {
	pages := make(map[string]string, n)
	for i := 0; i < n; i++ {
		base := "http://trap/"
		pages[base+itoa(i)] = link(base+itoa(2*i+1)) + link(base+itoa(2*i+2))
	}
	return pages
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestCrawlEmptySeeds(t *testing.T) {
	w := &fakeWriter{}
	c := newCrawler(t, Config{Workers: 3, MaxDepth: 2}, &fakeFetcher{}, w, nil)
	if err := c.Run(context.Background(), nil); err != nil {
		t.Fatalf("empty seeds should succeed, got: %v", err)
	}
	if len(w.all()) != 0 {
		t.Errorf("expected no results, got %d", len(w.all()))
	}
}

func TestCrawlCancelledContext(t *testing.T) {
	f := &fakeFetcher{pages: map[string]string{"http://site/seed": ""}}
	w := &fakeWriter{}
	c := newCrawler(t, Config{Workers: 3, MaxDepth: 5}, f, w, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before Run

	if err := c.Run(ctx, []string{"http://site/seed"}); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}
