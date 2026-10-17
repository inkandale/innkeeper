package api

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
)

// How long an instance's list, or its failure to list, is reused.
const (
	listTTL       = time.Minute
	listFailedTTL = 30 * time.Second
)

// errListing means a request is already asking the plugin for its list.
var errListing = errors.New("still listing what this instance offers")

// listMethod is a plugin method that lists what an instance offers, such as
// mcp.tools. clean turns the reply R into what callers use, V, calling skip
// for each item it leaves out.
type listMethod[R, V any] struct {
	name  string
	clean func(r R, skip func(item string, err error)) V
}

// valid keeps the items that pass validate, each key once, and skips the rest.
func valid[T any](list []T, key func(T) string, validate func(T) error, skip func(string, error)) []T {
	var out []T
	seen := map[string]bool{}
	for _, it := range list {
		if err := validate(it); err != nil || seen[key(it)] {
			skip(key(it), err)
			continue
		}
		seen[key(it)] = true
		out = append(out, it)
	}
	return out
}

type cachedList struct {
	version string
	at      time.Time
	value   any
	err     error
}

// instanceList returns an instance's cleaned list, from cache if its settings
// haven't changed in the last minute.
func instanceList[R, V any](ctx context.Context, s *Server, cache *sync.Map, m listMethod[R, V], id int64) (pluginhost.Instance, V, error) {
	var value V
	inst, err := s.Registry.Instance(ctx, id)
	if err != nil {
		return inst, value, err
	}
	if c, ok := cache.Load(id); ok {
		c := c.(cachedList)
		ttl := listTTL
		if c.err != nil {
			ttl = listFailedTTL
		}
		if c.version == inst.Version && time.Since(c.at) < ttl {
			value, _ = c.value.(V)
			return inst, value, c.err
		}
	}
	// Requests meanwhile skip the instance rather than wait for it.
	cache.Store(id, cachedList{inst.Version, time.Now(), nil, errListing})
	// Detached from ctx, so a caller giving up doesn't cache a failure.
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var res R
	err = s.Registry.Call(callCtx, s.Plugins, inst, m.name, nil, &res)
	value = m.clean(res, func(item string, err error) {
		s.Log.Warn("skip invalid item", "instance", id, "method", m.name, "item", item, "err", err)
	})
	cache.Store(id, cachedList{inst.Version, time.Now(), value, err})
	return inst, value, err
}

// eachWorking runs f for each usable instance, in parallel, and
// returns when all finish or after 3 seconds. Slow ones finish in the
// background and fill the list caches.
func eachWorking(insts []plugins.InstanceInfo, f func(plugins.InstanceInfo)) {
	var wg sync.WaitGroup
	for _, i := range insts {
		if i.Usable() {
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
func collect[R, V, O any](ctx context.Context, s *Server, cache *sync.Map, m listMethod[R, V], insts []plugins.InstanceInfo,
	f func(plugins.InstanceInfo, V) []O) []O {
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
