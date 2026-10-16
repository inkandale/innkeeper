package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
)

// The sites capability reads which sites a card or lorebook was published on.
// Both methods only parse: they answer quickly and contact no service.
const (
	MethodSitesDetect = "sites.detect"
	MethodSitesParse  = "sites.parse"
)

// MaxSources is the most sources one sites.detect reply may hold.
const MaxSources = 32

var sourceKindPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Source is a site's page for an item.
type Source struct {
	// Kind names the site. Stored rows and search filters use it, so a plugin never renames one.
	Kind string `json:"kind"`
	// Ref is the site's ID for the page.
	Ref        string `json:"ref"`
	ExternalID string `json:"external_id,omitempty"`
	URL        string `json:"url"`
}

// Validate reports every problem with the source at once.
func (s Source) Validate() error {
	var errs []error
	if !sourceKindPattern.MatchString(s.Kind) {
		errs = append(errs, fmt.Errorf("kind %q must match %s", s.Kind, sourceKindPattern))
	}
	if s.Ref == "" {
		errs = append(errs, errors.New("ref is empty"))
	}
	if len(s.Ref) > 512 {
		errs = append(errs, errors.New("ref is longer than 512 bytes"))
	}
	if len(s.ExternalID) > 128 {
		errs = append(errs, errors.New("external_id is longer than 128 bytes"))
	}
	if u, err := url.Parse(s.URL); err != nil || u.Scheme != "https" || u.Host == "" {
		errs = append(errs, fmt.Errorf("url %q is not an https URL", s.URL))
	}
	if len(s.URL) > 2048 {
		errs = append(errs, errors.New("url is longer than 2048 bytes"))
	}
	return errors.Join(errs...)
}

// SitesDetectParams sends an item's data: a card's CCv3 data object, or a
// lorebook's character_book. Entity is KindCard or KindLorebook.
type SitesDetectParams struct {
	Entity string          `json:"entity"`
	Data   json.RawMessage `json:"data"`
}

// SitesDetectResult is the sources the data names that fit the entity.
type SitesDetectResult struct {
	Sources []Source `json:"sources"`
}

// SitesParseParams sends what the owner typed: URL, or Kind and Ref.
type SitesParseParams struct {
	Entity string `json:"entity"`
	URL    string `json:"url,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Ref    string `json:"ref,omitempty"`
}

// SitesParseResult is the source with its canonical URL, or nil when the URL
// or kind isn't the plugin's.
type SitesParseResult struct {
	Source *Source `json:"source"`
}
