module github.com/ArfaMujahid/scraper

go 1.25.0

// Dependencies are added as each package is implemented:
//   github.com/PuerkitoBio/goquery   — CSS-selector extraction (parser)
//   golang.org/x/time/rate           — token-bucket rate limiter (ratelimit)
//   golang.org/x/sync/errgroup       — coordinated goroutines (crawler)
//   github.com/google/uuid           — job + session IDs (job, web)
//   github.com/temoto/robotstxt      — robots.txt parsing (ratelimit)

require (
	github.com/PuerkitoBio/goquery v1.12.0
	github.com/google/go-cmp v0.7.0
	github.com/google/uuid v1.6.0
)

require (
	github.com/andybalholm/cascadia v1.3.3 // indirect
	golang.org/x/net v0.56.0 // indirect
)
