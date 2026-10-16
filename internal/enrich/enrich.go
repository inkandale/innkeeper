// Package enrich runs enrich jobs: it sends cards' head revisions to an
// enrich plugin in batches and stores the values that match the plugin's
// declarations.
package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"unicode/utf8"

	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/protocol"
)

// Kind is the enrich job kind. Its InstanceID is the enrich instance.
const Kind = "enrich"

// MaxCards is the most cards one run takes.
const MaxCards = 10_000

// maxWarnings and maxWarning bound the warnings a job's progress keeps.
const (
	maxWarnings = 100
	maxWarning  = 300
)

// Payload is an enrich job's payload.
type Payload struct {
	CardIDs []int64 `json:"card_ids"`
	// Rescore sends cards whose head revision already holds a value from the instance.
	Rescore bool `json:"rescore,omitempty"`
}

// Progress is an enrich job's progress, counted in cards.
type Progress struct {
	Message  string   `json:"message,omitempty"`
	Done     int      `json:"done"`
	Failed   int      `json:"failed"`
	Skipped  int      `json:"skipped"`
	Total    int      `json:"total"`
	Warnings []string `json:"warnings,omitempty"`
}

func (p *Progress) warn(msgs ...string) {
	for _, m := range msgs {
		if len(p.Warnings) < maxWarnings {
			p.Warnings = append(p.Warnings, cut(m, maxWarning))
		}
	}
}

// cut shortens s to at most n bytes, on a rune boundary.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// Checked is what an enrich.run reply yields once checked against the
// plugin's declarations.
type Checked struct {
	Values []library.NewEnrichment
	// Done and Failed count revisions: done ones stored at least one value.
	Done, Failed int
	// Skipped counts revisions not sent because they already hold a value.
	Skipped  int
	Warnings []string
}

// Check keeps the values in reply that decl declares with a valid value, for
// revisions in sent. A revision with an error, with no valid value, or
// missing from the reply fails.
func Check(decl map[string]protocol.Enrichment, sent []protocol.EnrichRevision, reply protocol.EnrichRunResult) Checked {
	var out Checked
	byRev := map[int64]protocol.EnrichRevision{}
	for _, r := range sent {
		byRev[r.RevisionID] = r
	}
	answered := map[int64]bool{}
	for _, res := range reply.Results {
		rev, ok := byRev[res.RevisionID]
		if !ok || answered[res.RevisionID] {
			out.Warnings = append(out.Warnings, fmt.Sprintf("ignored a result for revision %d, which wasn't asked for or was answered twice", res.RevisionID))
			continue
		}
		answered[res.RevisionID] = true
		name := library.DataName(rev.Data)
		if res.Error != "" {
			out.Failed++
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s: %s", name, cut(res.Error, maxWarning)))
			continue
		}
		meta := res.Meta
		if len(meta) > 0 {
			if err := protocol.CheckMeta(meta); err != nil {
				out.Warnings = append(out.Warnings, fmt.Sprintf("%s: dropped meta: %v", name, err))
				meta = nil
			}
		}
		stored := 0
		for _, key := range slices.Sorted(maps.Keys(res.Values)) {
			d, ok := decl[key]
			if !ok {
				out.Warnings = append(out.Warnings, fmt.Sprintf("%s: dropped %s, which the plugin doesn't declare", name, key))
				continue
			}
			if err := d.Check(res.Values[key]); err != nil {
				out.Warnings = append(out.Warnings, fmt.Sprintf("%s: dropped %s: %v", name, key, err))
				continue
			}
			out.Values = append(out.Values, library.NewEnrichment{CardID: rev.CardID, RevisionID: rev.RevisionID, Key: key,
				Value: res.Values[key], Meta: meta})
			stored++
		}
		if stored == 0 {
			out.Failed++
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s: no valid values", name))
			continue
		}
		out.Done++
	}
	for _, r := range sent {
		if !answered[r.RevisionID] {
			out.Failed++
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s: the plugin sent no result", library.DataName(r.Data)))
		}
	}
	return out
}

// Runner runs enrich jobs.
type Runner struct {
	Lib      *library.Library
	Registry *plugins.Registry
	// Plugins runs the enrich plugins. Its core handler must be Runs.Handle.
	Plugins *pluginhost.Manager
	Runs    *pluginhost.Runs
}

// Run is the jobs.Handler for Kind. Every failure is final: each call costs
// the owner money, so running it again is the owner's choice.
func (rn *Runner) Run(ctx context.Context, r *jobs.Run) error {
	if err := rn.run(ctx, r); err != nil {
		return fmt.Errorf("%w: %w", jobs.ErrFinal, err)
	}
	return nil
}

func (rn *Runner) run(ctx context.Context, r *jobs.Run) error {
	if r.Attempts > 1 {
		return errors.New("the run was interrupted; start it again")
	}
	if r.InstanceID == nil {
		return errors.New("enrich job has no plugin instance")
	}
	var p Payload
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return fmt.Errorf("enrich payload: %w", err)
	}
	inst, err := rn.Registry.InstanceWith(ctx, *r.InstanceID, "enrich")
	if err != nil {
		return err
	}
	var mu sync.Mutex
	prog := Progress{Total: len(p.CardIDs)}
	publish := func() {
		raw, _ := json.Marshal(prog)
		r.SetProgress(raw)
	}
	token, remove := rn.Runs.Add(inst.ID, func(_ context.Context, method string, params json.RawMessage) (any, error) {
		var n protocol.Progress
		if method != protocol.NotifyProgress {
			return nil, protocol.MethodNotFound(method)
		}
		if json.Unmarshal(params, &n) == nil {
			mu.Lock()
			prog.Message = n.Message
			publish()
			mu.Unlock()
		}
		return nil, nil
	})
	defer remove()

	var lastErr error
	batches, failedBatches := 0, 0
	for ids := range slices.Chunk(p.CardIDs, protocol.MaxEnrichBatch) {
		batches++
		checked, err := rn.batch(ctx, inst, *r.InstanceID, token, ids, p.Rescore)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		mu.Lock()
		if err != nil {
			failedBatches++
			lastErr = err
			prog.Failed += len(ids)
			prog.warn(fmt.Sprintf("a batch of %d cards failed: %v", len(ids), err))
		} else {
			prog.Done += checked.Done
			prog.Failed += checked.Failed
			prog.Skipped += checked.Skipped
			prog.warn(checked.Warnings...)
		}
		publish()
		mu.Unlock()
		// A failed save is left to the lease renewal, which saves the progress again or stops the run.
		_ = r.SaveProgress(ctx)
	}
	if batches > 0 && failedBatches == batches {
		return fmt.Errorf("every batch failed: %w", lastErr)
	}
	return nil
}

// batch sends the head revisions of ids to the plugin and stores the values
// that pass Check. A card that doesn't exist counts as failed. Without
// rescore, a head that already holds a value from the instance is skipped.
func (rn *Runner) batch(ctx context.Context, inst pluginhost.Instance, instanceID int64, token string, ids []int64, rescore bool) (Checked, error) {
	revs, missing := rn.heads(ctx, ids)
	if len(revs) == 0 {
		return Checked{}, errors.New("none of its cards exist")
	}
	skipped := 0
	if !rescore {
		all := len(revs)
		var err error
		if revs, err = rn.unscored(ctx, instanceID, revs); err != nil {
			return Checked{}, err
		}
		skipped = all - len(revs)
		if len(revs) == 0 {
			return Checked{Failed: len(missing), Skipped: skipped, Warnings: missing}, nil
		}
	}
	var reply protocol.EnrichRunResult
	if err := rn.Registry.Call(ctx, rn.Plugins, inst, protocol.MethodEnrichRun,
		protocol.EnrichRunParams{Run: token, Revisions: revs}, &reply); err != nil {
		return Checked{}, err
	}
	checked := Check(inst.Manifest.Enrichments, revs, reply)
	checked.Failed += len(missing)
	checked.Skipped = skipped
	checked.Warnings = append(missing, checked.Warnings...)
	// One insert per revision, so a row the database refuses loses only its own revision's values.
	for _, rev := range revs {
		vals := slices.DeleteFunc(slices.Clone(checked.Values), func(v library.NewEnrichment) bool { return v.RevisionID != rev.RevisionID })
		if len(vals) == 0 {
			continue
		}
		if err := rn.Lib.AddEnrichments(ctx, instanceID, vals); err != nil {
			if ctx.Err() != nil {
				return Checked{}, err
			}
			checked.Done--
			checked.Failed++
			checked.Warnings = append(checked.Warnings, fmt.Sprintf("%s: storing its values failed: %v", library.DataName(rev.Data), err))
		}
	}
	return checked, nil
}

// heads returns the head revision of each card in ids, and a warning for
// each one that doesn't exist.
func (rn *Runner) heads(ctx context.Context, ids []int64) ([]protocol.EnrichRevision, []string) {
	var revs []protocol.EnrichRevision
	var missing []string
	for _, id := range ids {
		c, err := rn.Lib.Cards.Get(ctx, id)
		var rev library.Revision
		if err == nil {
			rev, err = rn.Lib.Cards.Revision(ctx, c.HeadRevisionID)
		}
		if err != nil {
			missing = append(missing, fmt.Sprintf("card %d: %v", id, err))
			continue
		}
		revs = append(revs, protocol.EnrichRevision{CardID: id, RevisionID: rev.ID, Data: rev.Data})
	}
	return revs, missing
}

// unscored keeps the revisions in revs that hold no value from instanceID.
func (rn *Runner) unscored(ctx context.Context, instanceID int64, revs []protocol.EnrichRevision) ([]protocol.EnrichRevision, error) {
	cardIDs, revIDs := make([]int64, len(revs)), make([]int64, len(revs))
	for i, rev := range revs {
		cardIDs[i], revIDs[i] = rev.CardID, rev.RevisionID
	}
	scored, err := rn.Lib.EnrichedRevisions(ctx, instanceID, cardIDs, revIDs)
	if err != nil {
		return nil, fmt.Errorf("find scored revisions: %w", err)
	}
	return slices.DeleteFunc(revs, func(rev protocol.EnrichRevision) bool { return slices.Contains(scored, rev.RevisionID) }), nil
}
