package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

const base = "https://example.com/dir/page.html"

// sample exercises relative/absolute/non-web/fragment/duplicate links plus
// selector targets and a title.
const sample = `
<html>
<head><title>  Hello World  </title></head>
<body>
  <a href="/about">About</a>
  <a href="page2.html">Page2</a>
  <a href="https://other.com/x">Other</a>
  <a href="mailto:a@b.com">Mail</a>
  <a href="javascript:void(0)">JS</a>
  <a href="tel:+123">Tel</a>
  <a href="/about">Dup About</a>
  <a href="#section">Fragment</a>
  <a href="https://example.com/dir/page.html#top">Self with fragment</a>
  <a>missing href</a>
  <p class="price">  $9.99 </p>
  <h1>Heading</h1>
</body>
</html>`

func TestLinks(t *testing.T) {
	d, err := Parse([]byte(sample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got, err := d.Links(base)
	if err != nil {
		t.Fatalf("Links: %v", err)
	}

	want := []string{
		"https://example.com/about",          // /about resolved
		"https://example.com/dir/page2.html", // relative resolved against dir/
		"https://other.com/x",                // absolute external kept
		"https://example.com/dir/page.html",  // #section -> page itself, fragment stripped
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Links mismatch (-want +got):\n%s", diff)
	}
}

func TestLinksEmpty(t *testing.T) {
	d, err := Parse([]byte(`<html><body><p>no links</p></body></html>`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got, err := d.Links(base)
	if err != nil {
		t.Fatalf("Links: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no links, got %v", got)
	}
}

func TestLinksBadBase(t *testing.T) {
	d, err := Parse([]byte(sample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := d.Links("://not-a-url"); err == nil {
		t.Error("expected error for malformed base URL")
	}
}

func TestData(t *testing.T) {
	d, err := Parse([]byte(sample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := d.Data(map[string]string{
		"price":   ".price",
		"heading": "h1",
		"missing": ".does-not-exist",
	})
	want := map[string]string{
		"price":   "$9.99",
		"heading": "Heading",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Data mismatch (-want +got):\n%s", diff)
	}
}

func TestDataNoSelectors(t *testing.T) {
	d, err := Parse([]byte(sample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := d.Data(nil); got != nil {
		t.Errorf("expected nil for no selectors, got %v", got)
	}
}

func TestTitle(t *testing.T) {
	tests := []struct {
		name string
		html string
		want string
	}{
		{"present", sample, "Hello World"},
		{"absent", `<html><body>no title</body></html>`, ""},
		{"empty", `<html><head><title></title></head></html>`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Parse([]byte(tt.html))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := d.Title(); got != tt.want {
				t.Errorf("Title() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractWrappers(t *testing.T) {
	links, err := ExtractLinks(base, []byte(sample))
	if err != nil {
		t.Fatalf("ExtractLinks: %v", err)
	}
	if len(links) != 4 {
		t.Errorf("ExtractLinks returned %d links, want 4", len(links))
	}

	data, err := ExtractData([]byte(sample), map[string]string{"price": ".price"})
	if err != nil {
		t.Fatalf("ExtractData: %v", err)
	}
	if data["price"] != "$9.99" {
		t.Errorf("ExtractData price = %q, want %q", data["price"], "$9.99")
	}

	if got := ExtractTitle([]byte(sample)); got != "Hello World" {
		t.Errorf("ExtractTitle = %q, want %q", got, "Hello World")
	}
}

func TestExtractTitleUnparseableReturnsEmpty(t *testing.T) {
	// goquery tolerates junk, so this mainly asserts no panic and a safe "".
	if got := ExtractTitle([]byte("\x00\xff not really html")); got != "" {
		t.Errorf("ExtractTitle on junk = %q, want \"\"", got)
	}
}
