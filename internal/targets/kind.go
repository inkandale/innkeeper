package targets

import (
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/protocol"
)

// kind is what a binding syncs: a library kind, with the target methods that
// carry it.
type kind struct {
	*library.Kind
	put, get, list, delete string
	// fields are the data keys the target keeps for this kind.
	fields func(protocol.TargetInfo) []string
}

var (
	cardKind = kind{library.Card, protocol.MethodTargetPut, protocol.MethodTargetGet, protocol.MethodTargetList, protocol.MethodTargetDelete,
		func(info protocol.TargetInfo) []string { return info.Fields }}
	lorebookKind = kind{library.Lorebook, protocol.MethodTargetPutLorebook, protocol.MethodTargetGetLorebook, protocol.MethodTargetListLorebooks,
		protocol.MethodTargetDeleteLorebook,
		func(info protocol.TargetInfo) []string { return info.LorebookFields }}
)

// targetKind is the kind that syncs k.
func targetKind(k *library.Kind) kind {
	if k == library.Lorebook {
		return lorebookKind
	}
	return cardKind
}

// kindNamed is the kind called name: card unless name is another.
func kindNamed(name string) kind {
	k, _ := library.KindNamed(name)
	return targetKind(k)
}

func (k kind) isLorebook() bool { return k.Kind == library.Lorebook }
