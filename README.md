# Concurrent Web Scraper

A configurable, polite, concurrent web scraper in Go — a single static binary
that crawls thousands of pages concurrently, respects `robots.txt` and per-host
rate limits, extracts links and CSS-selector data, and streams structured output
(JSON Lines or CSV). It runs **headless** for real jobs or serves its own
**embedded live dashboard** where multiple anonymous users each run isolated
scrapes.

## Quick start

```sh
# Build
go build -o scraper ./cmd/scraper

# Headless — one scrape job, prints a summary, exits
./scraper --seeds=https://example.com --depth=1 --select price=.price

# UI — embedded live dashboard at http://127.0.0.1:8080
./scraper --ui
```

## Modes

**Headless** crawls from the seeds, streams results to a file, prints a summary,
and exits (cron-friendly). **UI** starts an HTTP server; each browser gets an
anonymous session cookie, can start a scrape, and watches live worker count,
pages/sec, errors, and progress over Server-Sent Events — seeing only its own
jobs.

Output is isolated per job: `data/scrapes/{owner}/{job-id}_{timestamp}.{jsonl,csv}`.
In headless mode the owner is `--owner` (default `local`); in UI mode it's the
session id from the cookie.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--seeds` | – | comma-separated seed URLs (or pass as positional args) |
| `--depth` | 2 | max crawl depth (0 = seeds only) |
| `--workers` | 10 | concurrent workers (bounded pool) |
| `--rate` | 1 | max requests/sec per host |
| `--timeout` | 30s | per-request timeout |
| `--retries` | 3 | retries for transient failures (network / 5xx) |
| `--respect-robots` | true | obey `robots.txt` |
| `--user-agent` | `scraper/0.1 …` | User-Agent header |
| `--select` | – | CSS field to extract as `name=selector` (repeatable) |
| `--format` | jsonl | output format: `jsonl` or `csv` |
| `--owner` | local | owner id for output isolation (headless) |
| `--data-dir` | data | output root directory |
| `--max-body` | 10 MiB | response-body size cap |
| `--ui` | false | run the web dashboard |
| `--ui-addr` | 127.0.0.1:8080 | dashboard listen address |
| `--retention` | 168h | delete completed jobs older than this (UI janitor) |
| `--sweep-interval` | 1h | how often the janitor runs (UI) |

Example:

```sh
./scraper --seeds=https://example.com --depth=2 --workers=8 --rate=5 \
  --select title=h1 --select price=.price --format=csv
```

## Architecture

`cmd/scraper` is the composition root; all logic lives in `internal/`, organized
by domain:

| Package | Responsibility |
|---------|----------------|
| `config` | tunables + fail-fast validation |
| `job` | `Job`/`OwnerID`/`ID`, isolated output-path scheme |
| `fetcher` | HTTP client: timeout, retries+backoff, body cap |
| `parser` | HTML → links + CSS-selector data (pure) |
| `ratelimit` | per-host token bucket + robots.txt cache |
| `output` | streaming JSONL/CSV writers |
| `stats` | thread-safe live counters |
| `crawler` | the engine: coordinator + bounded worker pool |
| `registry` | in-memory live-job store (RWMutex) |
| `cleanup` | retention janitor |
| `web` | session, server, SSE, embedded dashboard |
| `model` | shared `Result` type (leaf package) |

The crawler emits progress on a channel, so the same engine drives the CLI and
the dashboard. See `scraper-architecture.md` (the what/why),
`implementation-design.md` (the low-level design), and `coding-standards.md`.

## Identity & auth (v1)

There is **no authentication, by design**. In UI mode the server mints an
anonymous session UUID stored in an `HttpOnly` cookie; isolation is
**organizational, not a security boundary** (a session cookie is an unverified
bearer token). The `OwnerID` abstraction is the seam where real auth slots in
for v2.

## Development

```sh
make tools   # install goimports, govulncheck, golangci-lint (once)
make check   # gofmt, goimports, vet, golangci-lint, govulncheck, build, test -race
make help    # list all targets
```

CI runs the same `make check` on every push and PR.

## Docker

```sh
docker build -t scraper .
docker run --rm -p 8080:8080 -v "$PWD/data:/home/nonroot/data" scraper
```

The image is a multi-stage build on a distroless static base (a few MB,
`CGO_ENABLED=0`), with the dashboard assets embedded in the binary.
