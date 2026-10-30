// Package enrich runs enrich jobs: it sends cards' head revisions to an
// enrich plugin in batches and stores the values that match the plugin's
// declarations.
package enrich

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"log/slog"
	"sync"
	"unicode/utf8"

	"github.com/inkandale/innkeeper/internal/embed"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/protocol"
)

// Kind is the enrich job kind. Its InstanceID is the enrich instance.
const Kind = "enrich"

// DefaultMaxCards is the most cards one run takes unless its plugin says
// otherwise: each card can cost the owner a model call.
const DefaultMaxCards = 10_000

// MaxCards is the most cards one run of m's plugin takes.
func MaxCards(m *protocol.Manifest) int { return cmp.Or(m.EnrichMaxCards, DefaultMaxCards) }

// maxWarnings and maxWarning bound the warnings a job's progress keeps.
const (
	maxWarnings = 100
	maxWarning  = 300
)

// Payload is an enrich job's payload: CardIDs, or the cards Query matches.
type Payload struct {
	CardIDs []int64        `json:"card_ids,omitempty"`
	Query   *library.Query `json:"query,omitempty"`
	// Scorers are the Query's scorers, which its JSON leaves out.
	Scorers []library.Scorer `json:"scorers,omitempty"`
	// Rescore sends cards whose head revision already holds a value from the instance.
	Rescore bool `json:"rescore,omitempty"`
	// Confirmed lifts the plugin's card cap: the owner agreed to the run's size.
	Confirmed bool `json:"confirmed,omitempty"`
	// Thorough is sent to the plugin with each batch.
	Thorough bool `json:"thorough,omitempty"`
}

// batches returns the payload's cards a batch at a time, reading a query's
// from the library as it goes and stopping at max cards, and the card count.
func (p Payload) batches(ctx context.Context, lib *library.Library, max int) (next func() ([]int64, error), total int, err error) {
	if p.Query == nil {
		chunks := slices.Collect(slices.Chunk(p.CardIDs, protocol.MaxEnrichBatch))
		return func() ([]int64, error) {
			if len(chunks) == 0 {
				return nil, nil
			}
			ids := chunks[0]
			chunks = chunks[1:]
			return ids, nil
		}, len(p.CardIDs), nil
	}
	q := *p.Query
	q.Scorers = p.Scorers
	if total, err = lib.CountMatches(ctx, library.Card, q); err != nil {
		return nil, 0, err
	}
	// Cards added since the run was queued may push the query past the cap it was checked against.
	total = min(total, max)
	var after int64
	read := 0
	return func() ([]int64, error) {
		if read >= max {
			return nil, nil
		}
		ids, err := lib.SearchIDsAfter(ctx, library.Card, q, after, min(protocol.MaxEnrichBatch, max-read))
		if len(ids) > 0 {
			after = ids[len(ids)-1]
		}
		read += len(ids)
		return ids, err
	}, total, nil
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
		kept, problems := protocol.CheckValues(decl, res.Values)
		for _, p := range problems {
			out.Warnings = append(out.Warnings, name+": "+p)
		}
		for _, key := range slices.Sorted(maps.Keys(kept)) {
			out.Values = append(out.Values, library.NewEnrichment{CardID: rev.CardID, RevisionID: rev.RevisionID, Key: key,
				Value: kept[key], Meta: meta})
		}
		if len(kept) == 0 {
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
	plugins.JobRunner
	Lib *library.Library
	// Queue and Log queue an embed job after a run stores values of a key
	// that declares embed, so vectors follow the new text.
	Queue *jobs.Queue
	Log   *slog.Logger
}

// Run is the jobs.Handler for Kind. It runs once: each call costs the owner
// money, so running it again is the owner's choice.
func (rn *Runner) Run(ctx context.Context, r *jobs.Run) error { return jobs.Once(rn.run)(ctx, r) }

func (rn *Runner) run(ctx context.Context, r *jobs.Run) error {
	var p Payload
	pr, err := rn.OpenRun(ctx, r, protocol.CapabilityEnrich, &p)
	if err != nil {
		return err
	}
	defer pr.Close()
	inst := pr.Instance
	most := MaxCards(inst.Manifest)
	if p.Confirmed {
		most = math.MaxInt
	}
	next, total, err := p.batches(ctx, rn.Lib, most)
	if err != nil {
		return err
	}
	var mu sync.Mutex
	prog := Progress{Total: total}
	token := pr.Serve(func(n protocol.Progress) {
		mu.Lock()
		prog.Message = n.Message
		pr.Publish(prog)
		mu.Unlock()
	}, nil)

	var lastErr error
	batches, failedBatches := 0, 0
	for {
		ids, err := next()
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		batches++
		checked, err := rn.batch(ctx, r, inst, token, ids, p)
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
			r.AddCardsChanged(checked.Done)
			prog.Failed += checked.Failed
			prog.Skipped += checked.Skipped
			prog.warn(checked.Warnings...)
		}
		pr.Publish(prog)
		mu.Unlock()
	}
	if prog.Done > 0 && slices.ContainsFunc(slices.Collect(maps.Values(inst.Manifest.Enrichments)), func(e protocol.Enrichment) bool { return e.Embed }) {
		embed.Enqueue(ctx, rn.Queue, rn.Log)
	}
	if batches > 0 && failedBatches == batches {
		return fmt.Errorf("every batch failed: %w", lastErr)
	}
	return nil
}

// batch sends the head revisions of ids to the plugin, stores the values
// that pass Check, and logs each scored card. A card that doesn't exist
// counts as failed. Without rescore, a head that already holds a value from
// the instance is skipped.
func (rn *Runner) batch(ctx context.Context, r *jobs.Run, inst pluginhost.Instance, token string, ids []int64, p Payload) (Checked, error) {
	instanceID := *r.InstanceID
	revs, missing := rn.heads(ctx, ids)
	if len(revs) == 0 {
		return Checked{}, errors.New("none of its cards exist")
	}
	skipped := 0
	if !p.Rescore {
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
	names := make([]string, len(revs))
	for i, rev := range revs {
		names[i] = library.DataName(rev.Data)
	}
	r.Log("info", "Scoring "+strings.Join(names, ", "))
	var reply protocol.EnrichRunResult
	if err := rn.Registry.Call(ctx, rn.Plugins, inst, protocol.MethodEnrichRun,
		protocol.EnrichRunParams{Run: token, Revisions: revs, Thorough: p.Thorough}, &reply); err != nil {
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
		line, err := rn.scoreLine(ctx, inst.Manifest.Enrichments, instanceID, rev, vals)
		if err != nil {
			return Checked{}, err
		}
		if err := rn.Lib.AddEnrichments(ctx, instanceID, vals); err != nil {
			if ctx.Err() != nil {
				return Checked{}, err
			}
			checked.Done--
			checked.Failed++
			checked.Warnings = append(checked.Warnings, fmt.Sprintf("%s: storing its values failed: %v", library.DataName(rev.Data), err))
			continue
		}
		r.Log("info", line)
	}
	return checked, nil
}

// scoreLine says rev's card was scored and, when the plugin declares a grid
// key and vals hold it, its new value beside the instance's current one.
func (rn *Runner) scoreLine(ctx context.Context, decl map[string]protocol.Enrichment, instanceID int64,
	rev protocol.EnrichRevision, vals []library.NewEnrichment) (string, error) {
	line := "Scored " + library.DataName(rev.Data)
	key := ""
	for k, d := range decl {
		if d.Grid {
			key = k
		}
	}
	i := slices.IndexFunc(vals, func(v library.NewEnrichment) bool { return v.Key == key })
	if i < 0 {
		return line, nil
	}
	line += fmt.Sprintf(": %s %s", cmp.Or(decl[key].Label, key), vals[i].Value)
	current, err := rn.Lib.Enrichments(ctx, rev.CardID, false)
	if err != nil {
		return "", fmt.Errorf("read card %d's scores: %w", rev.CardID, err)
	}
	if j := slices.IndexFunc(current, func(e library.Enrichment) bool { return e.InstanceID == instanceID && e.Key == key }); j >= 0 {
		line += fmt.Sprintf(" (was %s)", current[j].Value)
	}
	return line, nil
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
