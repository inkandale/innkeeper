package library

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
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

var detectors = []detector{chub}

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
		if (u.Scheme != "http" && u.Scheme != "https") || (!strings.EqualFold(u.Hostname(), "chub.ai") && !strings.EqualFold(u.Hostname(), "www.chub.ai")) {
			return Source{}, false
		}
		dir, ref, ok := strings.Cut(strings.Trim(u.Path, "/"), "/")
		if !ok || (dir != "characters" && dir != "lorebooks") {
			return Source{}, false
		}
		return chubSource(dir, ref)
	},
}

// chubSource validates ref as exactly <user>/<name> so the stored URL is clean.
func chubSource(dir, ref string) (Source, bool) {
	parts := strings.Split(ref, "/")
	if len(parts) != 2 {
		return Source{}, false
	}
	for i, p := range parts {
		if p == "" || p == "." || p == ".." || strings.ContainsAny(p, "?#\\") || strings.IndexFunc(p, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			return Source{}, false
		}
		parts[i] = url.PathEscape(p)
	}
	return Source{Kind: "chub", Ref: ref, URL: "https://chub.ai/" + dir + "/" + parts[0] + "/" + parts[1]}, true
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
	for _, d := range detectors {
		if s, ok := d.fromData(m, lorebook); ok {
			out = append(out, s)
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
