// Package parser turns fetched HTML into extracted data: the links to crawl
// next and the CSS-selector fields the user asked for. It is pure (bytes in,
// values out) and does no I/O, so it is fully testable on fixture HTML.
//
// Parse once into a Document, then extract many times — the crawler pulls
// links, data, and title from the same Document so each page is parsed only
// once (LLD §4: "don't re-parse"). The ExtractX functions are convenience
// wrappers for callers that need a single extraction.
package parser

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Document is a parsed HTML page ready for repeated extraction.
type Document struct {
	doc *goquery.Document
}

// Parse parses HTML bytes into a Document. It rarely errors: goquery is lenient
// with malformed markup, so an error means the bytes could not be read at all.
func Parse(body []byte) (*Document, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parsing HTML: %w", err)
	}
	return &Document{doc: doc}, nil
}

// Links returns the de-duplicated absolute http(s) URLs found in <a href>
// elements, each resolved against base (the URL the page was fetched from).
// Fragments are stripped so "#section" links don't cause duplicate fetches, and
// non-web schemes (mailto:, javascript:, tel:, …) are dropped.
func (d *Document) Links(base string) ([]string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("parsing base URL %q: %w", base, err)
	}

	var links []string
	seen := make(map[string]struct{})
	d.doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, ok := s.Attr("href")
		if !ok {
			return
		}
		ref, err := url.Parse(strings.TrimSpace(href))
		if err != nil {
			return // skip a single malformed href, don't fail the whole page
		}
		abs := baseURL.ResolveReference(ref)
		if abs.Scheme != "http" && abs.Scheme != "https" {
			return
		}
		abs.Fragment = ""
		u := abs.String()
		if _, dup := seen[u]; dup {
			return
		}
		seen[u] = struct{}{}
		links = append(links, u)
	})
	return links, nil
}

// Data returns the trimmed text of the first match for each named CSS selector.
// Selectors that match nothing are omitted; nil is returned when none are asked
// for.
func (d *Document) Data(selectors map[string]string) map[string]string {
	if len(selectors) == 0 {
		return nil
	}
	out := make(map[string]string, len(selectors))
	for name, sel := range selectors {
		match := d.doc.Find(sel)
		if match.Length() == 0 {
			continue
		}
		out[name] = strings.TrimSpace(match.First().Text())
	}
	return out
}

// Title returns the page's <title> text trimmed of surrounding whitespace, or
// "" if absent.
func (d *Document) Title() string {
	return strings.TrimSpace(d.doc.Find("title").First().Text())
}

// ExtractLinks parses body and returns the absolute http(s) links resolved
// against base. Convenience wrapper over Parse + Document.Links.
func ExtractLinks(base string, body []byte) ([]string, error) {
	d, err := Parse(body)
	if err != nil {
		return nil, err
	}
	return d.Links(base)
}

// ExtractData parses body and returns the CSS-selector matches. Convenience
// wrapper over Parse + Document.Data.
func ExtractData(body []byte, selectors map[string]string) (map[string]string, error) {
	d, err := Parse(body)
	if err != nil {
		return nil, err
	}
	return d.Data(selectors), nil
}

// ExtractTitle parses body and returns the page title, or "" if the bytes can't
// be parsed or the page has no title.
func ExtractTitle(body []byte) string {
	d, err := Parse(body)
	if err != nil {
		return ""
	}
	return d.Title()
}
