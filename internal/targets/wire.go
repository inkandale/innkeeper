package targets

import "github.com/inkandale/innkeeper/internal/wire"

// The reconcile and sync API types live in wire so the ink client can import them.
type (
	ReconcileMatch  = wire.ReconcileMatch
	ReconcileParams = wire.ReconcileParams
	Snapshot        = wire.Snapshot
	ReconcileItem   = wire.ReconcileItem
	Candidate       = wire.Candidate
	Option          = wire.Option
	Comparison      = wire.Comparison
	AvatarDiff      = wire.AvatarDiff
	Resolution      = wire.Resolution
	Change          = wire.Change
	Push            = wire.Push
	Binding         = wire.Binding
	Preview         = wire.Preview
	SyncParams      = wire.SyncParams
	SyncResult      = wire.SyncResult
	SyncApp         = wire.SyncApp
	AppChange       = wire.AppChange
	ChangeRow       = wire.ChangeRow
	Changes         = wire.Changes
	ChangesFilter   = wire.ChangesFilter
)

const (
	ReconcileKind = wire.ReconcileKind
	ApplyObvious  = wire.ApplyObvious
	ApplyNone     = wire.ApplyNone
	SyncKind      = wire.SyncKind

	ChangeDiffers     = wire.ChangeDiffers
	ChangeBothChanged = wire.ChangeBothChanged
	ChangeAhead       = wire.ChangeAhead
	ChangeMissing     = wire.ChangeMissing
	ChangeError       = wire.ChangeError
	ChangeNew         = wire.ChangeNew
	ChangeUpdate      = wire.ChangeUpdate
)
