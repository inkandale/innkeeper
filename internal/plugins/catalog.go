package plugins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// ErrSource means a plugin source couldn't be read, or what it served is wrong.
var ErrSource = errors.New("plugin source")

// Limits on what Innkeeper downloads from a source.
const (
	maxIndex = 1 << 20
	// ponytail: artifacts are read into memory; stream to disk if plugins outgrow this.
	maxArtifact = 256 << 20
)

// Entry is one plugin version in a source's index.toml. Unknown keys are
// ignored, so a newer index still reads.
type Entry struct {
	Name         string   `toml:"name" json:"name"`
	Version      string   `toml:"version" json:"version"`
	Protocol     int      `toml:"protocol" json:"protocol"`
	Capabilities []string `toml:"capabilities" json:"capabilities"`
	Title        string   `toml:"title" json:"title,omitempty"`
	Description  string   `toml:"description" json:"description"`
	// Category groups the plugin in the setup wizard, such as "Apps".
	Category string `toml:"category" json:"category,omitempty"`
	// Recommended marks a plugin the Plugins page suggests on a fresh install.
	Recommended bool `toml:"recommended" json:"recommended,omitempty"`
	// Released is the version's publish date, YYYY-MM-DD.
	Released  string     `toml:"released" json:"released,omitempty"`
	Artifacts []Artifact `toml:"artifact" json:"-"`
}

// Artifact is an Entry's gzipped tarball for one platform. URL may be
// relative to the index.
type Artifact struct {
	OS     string `toml:"os"`
	Arch   string `toml:"arch"`
	URL    string `toml:"url"`
	SHA256 string `toml:"sha256"`
}

// Index is a source's index.toml.
type Index struct {
	// Categories is the setup wizard's category order; unlisted ones follow alphabetically.
	Categories []string `toml:"categories"`
	Plugins    []Entry  `toml:"plugin"`
}

// Index reads the index.toml at rawURL, an https:// or file:// URL.
func (r *Registry) Index(ctx context.Context, rawURL string) (Index, error) {
	var idx Index
	u, err := url.Parse(rawURL)
	if err != nil {
		return idx, fmt.Errorf("%w: %v", ErrSource, err)
	}
	b, err := r.fetch(ctx, u, maxIndex)
	if err != nil {
		return idx, err
	}
	if err := toml.Unmarshal(b, &idx); err != nil {
		return Index{}, fmt.Errorf("%w: %s: %v", ErrSource, u.Redacted(), err)
	}
	return idx, nil
}

// fetch reads u, up to max bytes.
func (r *Registry) fetch(ctx context.Context, u *url.URL, max int64) ([]byte, error) {
	var body io.ReadCloser
	switch u.Scheme {
	case "https":
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSource, err)
		}
		resp, err := r.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSource, err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("%w: GET %s: %s", ErrSource, u.Redacted(), resp.Status)
		}
		body = resp.Body
	case "file":
		f, err := os.Open(u.Path)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSource, err)
		}
		body = f
	default:
		return nil, fmt.Errorf("%w: %s is not an https:// or file:// URL", ErrSource, u.Redacted())
	}
	defer body.Close()
	b, err := io.ReadAll(io.LimitReader(body, max+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSource, err)
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes", ErrSource, u.Redacted(), max)
	}
	return b, nil
}

// artifactURL resolves a's URL against its index's, which it must share a
// scheme with: an https index can't point at local files.
func artifactURL(index *url.URL, a Artifact) (*url.URL, error) {
	u, err := index.Parse(a.URL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSource, err)
	}
	if u.Scheme != index.Scheme {
		return nil, fmt.Errorf("%w: artifact %s must use %s:// like its index", ErrSource, u.Redacted(), index.Scheme)
	}
	return u, nil
}

// Newer reports whether version a is higher than b, comparing dot-separated
// numbers.
// ponytail: ignores anything after "-" or "+"; use x/mod/semver if a source needs pre-releases ordered.
func Newer(a, b string) bool {
	pa, pb := versionParts(a), versionParts(b)
	for i := range max(len(pa), len(pb)) {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func versionParts(v string) []int {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out []int
	for _, s := range strings.Split(v, ".") {
		n, _ := strconv.Atoi(s)
		out = append(out, n)
	}
	return out
}
