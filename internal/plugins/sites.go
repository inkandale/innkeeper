package plugins

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/protocol"
)

// sitesTimeout bounds each sites call, which only parses. A variable so tests can shorten it.
var sitesTimeout = 5 * time.Second

// ErrNoSites means no working instance declares sites, so nothing can be detected yet.
var ErrNoSites = errors.New("no working sites plugin")

// ErrBadSource means a sites plugin sent a source that fails protocol.Source.Validate.
var ErrBadSource = errors.New("sites plugin sent an invalid source")

// SitesInstances returns the oldest working instance of each plugin that
// declares sites, ordered by that instance's ID.
func (r *Registry) SitesInstances(ctx context.Context) ([]pluginhost.Instance, error) {
	list, err := r.InstancesWith(ctx, "sites")
	if err != nil {
		return nil, err
	}
	var out []pluginhost.Instance
	seen := map[string]bool{}
	for _, i := range list {
		if i.BrokenError != nil || seen[i.Plugin] {
			continue
		}
		inst, err := r.Instance(ctx, i.ID)
		// Broken or deleted since the list was read.
		if errors.Is(err, ErrBroken) || errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		seen[i.Plugin] = true
		out = append(out, inst)
	}
	return out, nil
}

func (r *Registry) sitesCall(ctx context.Context, mgr *pluginhost.Manager, inst pluginhost.Instance, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(ctx, sitesTimeout)
	defer cancel()
	return r.Call(ctx, mgr, inst, method, params, result)
}

func fromProtocol(s protocol.Source) library.Source {
	return library.Source{Kind: s.Kind, Ref: s.Ref, ExternalID: s.ExternalID, URL: s.URL}
}

// DetectSources returns the sources an entity's data names, asking every
// sites plugin and keeping the first of two with the same kind and ref. With
// no sites plugin the error is ErrNoSites.
//
// Detection never fails the caller's work: a plugin that fails is logged and
// skipped, and invalid sources are dropped. The error is non-nil when ctx
// ended, or when a plugin failed in a way a later call may not; the sources
// are then the other plugins'.
func (r *Registry) DetectSources(ctx context.Context, mgr *pluginhost.Manager, k *library.Kind, data json.RawMessage) ([]library.Source, error) {
	insts, err := r.SitesInstances(ctx)
	if err != nil {
		return nil, err
	}
	if len(insts) == 0 {
		return nil, ErrNoSites
	}
	var out []library.Source
	var retry error
	for _, inst := range insts {
		plugin := inst.Manifest.Name
		var res protocol.SitesDetectResult
		err := r.sitesCall(ctx, mgr, inst, protocol.MethodSitesDetect, protocol.SitesDetectParams{Entity: k.Name, Data: data}, &res)
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if err != nil {
			r.log.Warn("sites.detect failed; skipped", "plugin", plugin, "err", err)
			if Retryable(err) {
				retry = err
			}
			continue
		}
		if len(res.Sources) > protocol.MaxSources {
			r.log.Warn("sites.detect sent too many sources; skipped", "plugin", plugin, "sources", len(res.Sources))
			continue
		}
		for _, s := range res.Sources {
			if err := s.Validate(); err != nil {
				r.log.Warn("sites.detect sent an invalid source; dropped", "plugin", plugin, "err", err)
				continue
			}
			if !slices.ContainsFunc(out, func(o library.Source) bool { return o.Kind == s.Kind && o.Ref == s.Ref }) {
				out = append(out, fromProtocol(s))
			}
		}
	}
	return out, retry
}

// ParseSource turns what the owner typed into a source with its canonical
// URL: in.URL alone, or in.Kind and in.Ref. The first sites plugin that
// claims it wins; the owner's ExternalID is kept. Input no plugin claims,
// and input a plugin refuses, is library.ErrInvalid.
func (r *Registry) ParseSource(ctx context.Context, mgr *pluginhost.Manager, k *library.Kind, in library.Source) (library.Source, error) {
	in.URL = strings.TrimSpace(in.URL)
	insts, err := r.SitesInstances(ctx)
	if err != nil {
		return library.Source{}, err
	}
	params := protocol.SitesParseParams{Entity: k.Name, URL: in.URL, Kind: in.Kind, Ref: in.Ref}
	for _, inst := range insts {
		plugin := inst.Manifest.Name
		var res protocol.SitesParseResult
		err := r.sitesCall(ctx, mgr, inst, protocol.MethodSitesParse, params, &res)
		if ctx.Err() != nil {
			return library.Source{}, ctx.Err()
		}
		var rpcErr *protocol.Error
		if errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeInvalidParams {
			return library.Source{}, fmt.Errorf("%w: %s", library.ErrInvalid, rpcErr.Message)
		}
		if err != nil {
			r.log.Warn("sites.parse failed; skipped", "plugin", plugin, "err", err)
			continue
		}
		if res.Source == nil {
			continue
		}
		if err := res.Source.Validate(); err != nil {
			return library.Source{}, fmt.Errorf("%w: %s: %w", ErrBadSource, plugin, err)
		}
		s := fromProtocol(*res.Source)
		s.ExternalID = cmp.Or(in.ExternalID, s.ExternalID)
		return s, nil
	}
	if in.URL != "" {
		return library.Source{}, fmt.Errorf("%w: no installed plugin recognizes that URL", library.ErrInvalid)
	}
	return library.Source{}, fmt.Errorf("%w: no installed plugin knows source kind %q", library.ErrInvalid, in.Kind)
}

// CreateBuiltinSites creates one instance of each built-in plugin that
// declares sites and needs no config or secrets, so a new server detects
// sources with no setup. A setting records each plugin it ran for, so an
// instance the owner deletes stays deleted. It returns how many it created.
func (r *Registry) CreateBuiltinSites(ctx context.Context) (int, error) {
	r.mu.RLock()
	builtin := maps.Clone(r.builtin)
	r.mu.RUnlock()
	created := 0
	for _, name := range slices.Sorted(maps.Keys(builtin)) {
		m := builtin[name]
		required, _ := m.Config["required"].([]any)
		if !m.Has("sites") || len(m.Secrets) > 0 || len(required) > 0 {
			continue
		}
		ok, err := r.createBuiltinOnce(ctx, name)
		if err != nil {
			return created, fmt.Errorf("create %s instance: %w", name, err)
		}
		if ok {
			created++
		}
	}
	return created, nil
}

// createBuiltinOnce runs in one transaction, so the api and worker starting
// together create one instance between them.
func (r *Registry) createBuiltinOnce(ctx context.Context, plugin string) (bool, error) {
	sealed, err := r.box.Seal(nil)
	if err != nil {
		return false, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `INSERT INTO setting (key, value) VALUES ($1, 'created') ON CONFLICT DO NOTHING`,
		"builtin-instance:"+plugin)
	if err != nil || tag.RowsAffected() == 0 {
		return false, err
	}
	// An owner who already has an instance, or one by that name, needs no other.
	tag, err = tx.Exec(ctx, `INSERT INTO plugin_instance (plugin, name, secrets)
		SELECT $1, $1, $2 WHERE NOT EXISTS (SELECT 1 FROM plugin_instance WHERE plugin = $1)
		ON CONFLICT (name) DO NOTHING`, plugin, sealed)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, tx.Commit(ctx)
}
