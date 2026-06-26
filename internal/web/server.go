// Package web serves the embedded live dashboard: anonymous sessions, a small
// JSON API to start and list scrapes, and a Server-Sent Events stream of live
// stats. The crawler is injected as a StartJobFunc so this package never imports
// crawler — the dependency stays one-directional.
package web

import (
	"embed"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ArfaMujahid/scraper/internal/job"
	"github.com/ArfaMujahid/scraper/internal/model"
	"github.com/ArfaMujahid/scraper/internal/registry"
	"github.com/ArfaMujahid/scraper/internal/stats"
)

//go:embed static
var staticFS embed.FS

// StartJobFunc creates and launches a scrape for owner, returning the new job,
// its live stats, and a channel of progress events (closed when the crawl
// finishes). It is injected by main so web doesn't import crawler; the crawl
// runs on a server-lifetime context captured by the closure, not the request
// context.
type StartJobFunc func(owner job.OwnerID, seeds []string) (*job.Job, *stats.Stats, <-chan model.Event)

// feedFrameLimit caps how many recent scraped items each SSE frame carries.
const feedFrameLimit = 50

// Server serves the dashboard, manages sessions, starts jobs, and streams SSE.
type Server struct {
	registry  *registry.Registry
	startJob  StartJobFunc
	cookieTTL time.Duration
	logger    *slog.Logger

	mu    sync.RWMutex            // guards live and feeds
	live  map[job.ID]*stats.Stats // live job stats, for SSE lookup by id
	feeds map[job.ID]*feed        // recent scraped items per job, for the UI feed
}

// New builds a Server. cookieTTL sets the session cookie lifetime (≈ retention).
func New(reg *registry.Registry, startJob StartJobFunc, cookieTTL time.Duration, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		registry:  reg,
		startJob:  startJob,
		cookieTTL: cookieTTL,
		logger:    logger,
		live:      make(map[job.ID]*stats.Stats),
		feeds:     make(map[job.ID]*feed),
	}
}

// Handler returns the configured http.Handler: routes wrapped in the session
// middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("POST /api/scrape", s.handleScrape)
	mux.HandleFunc("GET /api/jobs", s.handleJobs)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	return s.withSession(mux)
}

// handleIndex serves the embedded dashboard page.
func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	b, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

// scrapeRequest is the POST /api/scrape body.
type scrapeRequest struct {
	Seeds []string `json:"seeds"`
}

// handleScrape starts a scrape for the session owner and returns the job id.
func (s *Server) handleScrape(w http.ResponseWriter, r *http.Request) {
	owner := ownerFrom(r.Context())

	var req scrapeRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	seeds := cleanSeeds(req.Seeds)
	if len(seeds) == 0 {
		http.Error(w, "no seeds provided", http.StatusBadRequest)
		return
	}

	j, st, events := s.startJob(owner, seeds)
	f := &feed{}
	s.mu.Lock()
	s.live[j.ID] = st
	s.feeds[j.ID] = f
	s.mu.Unlock()
	go consumeEvents(f, events) // drains until the crawl closes the channel

	s.writeJSON(w, map[string]string{"job": string(j.ID)})
}

// jobDTO is the wire shape for a job in the list response.
type jobDTO struct {
	ID        string   `json:"id"`
	Status    string   `json:"status"`
	Seeds     []string `json:"seeds,omitempty"`
	StartedAt string   `json:"started_at,omitempty"`
	EndedAt   string   `json:"ended_at,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// handleJobs lists the session owner's live jobs.
func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	owner := ownerFrom(r.Context())
	jobs := s.registry.ListByOwner(owner)
	out := make([]jobDTO, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, jobDTO{
			ID:        string(j.ID),
			Status:    j.Status.String(),
			Seeds:     j.Seeds,
			StartedAt: rfc3339(j.StartedAt),
			EndedAt:   rfc3339(j.EndedAt),
			Error:     j.Err,
		})
	}
	s.writeJSON(w, out)
}

// eventFrame is one SSE payload: job status plus a stats snapshot, in the wire
// shape the dashboard consumes (decoupled from stats.Snapshot).
type eventFrame struct {
	Status      string     `json:"status"`
	Done        int64      `json:"done"`
	Errors      int64      `json:"errors"`
	InFlight    int64      `json:"in_flight"`
	Bytes       int64      `json:"bytes"`
	PagesPerSec float64    `json:"pages_per_sec"`
	ElapsedMs   int64      `json:"elapsed_ms"`
	Results     []feedItem `json:"results"` // most-recent-first scraped items
}

// handleEvents streams a job's live stats to the browser as Server-Sent Events
// until the job finishes or the browser disconnects.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	owner := ownerFrom(r.Context())
	id := job.ID(r.URL.Query().Get("job"))
	if id == "" {
		http.Error(w, "missing job parameter", http.StatusBadRequest)
		return
	}
	// Authorization: the session owner must own this job.
	if _, ok := s.registry.Get(owner, id); !ok {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// SSE is a long-lived write; disable the server's WriteTimeout for it.
	if rc := http.NewResponseController(w); rc != nil {
		_ = rc.SetWriteDeadline(time.Time{})
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	if s.sendFrame(w, flusher, owner, id) {
		return // already finished
	}
	for {
		select {
		case <-r.Context().Done():
			return // browser disconnected — no goroutine leak (NFR-R1)
		case <-ticker.C:
			if s.sendFrame(w, flusher, owner, id) {
				return
			}
		}
	}
}

// sendFrame writes one SSE frame and reports whether the job has finished.
func (s *Server) sendFrame(w http.ResponseWriter, flusher http.Flusher, owner job.OwnerID, id job.ID) bool {
	frame := eventFrame{Status: "unknown"}
	if st := s.statsFor(id); st != nil {
		snap := st.Snapshot()
		frame.Done = snap.Done
		frame.Errors = snap.Errors
		frame.InFlight = snap.InFlight
		frame.Bytes = snap.Bytes
		frame.PagesPerSec = snap.PagesPerSec
		frame.ElapsedMs = snap.Elapsed.Milliseconds()
	}
	if f := s.feedFor(id); f != nil {
		frame.Results = f.recent(feedFrameLimit)
	}
	finished := false
	if j, ok := s.registry.Get(owner, id); ok {
		frame.Status = j.Status.String()
		finished = j.Status == job.StatusDone || j.Status == job.StatusFailed
	}
	b, err := json.Marshal(frame)
	if err != nil {
		return true
	}
	if _, err := w.Write([]byte("data: " + string(b) + "\n\n")); err != nil {
		return true // client gone
	}
	flusher.Flush()
	return finished
}

// statsFor returns the live stats for a job id, or nil if not tracked.
func (s *Server) statsFor(id job.ID) *stats.Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.live[id]
}

// feedFor returns the scraped-content feed for a job id, or nil if not tracked.
func (s *Server) feedFor(id job.ID) *feed {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.feeds[id]
}

// feedItem is one scraped page as shown in the dashboard's content feed.
type feedItem struct {
	URL    string `json:"url"`
	Title  string `json:"title,omitempty"`
	Status int    `json:"status"`
	Error  string `json:"error,omitempty"`
}

// feedCap bounds how many recent items a feed retains.
const feedCap = 200

// feed is a per-job ring buffer of recently scraped items, safe for concurrent
// append (the consumer goroutine) and read (the SSE handler).
type feed struct {
	mu    sync.Mutex
	items []feedItem
}

// add appends an item, dropping the oldest once the cap is exceeded.
func (f *feed) add(it feedItem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items = append(f.items, it)
	if len(f.items) > feedCap {
		f.items = f.items[len(f.items)-feedCap:]
	}
}

// recent returns up to n most recent items, newest first.
func (f *feed) recent(n int) []feedItem {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n > len(f.items) {
		n = len(f.items)
	}
	out := make([]feedItem, 0, n)
	for i := len(f.items) - 1; i >= len(f.items)-n; i-- {
		out = append(out, f.items[i])
	}
	return out
}

// consumeEvents drains crawler events into the feed until the channel closes
// (when the crawl finishes), so the goroutine never leaks.
func consumeEvents(f *feed, events <-chan model.Event) {
	for ev := range events {
		it := feedItem{URL: ev.URL, Status: ev.StatusCode}
		if ev.Type == model.EventPageError && ev.Err != nil {
			it.Error = ev.Err.Error()
		} else {
			it.Title = ev.Title
		}
		f.add(it)
	}
}

// writeJSON encodes v as a JSON response.
func (s *Server) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.Warn("web: encoding response", "err", err)
	}
}

// cleanSeeds trims whitespace and drops empty entries.
func cleanSeeds(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// rfc3339 formats t, or "" when zero.
func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
