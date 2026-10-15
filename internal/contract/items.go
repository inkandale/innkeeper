package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/protocol"
)

const (
	checkItemsListName      = "items.list of each type returns valid items or a plugin error"
	checkItemsRoundTripName = "items.get and items.put round-trip a listed item"
	checkItemsGoneName      = "items.get and items.delete answer -32004 for an item that doesn't exist"
	contractNone            = "innkeeper-contract:none"
)

// checkItems lists each item type. A plugin checked without its database
// may only fail, so an error other than -32601 counts as an answer, and the
// checks that need a working store run only for types that listed.
func checkItems(ctx context.Context, p *pluginhost.Process, m *protocol.Manifest) []Result {
	var listErrs []error
	listed := map[string][]protocol.ItemSummary{}
	for _, typ := range slices.Sorted(maps.Keys(m.Items)) {
		var res protocol.ItemsListResult
		err := call(ctx, p, protocol.MethodItemsList, protocol.ItemsListParams{Type: typ}, &res)
		var rpcErr *protocol.Error
		if errors.As(err, &rpcErr) && rpcErr.Code != protocol.CodeMethodNotFound {
			continue
		}
		if err == nil {
			err = validSummaries(res.Items)
			listed[typ] = res.Items
		}
		if err != nil {
			listErrs = append(listErrs, fmt.Errorf("%s: %w", typ, err))
		}
	}
	results := []Result{{checkItemsListName, errors.Join(listErrs...)}}
	if len(listErrs) > 0 || dead(results[0].Err, p) {
		return results
	}
	return append(results, runChecks(ctx, p, []check{
		{checkItemsRoundTripName, func(ctx context.Context) error { return checkItemsRoundTrip(ctx, p, listed) }},
		{checkItemsGoneName, func(ctx context.Context) error { return checkItemsGone(ctx, p, listed) }},
	})...)
}

func validSummaries(list []protocol.ItemSummary) error {
	var errs []error
	seen := map[string]bool{}
	for _, it := range list {
		if seen[it.ID] {
			errs = append(errs, fmt.Errorf("item id %q repeats", it.ID))
		}
		seen[it.ID] = true
		errs = append(errs, it.Validate())
	}
	return errors.Join(errs...)
}

// checkItemsRoundTrip gets the first item of each type and puts it back
// unchanged. It writes only to the test store the check was given.
func checkItemsRoundTrip(ctx context.Context, p *pluginhost.Process, listed map[string][]protocol.ItemSummary) error {
	for typ, items := range listed {
		if len(items) == 0 {
			continue
		}
		ref := protocol.ItemRef{Type: typ, ID: items[0].ID}
		var got protocol.ItemsGetResult
		if err := call(ctx, p, protocol.MethodItemsGet, ref, &got); err != nil {
			return fmt.Errorf("%s: get %s: %w", typ, ref.ID, err)
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(got.Item, &obj) != nil || obj == nil {
			return fmt.Errorf("%s: get %s: item is not a JSON object", typ, ref.ID)
		}
		var put protocol.ItemsPutResult
		if err := call(ctx, p, protocol.MethodItemsPut, protocol.ItemsPutParams{Type: typ, ID: ref.ID, Item: got.Item}, &put); err != nil {
			return fmt.Errorf("%s: put %s: %w", typ, ref.ID, err)
		}
		if err := protocol.CheckItemID(put.ID); err != nil {
			return fmt.Errorf("%s: put %s: %w", typ, ref.ID, err)
		}
	}
	return nil
}

func checkItemsGone(ctx context.Context, p *pluginhost.Process, listed map[string][]protocol.ItemSummary) error {
	for typ := range listed {
		for _, method := range []string{protocol.MethodItemsGet, protocol.MethodItemsDelete} {
			err := call(ctx, p, method, protocol.ItemRef{Type: typ, ID: contractNone}, nil)
			if err := expectCode(err, protocol.CodeRemoteNotFound); err != nil {
				return fmt.Errorf("%s: %s: %w", typ, method, err)
			}
		}
	}
	return nil
}
