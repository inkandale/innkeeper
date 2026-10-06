package library

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
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
}

var detectors = []detector{chub}

var chub = detector{
	kind: "chub",
	fromData: func(data map[string]any, lorebook bool) (Source, bool) {
		ext, _ := data["extensions"].(map[string]any)
		c, _ := ext["chub"].(map[string]any)
		path, _ := c["full_path"].(string)
		if path == "" {
			return Source{}, false
		}
		s := Source{Kind: "chub", Ref: path, URL: "https://chub.ai/" + chubDir(lorebook) + "/" + path}
		// UseNumber keeps big IDs out of float formatting.
		if n, ok := c["id"].(json.Number); ok {
			s.ExternalID = n.String()
		}
		return s, true
	},
	fromURL: func(u *url.URL) (Source, bool) {
		if u.Host != "chub.ai" && u.Host != "www.chub.ai" {
			return Source{}, false
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) != 3 || (parts[0] != "characters" && parts[0] != "lorebooks") || parts[1] == "" || parts[2] == "" {
			return Source{}, false
		}
		ref := parts[1] + "/" + parts[2]
		return Source{Kind: "chub", Ref: ref, URL: "https://chub.ai/" + parts[0] + "/" + ref}, true
	},
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
