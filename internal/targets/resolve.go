package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	Action  string `json:"action" enum:"bind,import,skip"`
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

// StaleError means the app item no longer hashes to the resolution's AppHash.
// Comparison is a fresh one to choose from again.
type StaleError struct{ Comparison Comparison }

func (e *StaleError) Error() string {
	return "the app's copy changed since it was compared; compare it again"
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
		err := s.pool.QueryRow(ctx, `SELECT `+k.col+` FROM binding
			WHERE instance_id = $1 AND remote_id = $2 AND `+k.col+` IS NOT NULL
			ORDER BY remote_state = 'missing', id LIMIT 1`, r.InstanceID, r.RemoteID).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return res, ErrNotFound
		} else if err != nil {
			return res, err
		}
	}
	res.CardID = id
	it, err := s.readItem(ctx, k, inst, rinfo, r.RemoteID, true)
	if remoteNotFound(err) {
		return res, fmt.Errorf("%w: the app has no %s %s", library.ErrNotFound, k.name, r.RemoteID)
	} else if err != nil {
		return res, err
	}
	if it.avatarErr != nil {
		return res, fmt.Errorf("app avatar: %w", it.avatarErr)
	}
	if it.hash != r.AppHash {
		c, err := s.compareItem(ctx, k, r.InstanceID, r.RemoteID, id)
		if err != nil {
			return res, err
		}
		return res, &StaleError{Comparison: c}
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
	var bindingID int64
	var boundRemote string
	var ancestor json.RawMessage
	err = tx.QueryRow(ctx, `SELECT id, remote_id, accepted_data FROM binding WHERE instance_id = $1 AND `+k.col+` = $2`,
		r.InstanceID, id).Scan(&bindingID, &boundRemote, &ancestor)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		ancestor = nil
	case err != nil:
		return res, err
	case boundRemote != r.RemoteID:
		return res, ErrBound
	}

	key := strconv.FormatInt(r.InstanceID, 10)
	overrides, err := object(headOverrides)
	if err != nil {
		return res, err
	}
	oldOverride, hadOverride := overrides[key]
	rendered := headData
	if hadOverride {
		if rendered, err = MergePatch(rendered, oldOverride); err != nil {
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
	// Only what was taken from the app reaches the base, so this instance's
	// override and the fields the target doesn't store stay out of it.
	base, err := patched(headData, lib, merged)
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
	avatar := ""
	if headAvatar != nil {
		avatar = *headAvatar
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
	revisionID := headID
	if baseChange != nil || overrideChanged || newAvatar != avatar {
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
	_, wantHash, err := Project(appCopy, k.fields(rinfo))
	if err != nil {
		return res, err
	}
	appBehind := wantHash != it.hash || newAvatar != it.avatar

	if bindingID != 0 {
		_, err = tx.Exec(ctx, `UPDATE binding SET remote_state = 'unchanged', accepted_data = $2, accepted_hash = $3,
				seen_data = NULL, checked_at = now(), version = version + 1
			WHERE id = $1`, bindingID, accepted, acceptedHash)
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
	// The app's copy is where the sources are, even if the target drops extensions.
	return res, k.entities(s.lib).DetectSources(ctx, id, it.raw)
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
