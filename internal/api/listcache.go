package api

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
)

// How long an instance's list, or its failure to list, is reused.
const (
	listTTL       = time.Minute
	listFailedTTL = 30 * time.Second
)

// errListing means a request is already asking the plugin for its list.
var errListing = errors.New("still listing what this instance offers")

// listMethod is a plugin method that lists what an instance offers, such as
// mcp.tools. items picks the list from the reply R.
type listMethod[R, T any] struct {
	name     string
	items    func(R) []T
	key      func(T) string
	validate func(T) error
}

type cachedList struct {
	version string
	at      time.Time
	items   any
	err     error
}

// instanceList returns an instance's valid items, each key once, from cache
// if its settings haven't changed in the last minute.
func instanceList[R, T any](ctx context.Context, s *Server, cache *sync.Map, m listMethod[R, T], id int64) (pluginhost.Instance, []T, error) {
	inst, err := s.Registry.Instance(ctx, id)
	if err != nil {
		return inst, nil, err
	}
	if c, ok := cache.Load(id); ok {
		c := c.(cachedList)
		ttl := listTTL
		if c.err != nil {
			ttl = listFailedTTL
		}
		if c.version == inst.Version && time.Since(c.at) < ttl {
			items, _ := c.items.([]T)
			return inst, items, c.err
		}
	}
	// Requests meanwhile skip the instance rather than wait for it.
	cache.Store(id, cachedList{inst.Version, time.Now(), nil, errListing})
	// Detached from ctx, so a caller giving up doesn't cache a failure.
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var res R
	err = s.Registry.Call(callCtx, s.Plugins, inst, m.name, nil, &res)
	var items []T
	seen := map[string]bool{}
	for _, it := range m.items(res) {
		if err := m.validate(it); err != nil || seen[m.key(it)] {
			s.Log.Warn("skip invalid item", "instance", id, "method", m.name, "item", m.key(it), "err", err)
			continue
		}
		seen[m.key(it)] = true
		items = append(items, it)
	}
	cache.Store(id, cachedList{inst.Version, time.Now(), items, err})
	return inst, items, err
}

// eachWorking runs f for each instance that isn't broken, in parallel, and
// returns when all finish or after 3 seconds. Slow ones finish in the
// background and fill the list caches.
func eachWorking(insts []plugins.InstanceInfo, f func(plugins.InstanceInfo)) {
	var wg sync.WaitGroup
	for _, i := range insts {
		if i.BrokenError == nil {
			wg.Go(func() { f(i) })
		}
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
}

// collect gathers each working instance's list from m, turned into out items
// by f, for the instances that answer within eachWorking's limit.
func collect[R, T, O any](ctx context.Context, s *Server, cache *sync.Map, m listMethod[R, T], insts []plugins.InstanceInfo,
	f func(plugins.InstanceInfo, []T) []O) []O {
	var mu sync.Mutex
	out := []O{}
	eachWorking(insts, func(i plugins.InstanceInfo) {
		_, list, err := instanceList(ctx, s, cache, m, i.ID)
		if err != nil {
			s.Log.Warn("list from plugin", "instance", i.ID, "method", m.name, "err", err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		out = append(out, f(i, list)...)
	})
	// Instances still listing append later; the caller gets what is here now.
	mu.Lock()
	defer mu.Unlock()
	return slices.Clone(out)
}
