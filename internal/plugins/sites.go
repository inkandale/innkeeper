package plugins

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/protocol"
)

// sitesTimeout bounds each sites call, which only parses. A variable so tests can shorten it.
var sitesTimeout = 5 * time.Second

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
// no sites plugin it falls back to the library's own detectors.
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
	// ponytail: core's own detectors until url-import ships built in; delete them then.
	if len(insts) == 0 {
		return library.Detect(data, k == library.Lorebook), nil
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
	if len(insts) == 0 {
		return parseFallback(in, k == library.Lorebook)
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

func parseFallback(in library.Source, lorebook bool) (library.Source, error) {
	if in.URL != "" {
		s, err := library.ParseSourceURL(in.URL)
		if err != nil {
			return library.Source{}, err
		}
		s.ExternalID = in.ExternalID
		in = s
	}
	// Rebuilt so the stored URL always matches the entity type.
	return library.ValidateSource(in, lorebook)
}
