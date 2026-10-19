package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/protocol"
)

const checkEnrichName = "enrich.run of a sample card returns declared, valid values or a plugin error"

// checkEnrich runs enrich.run on the sample card. A plugin checked without
// its config, such as an AI without a model, may only fail, so an error other
// than -32601, for the call or the revision, counts as an answer.
func checkEnrich(ctx context.Context, p *pluginhost.Process, m *protocol.Manifest) error {
	var res protocol.EnrichRunResult
	err := call(ctx, p, protocol.MethodEnrichRun, enrichRunParams(), &res)
	var rpcErr *protocol.Error
	switch {
	case errors.As(err, &rpcErr) && rpcErr.Code != protocol.CodeMethodNotFound:
		return nil
	case err != nil:
		return err
	}
	i := slices.IndexFunc(res.Results, func(r protocol.EnrichResult) bool { return r.RevisionID == sampleRevision })
	if i < 0 {
		return fmt.Errorf("no result for revision %d", sampleRevision)
	}
	r := res.Results[i]
	if r.Error != "" {
		return nil
	}
	if len(r.Values) == 0 {
		return errors.New("the result has neither values nor an error")
	}
	var errs []error
	for _, key := range slices.Sorted(maps.Keys(r.Values)) {
		d, ok := m.Enrichments[key]
		if !ok {
			errs = append(errs, fmt.Errorf("%s isn't declared in [enrichments]", key))
		} else if err := d.Check(r.Values[key]); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
		}
	}
	return errors.Join(errs...)
}

// sampleRevision is the revision id enrichRunParams sends.
const sampleRevision = 1

// enrichRunParams asks for the sample card's enrichments.
func enrichRunParams() protocol.EnrichRunParams {
	var card struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(sampleCard, &card)
	return protocol.EnrichRunParams{Run: "contract-check",
		Revisions: []protocol.EnrichRevision{{CardID: 1, RevisionID: sampleRevision, Data: card.Data}}}
}

// driveEnrich runs enrich.run on the sample card.
func driveEnrich(_ context.Context, call caller) []func(context.Context) error {
	return []func(context.Context) error{func(ctx context.Context) error {
		return call(ctx, protocol.MethodEnrichRun, enrichRunParams(), nil)
	}}
}
