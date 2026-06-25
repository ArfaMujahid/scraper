// Command scraper is the entry point and composition root for the concurrent
// web scraper.
//
// Responsibility: parse flags -> build config -> construct
// fetcher/parser/limiter/registry/crawler/cleanup -> set up
// signal.NotifyContext -> run (a headless job or the UI server). Thin
// orchestration only.
package main

func main() {
	// TODO: parse flags, build & validate config, wire dependencies,
	// set up graceful shutdown, then run headless mode or the UI server.
}
