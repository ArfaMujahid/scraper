package web

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArfaMujahid/scraper/internal/job"
	"github.com/ArfaMujahid/scraper/internal/model"
	"github.com/ArfaMujahid/scraper/internal/registry"
	"github.com/ArfaMujahid/scraper/internal/stats"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// newTestServer returns an httptest server. If markDone, started jobs are
// immediately completed so the SSE stream closes on its first frame.
func newTestServer(t *testing.T, markDone bool) *httptest.Server {
	t.Helper()
	reg := registry.New()
	dataDir := t.TempDir()
	start := func(owner job.OwnerID, seeds []string, _ int) (*job.Job, *stats.Stats, <-chan model.Event) {
		j := job.New(owner, seeds, dataDir, "jsonl")
		st := stats.New()
		reg.Add(j)
		reg.SetRunning(j.Owner, j.ID)
		if markDone {
			// Simulate completed output on disk for download tests.
			_ = os.MkdirAll(filepath.Dir(j.OutputPath), 0o755)
			_ = os.WriteFile(j.OutputPath, []byte(`{"url":"http://example.com","status_code":200}`+"\n"), 0o644)
			reg.SetDone(j.Owner, j.ID)
		}
		events := make(chan model.Event)
		close(events) // no live events in the fake; consumer exits immediately
		return j, st, events
	}
	srv := httptest.NewServer(New(reg, start, time.Hour, discardLogger()).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, Timeout: 5 * time.Second}
}

// decodeJSON reads and closes r's body into v.
func decodeJSON(t *testing.T, res *http.Response, v any) {
	t.Helper()
	defer func() { _ = res.Body.Close() }()
	if err := json.NewDecoder(res.Body).Decode(v); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
}

func startScrape(t *testing.T, c *http.Client, base string, seeds []string) *http.Response {
	t.Helper()
	body, err := json.Marshal(map[string][]string{"seeds": seeds})
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Post(base+"/api/scrape", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestSessionCookieMinted(t *testing.T) {
	srv := newTestServer(t, false)
	c := newClient(t)

	res, err := c.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()

	var found bool
	for _, ck := range res.Cookies() {
		if ck.Name == sessionCookie && ck.Value != "" {
			found = true
			if !ck.HttpOnly {
				t.Error("session cookie should be HttpOnly")
			}
		}
	}
	if !found {
		t.Fatal("expected a session_id cookie on first request")
	}
}

func TestScrapeAndList(t *testing.T) {
	srv := newTestServer(t, false)
	c := newClient(t)

	var created map[string]string
	res := startScrape(t, c, srv.URL, []string{"http://example.com"})
	if res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		t.Fatalf("scrape status = %d", res.StatusCode)
	}
	decodeJSON(t, res, &created)
	if created["job"] == "" {
		t.Fatal("expected a job id in the response")
	}

	jres, err := c.Get(srv.URL + "/api/jobs")
	if err != nil {
		t.Fatal(err)
	}
	var jobs []jobDTO
	decodeJSON(t, jres, &jobs)
	if len(jobs) != 1 || jobs[0].ID != created["job"] {
		t.Fatalf("expected to list the created job, got %+v", jobs)
	}
	if jobs[0].Status != "running" {
		t.Errorf("status = %q, want running", jobs[0].Status)
	}
}

func TestScrapeRequiresSeeds(t *testing.T) {
	srv := newTestServer(t, false)
	c := newClient(t)
	res := startScrape(t, c, srv.URL, []string{"   ", ""})
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("empty seeds should be 400, got %d", res.StatusCode)
	}
}

func TestJobIsolationBetweenSessions(t *testing.T) {
	srv := newTestServer(t, false)
	alice := newClient(t)
	bob := newClient(t)

	var created map[string]string
	decodeJSON(t, startScrape(t, alice, srv.URL, []string{"http://example.com"}), &created)

	// Bob (different session) must not see Alice's job.
	jres, err := bob.Get(srv.URL + "/api/jobs")
	if err != nil {
		t.Fatal(err)
	}
	var jobs []jobDTO
	decodeJSON(t, jres, &jobs)
	if len(jobs) != 0 {
		t.Errorf("bob should see no jobs, got %+v", jobs)
	}

	// Bob must not stream Alice's job.
	eres, err := bob.Get(srv.URL + "/api/events?job=" + created["job"])
	if err != nil {
		t.Fatal(err)
	}
	_ = eres.Body.Close()
	if eres.StatusCode != http.StatusNotFound {
		t.Errorf("bob streaming alice's job should be 404, got %d", eres.StatusCode)
	}
}

func TestEventsStream(t *testing.T) {
	srv := newTestServer(t, true) // jobs complete immediately
	c := newClient(t)

	var created map[string]string
	decodeJSON(t, startScrape(t, c, srv.URL, []string{"http://example.com"}), &created)

	eres, err := c.Get(srv.URL + "/api/events?job=" + created["job"])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eres.Body.Close() }()
	if ct := eres.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content-type = %q, want text/event-stream", ct)
	}
	body, err := io.ReadAll(eres.Body) // stream closes once the job is done
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "data:") || !strings.Contains(string(body), `"status":"done"`) {
		t.Errorf("expected an SSE frame with done status, got: %q", body)
	}
}

func TestEventsRequiresJobParam(t *testing.T) {
	srv := newTestServer(t, false)
	c := newClient(t)
	res, err := c.Get(srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("missing job param should be 400, got %d", res.StatusCode)
	}
}

func TestDownloadServesOutput(t *testing.T) {
	srv := newTestServer(t, true) // writes a fake output file on completion
	c := newClient(t)

	var created map[string]string
	decodeJSON(t, startScrape(t, c, srv.URL, []string{"http://example.com"}), &created)

	res, err := c.Get(srv.URL + "/api/download?job=" + created["job"])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("download status = %d, want 200", res.StatusCode)
	}
	if cd := res.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("expected attachment disposition, got %q", cd)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "http://example.com") {
		t.Errorf("download body missing expected content: %q", body)
	}
}

func TestDownloadConvertsFormat(t *testing.T) {
	srv := newTestServer(t, true) // writes a JSONL output file
	c := newClient(t)

	var created map[string]string
	decodeJSON(t, startScrape(t, c, srv.URL, []string{"http://example.com"}), &created)

	// Native is jsonl; request csv → server converts on the fly.
	res, err := c.Get(srv.URL + "/api/download?job=" + created["job"] + "&format=csv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("download?format=csv status = %d, want 200", res.StatusCode)
	}
	if cd := res.Header.Get("Content-Disposition"); !strings.Contains(cd, ".csv") {
		t.Errorf("expected a .csv filename, got %q", cd)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.HasPrefix(string(body), "url,status_code,") {
		t.Errorf("expected CSV header, got: %q", body)
	}
}

func TestDownloadIsolatedByOwner(t *testing.T) {
	srv := newTestServer(t, true)
	alice := newClient(t)
	bob := newClient(t)

	var created map[string]string
	decodeJSON(t, startScrape(t, alice, srv.URL, []string{"http://example.com"}), &created)

	res, err := bob.Get(srv.URL + "/api/download?job=" + created["job"])
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("bob downloading alice's job should be 404, got %d", res.StatusCode)
	}
}
