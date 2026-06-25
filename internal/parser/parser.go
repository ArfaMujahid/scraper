// Package parser extracts structured data from HTML.
//
// Responsibility: pure functions that turn HTML into (a) extracted links for
// crawling and (b) CSS-selector matches for scraping. No I/O, so it is trivially
// testable.
package parser

// TODO: link extraction + CSS-selector extraction (goquery).
