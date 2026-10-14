package targets

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/davidgibbons/innkeeper/cardhash"
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

// withDefaults fills unset settings and checks their ranges.
func withDefaults(p ReconcileParams) (ReconcileParams, error) {
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
	p.Apply = cmp.Or(p.Apply, ApplyObvious)
	if p.Apply != ApplyObvious && p.Apply != ApplyNone {
		return p, fmt.Errorf("%w: apply must be obvious or none", library.ErrInvalid)
	}
	return p, nil
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
	return inst, reconcileInfo(info), nil
}

// reconcileInfo leaves character_book out of the card fields.
func reconcileInfo(info protocol.TargetInfo) protocol.TargetInfo {
	info.Fields = slices.DeleteFunc(slices.Clone(info.Fields), func(f string) bool { return f == "character_book" })
	return info
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
	// avatarErr is why the app's avatar couldn't be stored; avatar and phash are then empty.
	avatarErr error
	// sources are what DetectSources found in raw.
	sources []library.Source
}

// readItem reads an app item and, with avatar, moves the avatar into the blob
// store for its phash.
func (s *Service) readItem(ctx context.Context, k kind, inst pluginhost.Instance, info protocol.TargetInfo, remoteID string,
	avatar bool) (appItem, error) {
	it, err := s.readItemData(ctx, k, inst, info, remoteID, avatar)
	if remoteNotFound(err) {
		err = fmt.Errorf("%w: the app has no %s %s: %w", library.ErrNotFound, k.Name, remoteID, err)
	}
	return it, err
}

func (s *Service) readItemData(ctx context.Context, k kind, inst pluginhost.Instance, info protocol.TargetInfo,
	remoteID string, avatar bool) (appItem, error) {
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
			if b, err := s.lib.IngestBlob(ctx, got.Avatar); err != nil {
				it.avatarErr = err
			} else {
				it.avatar, it.phash = b.SHA256, b.AvatarPhash
			}
		}
	}
	if it.data, it.hash, err = Project(it.raw, k.fields(info)); err != nil {
		return it, err
	}
	// A plugin that failed was logged and skipped, since detection never fails a sync.
	if it.sources, _ = s.reg.DetectSources(ctx, s.plugins, k.Kind, it.raw); ctx.Err() != nil {
		return it, ctx.Err()
	}
	it.name = library.DataName(it.data)
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
	// appOnly[i]: every hunk that differs from candidate i is the app's change alone.
	var appOnly []bool
	// appChanged[i]: some hunk that differs from candidate i is the app's change.
	var appChanged []bool
	for i := range item.Candidates {
		c := &item.Candidates[i]
		fields, err := compare(k, info, idx.byID[c.CardID].data, it.data, accepted)
		if err != nil {
			return item, err
		}
		n, only, changed := 0, true, false
		for _, f := range fields {
			differs := false
			for _, h := range f.Hunks {
				differs = differs || h.Status != merge.Same
				if h.Status == merge.Conflict {
					c.Conflicts++
				}
				changed = changed || h.Status == merge.AutoApp || h.Status == merge.Conflict
				if h.Status != merge.Same && h.Status != merge.AutoApp {
					only = false
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
		appOnly = append(appOnly, only && c.DifferingFields > 0)
		appChanged = append(appChanged, changed)
		item.Options = append(item.Options, Option{Action: "bind", CardID: c.CardID})
	}
	item.Options = append(item.Options, Option{Action: "import"}, Option{Action: "skip"})
	bind := func(c Candidate, why string) { item.Suggested = &Option{Action: "bind", CardID: c.CardID, Why: why} }
	switch item.Verdict {
	case VerdictBound:
		missing := idx.byRemote[item.RemoteID].remoteState == remoteMissing
		item.AppChanged = len(item.Candidates) > 0 && !missing && appChanged[0]
		if len(item.Candidates) > 0 && item.Candidates[0].DifferingFields > 0 {
			why := "the app and the library differ; bind to resolve the drift"
			// A missing binding has no ancestor to pull against.
			if item.AppOnly = appOnly[0] && !missing; item.AppOnly {
				why = "changed only in the app"
			}
			bind(item.Candidates[0], why)
		}
	case VerdictExact:
		if i := unbound(item.Candidates); i >= 0 {
			c := item.Candidates[i]
			bind(c, fmt.Sprintf("same content as #%d %s", c.CardID, c.Name))
		} else {
			c := item.Candidates[0]
			item.Suggested = &Option{Action: "skip", Why: fmt.Sprintf(
				"same as #%d %s, already bound to another app item: the app has a duplicate", c.CardID, c.Name)}
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
	return merge.Compare(k.Schema, lib, app, ancestor)
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
	if it.avatarErr != nil {
		// One bad image only costs the item its avatar signal.
		s.log.Warn("reconcile: avatar not stored", "remote_id", remoteID, "err", it.avatarErr)
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
		item.Verdict = VerdictBound
		if b.remoteState != remoteMissing {
			item.BindingID = b.id
		}
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
		item.Candidates = capCandidates(item.Candidates)
		return item, it, nil
	}

	sourced := map[int64]bool{}
	for _, src := range it.sources {
		ids, err := s.lib.Entities(k.Kind).BySource(ctx, src.Kind, src.Ref)
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
	item.Candidates = capCandidates(item.Candidates)
	return item, it, nil
}

// maxCandidates keeps a snapshot small when a common name matches many cards.
// ponytail: fixed; make it a reconcile param if someone needs more.
const maxCandidates = 10

// capCandidates keeps the best maxCandidates. When every kept candidate is
// bound to another app item, the best unbound one takes the last place, so
// the suggestion can still bind to it.
func capCandidates(cs []Candidate) []Candidate {
	if len(cs) <= maxCandidates {
		return cs
	}
	kept := cs[:maxCandidates]
	if unbound(kept) < 0 {
		if i := unbound(cs); i >= 0 {
			kept[maxCandidates-1] = cs[i]
		}
	}
	return kept
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
	p, err := withDefaults(p)
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{Params: p, Items: []ReconcileItem{}, Lorebooks: []ReconcileItem{}, Missing: []int64{},
		Unpushed: []int64{}, Totals: map[string]int{}}
	inst, info, err := s.describeReconcile(ctx, cardKind, p.InstanceID)
	if err != nil {
		return snap, err
	}
	cards, err := s.listItems(ctx, cardKind, inst)
	if err != nil {
		return snap, err
	}
	var books []protocol.TargetItem
	standalone := info.Lorebooks == protocol.LorebooksStandalone
	if standalone {
		if books, err = s.listItems(ctx, lorebookKind, inst); err != nil {
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

	if p.Apply != ApplyNone {
		imported := map[string]bool{}
		s.autoApply(ctx, lorebookKind, p.InstanceID, bookIdx, bookIdx, snap.Lorebooks, imported)
		// The index was read before this run applied anything; an applied card isn't unpushed.
		for id := range s.autoApply(ctx, cardKind, p.InstanceID, cardIdx, bookIdx, snap.Items, imported) {
			cardIdx.remoteOf[id] = ""
		}
		maps.Copy(snap.Totals, autoTotals(snap.Items, snap.Lorebooks))
	}
	snap.Missing = missing(cardIdx, listed)
	snap.Unpushed = unpushed(cardIdx)
	return snap, nil
}

// autoTotals counts the items auto-apply imported, bound, or pulled.
func autoTotals(items, lorebooks []ReconcileItem) map[string]int {
	type auto struct{ bound, imported, pulled bool }
	// Each world book once, as the verdict totals count them.
	books := map[string]auto{}
	mark := func(lb ReconcileItem) {
		a := books[lb.RemoteID]
		books[lb.RemoteID] = auto{a.bound || lb.AutoBound, a.imported || lb.AutoImported, a.pulled || lb.AutoPulled}
	}
	all := []auto{}
	for _, it := range items {
		all = append(all, auto{it.AutoBound, it.AutoImported, it.AutoPulled})
		for _, lb := range it.Lorebooks {
			mark(lb)
		}
	}
	for _, lb := range lorebooks {
		mark(lb)
	}
	for _, a := range books {
		all = append(all, a)
	}
	totals := map[string]int{}
	for _, a := range all {
		// A world book one card imported, a later card binds; it counts as imported.
		switch {
		case a.imported:
			totals["auto_imported"]++
		case a.bound:
			totals["auto_bound"]++
		case a.pulled:
			totals["auto_pulled"]++
		}
	}
	return totals
}

// missing are the bound cards whose app item isn't listed. A card whose
// remote ID the app reused for another bound item is gone too.
func missing(idx index, listed map[string]bool) []int64 {
	out := []int64{}
	for id, remoteID := range idx.remoteOf {
		if remoteID != "" && (!listed[remoteID] || idx.byRemote[remoteID].entityID != id) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// unpushed are the library's cards with no binding on the instance.
func unpushed(idx index) []int64 {
	out := []int64{}
	for _, e := range idx.entries {
		if _, ok := idx.remoteOf[e.id]; !ok {
			out = append(out, e.id)
		}
	}
	return out
}

// autoApply binds each exact item, keeping the library's copy, and imports
// each new one, when its world books are all exact, new, or bound without
// drift. It returns the card IDs it applied. It resolves with the hashes this
// run read, so an item that changed since is left for the owner, and it never
// changes an existing library item. A failure leaves the item as it was.
// imported holds the kind:app_hash of each item imported this run; a copy of
// one is left for the owner rather than imported twice.
func (s *Service) autoApply(ctx context.Context, k kind, instanceID int64, idx, books index,
	items []ReconcileItem, imported map[string]bool) map[int64]bool {
	applied := map[int64]bool{}
	// World books bound or imported earlier in this run, as the next card must name them.
	nested := map[string]Resolution{}
items:
	for i := range items {
		it := &items[i]
		var r Resolution
		pull := false
		switch {
		case it.Suggested == nil:
			continue
		case it.Verdict == VerdictExact && it.Suggested.Action == "bind":
			r = Resolution{InstanceID: instanceID, RemoteID: it.RemoteID, AppHash: it.AppHash,
				BaseRevisionID: idx.byID[it.Suggested.CardID].head, Action: "bind", CardID: it.Suggested.CardID,
				Default: merge.ChooseLibrary}
		case it.Verdict == VerdictNew && it.Suggested.Action == "import" && !imported[k.Name+":"+it.AppHash]:
			r = Resolution{InstanceID: instanceID, RemoteID: it.RemoteID, AppHash: it.AppHash,
				BindingID: it.BindingID, Action: "import"}
		case it.Verdict == VerdictBound && it.AppOnly && it.Suggested.Action == "bind":
			// A bind takes auto_app hunks by status, so it brings the app's changes in.
			// ponytail: a world book linked to a card is only resolved through its card, so an app-only edit to it waits for the owner.
			r = Resolution{InstanceID: instanceID, RemoteID: it.RemoteID, AppHash: it.AppHash,
				BaseRevisionID: idx.byID[it.Suggested.CardID].head, AcceptedHash: idx.byRemote[it.RemoteID].acceptedHash,
				Action: "bind", CardID: it.Suggested.CardID}
			pull = true
		default:
			continue
		}
		opts := resolveOpts{bindOnly: true, importNew: true}
		if pull {
			// Writes a revision, and leaves the card's world books as they are.
			opts = resolveOpts{skipLeftOut: true}
		} else {
			// This card's own world books may be copies of each other too.
			importing := map[string]bool{}
			for _, lb := range it.Lorebooks {
				n, ok := nested[lb.RemoteID]
				if !ok {
					if n, ok = autoNested(lb, books); !ok {
						continue items
					}
				}
				if key := lorebookKind.Name + ":" + n.AppHash; n.Action == "import" {
					if imported[key] || importing[key] {
						continue items
					}
					importing[key] = true
				}
				r.Lorebooks = append(r.Lorebooks, n)
			}
		}
		res, err := s.resolve(ctx, k, r, opts)
		if err != nil {
			s.log.Warn("reconcile: auto-apply failed", "remote_id", it.RemoteID, "err", err)
			continue
		}
		it.AutoPulled = pull
		it.AutoBound, it.AutoImported = res.Action == "bind" && !pull, res.Action == "import"
		it.BindingID = res.BindingID
		if it.AutoImported {
			imported[k.Name+":"+it.AppHash] = true
		}
		applied[res.CardID] = true
		if pull {
			// Its world books were skipped, so there is nothing to record for them.
			continue
		}
		for j, lr := range res.Lorebooks {
			lb, n := &it.Lorebooks[j], r.Lorebooks[j]
			lb.AutoBound, lb.AutoImported, lb.BindingID = n.Action == "bind", n.Action == "import", lr.BindingID
			if n.Action == "import" {
				imported[lorebookKind.Name+":"+n.AppHash] = true
				// The next card binds the world book this one imported; books.byID predates it.
				n = Resolution{RemoteID: n.RemoteID, AppHash: n.AppHash, Action: "bind", CardID: lr.CardID,
					BaseRevisionID: lr.RevisionID, Default: merge.ChooseLibrary}
			}
			if err := s.pool.QueryRow(ctx, `SELECT accepted_hash FROM binding WHERE id = $1`, lr.BindingID).
				Scan(&n.AcceptedHash); err == nil {
				nested[lb.RemoteID] = n
			}
		}
	}
	return applied
}

// autoNested is the resolution auto-apply gives a card's world book: bind to
// its exact match or to the lorebook it's bound to when nothing differs, or
// import it when it's new.
func autoNested(lb ReconcileItem, books index) (Resolution, bool) {
	n := Resolution{RemoteID: lb.RemoteID, AppHash: lb.AppHash, Action: "bind", Default: merge.ChooseLibrary}
	switch {
	case lb.Verdict == VerdictNew && lb.Suggested != nil && lb.Suggested.Action == "import":
		return Resolution{RemoteID: lb.RemoteID, AppHash: lb.AppHash, BindingID: lb.BindingID, Action: "import"}, true
	case lb.Verdict == VerdictExact && lb.Suggested != nil && lb.Suggested.Action == "bind":
		n.CardID = lb.Suggested.CardID
	case lb.Verdict == VerdictBound && len(lb.Candidates) == 1 && lb.Candidates[0].DifferingFields == 0:
		b := books.byRemote[lb.RemoteID]
		if b.remoteState == remoteMissing {
			return n, false
		}
		n.CardID, n.AcceptedHash = b.entityID, b.acceptedHash
	default:
		return n, false
	}
	n.BaseRevisionID = books.byID[n.CardID].head
	return n, true
}
