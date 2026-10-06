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
	Totals    map[string]int  `json:"totals"`    // by verdict, each world book once
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
	Reasons map[string]int `json:"reasons"`
	// DifferingFields counts every field with a change, not only the core
	// fields that decide the suggestion.
	DifferingFields int `json:"differing_fields"`
	Conflicts       int `json:"conflicts"`
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
	if err != nil {
		return inst, info, err
	}
	info.Fields = slices.DeleteFunc(slices.Clone(info.Fields), func(f string) bool { return f == "character_book" })
	return inst, info, nil
}

// appItem is an app item as reconcile reads it.
type appItem struct {
	raw, data      json.RawMessage
	name, hash     string
	simhash, phash *int64
	// avatar is the avatar blob's sha256, or empty.
	avatar string
	// links are a character's world books in the app, for a standalone target.
	links []string
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
		it.links = got.LorebookRemoteIDs
		if got.Avatar != "" {
			// One bad image only costs the item its avatar signal.
			if b, err := s.lib.IngestBlob(ctx, got.Avatar); err != nil {
				s.log.Warn("reconcile: avatar not stored", "remote_id", remoteID, "err", err)
			} else {
				it.avatar, it.phash = b.SHA256, b.AvatarPhash
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
// suggestion, and returns the world books the app links to it. An item the
// app can't give back is an error item; a library failure fails the run.
func (s *Service) reconcileItem(ctx context.Context, k kind, inst pluginhost.Instance, info protocol.TargetInfo,
	idx index, p ReconcileParams, remoteID string) (ReconcileItem, []string, error) {
	item, it, err := s.verdict(ctx, k, inst, info, idx, p, remoteID)
	if err != nil || item.Verdict == VerdictError {
		item.Options = []Option{{Action: "skip"}}
		return item, nil, err
	}
	item, err = suggest(k, info, idx, p, item, it)
	return item, it.links, err
}

// suggest compares an item with its candidates and fills its options and
// suggestion.
func suggest(k kind, info protocol.TargetInfo, idx index, p ReconcileParams, item ReconcileItem,
	it appItem) (ReconcileItem, error) {
	var accepted json.RawMessage
	if b, ok := idx.byRemote[item.RemoteID]; ok {
		accepted = b.accepted
	}
	var core []int
	for i := range item.Candidates {
		c := &item.Candidates[i]
		fields, err := compare(k, info, idx.byID[c.CardID].data, it.data, accepted)
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
		if i := unbound(item.Candidates); i >= 0 {
			c := item.Candidates[i]
			bind(c, fmt.Sprintf("same content as #%d %s", c.CardID, c.Name))
		} else {
			c := item.Candidates[0]
			item.Suggested = &Option{Action: "skip", Why: fmt.Sprintf(
				"same as #%d %s, already bound to %s: the app has a duplicate", c.CardID, c.Name, c.BoundTo)}
		}
	case VerdictLikely:
		i := unbound(item.Candidates)
		switch {
		case i < 0:
			item.Suggested = &Option{Action: "import", Why: "every likely match is already bound to another app item"}
		case k.isLorebook():
			// split_fields is for cards; a world book binds to its best unbound match.
			c := item.Candidates[i]
			bind(c, fmt.Sprintf("likely the same as #%d %s", c.CardID, c.Name))
		default:
			c := item.Candidates[i]
			why := fmt.Sprintf("%d of %d core fields differ from #%d %s", core[i], len(coreFields), c.CardID, c.Name)
			if core[i] < *p.SplitFields {
				bind(c, why)
			} else {
				item.Suggested = &Option{Action: "import", Why: why}
			}
		}
	case VerdictNew:
		item.Suggested = &Option{Action: "import", Why: "no library card matches"}
	}
	return item, nil
}

// unbound is the index of the first candidate not bound to another app item, or -1.
func unbound(cs []Candidate) int {
	return slices.IndexFunc(cs, func(c Candidate) bool { return c.BoundTo == "" })
}

// compare diffs a library entry's projected data with the app's, with
// accepted, the binding's copy, projected the same way as the ancestor.
func compare(k kind, info protocol.TargetInfo, lib, app, accepted json.RawMessage) ([]merge.Field, error) {
	var ancestor json.RawMessage
	if accepted != nil {
		var err error
		if ancestor, _, err = Project(accepted, k.fields(info)); err != nil {
			return nil, err
		}
	}
	schema := merge.Card
	if k.isLorebook() {
		schema = merge.Lorebook
	}
	return merge.Compare(schema, lib, app, ancestor)
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
		if e, ok := idx.byID[b.entityID]; ok {
			item.Candidates = []Candidate{candidate(e, map[string]int{})}
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

// Reconcile lines every item in an instance's app up against the library.
// progress, if set, hears after each item read.
func (s *Service) Reconcile(ctx context.Context, p ReconcileParams, progress func(done, total int)) (Snapshot, error) {
	p, err := p.withDefaults()
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{Params: p, Items: []ReconcileItem{}, Lorebooks: []ReconcileItem{}, Missing: []int64{},
		Unpushed: []int64{}, Totals: map[string]int{}}
	inst, info, err := s.describeReconcile(ctx, cardKind, p.InstanceID)
	if err != nil {
		return snap, err
	}
	list := func(k kind) ([]protocol.TargetItem, error) {
		var res protocol.TargetListResult
		err := s.reg.Call(ctx, s.plugins, inst, k.list, nil, &res)
		return res.Items, err
	}
	cards, err := list(cardKind)
	if err != nil {
		return snap, err
	}
	var books []protocol.TargetItem
	standalone := info.Lorebooks == protocol.LorebooksStandalone
	if standalone {
		if books, err = list(lorebookKind); err != nil {
			return snap, err
		}
	}
	cardIdx, err := s.loadIndex(ctx, cardKind, inst, info)
	if err != nil {
		return snap, err
	}
	var bookIdx index
	if standalone {
		if bookIdx, err = s.loadIndex(ctx, lorebookKind, inst, info); err != nil {
			return snap, err
		}
	}

	done, total := 0, len(cards)+len(books)
	step := func(it ReconcileItem) {
		snap.Totals[it.Verdict]++
		done++
		if progress != nil {
			progress(done, max(done, total))
		}
	}
	// Each world book is read once, however many characters link it.
	read := map[string]ReconcileItem{}
	book := func(id string) (ReconcileItem, error) {
		if it, ok := read[id]; ok {
			return it, nil
		}
		it, _, err := s.reconcileItem(ctx, lorebookKind, inst, info, bookIdx, p, id)
		if err == nil {
			read[id] = it
			step(it)
		}
		return it, err
	}
	listed := map[string]bool{}
	for _, c := range cards {
		listed[c.RemoteID] = true
		item, links, err := s.reconcileItem(ctx, cardKind, inst, info, cardIdx, p, c.RemoteID)
		if err != nil {
			return snap, err
		}
		step(item)
		for _, id := range links {
			if !standalone {
				break
			}
			lb, err := book(id)
			if err != nil {
				return snap, err
			}
			item.Lorebooks = append(item.Lorebooks, lb)
		}
		snap.Items = append(snap.Items, item)
	}
	for _, b := range books {
		if _, linked := read[b.RemoteID]; linked {
			continue
		}
		lb, err := book(b.RemoteID)
		if err != nil {
			return snap, err
		}
		snap.Lorebooks = append(snap.Lorebooks, lb)
	}

	for remoteID, b := range cardIdx.byRemote {
		if !listed[remoteID] {
			snap.Missing = append(snap.Missing, b.entityID)
		}
	}
	slices.Sort(snap.Missing)
	for _, e := range cardIdx.entries {
		if _, ok := cardIdx.remoteOf[e.id]; !ok {
			snap.Unpushed = append(snap.Unpushed, e.id)
		}
	}
	return snap, nil
}
