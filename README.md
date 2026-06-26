# Concurrent Web Scraper

A configurable, polite, concurrent web scraper written in Go — a single static
binary that crawls pages concurrently, respects `robots.txt` and per-host rate
limits, extracts links and CSS-selector data, and streams structured output
(JSON Lines or CSV). It runs **headless** for real jobs (cron-friendly) or
serves its own **embedded live dashboard** where multiple anonymous users each
run isolated scrapes and watch progress — and the scraped content — in real
time.

## What it does

- **Bounded concurrency** — a fixed worker pool, never goroutine-per-URL, so it
  scales without DoS-ing the target or itself.
- **Polite by default** — obeys `robots.txt` and a per-host rate limit; different
  hosts are crawled in parallel at full speed.
- **Safe crawling** — deduplicates URLs, caps crawl size with `--depth` and
  `--max-pages` (so infinite-link traps terminate), and caps each response body.
- **Structured output** — one file per job (`JSONL` or `CSV`), streamed to disk
  as it goes (constant memory; a crash leaves a valid partial file).
- **Two modes** — a headless CLI and a live web dashboard, driven by the same
  engine.
- **Graceful shutdown** — `Ctrl-C`/`SIGTERM` drains in-flight work and flushes
  results; nothing already scraped is lost.

---

## Setup

Prerequisites: **Go 1.23+** (the build uses a recent toolchain; CI builds on the
latest stable Go).

```sh
git clone https://github.com/ArfaMujahid/scraper.git
cd scraper
go build -o scraper ./cmd/scraper
```

That produces a single self-contained `./scraper` binary (the dashboard assets
are embedded). Optionally install the dev tools used by the quality gate:

```sh
make tools   # goimports, govulncheck, golangci-lint
```

---

## Running

### Headless (one job, then exit)

```sh
# Scrape one site two levels deep, capture a CSS field, write JSONL
./scraper --seeds=https://example.com --depth=2 --select price=.price

# Multiple seeds + fields, CSV output
./scraper --seeds=https://a.com,https://b.com \
  --select title=h1 --select price=.price --format=csv
```

On completion it prints a summary (pages scraped, errors, elapsed, throughput,
output path) and writes results to
`data/scrapes/{owner}/{job-id}_{timestamp}.{jsonl,csv}`.

### UI (live dashboard)

```sh
./scraper --ui
# open http://127.0.0.1:8080
```

In the browser: paste seed URLs (one per line), optionally set **Max pages**
for that scrape, click **Start scrape**, and watch live —

- **metrics**: pages done, in-flight, errors, pages/sec, KB, elapsed;
- **scraped content**: a feed of each page as it's fetched (status, title, URL);
- **your jobs**: the list of scrapes from your session;
- when a job finishes, **Download** buttons offer the full results as **JSONL or
  CSV** (the file is converted on the fly, regardless of which format it was
  scraped in).

Each browser gets an anonymous session cookie and sees (and downloads) only its
own jobs — open a second browser (or incognito window) to see two isolated users
at once. **Max pages** is the one parameter overridable per scrape from the UI
(blank uses the server default; the UI never enables an unbounded crawl); the
rest (depth, workers, rate, selectors) come from the server's startup flags.

---

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--seeds` | – | comma-separated seed URLs (or positional args) |
| `--depth` | 2 | max crawl depth (0 = seeds only) |
| `--max-pages` | 10 | max pages to fetch per job (0 = unlimited) |
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

---

## How it works

`cmd/scraper` is the composition root; all logic lives in `internal/`, organized
by domain:

| Package | Responsibility |
|---------|----------------|
| `config` | tunables + fail-fast validation |
| `job` | `Job`/`OwnerID`/`ID`, isolated output-path scheme |
| `fetcher` | HTTP client: timeout, retries + backoff, body cap |
| `parser` | HTML → links + CSS-selector data (pure) |
| `ratelimit` | per-host token bucket + robots.txt cache |
| `output` | streaming JSONL/CSV writers |
| `stats` | thread-safe live counters |
| `crawler` | the engine: coordinator + bounded worker pool |
| `registry` | in-memory live-job store (RWMutex) |
| `cleanup` | retention janitor |
| `web` | session, server, SSE, embedded dashboard |
| `model` | shared `Result`/`Event` types (leaf package) |

**The engine.** A coordinator goroutine owns the frontier, the visited set, and
the page counters, so deduplication needs no lock. It feeds a bounded pool of
worker goroutines; each worker waits for a rate-limit token, fetches, parses,
and reports discovered links back. Results stream to a single writer goroutine
(no file lock needed), and progress events fan out on a channel — the same
engine drives both the CLI summary and the dashboard's live feed.

**How traps terminate.** Cycles are bounded by the visited set (each URL fetched
once); infinitely deep chains by `--depth`; and infinite *distinct-URL* spaces
(calendars, pagination, faceted search) by `--max-pages`. So a crawl always
terminates.

**Output.** JSON Lines (one object per line) is the default — streamable and
partial-file-safe. CSV is also supported. Output is partitioned per owner and
per job, so concurrent users (or runs) never share or corrupt a file.

See `scraper-architecture.md` (the what/why), `implementation-design.md` (the
low-level design), and `coding-standards.md` (how the code is written).

## Identity & auth (v1)

There is **no authentication, by design**. In UI mode the server mints an
anonymous session UUID stored in an `HttpOnly` cookie; isolation is
**organizational, not a security boundary** (a session cookie is an unverified
bearer token). The `OwnerID` abstraction is the seam where real auth slots in
for v2.

---

## Development

```sh
make check   # gofmt, goimports, vet, golangci-lint, govulncheck, build, test -race
make help    # list all targets
```

CI runs the same `make check` on every push and PR.

## Docker

```sh
docker build -t scraper .
docker run --rm -p 8080:8080 -v "$PWD/data:/home/nonroot/data" scraper
```

A multi-stage build on a distroless static base (a few MB, `CGO_ENABLED=0`) with
the dashboard assets embedded in the binary.
