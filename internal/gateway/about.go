package gateway

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// The GUI's About page: the running build and the changelog. main embeds
// CHANGELOG.md into the binary and hands it over with the build stamp
// (SetAbout), so the page always shows the notes of exactly the code that is
// running — no network fetch, no second copy to keep in sync.

const (
	projectName       = "Nano LLM Proxy"
	projectRepository = "https://github.com/kmmuntasir/nano-llm-proxy"
	projectLicense    = "GPL-3.0"
)

// releaseSection is one "### Added"-style block of a release.
type releaseSection struct {
	Title string   `json:"title"`
	Items []string `json:"items"` // inline markdown, unwrapped
}

// releaseNotes is one "## [x.y.z] — date" entry of the changelog.
type releaseNotes struct {
	Version  string           `json:"version"` // "Unreleased" or "0.1.1"
	Date     string           `json:"date"`    // "" for Unreleased
	URL      string           `json:"url"`     // compare/tag link from the reference list
	Intro    string           `json:"intro"`   // paragraphs before the first section, "\n\n"-separated
	Sections []releaseSection `json:"sections"`
}

// aboutInfo is the GET /api/about payload; built once at startup.
type aboutInfo struct {
	Name           string         `json:"name"`
	Version        string         `json:"version"`
	DisplayVersion string         `json:"displayVersion"`
	BaseVersion    string         `json:"baseVersion"`
	CommitsAhead   int            `json:"commitsAhead"`
	Commit         string         `json:"commit"`
	BuildDate      string         `json:"buildDate"`
	Repository     string         `json:"repository"`
	License        string         `json:"license"`
	Releases       []releaseNotes `json:"releases"`
}

// SetAbout records the build stamp and the embedded changelog. Called once
// by main before serving; the result is read-only afterwards.
func (g *gateway) SetAbout(version, commit, date, changelog string) {
	display, base, ahead := describeVersion(version)
	if commit == "none" {
		commit = ""
	}
	if date == "unknown" {
		date = ""
	}
	g.about = &aboutInfo{
		Name: projectName, Version: version, DisplayVersion: display,
		BaseVersion: base, CommitsAhead: ahead, Commit: commit, BuildDate: date,
		Repository: projectRepository, License: projectLicense,
		Releases: parseChangelog(changelog),
	}
}

// handleAbout serves GET /api/about.
func (g *gateway) handleAbout(w http.ResponseWriter, r *http.Request) {
	about := g.about
	if about == nil { // store-less tests / SetAbout never called
		about = &aboutInfo{Name: projectName, Version: "dev", DisplayVersion: "dev",
			Repository: projectRepository, License: projectLicense, Releases: []releaseNotes{}}
	}
	writeJSON(w, http.StatusOK, about)
}

var describeRe = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)(?:-(\d+)-g[0-9a-f]+)?(?:-dirty)?$`)

// describeVersion turns a `git describe` stamp into a compact label, the
// release it is based on and how many commits ahead it is:
// "v0.1.1-48-g9067e49" → ("v0.1.1+48", "0.1.1", 48); "v0.1.1" → ("v0.1.1",
// "0.1.1", 0). Anything else (a "dev" build) is shown as-is.
func describeVersion(v string) (display, base string, ahead int) {
	m := describeRe.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return v, "", 0
	}
	base = m[1]
	if m[2] != "" {
		ahead, _ = strconv.Atoi(m[2])
	}
	display = "v" + base
	if ahead > 0 {
		display += "+" + strconv.Itoa(ahead)
	}
	return display, base, ahead
}

var (
	releaseHeadRe = regexp.MustCompile(`^## \[([^\]]+)\](?:\s*[—–-]\s*(\S.*))?$`)
	linkRefRe     = regexp.MustCompile(`^\[([^\]]+)\]:\s*(\S+)\s*$`)
)

// parseChangelog reads a Keep-a-Changelog document into releases, newest
// first (document order). It understands exactly what CHANGELOG.md uses:
// "## [version] — date" heads, "### Section" heads, intro paragraphs, and
// flat "- " bullets whose wrapped continuation lines are joined. Text before
// the first release and the trailing link references (which become each
// release's URL) are not content.
func parseChangelog(md string) []releaseNotes {
	urls := map[string]string{}
	for _, line := range strings.Split(md, "\n") {
		if m := linkRefRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			urls[m[1]] = m[2]
		}
	}
	releases := []releaseNotes{}
	var cur *releaseNotes
	var sec *releaseSection
	var para []string // intro paragraph being collected
	var item *string  // bullet being collected
	flushPara := func() {
		if cur != nil && len(para) > 0 {
			if cur.Intro != "" {
				cur.Intro += "\n\n"
			}
			cur.Intro += strings.Join(para, " ")
		}
		para = nil
	}
	flushRelease := func() {
		flushPara()
		item = nil
		if cur != nil {
			releases = append(releases, *cur)
		}
	}
	for _, raw := range strings.Split(md, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)
		if m := releaseHeadRe.FindStringSubmatch(line); m != nil {
			flushRelease()
			cur = &releaseNotes{Version: m[1], Date: strings.TrimSpace(m[2]), URL: urls[m[1]], Sections: []releaseSection{}}
			sec = nil
			continue
		}
		if cur == nil || linkRefRe.MatchString(trimmed) {
			continue
		}
		switch {
		case strings.HasPrefix(line, "### "):
			flushPara()
			cur.Sections = append(cur.Sections, releaseSection{Title: strings.TrimSpace(line[4:]), Items: []string{}})
			sec = &cur.Sections[len(cur.Sections)-1]
			item = nil
		case strings.HasPrefix(line, "- ") && sec != nil:
			sec.Items = append(sec.Items, strings.TrimSpace(line[2:]))
			item = &sec.Items[len(sec.Items)-1]
		case trimmed == "":
			flushPara()
			item = nil
		case item != nil && strings.HasPrefix(line, " "):
			*item += " " + trimmed // wrapped continuation of the bullet
		case sec == nil:
			para = append(para, trimmed)
		default:
			// prose inside a section (rare): keep it as its own item
			sec.Items = append(sec.Items, trimmed)
			item = &sec.Items[len(sec.Items)-1]
		}
	}
	flushRelease()
	return releases
}
