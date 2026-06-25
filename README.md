# Concurrent Web Scraper

A configurable, polite, concurrent web scraper written in Go — a single static
binary that crawls thousands of pages concurrently, respects `robots.txt` and
per-host rate limits, extracts links / CSS-selector data, and writes streaming
JSONL or CSV output. Runs headless (cron-friendly) or serves its own embedded
live dashboard.

> Project scaffold. See [`scraper-architecture.md`](../scraper-architecture.md)
> for the full spec. Packages are currently stubs — implemented incrementally
> per the build order below.

## Modes

```sh
# Headless — one scrape job, no UI
scraper --seeds=https://example.com --owner=local

# UI — embedded live dashboard, multiple anonymous users
scraper --ui
```

## Layout

```
cmd/scraper        entry point: flags, wiring, signals, run
internal/config    Config + Validate()
internal/job       Job / JobID / OwnerID types; output-path logic
internal/fetcher   HTTP layer: client, timeout, retries, Fetcher interface
internal/parser    HTML -> links + CSS-selector data (pure funcs)
internal/ratelimit per-host token bucket + robots.txt cache
internal/crawler   the engine: worker pool, queue, dedup, events
internal/stats     thread-safe live metrics
internal/registry  in-memory live-job state (RWMutex)
internal/output    streaming JSONL/CSV writers
internal/cleanup   retention janitor
internal/web       HTTP server, SSE, session middleware, embedded dashboard
```

## Build order

1. `go.mod` + `internal/config`
2. `internal/job`
3. `internal/fetcher` (+ httptest)
4. `internal/parser` (+ fixtures)
5. `internal/ratelimit`
6. `internal/output` (+ test)
7. `internal/stats` (+ test)
8. `internal/crawler` (+ fake Fetcher test)
9. `cmd/scraper/main.go` — headless mode. ✅ Demo-able in terminal.
10. `internal/registry` (+ test)
11. `internal/cleanup` (+ test)
12. `internal/web` — session, server, SSE dashboard
13. Wire `--ui` into `main.go`. ✅ Multi-user demo.
14. README polish + Dockerfile.

## Development

```sh
go build ./...
go test ./...
go test -race ./...
```
