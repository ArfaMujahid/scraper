// Package fetcher is the HTTP layer for the scraper.
//
// Responsibility: wrap one shared http.Client (timeout set); add retries with
// exponential backoff, a configurable User-Agent, context cancellation, and
// io.LimitReader to cap response size. Exposes a Fetcher interface so the
// crawler can be tested without real network access.
package fetcher

// TODO: define the Fetcher interface and an http.Client-backed implementation.
