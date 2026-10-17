package targets

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/inkandale/innkeeper/internal/db"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/merge"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/protocol"
)

// validateResolution checks a resolution's shape, and its nested ones', before
// anything is read.
func validateResolution(r Resolution, k kind) error {
	bad := func(msg string) error { return fmt.Errorf("%w: %s: %s", library.ErrInvalid, r.RemoteID, msg) }
	switch {
	case r.RemoteID == "":
		return fmt.Errorf("%w: remote_id is required", library.ErrInvalid)
	case r.Kind != "" && r.Kind != k.Name:
		return bad("kind must be " + k.Name + " here")
	case r.VariantOf != 0 && r.Action != "import":
		return bad("variant_of needs action import")
	case r.Action == "skip":
	case r.Action != "bind" && r.Action != "import":
		return bad("action must be bind, import, or skip")
	case r.AppHash == "":
		return bad("app_hash is required")
	case r.Action == "bind" && r.BaseRevisionID == 0:
		return bad("base_revision_id is required")
	case k.isLorebook() && len(r.Lorebooks) > 0:
		return bad("a world book has no nested world books")
	}
	for _, n := range r.Lorebooks {
		if err := validateResolution(n, lorebookKind); err != nil {
			return err
		}
	}
	return nil
}

// ResolveResult is what a resolution did.
type ResolveResult struct {
	RemoteID  string `json:"remote_id"`
	Action    string `json:"action"`
	CardID    int64  `json:"card_id,omitempty"`
	BindingID int64  `json:"binding_id,omitempty"`
	// RevisionID is the new revision, or 0 when the library didn't change.
	RevisionID int64           `json:"revision_id,omitempty"`
	Lorebooks  []ResolveResult `json:"lorebooks,omitempty"`
}

// StaleError means the app item, the library head, or the binding changed
// since the comparison. Comparison is a fresh one to choose from again.
type StaleError struct {
	// Item is the item that went stale, as card:<remote_id> or lorebook:<remote_id>.
	Item       string     `json:"item"`
	Comparison Comparison `json:"comparison"`
}

func (e *StaleError) Error() string {
	return "the app's copy, the library, or the binding changed since the comparison; compare again"
}

// avatarChoice is the choice key for a card's avatar, which isn't a hunk.
const avatarChoice = "avatar"

// Resolve applies a resolution to an app character or world book.
func (s *Service) Resolve(ctx context.Context, k *library.Kind, r Resolution) (ResolveResult, error) {
	return s.resolve(ctx, targetKind(k), r, resolveOpts{})
}

// resolveOpts limit a resolve nobody reviewed hunk by hunk.
type resolveOpts struct {
	// bindOnly refuses the whole resolve if it would write to the library.
	bindOnly bool
	// importNew lets a bindOnly resolve import, which adds to the library
	// without changing anything already there.
	importNew bool
	// skipLeftOut skips the app's linked world books the resolution leaves
	// out, instead of taking their suggestions.
	skipLeftOut bool
}

// resolve applies r in one transaction.
func (s *Service) resolve(ctx context.Context, k kind, r Resolution, o resolveOpts) (ResolveResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ResolveResult{}, err
	}
	defer tx.Rollback(ctx)
	if err := waitForSync(ctx, tx); err != nil {
		return ResolveResult{}, err
	}
	rs := &resolver{s: s, tx: tx, books: map[string]ResolveResult{}, wrote: map[int64]string{}, resolveOpts: o}
	res, err := rs.item(ctx, k, r)
	if st := (*staleItem)(nil); errors.As(err, &st) {
		// Fresh, so it reads the library without this transaction's writes.
		_ = tx.Rollback(ctx)
		return res, st.fresh(ctx, s)
	}
	if err != nil {
		return res, err
	}
	return res, tx.Commit(ctx)
}

// resolver applies one call's resolutions in one transaction.
type resolver struct {
	s  *Service
	tx pgx.Tx
	// books are the world books resolved so far, by remote ID, so one that
	// several cards link is resolved once.
	books map[string]ResolveResult
	// index is the instance's lorebook index, loaded for the first left-out world book.
	index *index
	// wrote maps each lorebook this call bound or imported to the world book that did it.
	wrote map[int64]string
	resolveOpts
}

// staleItem is a StaleError before its fresh comparison, which is read after
// the transaction rolls back.
type staleItem struct {
	k          kind
	instanceID int64
	remoteID   string
	id         int64
}

func (e *staleItem) Error() string { return (&StaleError{}).Error() }

func (e *staleItem) fresh(ctx context.Context, s *Service) error {
	c, err := s.compareItem(ctx, e.k, e.instanceID, e.remoteID, e.id, false)
	if errors.Is(err, ErrNotFound) && e.id == 0 {
		// An import of an unbound item has no card to compare with.
		inst, info, err := s.describeReconcile(ctx, e.k, e.instanceID)
		if err != nil {
			return err
		}
		it, err := s.readItem(ctx, e.k, inst, info, e.remoteID, false)
		if err != nil {
			return err
		}
		c = Comparison{InstanceID: e.instanceID, RemoteID: e.remoteID, Base: "none", AppHash: it.hash}
	} else if err != nil {
		return err
	}
	return &StaleError{Item: e.k.Name + ":" + e.remoteID, Comparison: c}
}

// item applies one resolution, its world books first.
func (rs *resolver) item(ctx context.Context, k kind, r Resolution) (ResolveResult, error) {
	res := ResolveResult{RemoteID: r.RemoteID, Action: r.Action}
	if err := validateResolution(r, k); err != nil {
		return res, err
	}
	if r.Action == "skip" {
		return res, nil
	}
	if rs.bindOnly && !rs.importNew && r.Action == "import" {
		return res, errWouldRevise
	}
	if done, ok := rs.books[r.RemoteID]; ok && k.isLorebook() {
		return done, nil
	}
	s := rs.s
	inst, info, it, imported, err := s.importedItem(ctx, rs.tx, k, r.InstanceID, r.RemoteID, r.CardID)
	if err != nil {
		return res, err
	}
	if !imported {
		if inst, info, err = s.describePull(ctx, k, r.InstanceID); err != nil {
			return res, err
		}
	}
	rinfo := reconcileInfo(info)
	id := r.CardID
	if r.Action == "import" {
		id = 0
	} else if id == 0 {
		b, err := itemBinding(ctx, rs.tx, k, r.InstanceID, r.RemoteID, 0)
		if err != nil {
			return res, err
		}
		if id = b.entityID; id == 0 {
			return res, ErrNotFound
		}
	}
	stale := &staleItem{k: k, instanceID: r.InstanceID, remoteID: r.RemoteID, id: id}
	if !imported {
		if it, err = s.readItem(ctx, k, inst, rinfo, r.RemoteID, true); err != nil {
			return res, err
		}
	}
	if it.avatarErr != nil {
		return res, fmt.Errorf("app avatar: %w", it.avatarErr)
	}
	if it.hash != r.AppHash {
		return res, stale
	}
	if r.Action == "import" && r.BindingID == 0 {
		// Another app's import may have added this item since the comparison.
		m, ok, err := rs.exactMatch(ctx, k, inst, rinfo, r.InstanceID, it)
		if err != nil {
			return res, err
		}
		if ok {
			r.Action, r.CardID, r.BaseRevisionID, r.Default = "bind", m.id, m.head, merge.ChooseLibrary
			res.Action, id, stale.id = "bind", m.id, m.id
		}
	}
	var linked []int64
	if !k.isLorebook() {
		if res.Lorebooks, linked, err = rs.lorebooks(ctx, inst, rinfo, r, it.links); err != nil {
			return res, err
		}
	}
	if r.Action == "import" {
		if id, r.BaseRevisionID, err = rs.importItem(ctx, k, inst, info, r, it, stale); err != nil {
			return res, err
		}
		if r.VariantOf != 0 {
			if err := s.lib.Entities(k.Kind).SetVariantOfTx(ctx, rs.tx, id, &r.VariantOf); err != nil {
				return res, fmt.Errorf("link %s as a variant of %d: %w", r.RemoteID, r.VariantOf, err)
			}
		}
		res.RevisionID = r.BaseRevisionID
		// The new copy came from the app, so only what import left out
		// differs, and this app keeps it.
		r.Choices, r.Default, r.AcceptedHash = nil, merge.ChooseKeepInApp, ""
	}
	bound, err := rs.bind(ctx, k, info, rinfo, it, r, id, inst.Manifest.Has("push"))
	if r.Action == "import" && errors.Is(err, ErrBound) {
		// A new card can only clash on the item: a concurrent import bound it first.
		return res, stale
	}
	if err != nil {
		return res, err
	}
	res.CardID, res.BindingID = bound.CardID, bound.BindingID
	if bound.RevisionID != 0 {
		res.RevisionID = bound.RevisionID
	}
	for _, lb := range linked {
		if err := s.lib.LinkLorebookTx(ctx, rs.tx, id, lb); err != nil {
			return res, err
		}
	}
	if k.isLorebook() {
		rs.books[r.RemoteID] = res
		rs.wrote[res.CardID] = r.RemoteID
	}
	return res, nil
}

// lorebooks resolves a card's world books: the nested resolutions, then each
// world book the app links to the card that the owner left out, by its
// suggestion. It returns the lorebooks to link to the card.
func (rs *resolver) lorebooks(ctx context.Context, inst pluginhost.Instance, info protocol.TargetInfo, r Resolution,
	links []string) ([]ResolveResult, []int64, error) {
	todo := slices.Clone(r.Lorebooks)
	// Suggestions are all read before any world book is written.
	suggested := map[string]bool{}
	given := map[string]bool{}
	for _, n := range r.Lorebooks {
		given[n.RemoteID] = true
	}
	if info.Lorebooks == protocol.LorebooksStandalone {
		for _, id := range links {
			if given[id] {
				continue
			}
			given[id] = true
			if rs.skipLeftOut {
				todo = append(todo, Resolution{RemoteID: id, Action: "skip"})
				continue
			}
			n, err := rs.suggested(ctx, inst, info, r.InstanceID, id)
			if err != nil {
				return nil, nil, fmt.Errorf("world book %s: %w", id, err)
			}
			todo = append(todo, n)
			suggested[id] = true
		}
	}
	var out []ResolveResult
	var linked []int64
	for _, n := range todo {
		n.InstanceID = r.InstanceID
		res, err := rs.item(ctx, lorebookKind, n)
		if by, ok := rs.wrote[n.CardID]; ok && suggested[n.RemoteID] && (errors.As(err, new(*staleItem)) || errors.Is(err, ErrBound)) {
			// A fresh read can't see this call's writes, so a retry would suggest the same.
			return nil, nil, conflictError(fmt.Sprintf(
				"world book %s's suggested match #%d %s was changed by world book %s in this request; choose %s explicitly",
				n.RemoteID, n.CardID, rs.index.byID[n.CardID].name, by, n.RemoteID))
		}
		if err != nil {
			return nil, nil, fmt.Errorf("world book %s: %w", n.RemoteID, err)
		}
		out = append(out, res)
		if res.Action != "skip" {
			linked = append(linked, res.CardID)
		}
	}
	return out, linked, nil
}

// suggested is the resolution Reconcile would suggest for a world book, read
// fresh. A bound one with no drift binds again, which changes nothing.
func (rs *resolver) suggested(ctx context.Context, inst pluginhost.Instance, info protocol.TargetInfo, instanceID int64,
	remoteID string) (Resolution, error) {
	s := rs.s
	if rs.index == nil {
		idx, err := s.loadIndex(ctx, lorebookKind, inst, info)
		if err != nil {
			return Resolution{}, err
		}
		rs.index = &idx
	}
	p, err := withDefaults(ReconcileParams{InstanceID: instanceID})
	if err != nil {
		return Resolution{}, err
	}
	item, _, err := s.reconcileItem(ctx, lorebookKind, inst, info, *rs.index, p, remoteID)
	if err != nil {
		return Resolution{}, err
	}
	opt := item.Suggested
	if opt == nil {
		opt = &Option{Action: "skip"}
		// An item the app can't give back gets only skip, as in Reconcile.
		if b, ok := rs.index.byRemote[remoteID]; ok && item.Verdict != VerdictError {
			opt = &Option{Action: "bind", CardID: b.entityID}
		}
	}
	n := Resolution{RemoteID: remoteID, Action: opt.Action, AppHash: item.AppHash, BindingID: item.BindingID}
	if opt.Action == "bind" {
		c, err := s.compareItem(ctx, lorebookKind, instanceID, remoteID, opt.CardID, false)
		if err != nil {
			return Resolution{}, err
		}
		n.CardID, n.AppHash, n.BaseRevisionID, n.AcceptedHash = c.CardID, c.AppHash, c.HeadRevisionID, c.AcceptedHash
	}
	return n, nil
}

// importItem adds the app item to the library as a new card or lorebook and
// returns it with its head revision. A card it was bound to on this instance
// counts as missing from now on, as when an app reuses an ID. A live binding
// other than r.BindingID returns stale, so a repeated import can't add a copy.
func (rs *resolver) importItem(ctx context.Context, k kind, inst pluginhost.Instance, info protocol.TargetInfo,
	r Resolution, it appItem, stale error) (int64, int64, error) {
	s := rs.s
	instanceID, remoteID := r.InstanceID, r.RemoteID
	b, err := itemBinding(ctx, rs.tx, k, instanceID, remoteID, 0)
	if err != nil {
		return 0, 0, err
	}
	if b.remoteState == remoteMissing {
		b = boundItem{}
	}
	if b.id != r.BindingID {
		return 0, 0, stale
	}
	if b.id != 0 {
		// As a push of the old card would, so the two don't interleave.
		if err := lock(ctx, rs.tx, k, instanceID, b.entityID); err != nil {
			return 0, 0, err
		}
		// No row means a concurrent import let go of it while this one waited for the lock.
		tag, err := rs.tx.Exec(ctx, `UPDATE binding SET remote_state = 'missing', seen_data = NULL, version = version + 1
			WHERE id = $1 AND remote_state <> 'missing'`, b.id)
		if err != nil {
			return 0, 0, err
		}
		if tag.RowsAffected() == 0 {
			return 0, 0, stale
		}
	}
	plugin := inst.Manifest.Name
	provenance, source := plugin+":"+remoteID, "plugin:"+plugin
	if !k.isLorebook() {
		data := it.raw
		if info.Lorebooks == protocol.LorebooksStandalone && !slices.Contains(info.Fields, "character_book") {
			// The app's linked world books are the card's lorebooks there, resolved as nested items.
			if data, err = MergePatch(data, json.RawMessage(`{"character_book": null}`)); err != nil {
				return 0, 0, err
			}
		}
		card, err := library.WrapCard(data)
		if err != nil {
			return 0, 0, err
		}
		var bookSources []library.Source
		if _, _, book, err := library.SplitCard(card); err == nil && book != nil {
			// A plugin that failed was logged and skipped, since detection never fails a sync.
			if bookSources, _ = s.reg.DetectSources(ctx, s.plugins, library.Lorebook, book); ctx.Err() != nil {
				return 0, 0, ctx.Err()
			}
		}
		rev, err := s.lib.ImportCardTx(ctx, rs.tx, provenance, source, card, it.avatar, bookSources)
		return rev.EntityID, rev.ID, err
	}
	_, rev, err := s.lib.Lorebooks.CreateFromNameTx(ctx, rs.tx, cmp.Or(it.name, "lorebook"), provenance,
		library.Edit{Data: it.raw, Source: source, Message: "Imported"})
	return rev.EntityID, rev.ID, err
}

// exactMatch finds an unbound library item with the app item's content. It
// locks the item's name until the transaction ends, so concurrent imports from two apps see each other's copy.
func (rs *resolver) exactMatch(ctx context.Context, k kind, inst pluginhost.Instance, info protocol.TargetInfo,
	instanceID int64, it appItem) (indexEntry, bool, error) {
	if _, err := rs.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		fmt.Sprintf("import %s %s", k.Name, it.name)); err != nil {
		return indexEntry{}, false, err
	}
	ents, err := rs.s.loadEntries(ctx, k, inst, info, `e.archived_at IS NULL
		AND coalesce(r.overrides -> $1 ->> 'name', r.data ->> 'name') = $2
		AND NOT EXISTS (SELECT 1 FROM binding WHERE instance_id = $3 AND `+k.FK+` = e.id AND NOT imported)`, it.name, instanceID)
	if err != nil {
		return indexEntry{}, false, err
	}
	for _, e := range ents {
		if e.hash == it.hash {
			return e, true, nil
		}
	}
	return indexEntry{}, false, nil
}

// bind binds the app item to the card, taking each hunk from the side its
// choice names.
func (rs *resolver) bind(ctx context.Context, k kind, info, rinfo protocol.TargetInfo, it appItem, r Resolution,
	id int64, push bool) (ResolveResult, error) {
	s := rs.s
	res := ResolveResult{RemoteID: r.RemoteID, Action: r.Action, CardID: id}
	choices, takeAppAvatar, err := splitAvatar(k, r.Choices)
	if err != nil {
		return res, err
	}
	// A followed source is checked as review compares it, without character_book.
	acceptInfo := info
	if !push {
		acceptInfo = rinfo
	}
	accepted, acceptedHash, err := Project(it.raw, k.fields(acceptInfo))
	if err != nil {
		return res, err
	}
	named, err := s.reg.GetInstance(ctx, r.InstanceID)
	if err != nil {
		return res, err
	}
	if err := lock(ctx, rs.tx, k, r.InstanceID, id); err != nil {
		return res, err
	}
	books, err := rs.pushedLorebooks(ctx, k, info, it, id)
	if err != nil {
		return res, err
	}
	head, err := rs.head(ctx, k, id)
	if err != nil {
		return res, err
	}
	ancestor, err := rs.ancestor(ctx, k, r, id, head.id)
	if err != nil {
		return res, err
	}
	sameAvatar, err := rs.sameAvatar(ctx, r, it, head.avatar, takeAppAvatar)
	if err != nil {
		return res, err
	}
	cb, err := rs.entityBinding(ctx, k, r.InstanceID, id, r.RemoteID, it.imported)
	if err != nil {
		return res, err
	}
	plan, err := planBind(k, rinfo, bindInput{head: head, ancestor: ancestor, app: it, instanceID: r.InstanceID,
		choices: choices, def: r.Default, takeAppAvatar: takeAppAvatar, sameAvatar: sameAvatar})
	if err != nil {
		return res, err
	}

	// The app's copy is where the sources are, even if the target drops extensions,
	// and they are facts about the item, so a no-op records them too.
	if err := s.lib.Entities(k.Kind).RecordSourcesTx(ctx, rs.tx, id, it.sources); err != nil {
		return res, err
	}
	if !plan.revise && cb.remoteID == r.RemoteID && cb.state == remoteUnchanged && cb.accepted == acceptedHash {
		same, err := rs.samePush(ctx, cb.id, lastPush{revision: head.id, behind: plan.appBehind, books: books})
		if err != nil {
			return res, err
		}
		if same {
			// Nothing would change, so nothing is written.
			res.BindingID = cb.id
			return res, nil
		}
	}

	revisionID := head.id
	if plan.revise && rs.bindOnly {
		return res, errWouldRevise
	}
	if plan.revise {
		rev, err := s.lib.Entities(k.Kind).UpdateTx(ctx, rs.tx, id, head.id, library.Edit{Data: plan.base,
			AvatarBlob: plan.avatar, Overrides: plan.overrides, Source: "reconcile", Message: "Reconcile with " + named.Name})
		if err != nil {
			return res, err
		}
		revisionID, res.RevisionID = rev.ID, rev.ID
	}
	bindingID, err := rs.saveBinding(ctx, k, r, id, cb, accepted, acceptedHash, it.avatar, push)
	if err != nil {
		return res, err
	}
	_, err = rs.tx.Exec(ctx, `INSERT INTO push (binding_id, kind, revision_id, lorebook_revision_ids, lorebook_remote_ids,
			pushed_hash, remote_id, app_behind)
		VALUES ($1, 'bind', $2, $3, $4, $5, $6, $7)`,
		bindingID, revisionID, books.revisionIDs, books.remoteIDs, acceptedHash, r.RemoteID, plan.appBehind)
	if err != nil {
		return res, err
	}
	res.BindingID = bindingID
	return res, nil
}

// pushLorebooks are a push row's lorebook columns.
type pushLorebooks struct {
	revisionIDs []int64
	remoteIDs   []string
}

// pushedLorebooks are the card's lorebook columns as bind writes them.
func (rs *resolver) pushedLorebooks(ctx context.Context, k kind, info protocol.TargetInfo, it appItem,
	id int64) (pushLorebooks, error) {
	var books pushLorebooks
	if k.isLorebook() {
		return books, nil
	}
	switch info.Lorebooks {
	case protocol.LorebooksEmbedded:
		// In the transaction, so an import's split lorebook counts.
		err := rs.tx.QueryRow(ctx, `SELECT array(SELECT l.head_revision_id FROM card_lorebook cl
			JOIN lorebook l ON l.id = cl.lorebook_id WHERE cl.card_id = $1 ORDER BY l.head_revision_id)`, id).
			Scan(&books.revisionIDs)
		return books, err
	case protocol.LorebooksStandalone:
		books.remoteIDs = slices.Sorted(slices.Values(it.links))
		if books.remoteIDs == nil {
			books.remoteIDs = []string{}
		}
	}
	return books, nil
}

// headRow is a card or lorebook's head revision.
type headRow struct {
	id              int64
	data, overrides json.RawMessage
	// avatar is the avatar blob's sha256, or empty.
	avatar string
}

// head reads the entity's head revision.
func (rs *resolver) head(ctx context.Context, k kind, id int64) (headRow, error) {
	var h headRow
	var avatar *string
	err := rs.tx.QueryRow(ctx, `SELECT r.id, r.data, r.overrides, r.avatar_blob FROM `+k.Table+` e
		JOIN `+k.RevTable+` r ON r.id = e.head_revision_id WHERE e.id = $1`, id).
		Scan(&h.id, &h.data, &h.overrides, &avatar)
	if errors.Is(err, pgx.ErrNoRows) {
		return h, library.ErrNotFound
	}
	if avatar != nil {
		h.avatar = *avatar
	}
	return h, err
}

// ancestor is the merge's common copy, or nil for none. It returns stale
// unless the head and the item's binding are the ones r was compared against.
func (rs *resolver) ancestor(ctx context.Context, k kind, r Resolution, id, headID int64) (json.RawMessage, error) {
	item, err := itemBinding(ctx, rs.tx, k, r.InstanceID, r.RemoteID, id)
	if err != nil {
		return nil, err
	}
	ancestor := item.ancestor(id)
	ancestorHash := ""
	if ancestor != nil {
		ancestorHash = item.acceptedHash
	}
	// Hunk IDs and statuses come from the compared head and ancestor, so choices can't apply to others.
	if headID != r.BaseRevisionID || ancestorHash != r.AcceptedHash {
		return nil, &staleItem{k: k, instanceID: r.InstanceID, remoteID: r.RemoteID, id: id}
	}
	if r.Base == "none" {
		return nil, nil
	}
	return ancestor, nil
}

// sameAvatar says whether the app's avatar is the same picture as the
// library's, and refuses an avatar choice that can't apply.
func (rs *resolver) sameAvatar(ctx context.Context, r Resolution, it appItem, avatar string,
	takeAppAvatar bool) (bool, error) {
	same, err := rs.s.lib.SameImage(ctx, avatar, it.avatar)
	if err != nil {
		return false, err
	}
	if _, ok := r.Choices[avatarChoice]; ok && same {
		return false, fmt.Errorf("%w: %s", merge.ErrUnknownHunk, avatarChoice)
	}
	// An empty app avatar can't clear the library's.
	if takeAppAvatar && it.avatar == "" {
		return false, fmt.Errorf("%w: %s: the app has no avatar", merge.ErrBadChoice, avatarChoice)
	}
	return same, nil
}

// entityBinding is the entity's binding on the instance, if any.
type entityBinding struct {
	id                        int64
	remoteID, state, accepted string
	push                      bool
}

// entityBinding reads the entity's binding on the instance, its import
// binding when imported, and fails with ErrBound if it binds a different app
// item that isn't missing.
func (rs *resolver) entityBinding(ctx context.Context, k kind, instanceID, id int64,
	remoteID string, imported bool) (entityBinding, error) {
	var b entityBinding
	err := rs.tx.QueryRow(ctx, `SELECT id, remote_id, remote_state, accepted_hash, push FROM binding
		WHERE instance_id = $1 AND `+k.FK+` = $2 AND imported = $3`, instanceID, id, imported).
		Scan(&b.id, &b.remoteID, &b.state, &b.accepted, &b.push)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return b, err
	}
	// A card whose copy went missing can bind to the app's new one.
	if b.id != 0 && b.remoteID != remoteID && b.state != remoteMissing {
		return b, ErrBound
	}
	return b, nil
}

// bindInput is what a bind merges.
type bindInput struct {
	head     headRow
	ancestor json.RawMessage
	app      appItem
	// instanceID keys the instance's override in head.overrides.
	instanceID int64
	choices    map[string]merge.Choice
	def        merge.Choice
	// takeAppAvatar is the avatar choice; sameAvatar, whether the two avatars are the same picture.
	takeAppAvatar, sameAvatar bool
}

// bindPlan is the revision a bind would write, and what it leaves the app.
type bindPlan struct {
	// base is the new revision's data; overrides, its overrides, set only when revise.
	base, overrides json.RawMessage
	avatar          string
	// appBehind means the app's copy differs from the library's after the bind.
	appBehind bool
	// revise means the bind writes a new revision.
	revise bool
}

// planBind merges the app's copy into the head by the choices. Choices on a
// field the instance overrides change the override, never the base.
func planBind(k kind, info protocol.TargetInfo, in bindInput) (bindPlan, error) {
	var plan bindPlan
	key := strconv.FormatInt(in.instanceID, 10)
	overrides, err := object(in.head.overrides)
	if err != nil {
		return plan, err
	}
	oldOverride, hadOverride := overrides[key]
	rendered := in.head.data
	var pinned map[string]any
	if hadOverride {
		if rendered, err = MergePatch(rendered, oldOverride); err != nil {
			return plan, err
		}
		if pinned, err = decodeObject(oldOverride); err != nil {
			return plan, err
		}
	}
	lib, _, err := Project(rendered, k.fields(info))
	if err != nil {
		return plan, err
	}
	ancestor := in.ancestor
	if ancestor != nil {
		if ancestor, _, err = Project(ancestor, k.fields(info)); err != nil {
			return plan, err
		}
	}
	choices, err := keepPinnedLists(k, lib, in.app.data, ancestor, pinned, in.choices, in.def)
	if err != nil {
		return plan, err
	}
	merged, patch, err := merge.Apply(k.Schema, lib, in.app.data, ancestor, choices, in.def)
	if err != nil {
		return plan, err
	}
	forApp := merged
	if patch != nil {
		if forApp, err = MergePatch(merged, patch); err != nil {
			return plan, err
		}
	}
	toBase, err := restore(merged, lib, pinned)
	if err != nil {
		return plan, err
	}
	// Only what was taken from the app reaches the base, and fields the target
	// doesn't store stay as they are.
	if plan.base, err = patched(in.head.data, lib, toBase); err != nil {
		return plan, err
	}
	appCopy, err := patched(rendered, lib, forApp)
	if err != nil {
		return plan, err
	}
	// From the full data, so override values on fields the target doesn't store survive.
	newOverride, err := merge.Diff(plan.base, appCopy)
	if err != nil {
		return plan, err
	}
	plan.avatar = in.head.avatar
	// Taking the app's copy of the same picture would only add a revision.
	if in.takeAppAvatar && !in.sameAvatar {
		plan.avatar = in.app.avatar
	}
	baseChange, err := merge.Diff(in.head.data, plan.base)
	if err != nil {
		return plan, err
	}
	overrideChanged := hadOverride != (newOverride != nil)
	if hadOverride && newOverride != nil {
		d, err := merge.Diff(oldOverride, newOverride)
		if err != nil {
			return plan, err
		}
		overrideChanged = d != nil
	}
	_, wantHash, err := Project(appCopy, k.fields(info))
	if err != nil {
		return plan, err
	}
	plan.appBehind = wantHash != in.app.hash || (plan.avatar != in.app.avatar && !in.sameAvatar)
	plan.revise = baseChange != nil || overrideChanged || plan.avatar != in.head.avatar
	if !plan.revise {
		return plan, nil
	}
	if newOverride == nil {
		delete(overrides, key)
	} else {
		overrides[key] = newOverride
	}
	plan.overrides, err = json.Marshal(overrides)
	return plan, err
}

// lastPush is a push row's record of what the app was left with.
type lastPush struct {
	revision int64
	behind   bool
	books    pushLorebooks
}

// samePush says whether the binding's last push recorded want, so a bind that
// changes nothing needn't write.
func (rs *resolver) samePush(ctx context.Context, bindingID int64, want lastPush) (bool, error) {
	var last lastPush
	err := rs.tx.QueryRow(ctx, `SELECT revision_id, app_behind, lorebook_revision_ids, lorebook_remote_ids FROM push
		WHERE binding_id = $1 ORDER BY id DESC LIMIT 1`, bindingID).
		Scan(&last.revision, &last.behind, &last.books.revisionIDs, &last.books.remoteIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return last.revision == want.revision && last.behind == want.behind &&
		slices.Equal(last.books.revisionIDs, want.books.revisionIDs) &&
		slices.Equal(last.books.remoteIDs, want.books.remoteIDs) &&
		(last.books.remoteIDs == nil) == (want.books.remoteIDs == nil), nil
}

// saveBinding points the entity's binding cb, or a new one, at the app item as
// accepted, with appAvatar accepted too if it follows a source, and returns its
// ID. push is false for a new binding on a target that only follows site
// sources. It fails with ErrBound if another binding has the item.
func (rs *resolver) saveBinding(ctx context.Context, k kind, r Resolution, id int64, cb entityBinding,
	accepted json.RawMessage, acceptedHash, appAvatar string, push bool) (int64, error) {
	// A binding keeps the push it was made with, even if the plugin's capabilities change.
	if cb.id != 0 {
		push = cb.push
	}
	// Stored as Check compares it, "" for none, so the next check finds it accepted.
	var avatar *string
	if !push {
		key, err := rs.s.lib.AvatarKey(ctx, appAvatar)
		if err != nil {
			return 0, err
		}
		avatar = &key
	}
	bindingID := cb.id
	var err error
	if bindingID != 0 {
		_, err = rs.tx.Exec(ctx, `UPDATE binding SET remote_id = $2, remote_state = 'unchanged', accepted_data = $3,
				accepted_hash = $4, seen_data = NULL, seen_avatar = NULL, checked_at = now(), version = version + 1,
				avatar_hash = $5, check_failures = 0, check_error = NULL
			WHERE id = $1`, bindingID, r.RemoteID, accepted, acceptedHash, avatar)
	} else {
		err = rs.tx.QueryRow(ctx, `INSERT INTO binding (instance_id, `+k.FK+`, remote_id, remote_state, accepted_data,
				accepted_hash, checked_at, avatar_hash, push)
			VALUES ($1, $2, $3, 'unchanged', $4, $5, now(), $6, $7) RETURNING id`,
			r.InstanceID, id, r.RemoteID, accepted, acceptedHash, avatar, push).Scan(&bindingID)
	}
	if db.IsCode(err, db.UniqueViolation) {
		return 0, ErrBound
	}
	return bindingID, err
}

// splitAvatar takes the avatar choice out of choices, since it isn't a hunk,
// and says whether the app's avatar wins. Only an explicit choice takes it:
// an app that doesn't send avatars would otherwise clear the library's.
func splitAvatar(k kind, choices map[string]merge.Choice) (map[string]merge.Choice, bool, error) {
	c, ok := choices[avatarChoice]
	if !ok {
		return choices, false, nil
	}
	if k.isLorebook() {
		return nil, false, fmt.Errorf("%w: %s", merge.ErrUnknownHunk, avatarChoice)
	}
	if c != merge.ChooseLibrary && c != merge.ChooseApp {
		// An override can't hold an image.
		return nil, false, fmt.Errorf("%w: %s", merge.ErrBadChoice, avatarChoice)
	}
	rest := map[string]merge.Choice{}
	for id, ch := range choices {
		if id != avatarChoice {
			rest[id] = ch
		}
	}
	return rest, c == merge.ChooseApp, nil
}

// patched is full with the change from projected to want applied.
func patched(full, projected, want json.RawMessage) (json.RawMessage, error) {
	d, err := merge.Diff(projected, want)
	if err != nil || d == nil {
		return full, err
	}
	return MergePatch(full, d)
}

// restore puts back lib's value, or its absence, in merged at each leaf of
// the override, so only what the override sets is reverted.
func restore(merged, lib json.RawMessage, override map[string]any) (json.RawMessage, error) {
	if len(override) == 0 {
		return merged, nil
	}
	m, err := decodeObject(merged)
	if err != nil {
		return nil, err
	}
	l, err := decodeObject(lib)
	if err != nil {
		return nil, err
	}
	restoreLeaves(m, l, override)
	return json.Marshal(m)
}

func restoreLeaves(to, from, override map[string]any) {
	for k, v := range override {
		om, ook := v.(map[string]any)
		tm, tok := to[k].(map[string]any)
		fm, fok := from[k].(map[string]any)
		switch fv, ok := from[k]; {
		case ook && tok && fok:
			restoreLeaves(tm, fm, om)
		case ok:
			to[k] = fv
		default:
			delete(to, k)
		}
	}
}

// keepPinnedLists turns keep_in_app on a list field this instance overrides
// into app: the override already holds the whole list, and app on a pinned
// field changes only the override, so it's the same choice.
func keepPinnedLists(k kind, lib, app, ancestor json.RawMessage, pinned map[string]any,
	choices map[string]merge.Choice, def merge.Choice) (map[string]merge.Choice, error) {
	if len(pinned) == 0 {
		return choices, nil
	}
	fields, err := merge.Compare(k.Schema, lib, app, ancestor)
	if err != nil {
		return nil, err
	}
	out := maps.Clone(choices)
	if out == nil {
		out = map[string]merge.Choice{}
	}
	for _, f := range fields {
		// By schema, since a list the override deletes compares as a scalar.
		if _, ok := pinned[f.Path]; !ok || k.Schema[f.Path] != merge.List {
			continue
		}
		for _, h := range f.Hunks {
			if c, ok := out[h.ID]; c == merge.ChooseKeepInApp || !ok && def == merge.ChooseKeepInApp {
				out[h.ID] = merge.ChooseApp
			}
		}
	}
	return out, nil
}

// decodeObject decodes a JSON object, keeping numbers as written.
func decodeObject(raw json.RawMessage) (map[string]any, error) {
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("not a JSON object")
	}
	return m, nil
}

// errWouldRevise means a bind-only resolve would have changed the library.
var errWouldRevise = errors.New("the resolve would change the library, which only the owner can choose")

// conflictError is a library.ErrConflict that reads as its own explanation.
type conflictError string

func (e conflictError) Error() string { return string(e) }
func (e conflictError) Unwrap() error { return library.ErrConflict }
