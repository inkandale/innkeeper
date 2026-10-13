package library

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

// Source is a site an item was published on.
type Source struct {
	ID         int64     `json:"id"`
	Kind       string    `json:"kind"`
	Ref        string    `json:"ref"`
	ExternalID string    `json:"external_id,omitempty"`
	URL        string    `json:"url,omitempty"`
	LinkedBy   string    `json:"linked_by"`
	CreatedAt  time.Time `json:"created_at"`
}

type detector struct {
	kind     string
	fromData func(data map[string]any, lorebook bool) (Source, bool)
	fromURL  func(u *url.URL) (Source, bool)
	fromRef  func(ref string, lorebook bool) (Source, bool)
}

var detectors = []detector{chub, risu, characterTavern}

var chub = detector{
	kind: "chub",
	fromData: func(data map[string]any, lorebook bool) (Source, bool) {
		ext, _ := data["extensions"].(map[string]any)
		c, _ := ext["chub"].(map[string]any)
		path, _ := c["full_path"].(string)
		s, ok := chubSource(chubDir(lorebook), path)
		if !ok {
			return Source{}, false
		}
		// UseNumber keeps big IDs out of float formatting.
		switch id := c["id"].(type) {
		case json.Number:
			s.ExternalID = id.String()
		case string:
			s.ExternalID = id
		}
		return s, true
	},
	fromRef: func(ref string, lorebook bool) (Source, bool) { return chubSource(chubDir(lorebook), ref) },
	fromURL: func(u *url.URL) (Source, bool) {
		if !webURL(u, "chub.ai", "www.chub.ai", "characterhub.org", "www.characterhub.org", "venus.chub.ai") {
			return Source{}, false
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		switch {
		case len(parts) == 3 && (parts[0] == "characters" || parts[0] == "lorebooks"):
			return chubSource(parts[0], parts[1]+"/"+parts[2])
		case len(parts) == 2 && parts[0] != "characters" && parts[0] != "lorebooks":
			// chub.ai/{user}/{slug} is a character's page.
			return chubSource("characters", parts[0]+"/"+parts[1])
		}
		return Source{}, false
	},
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// noData is fromData for a site whose cards name it only in data.source.
func noData(map[string]any, bool) (Source, bool) { return Source{}, false }

// risu is RisuRealm. Its ref is the card's UUID; it has no lorebook pages.
var risu = detector{
	kind:     "risu",
	fromData: noData,
	fromRef:  risuSource,
	fromURL: func(u *url.URL) (Source, bool) {
		// A RisuRealm card's own data.source says risurealm:{uuid}.
		if u.Scheme == "risurealm" {
			return risuSource(u.Opaque, false)
		}
		if !webURL(u, "realm.risuai.net") {
			return Source{}, false
		}
		id, ok := strings.CutPrefix(strings.TrimSuffix(u.Path, "/"), "/character/")
		if !ok {
			return Source{}, false
		}
		return risuSource(id, false)
	},
}

func risuSource(ref string, lorebook bool) (Source, bool) {
	ref = strings.ToLower(ref)
	if lorebook || !uuidPattern.MatchString(ref) {
		return Source{}, false
	}
	return Source{Kind: "risu", Ref: ref, URL: "https://realm.risuai.net/character/" + ref}, true
}

// characterTavern is Character Tavern. Its ref is <author>/<slug>; it has no lorebook pages.
var characterTavern = detector{
	kind:     "character-tavern",
	fromData: noData,
	fromRef:  tavernSource,
	fromURL: func(u *url.URL) (Source, bool) {
		if !webURL(u, "character-tavern.com") {
			return Source{}, false
		}
		ref, ok := strings.CutPrefix(strings.Trim(u.Path, "/"), "character/")
		if !ok {
			return Source{}, false
		}
		return tavernSource(ref, false)
	},
}

func tavernSource(ref string, lorebook bool) (Source, bool) {
	esc, ok := pathRef(ref)
	if lorebook || !ok {
		return Source{}, false
	}
	return Source{Kind: "character-tavern", Ref: ref, URL: "https://character-tavern.com/character/" + esc}, true
}

// pathRef validates ref as exactly <a>/<b> and returns it path-escaped, so the stored URL is clean.
func pathRef(ref string) (string, bool) {
	parts := strings.Split(ref, "/")
	if len(parts) != 2 {
		return "", false
	}
	for i, p := range parts {
		if p == "" || p == "." || p == ".." || strings.ContainsAny(p, "?#\\") || strings.IndexFunc(p, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			return "", false
		}
		parts[i] = url.PathEscape(p)
	}
	return parts[0] + "/" + parts[1], true
}

func chubSource(dir, ref string) (Source, bool) {
	esc, ok := pathRef(ref)
	if !ok {
		return Source{}, false
	}
	return Source{Kind: "chub", Ref: ref, URL: "https://chub.ai/" + dir + "/" + esc}, true
}

// webURL reports whether u is an http or https URL on one of hosts.
func webURL(u *url.URL, hosts ...string) bool {
	return (u.Scheme == "http" || u.Scheme == "https") &&
		slices.ContainsFunc(hosts, func(h string) bool { return strings.EqualFold(u.Hostname(), h) })
}

func chubDir(lorebook bool) string {
	if lorebook {
		return "lorebooks"
	}
	return "characters"
}

// Detect returns the sources a card's or lorebook's data records.
func Detect(data json.RawMessage, lorebook bool) []Source {
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if dec.Decode(&m) != nil {
		return nil
	}
	var out []Source
	add := func(s Source) {
		// The first wins, so an ID read from the data isn't lost to the same page's URL.
		if !slices.ContainsFunc(out, func(o Source) bool { return o.Kind == s.Kind && o.Ref == s.Ref }) {
			out = append(out, s)
		}
	}
	for _, d := range detectors {
		if s, ok := d.fromData(m, lorebook); ok {
			add(s)
		}
	}
	// data.source lists the card's pages, such as the one url-import read it from.
	list, _ := m["source"].([]any)
	for _, v := range list {
		if raw, ok := v.(string); ok {
			if s, err := ParseSourceURL(raw); err == nil {
				add(s)
			}
		}
	}
	return out
}

// ParseSourceURL turns a site URL into a source.
func ParseSourceURL(raw string) (Source, error) {
	if u, err := url.Parse(strings.TrimSpace(raw)); err == nil {
		for _, d := range detectors {
			if s, ok := d.fromURL(u); ok {
				return s, nil
			}
		}
	}
	return Source{}, fmt.Errorf("%w: no known site matches that URL", ErrInvalid)
}

// ValidateSource checks a kind and ref the same way detection and URL parsing
// do, and returns the source with its canonical URL. ExternalID is kept.
func ValidateSource(src Source, lorebook bool) (Source, error) {
	for _, d := range detectors {
		if d.kind != src.Kind {
			continue
		}
		if got, ok := d.fromRef(src.Ref, lorebook); ok {
			got.ExternalID = src.ExternalID
			return got, nil
		}
		return Source{}, fmt.Errorf("%w: %q is not a valid %s ref", ErrInvalid, src.Ref, src.Kind)
	}
	return Source{}, fmt.Errorf("%w: unknown source kind %q", ErrInvalid, src.Kind)
}
