package targets

import "github.com/davidgibbons/innkeeper/internal/wire"

// The reconcile API types live in wire so the ink client can import them.
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
)

const ReconcileKind = wire.ReconcileKind
