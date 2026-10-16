package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/inkandale/innkeeper/cardhash"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/protocol"
)

const (
	catalogDescribeName = "catalog.describe returns a valid description"
	catalogPagesName    = "catalog.search pages without repeating items"
	catalogFiltersName  = "catalog.search refuses an undeclared filter with -32602"
	catalogNotFoundName = "catalog.get returns -32004 for an unknown ID"
	catalogGetName      = "catalog.get returns a search result's data and avatar"
	catalogSimilarName  = "catalog.similar finds an item by its own hashes"
	catalogIngestName   = "catalog.ingest completes"
)

// catalogPages is how many one-item pages the paging check walks.
const catalogPages = 3

// checkCatalog runs the catalog checks, stopping if the plugin dies.
func checkCatalog(ctx context.Context, p *pluginhost.Process, tmp string) []Result {
	var info protocol.CatalogInfo
	err := call(ctx, p, protocol.MethodCatalogDescribe, nil, &info)
	if err == nil {
		err = info.Validate()
	}
	results := []Result{{catalogDescribeName, err}}
	if err != nil {
		return results
	}
	// An ingested catalog is empty until it ingests its source.
	if info.Ingest && len(Config) > 0 {
		params := protocol.CatalogIngestParams{Run: "contract-check"}
		err := call(ctx, p, protocol.MethodCatalogIngest, params, &protocol.CatalogIngestResult{})
		results = append(results, Result{catalogIngestName, err})
		if err != nil {
			return results
		}
	}
	var first *protocol.CatalogItem
	var sourceless bool
	checks := []check{
		{catalogPagesName, func(ctx context.Context) (err error) {
			first, sourceless, err = checkCatalogPages(ctx, p, info)
			return err
		}},
		{catalogFiltersName, func(ctx context.Context) error { return checkCatalogFilters(ctx, p) }},
		{catalogNotFoundName, func(ctx context.Context) error { return checkCatalogNotFound(ctx, p, sourceless) }},
		{catalogGetName, func(ctx context.Context) error { return checkCatalogGet(ctx, p, first, tmp) }},
	}
	if info.Similar {
		checks = append(checks, check{catalogSimilarName, func(ctx context.Context) error { return checkCatalogSimilar(ctx, p, info, first, sourceless) }})
	}
	return append(results, runChecks(ctx, p, checks)...)
}

// answered reports whether err is a reply other than -32601. Without a
// config, a catalog may refuse calls that need its source that way.
func answered(err error) bool {
	var rpcErr *protocol.Error
	return errors.As(err, &rpcErr) && rpcErr.Code != protocol.CodeMethodNotFound
}

// checkCatalogPages walks one-item pages and returns the first item. Every
// item must be valid and none may come back on a later page. sourceless
// reports that, checked without a config, the catalog refused to search.
func checkCatalogPages(ctx context.Context, p *pluginhost.Process, info protocol.CatalogInfo) (first *protocol.CatalogItem, sourceless bool, err error) {
	seen := map[string]bool{}
	cursor := ""
	for range catalogPages {
		var res protocol.CatalogSearchResult
		params := protocol.CatalogSearchParams{Page: protocol.CatalogPage{Size: 1, Cursor: cursor}}
		err := call(ctx, p, protocol.MethodCatalogSearch, params, &res)
		if cursor == "" && len(Config) == 0 && answered(err) {
			return nil, true, nil
		}
		if err != nil {
			return first, false, err
		}
		if len(res.Items) > 1 {
			return first, false, fmt.Errorf("asked for 1 item, got %d", len(res.Items))
		}
		for _, it := range res.Items {
			if err := it.Validate(info.Kinds); err != nil {
				return first, false, err
			}
			if seen[it.ID] {
				return first, false, fmt.Errorf("item %q came back on a later page", it.ID)
			}
			seen[it.ID] = true
			if first == nil {
				first = &it
			}
		}
		if res.Next == "" {
			break
		}
		cursor = res.Next
	}
	return first, false, nil
}

func checkCatalogFilters(ctx context.Context, p *pluginhost.Process) error {
	params := protocol.CatalogSearchParams{Filters: map[string]json.RawMessage{"innkeeper_contract_check": json.RawMessage(`"x"`)}}
	return expectCode(call(ctx, p, protocol.MethodCatalogSearch, params, nil), protocol.CodeInvalidParams)
}

func checkCatalogNotFound(ctx context.Context, p *pluginhost.Process, sourceless bool) error {
	params := protocol.CatalogGetParams{ID: "innkeeper-contract-check-missing"}
	err := call(ctx, p, protocol.MethodCatalogGet, params, nil)
	if sourceless && answered(err) {
		return nil
	}
	return expectCode(err, protocol.CodeRemoteNotFound)
}

// checkCatalogGet fetches the first search result with its avatar and wants
// its latest data to hash to the content_hash search reported. It passes
// when search returned nothing.
func checkCatalogGet(ctx context.Context, p *pluginhost.Process, it *protocol.CatalogItem, tmp string) error {
	if it == nil {
		return nil
	}
	var res protocol.CatalogGetResult
	if err := call(ctx, p, protocol.MethodCatalogGet, protocol.CatalogGetParams{ID: it.ID, Avatar: true}, &res); err != nil {
		return err
	}
	if res.Item.ID != it.ID {
		return fmt.Errorf("asked for %q, got %q", it.ID, res.Item.ID)
	}
	if res.Version != it.LatestVersion {
		return fmt.Errorf("version is %q, want the latest, %q", res.Version, it.LatestVersion)
	}
	if !slices.ContainsFunc(res.Versions, func(v protocol.CatalogVersion) bool { return v.Version == res.Version }) {
		return fmt.Errorf("versions don't list version %q", res.Version)
	}
	// The library has no hash for other kinds yet.
	switch it.Kind {
	case protocol.KindCard:
		var c struct {
			Spec string `json:"spec"`
		}
		if err := json.Unmarshal(res.Data, &c); err != nil || c.Spec != "chara_card_v3" {
			return errors.New("data is not a chara_card_v3 card")
		}
		data, _, err := cardhash.CardData(res.Data)
		if err != nil {
			return fmt.Errorf("data: %w", err)
		}
		if err := checkHash(data, it.Hashes.ContentHash); err != nil {
			return err
		}
	case protocol.KindLorebook:
		if err := checkHash(res.Data, it.Hashes.ContentHash); err != nil {
			return err
		}
	}
	if res.Avatar == "" {
		return nil
	}
	if err := checkTmpFile(res.Avatar, tmp); err != nil {
		return fmt.Errorf("avatar: %w", err)
	}
	return nil
}

func checkHash(data json.RawMessage, want string) error {
	hash, err := cardhash.ContentHash(data)
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if hash != want {
		return fmt.Errorf("data hashes to %s, but search reported %s; hash with the cardhash package", hash, want)
	}
	return nil
}

// checkCatalogSimilar looks up the first search result by its own hashes
// and wants it among the matches. With no item it only wants an answer.
func checkCatalogSimilar(ctx context.Context, p *pluginhost.Process, info protocol.CatalogInfo, it *protocol.CatalogItem, sourceless bool) error {
	params := protocol.CatalogSimilarParams{Kind: info.Kinds[0],
		Hashes: protocol.CatalogHashes{ContentHash: strings.Repeat("0", 64)}, MaxDistance: 3, Limit: 10}
	if it != nil {
		params.Kind, params.Hashes = it.Kind, it.Hashes
	}
	var res protocol.CatalogSimilarResult
	err := call(ctx, p, protocol.MethodCatalogSimilar, params, &res)
	if sourceless && answered(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(res.Items) > params.Limit {
		return fmt.Errorf("asked for at most %d items, got %d", params.Limit, len(res.Items))
	}
	for _, m := range res.Items {
		if err := m.Validate(info.Kinds); err != nil {
			return err
		}
	}
	if it != nil && !slices.ContainsFunc(res.Items, func(m protocol.CatalogItem) bool { return m.ID == it.ID }) {
		return fmt.Errorf("item %q is not among the items similar to its own hashes", it.ID)
	}
	return nil
}
