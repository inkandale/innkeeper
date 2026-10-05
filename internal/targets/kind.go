package targets

import (
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/protocol"
)

// kind is what a binding syncs: a card or a lorebook.
type kind struct {
	name string
	// col is the binding column that holds the entity's ID.
	col            string
	entities       func(*library.Library) *library.Entities
	put, get, list string
}

var (
	cardKind = kind{"card", "card_id", func(l *library.Library) *library.Entities { return l.Cards },
		protocol.MethodTargetPut, protocol.MethodTargetGet, protocol.MethodTargetList}
	lorebookKind = kind{"lorebook", "lorebook_id", func(l *library.Library) *library.Entities { return l.Lorebooks },
		protocol.MethodTargetPutLorebook, protocol.MethodTargetGetLorebook, protocol.MethodTargetListLorebooks}
)

func (k kind) isLorebook() bool { return k.name == lorebookKind.name }

// fields are the data keys the target keeps for this kind.
func (k kind) fields(info protocol.TargetInfo) []string {
	if k.isLorebook() {
		return info.LorebookFields
	}
	return info.Fields
}
