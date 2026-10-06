package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/merge"
	"github.com/davidgibbons/innkeeper/protocol"
)

// Resolution decides one app item: bind it to a library card, import it, or
// skip it.
type Resolution struct {
	InstanceID int64  `json:"instance_id"`
	RemoteID   string `json:"remote_id"`
	// AppHash is the comparison's app_hash; a different live copy is stale.
	AppHash string `json:"app_hash"`
	// BaseRevisionID is the comparison's head_revision_id; a moved head is stale.
	BaseRevisionID int64 `json:"base_revision_id"`
	// AcceptedHash is the comparison's accepted_hash, empty for an item that
	// wasn't bound to the card; a binding that changed since is stale.
	AcceptedHash string `json:"accepted_hash"`
	Action       string `json:"action" enum:"bind,import,skip"`
	// CardID is any library card or lorebook for bind; 0 means the bound one.
	CardID int64 `json:"card_id,omitempty"`
	// Choices maps hunk IDs, and avatar, to a choice.
	Choices map[string]merge.Choice `json:"choices,omitempty"`
	// Default is the choice for hunks not in Choices; empty means library.
	Default   merge.Choice `json:"default,omitempty"`
	Lorebooks []Resolution `json:"lorebooks,omitempty"`
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
type StaleError struct{ Comparison Comparison }

func (e *StaleError) Error() string {
	return "the app's copy, the library, or the binding changed since the comparison; compare again"
}

// avatarChoice is the choice key for a card's avatar, which isn't a hunk.
const avatarChoice = "avatar"

// Resolve applies a resolution to an app character.
func (s *Service) Resolve(ctx context.Context, r Resolution) (ResolveResult, error) {
	return s.resolve(ctx, cardKind, r)
}

// ResolveLorebook applies a resolution to an app world book.
func (s *Service) ResolveLorebook(ctx context.Context, r Resolution) (ResolveResult, error) {
	return s.resolve(ctx, lorebookKind, r)
}

func (s *Service) resolve(ctx context.Context, k kind, r Resolution) (ResolveResult, error) {
	switch {
	case len(r.Lorebooks) > 0:
		return ResolveResult{}, fmt.Errorf("%w: nested lorebook resolutions aren't supported yet", library.ErrInvalid)
	case r.Action == "bind" && r.BaseRevisionID == 0:
		return ResolveResult{}, fmt.Errorf("%w: base_revision_id is required", library.ErrInvalid)
	case r.Action == "bind":
		return s.resolveBind(ctx, k, r)
	}
	return ResolveResult{}, fmt.Errorf("%w: action must be bind", library.ErrInvalid)
}

// resolveBind binds the app item to the card, taking each hunk from the side
// its choice names, in one transaction.
func (s *Service) resolveBind(ctx context.Context, k kind, r Resolution) (ResolveResult, error) {
	res := ResolveResult{RemoteID: r.RemoteID, Action: r.Action}
	inst, info, err := s.describePull(ctx, k, r.InstanceID)
	if err != nil {
		return res, err
	}
	rinfo := reconcileInfo(info)
	choices, takeAppAvatar, err := splitAvatar(k, r.Choices)
	if err != nil {
		return res, err
	}
	id := r.CardID
	if id == 0 {
		b, err := itemBinding(ctx, s.pool, k, r.InstanceID, r.RemoteID)
		if err != nil {
			return res, err
		}
		if id = b.entityID; id == 0 {
			return res, ErrNotFound
		}
	}
	res.CardID = id
	it, err := s.readItem(ctx, k, inst, rinfo, r.RemoteID, true)
	if err != nil {
		return res, err
	}
	if it.avatarErr != nil {
		return res, fmt.Errorf("app avatar: %w", it.avatarErr)
	}
	stale := func() error {
		c, err := s.compareItem(ctx, k, r.InstanceID, r.RemoteID, id)
		if err != nil {
			return err
		}
		return &StaleError{Comparison: c}
	}
	if it.hash != r.AppHash {
		return res, stale()
	}
	accepted, acceptedHash, err := Project(it.raw, k.fields(info))
	if err != nil {
		return res, err
	}
	// Lorebook columns as bind writes them.
	var lorebooks []int64
	var lorebookRemoteIDs []string
	if !k.isLorebook() {
		switch info.Lorebooks {
		case protocol.LorebooksEmbedded:
			if _, lorebooks, err = s.lib.CardBook(ctx, id); err != nil {
				return res, err
			}
		case protocol.LorebooksStandalone:
			lorebookRemoteIDs = slices.Sorted(slices.Values(it.links))
			if lorebookRemoteIDs == nil {
				lorebookRemoteIDs = []string{}
			}
		}
	}
	named, err := s.reg.GetInstance(ctx, r.InstanceID)
	if err != nil {
		return res, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx)
	if err := lock(ctx, tx, k, r.InstanceID, id); err != nil {
		return res, err
	}
	var headID int64
	var headData, headOverrides json.RawMessage
	var headAvatar *string
	err = tx.QueryRow(ctx, `SELECT r.id, r.data, r.overrides, r.avatar_blob FROM `+k.table+` e
		JOIN `+k.revTable+` r ON r.id = e.head_revision_id WHERE e.id = $1`, id).
		Scan(&headID, &headData, &headOverrides, &headAvatar)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, library.ErrNotFound
	} else if err != nil {
		return res, err
	}
	item, err := itemBinding(ctx, tx, k, r.InstanceID, r.RemoteID)
	if err != nil {
		return res, err
	}
	ancestor := item.ancestor(id)
	ancestorHash := ""
	if ancestor != nil {
		ancestorHash = item.acceptedHash
	}
	// Hunk IDs and statuses come from the compared head and ancestor, so choices can't apply to others.
	if headID != r.BaseRevisionID || ancestorHash != r.AcceptedHash {
		_ = tx.Rollback(ctx)
		return res, stale()
	}
	avatar := ""
	if headAvatar != nil {
		avatar = *headAvatar
	}
	if _, ok := r.Choices[avatarChoice]; ok && avatar == it.avatar {
		return res, fmt.Errorf("%w: %s", merge.ErrUnknownHunk, avatarChoice)
	}
	// An empty app avatar can't clear the library's.
	if takeAppAvatar && it.avatar == "" {
		return res, fmt.Errorf("%w: %s: the app has no avatar", merge.ErrBadChoice, avatarChoice)
	}
	var cardBinding struct {
		id                        int64
		remoteID, state, accepted string
	}
	err = tx.QueryRow(ctx, `SELECT id, remote_id, remote_state, accepted_hash FROM binding
		WHERE instance_id = $1 AND `+k.col+` = $2`, r.InstanceID, id).
		Scan(&cardBinding.id, &cardBinding.remoteID, &cardBinding.state, &cardBinding.accepted)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return res, err
	}
	// A card whose copy went missing can bind to the app's new one.
	if cardBinding.id != 0 && cardBinding.remoteID != r.RemoteID && cardBinding.state != remoteMissing {
		return res, ErrBound
	}

	key := strconv.FormatInt(r.InstanceID, 10)
	overrides, err := object(headOverrides)
	if err != nil {
		return res, err
	}
	oldOverride, hadOverride := overrides[key]
	rendered := headData
	var pinned map[string]any
	if hadOverride {
		if rendered, err = MergePatch(rendered, oldOverride); err != nil {
			return res, err
		}
		if pinned, err = decodeObject(oldOverride); err != nil {
			return res, err
		}
	}
	lib, _, err := Project(rendered, k.fields(rinfo))
	if err != nil {
		return res, err
	}
	if ancestor != nil {
		if ancestor, _, err = Project(ancestor, k.fields(rinfo)); err != nil {
			return res, err
		}
	}
	if choices, err = keepPinnedLists(k, lib, it.data, ancestor, pinned, choices, r.Default); err != nil {
		return res, err
	}
	merged, patch, err := merge.Apply(k.schema(), lib, it.data, ancestor, choices, r.Default)
	if err != nil {
		return res, err
	}
	forApp := merged
	if patch != nil {
		if forApp, err = MergePatch(merged, patch); err != nil {
			return res, err
		}
	}
	// In a field or setting this instance overrides, choices change the override, never the base.
	toBase, err := restore(merged, lib, pinned)
	if err != nil {
		return res, err
	}
	// Only what was taken from the app reaches the base, and fields the target
	// doesn't store stay as they are.
	base, err := patched(headData, lib, toBase)
	if err != nil {
		return res, err
	}
	appCopy, err := patched(rendered, lib, forApp)
	if err != nil {
		return res, err
	}
	// From the full data, so override values on fields the target doesn't store survive.
	newOverride, err := merge.Diff(base, appCopy)
	if err != nil {
		return res, err
	}
	newAvatar := avatar
	if takeAppAvatar {
		newAvatar = it.avatar
	}
	baseChange, err := merge.Diff(headData, base)
	if err != nil {
		return res, err
	}
	overrideChanged := hadOverride != (newOverride != nil)
	if hadOverride && newOverride != nil {
		d, err := merge.Diff(oldOverride, newOverride)
		if err != nil {
			return res, err
		}
		overrideChanged = d != nil
	}
	_, wantHash, err := Project(appCopy, k.fields(rinfo))
	if err != nil {
		return res, err
	}
	appBehind := wantHash != it.hash || newAvatar != it.avatar
	revise := baseChange != nil || overrideChanged || newAvatar != avatar

	if !revise && cardBinding.remoteID == r.RemoteID && cardBinding.state == remoteUnchanged &&
		cardBinding.accepted == acceptedHash {
		var last struct {
			revision int64
			behind   bool
			books    []int64
			links    []string
		}
		err := tx.QueryRow(ctx, `SELECT revision_id, app_behind, lorebook_revision_ids, lorebook_remote_ids FROM push
			WHERE binding_id = $1 ORDER BY id DESC LIMIT 1`, cardBinding.id).
			Scan(&last.revision, &last.behind, &last.books, &last.links)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return res, err
		}
		if err == nil && last.revision == headID && last.behind == appBehind && slices.Equal(last.books, lorebooks) &&
			slices.Equal(last.links, lorebookRemoteIDs) && (last.links == nil) == (lorebookRemoteIDs == nil) {
			// Nothing would change, so nothing is written.
			res.BindingID = cardBinding.id
			return res, nil
		}
	}

	// The app's copy is where the sources are, even if the target drops extensions.
	if err := k.entities(s.lib).DetectSourcesTx(ctx, tx, id, it.raw); err != nil {
		return res, err
	}
	revisionID := headID
	if revise {
		if newOverride == nil {
			delete(overrides, key)
		} else {
			overrides[key] = newOverride
		}
		raw, err := json.Marshal(overrides)
		if err != nil {
			return res, err
		}
		rev, err := k.entities(s.lib).UpdateTx(ctx, tx, id, headID, library.Edit{Data: base, AvatarBlob: newAvatar,
			Overrides: raw, Source: "reconcile", Message: "Reconcile with " + named.Name})
		if err != nil {
			return res, err
		}
		revisionID, res.RevisionID = rev.ID, rev.ID
	}
	bindingID := cardBinding.id
	if bindingID != 0 {
		_, err = tx.Exec(ctx, `UPDATE binding SET remote_id = $2, remote_state = 'unchanged', accepted_data = $3,
				accepted_hash = $4, seen_data = NULL, checked_at = now(), version = version + 1
			WHERE id = $1`, bindingID, r.RemoteID, accepted, acceptedHash)
	} else {
		err = tx.QueryRow(ctx, `INSERT INTO binding (instance_id, `+k.col+`, remote_id, remote_state, accepted_data,
				accepted_hash, checked_at)
			VALUES ($1, $2, $3, 'unchanged', $4, $5, now()) RETURNING id`,
			r.InstanceID, id, r.RemoteID, accepted, acceptedHash).Scan(&bindingID)
	}
	if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return res, ErrBound
	} else if err != nil {
		return res, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO push (binding_id, kind, revision_id, lorebook_revision_ids, lorebook_remote_ids,
			pushed_hash, remote_id, app_behind)
		VALUES ($1, 'bind', $2, $3, $4, $5, $6, $7)`,
		bindingID, revisionID, lorebooks, lorebookRemoteIDs, acceptedHash, r.RemoteID, appBehind)
	if err != nil {
		return res, err
	}
	if err := tx.Commit(ctx); err != nil {
		return res, err
	}
	res.BindingID = bindingID
	return res, nil
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
	fields, err := merge.Compare(k.schema(), lib, app, ancestor)
	if err != nil {
		return nil, err
	}
	out := maps.Clone(choices)
	if out == nil {
		out = map[string]merge.Choice{}
	}
	for _, f := range fields {
		// By schema, since a list the override deletes compares as a scalar.
		if _, ok := pinned[f.Path]; !ok || k.schema()[f.Path] != merge.List {
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
