// Package ratelimit enforces politeness: per-host rate limiting + robots.txt.
//
// Responsibility: a per-host token-bucket limiter plus a robots.txt fetch/cache.
// Blocks (respecting ctx) until a host token is free; different hosts proceed in
// parallel at full speed.
package ratelimit

// TODO: per-host token-bucket limiter (golang.org/x/time/rate) + robots.txt
// cache (github.com/temoto/robotstxt).
