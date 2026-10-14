package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/protocol"
)

const (
	sitesCardName     = "sites.detect finds no source in a plain card"
	sitesLorebookName = "sites.detect finds no source in a plain lorebook"
	sitesBadDataName  = "sites.detect refuses data that isn't an object with -32602"
	sitesForeignName  = "sites.parse returns null for a URL that isn't its own"
	sitesBadParamName = "sites.parse refuses params without a url or kind with -32602"
	sitesValidName    = "every source sites returns is valid"
)

// checkSites runs the sites checks, stopping if the plugin dies.
func checkSites(ctx context.Context, p *pluginhost.Process) []Result {
	var returned []protocol.Source
	detectNone := func(entity, data string) func(context.Context) error {
		return func(ctx context.Context) error {
			var res protocol.SitesDetectResult
			err := call(ctx, p, protocol.MethodSitesDetect, protocol.SitesDetectParams{Entity: entity, Data: json.RawMessage(data)}, &res)
			returned = append(returned, res.Sources...)
			if err == nil && len(res.Sources) > 0 {
				err = fmt.Errorf("got %d sources, want none", len(res.Sources))
			}
			return err
		}
	}
	results := runChecks(ctx, p, []check{
		{sitesCardName, detectNone(protocol.KindCard, `{"name": "Contract"}`)},
		{sitesLorebookName, detectNone(protocol.KindLorebook, `{"entries": []}`)},
		{sitesBadDataName, func(ctx context.Context) error {
			return expectCode(call(ctx, p, protocol.MethodSitesDetect, protocol.SitesDetectParams{Entity: protocol.KindCard, Data: json.RawMessage(`"x"`)}, nil), protocol.CodeInvalidParams)
		}},
		{sitesForeignName, func(ctx context.Context) error {
			var res protocol.SitesParseResult
			err := call(ctx, p, protocol.MethodSitesParse, protocol.SitesParseParams{Entity: protocol.KindCard, URL: "https://example.invalid/contract"}, &res)
			if res.Source != nil {
				returned = append(returned, *res.Source)
				err = errors.New("got a source, want null")
			}
			return err
		}},
		{sitesBadParamName, func(ctx context.Context) error {
			return expectCode(call(ctx, p, protocol.MethodSitesParse, protocol.SitesParseParams{Entity: protocol.KindCard}, nil), protocol.CodeInvalidParams)
		}},
	})
	var errs []error
	for _, s := range returned {
		if err := s.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("%s %q: %w", s.Kind, s.Ref, err))
		}
	}
	return append(results, Result{sitesValidName, errors.Join(errs...)})
}
