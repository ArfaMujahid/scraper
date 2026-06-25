module github.com/ArfaMujahid/scraper

go 1.23

// Dependencies are added as each package is implemented:
//   github.com/PuerkitoBio/goquery   — CSS-selector extraction (parser)
//   golang.org/x/time/rate           — token-bucket rate limiter (ratelimit)
//   golang.org/x/sync/errgroup       — coordinated goroutines (crawler)
//   github.com/google/uuid           — job + session IDs (job, web)
//   github.com/temoto/robotstxt      — robots.txt parsing (ratelimit)
