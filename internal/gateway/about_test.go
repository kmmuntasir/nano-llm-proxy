package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestDescribeVersion(t *testing.T) {
	cases := []struct {
		in, display, base string
		ahead             int
	}{
		{"v0.1.1-48-g9067e49", "v0.1.1+48", "0.1.1", 48},
		{"v0.1.1", "v0.1.1", "0.1.1", 0},
		{"v0.2.0-3-gabc1234-dirty", "v0.2.0+3", "0.2.0", 3},
		{"0.1.0", "v0.1.0", "0.1.0", 0},
		{"dev", "dev", "", 0},
		{"9067e49", "9067e49", "", 0},
	}
	for _, c := range cases {
		d, b, a := describeVersion(c.in)
		if d != c.display || b != c.base || a != c.ahead {
			t.Errorf("describeVersion(%q) = %q %q %d, want %q %q %d", c.in, d, b, a, c.display, c.base, c.ahead)
		}
	}
}

func TestParseChangelogStructure(t *testing.T) {
	md := `# Changelog

Preamble text that is not a release.

## [Unreleased]

### Added

- First item with ` + "`code`" + ` that wraps
  onto a second line.
- Second item.

### Fixed

- A fix.

## [0.1.0] — 2026-09-27

First public release.
Still the first paragraph.

Second paragraph.

### Security

- Keys are hashed.

[Unreleased]: https://example.com/compare/v0.1.0...HEAD
[0.1.0]: https://example.com/releases/tag/v0.1.0
`
	got := parseChangelog(md)
	if len(got) != 2 {
		t.Fatalf("releases = %d, want 2: %+v", len(got), got)
	}
	u := got[0]
	if u.Version != "Unreleased" || u.Date != "" || u.URL != "https://example.com/compare/v0.1.0...HEAD" || u.Intro != "" {
		t.Errorf("unreleased head wrong: %+v", u)
	}
	if len(u.Sections) != 2 || u.Sections[0].Title != "Added" || len(u.Sections[0].Items) != 2 ||
		u.Sections[0].Items[0] != "First item with `code` that wraps onto a second line." || u.Sections[1].Items[0] != "A fix." {
		t.Errorf("unreleased sections wrong: %+v", u.Sections)
	}
	r := got[1]
	if r.Version != "0.1.0" || r.Date != "2026-09-27" || r.URL != "https://example.com/releases/tag/v0.1.0" ||
		r.Intro != "First public release. Still the first paragraph.\n\nSecond paragraph." {
		t.Errorf("0.1.0 head wrong: %+v", r)
	}
	if len(r.Sections) != 1 || r.Sections[0].Title != "Security" || r.Sections[0].Items[0] != "Keys are hashed." {
		t.Errorf("0.1.0 sections wrong: %+v", r.Sections)
	}
	// an empty Unreleased right after a release is skipped
	empty := parseChangelog("## [Unreleased]\n\n## [0.2.0] — 2026-10-14\n\n### Added\n\n- x\n")
	if len(empty) != 1 || empty[0].Version != "0.2.0" {
		t.Errorf("empty Unreleased not skipped: %+v", empty)
	}
	if out := parseChangelog(""); out == nil || len(out) != 0 {
		t.Errorf("empty changelog should give an empty, non-nil list: %#v", out)
	}
}

// TestParseRealChangelog keeps CHANGELOG.md parseable: a format change that
// would silently empty the About page fails here.
func TestParseRealChangelog(t *testing.T) {
	raw, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	rels := parseChangelog(string(raw))
	if len(rels) < 3 {
		t.Fatalf("releases = %d", len(rels))
	}
	for _, r := range rels {
		if r.Version != "Unreleased" && r.Date == "" {
			t.Errorf("release %s has no date", r.Version)
		}
		if r.URL == "" {
			t.Errorf("release %s has no link reference", r.Version)
		}
		if len(r.Sections) == 0 {
			t.Errorf("release %s has no sections", r.Version)
		}
		for _, s := range r.Sections {
			for _, it := range s.Items {
				if it == "" || strings.Contains(it, "\n") || strings.HasPrefix(it, "- ") {
					t.Errorf("release %s / %s: malformed item %q", r.Version, s.Title, it)
				}
			}
		}
	}
}

func TestHandleAbout(t *testing.T) {
	g, _ := testStoreGateway(t, "http://127.0.0.1:1")
	g.SetAbout("v0.1.1-48-g9067e49", "9067e49", "2026-10-14T18:33:13Z", "## [0.1.1] — 2026-09-27\n\n### Fixed\n\n- x\n")
	cookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	req := httptest.NewRequest("GET", "/api/about", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	g.requireSession(g.handleAbout)(rec, req)
	var got aboutInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s %v", rec.Code, rec.Body.String(), err)
	}
	if got.DisplayVersion != "v0.1.1+48" || got.BaseVersion != "0.1.1" || got.CommitsAhead != 48 ||
		got.Repository != projectRepository || len(got.Releases) != 1 || got.Releases[0].Sections[0].Items[0] != "x" {
		t.Fatalf("about = %+v", got)
	}
	// unauthenticated callers get nothing
	rec = httptest.NewRecorder()
	g.requireSession(g.handleAbout)(rec, httptest.NewRequest("GET", "/api/about", nil))
	if rec.Code != 401 {
		t.Fatalf("anonymous: %d", rec.Code)
	}
	// "none"/"unknown" build stamps (plain go build) become empty fields
	g.SetAbout("dev", "none", "unknown", "")
	if g.about.Commit != "" || g.about.BuildDate != "" || g.about.DisplayVersion != "dev" {
		t.Fatalf("dev stamp = %+v", g.about)
	}
}
