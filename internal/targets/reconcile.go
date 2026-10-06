package targets

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/davidgibbons/innkeeper/internal/cardhash"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/merge"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/protocol"
)

// Reconcile verdicts.
const (
	VerdictBound  = "bound"
	VerdictExact  = "exact"
	VerdictLikely = "likely"
	VerdictNew    = "new"
	VerdictError  = "error"
)

// ReconcileMatch says which signals make a library card a likely match.
type ReconcileMatch struct {
	Name           *bool `json:"name"`            // default true
	TextDistance   *int  `json:"text_distance"`   // default 8, 0..32
	AvatarDistance *int  `json:"avatar_distance"` // default 8, 0..32
}

// ReconcileParams are a reconcile run's settings.
type ReconcileParams struct {
	InstanceID int64          `json:"instance_id"`
	Match      ReconcileMatch `json:"match"`
	// SplitFields is how many core fields must differ before import is
	// suggested over bind.
	SplitFields *int `json:"split_fields"` // default 3, 1..4
}

// withDefaults fills unset settings and checks their ranges.
func (p ReconcileParams) withDefaults() (ReconcileParams, error) {
	def := func(v *int, d int) *int {
		if v == nil {
			return &d
		}
		return v
	}
	if p.Match.Name == nil {
		on := true
		p.Match.Name = &on
	}
	p.Match.TextDistance = def(p.Match.TextDistance, 8)
	p.Match.AvatarDistance = def(p.Match.AvatarDistance, 8)
	p.SplitFields = def(p.SplitFields, 3)
	if d := *p.Match.TextDistance; d < 0 || d > 32 {
		return p, fmt.Errorf("%w: match.text_distance must be 0 to 32", library.ErrInvalid)
	}
	if d := *p.Match.AvatarDistance; d < 0 || d > 32 {
		return p, fmt.Errorf("%w: match.avatar_distance must be 0 to 32", library.ErrInvalid)
	}
	if n := *p.SplitFields; n < 1 || n > 4 {
		return p, fmt.Errorf("%w: split_fields must be 1 to 4", library.ErrInvalid)
	}
	return p, nil
}

// Snapshot is a finished reconcile run.
type Snapshot struct {
	Params    ReconcileParams `json:"params"`
	Items     []ReconcileItem `json:"items"`     // characters
	Lorebooks []ReconcileItem `json:"lorebooks"` // world books no character links to
	Missing   []int64         `json:"missing"`   // card IDs bound here whose app item is gone
	Unpushed  []int64         `json:"unpushed"`  // card IDs with no binding here
	Totals    map[string]int  `json:"totals"`    // by verdict
}

// ReconcileItem is one app item and how it lines up with the library.
type ReconcileItem struct {
	RemoteID   string          `json:"remote_id"`
	Name       string          `json:"name"`
	AppHash    string          `json:"app_hash"`
	Verdict    string          `json:"verdict" enum:"bound,exact,likely,new,error"`
	Error      string          `json:"error,omitempty"`
	BindingID  int64           `json:"binding_id,omitempty"`
	Candidates []Candidate     `json:"candidates,omitempty"`
	Options    []Option        `json:"options"`
	Suggested  *Option         `json:"suggested,omitempty"`
	Lorebooks  []ReconcileItem `json:"lorebooks,omitempty"`
}

// Candidate is a library card or lorebook an app item may be a copy of.
type Candidate struct {
	CardID int64  `json:"card_id"` // or the lorebook's ID
	Name   string `json:"name"`
	// Reasons are the signals that matched: source, name, and content with
	// 0; text and avatar with their distance.
	Reasons         map[string]int `json:"reasons"`
	DifferingFields int            `json:"differing_fields"`
	Conflicts       int            `json:"conflicts"`
	// BoundTo is the remote ID this card is already bound to on the instance.
	BoundTo string `json:"bound_to,omitempty"`
}

// Option is one way to resolve an item.
type Option struct {
	Action string `json:"action" enum:"bind,import,skip"`
	CardID int64  `json:"card_id,omitempty"`
	Why    string `json:"why,omitempty"`
}

// describeReconcile resolves an instance that can pull, with character_book
// left out of the card fields so a target that embeds lorebooks still matches
// exactly. Reconcile reads info only through here.
// ponytail: embedded books aren't compared; split them out and reconcile
// them as lorebook items if that matters.
func (s *Service) describeReconcile(ctx context.Context, k kind, instanceID int64) (pluginhost.Instance, protocol.TargetInfo, error) {
	inst, info, err := s.describePull(ctx, k, instanceID)
	info.Fields = slices.DeleteFunc(slices.Clone(info.Fields), func(f string) bool { return f == "character_book" })
	return inst, info, err
}

// appItem is an app item as reconcile reads it.
type appItem struct {
	raw, data      json.RawMessage
	name, hash     string
	simhash, phash *int64
}

// readItem reads an app item and, with avatar, moves the avatar into the blob
// store for its phash.
func (s *Service) readItem(ctx context.Context, k kind, inst pluginhost.Instance, info protocol.TargetInfo, remoteID string,
	avatar bool) (appItem, error) {
	var it appItem
	var err error
	if k.isLorebook() {
		if it.raw, err = s.fetchRaw(ctx, k, inst, remoteID); err != nil {
			return it, err
		}
	} else {
		got, err := s.getCard(ctx, inst, protocol.TargetGetParams{RemoteID: remoteID, Avatar: avatar})
		if err != nil {
			return it, err
		}
		if it.raw, err = cardData(got.Card); err != nil {
			return it, err
		}
		if got.Avatar != "" {
			// One bad image only costs the item its avatar signal.
			if b, err := s.lib.IngestBlob(ctx, got.Avatar); err != nil {
				s.log.Warn("reconcile: avatar not stored", "remote_id", remoteID, "err", err)
			} else {
				it.phash = b.AvatarPhash
			}
		}
	}
	if it.data, it.hash, err = Project(it.raw, k.fields(info)); err != nil {
		return it, err
	}
	var named struct{ Name string }
	if err := json.Unmarshal(it.data, &named); err != nil {
		return it, err
	}
	it.name = named.Name
	it.simhash, err = textSimhash(k, it.data)
	return it, err
}

// reconcileItem gives one app item its verdict, candidates, options, and
// suggestion. An item the app can't give back is an error item; a library
// failure fails the run.
func (s *Service) reconcileItem(ctx context.Context, k kind, inst pluginhost.Instance, info protocol.TargetInfo,
	idx index, p ReconcileParams, remoteID string) (ReconcileItem, error) {
	item, it, err := s.verdict(ctx, k, inst, info, idx, p, remoteID)
	if err != nil || item.Verdict == VerdictError {
		item.Options = []Option{{Action: "skip"}}
		return item, err
	}
	var ancestor json.RawMessage
	if b, ok := idx.byRemote[remoteID]; ok {
		if ancestor, _, err = Project(b.accepted, k.fields(info)); err != nil {
			return item, err
		}
	}
	data := map[int64]json.RawMessage{}
	for _, e := range idx.entries {
		data[e.id] = e.data
	}
	schema := merge.Card
	if k.isLorebook() {
		schema = merge.Lorebook
	}
	var core []int
	for i := range item.Candidates {
		c := &item.Candidates[i]
		fields, err := merge.Compare(schema, data[c.CardID], it.data, ancestor)
		if err != nil {
			return item, err
		}
		n := 0
		for _, f := range fields {
			differs := false
			for _, h := range f.Hunks {
				differs = differs || h.Status != merge.Same
				if h.Status == merge.Conflict {
					c.Conflicts++
				}
			}
			if differs {
				c.DifferingFields++
				if slices.Contains(coreFields, f.Path) {
					n++
				}
			}
		}
		core = append(core, n)
		item.Options = append(item.Options, Option{Action: "bind", CardID: c.CardID})
	}
	item.Options = append(item.Options, Option{Action: "import"}, Option{Action: "skip"})
	bind := func(c Candidate, why string) { item.Suggested = &Option{Action: "bind", CardID: c.CardID, Why: why} }
	switch item.Verdict {
	case VerdictBound:
		if len(item.Candidates) > 0 && item.Candidates[0].DifferingFields > 0 {
			bind(item.Candidates[0], "the app and the library differ; bind to resolve the drift")
		}
	case VerdictExact:
		item.Suggested = &Option{Action: "import", Why: "every library card with this content is bound to another app item"}
		for _, c := range item.Candidates {
			if c.BoundTo == "" {
				bind(c, fmt.Sprintf("same content as #%d %s", c.CardID, c.Name))
				break
			}
		}
	case VerdictLikely:
		top := item.Candidates[0]
		why := fmt.Sprintf("%d of %d core fields differ from #%d %s", core[0], len(coreFields), top.CardID, top.Name)
		if core[0] < *p.SplitFields {
			bind(top, why)
		} else {
			item.Suggested = &Option{Action: "import", Why: why}
		}
	case VerdictNew:
		item.Suggested = &Option{Action: "import", Why: "no library card matches"}
	}
	return item, nil
}

// coreFields decide between binding to a likely match and importing a copy.
var coreFields = []string{"description", "personality", "scenario", "first_mes"}

// verdict reads one app item and gives it its verdict and candidates.
func (s *Service) verdict(ctx context.Context, k kind, inst pluginhost.Instance, info protocol.TargetInfo,
	idx index, p ReconcileParams, remoteID string) (ReconcileItem, appItem, error) {
	item := ReconcileItem{RemoteID: remoteID}
	b, bound := idx.byRemote[remoteID]
	it, err := s.readItem(ctx, k, inst, info, remoteID, !bound)
	if err != nil {
		item.Verdict, item.Error = VerdictError, err.Error()
		return item, it, nil
	}
	item.Name, item.AppHash = it.name, it.hash
	candidate := func(e indexEntry, reasons map[string]int) Candidate {
		c := Candidate{CardID: e.id, Name: e.name, Reasons: reasons}
		if r, ok := idx.remoteOf[e.id]; ok && r != remoteID {
			c.BoundTo = r
		}
		return c
	}
	if bound {
		item.Verdict, item.BindingID = VerdictBound, b.id
		// An archived bound card isn't in the index, so it gets no candidate.
		for _, e := range idx.entries {
			if e.id == b.entityID {
				item.Candidates = []Candidate{candidate(e, map[string]int{})}
			}
		}
		return item, it, nil
	}
	for _, e := range idx.entries {
		if e.hash == it.hash {
			item.Candidates = append(item.Candidates, candidate(e, map[string]int{"content": 0}))
		}
	}
	if item.Candidates != nil {
		item.Verdict = VerdictExact
		return item, it, nil
	}

	sourced := map[int64]bool{}
	for _, src := range library.Detect(it.raw, k.isLorebook()) {
		ids, err := k.entities(s.lib).BySource(ctx, src.Kind, src.Ref)
		if err != nil {
			return item, it, err
		}
		for _, id := range ids {
			sourced[id] = true
		}
	}
	norm := normName(it.name)
	for _, e := range idx.entries {
		reasons := map[string]int{}
		if sourced[e.id] {
			reasons["source"] = 0
		}
		if *p.Match.Name && norm != "" && e.norm == norm {
			reasons["name"] = 0
		}
		if it.simhash != nil && e.simhash != nil {
			if d := cardhash.Distance(*it.simhash, *e.simhash); d <= *p.Match.TextDistance {
				reasons["text"] = d
			}
		}
		if it.phash != nil && e.phash != nil {
			if d := cardhash.Distance(*it.phash, *e.phash); d <= *p.Match.AvatarDistance {
				reasons["avatar"] = d
			}
		}
		if len(reasons) > 0 {
			item.Candidates = append(item.Candidates, candidate(e, reasons))
		}
	}
	if item.Candidates == nil {
		item.Verdict = VerdictNew
		return item, it, nil
	}
	item.Verdict = VerdictLikely
	// Stable over the index's ID order, so full ties rank by ID.
	slices.SortStableFunc(item.Candidates, func(a, b Candidate) int {
		_, as := a.Reasons["source"]
		_, bs := b.Reasons["source"]
		if as != bs {
			if as {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(len(b.Reasons), len(a.Reasons)), cmp.Compare(total(a.Reasons), total(b.Reasons)))
	})
	return item, it, nil
}

func total(reasons map[string]int) int {
	n := 0
	for _, d := range reasons {
		n += d
	}
	return n
}
