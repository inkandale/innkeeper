package api

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/inkandale/innkeeper/internal/library"
)

const (
	// facetSampleOver is how many matches a sidebar counts exactly; past it, a
	// sample answers first.
	facetSampleOver = 20_000
	// facetFresh is how long cached counts go without a recount.
	facetFresh = time.Minute
	// facetKeep is how long unread counts stay cached.
	facetKeep = 10 * time.Minute
	// facetRecount bounds a background recount.
	facetRecount = 5 * time.Minute
)

// facetCache holds exact facet counts of big searches, so a sidebar gets
// them at once instead of waiting on a recount.
// ponytail: in this process only; move it to Redis if the API runs more than one replica.
type facetCache struct {
	mu       sync.Mutex
	entries  map[string]facetEntry
	counting map[string]bool
	// sampleOver overrides facetSampleOver, for tests.
	sampleOver int
}

type facetEntry struct {
	facets  *library.Facets
	counted time.Time
}

// facetKey names q's counts; ScoreInstance isn't in q's JSON but changes them.
func facetKey(k *library.Kind, q library.Query) string {
	body, _ := json.Marshal(struct {
		Kind  string
		Score int64
		Query library.Query
	}{k.Name, q.ScoreInstance, q})
	return string(body)
}

// facetSearch answers a facets-only search: with cached exact counts when it
// has them, recounting stale ones in the background, and otherwise with
// counts over a sample when the match is big, counting exactly in the
// background for the next request.
func (s *Server) facetSearch(ctx context.Context, k *library.Kind, q library.Query) (library.Results, error) {
	key := facetKey(k, q)
	if f, fresh, ok := s.facets.get(key); ok {
		if !fresh {
			s.recountFacets(key, k, q)
		}
		return library.Results{Hits: []library.Hit{}, Facets: f}, nil
	}
	sampled := q
	sampled.SampleOver = facetSampleOver
	if s.facets.sampleOver > 0 {
		sampled.SampleOver = s.facets.sampleOver
	}
	res, err := s.Lib.Search(ctx, k, sampled)
	if err == nil && res.Facets != nil && res.Facets.Approximate {
		s.recountFacets(key, k, q)
	}
	return res, err
}

// recountFacets counts q's facets exactly and caches them, unless a count
// for key is already running.
func (s *Server) recountFacets(key string, k *library.Kind, q library.Query) {
	if !s.facets.start(key) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), facetRecount)
		defer cancel()
		res, err := s.Lib.Search(ctx, k, q)
		if err != nil {
			s.Log.Warn("recount facets", "err", err)
		}
		s.facets.finish(key, res.Facets, err == nil)
	}()
}

func (c *facetCache) get(key string) (f *library.Facets, fresh, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	return e.facets, time.Since(e.counted) < facetFresh, ok
}

// start marks key as counting, and reports false when it already was.
func (c *facetCache) start(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counting[key] {
		return false
	}
	if c.counting == nil {
		c.counting = map[string]bool{}
	}
	c.counting[key] = true
	return true
}

// finish ends key's count, caching f when the count succeeded, and drops
// counts nobody has refreshed in facetKeep.
func (c *facetCache) finish(key string, f *library.Facets, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.counting, key)
	if c.entries == nil {
		c.entries = map[string]facetEntry{}
	}
	if ok {
		c.entries[key] = facetEntry{f, time.Now()}
	}
	for k, e := range c.entries {
		if time.Since(e.counted) > facetKeep {
			delete(c.entries, k)
		}
	}
}
