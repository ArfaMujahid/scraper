// Package web serves the embedded live dashboard and job API.
//
// Responsibility: an HTTP server (production timeouts) with routes to start a
// job, list the owner's jobs, and stream progress over SSE (GET /events); plus
// serving the embedded static files via http.FileServer over embed.FS.
package web

// TODO: server construction, routes, SSE /events broadcaster, go:embed static.
