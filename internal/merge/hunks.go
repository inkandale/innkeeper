package merge

import (
	"slices"

	diff3 "github.com/devsisters/go-diff3"
)

type Status string

const (
	Same        Status = "same"
	AutoLibrary Status = "auto_library"
	AutoApp     Status = "auto_app"
	Conflict    Status = "conflict"
	LibraryOnly Status = "library_only"
	AppOnly     Status = "app_only"
)

// span is a run of tokens on each side; anc is nil without an ancestor.
type span struct {
	status Status
	lib    []string
	app    []string
	anc    []string
}

// hunks diffs token lists into ordered spans covering lib and app exactly.
func hunks(lib, app, anc []string, hasAnc bool) []span {
	var out []span
	add := func(st Status, l, a, o []string) {
		if len(l) == 0 && len(a) == 0 && len(o) == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1].status == st {
			p := &out[n-1]
			p.lib = append(p.lib, l...)
			p.app = append(p.app, a...)
			p.anc = append(p.anc, o...)
			return
		}
		// clone so merging never writes into the caller's backing arrays
		out = append(out, span{st, slices.Clone(l), slices.Clone(a), slices.Clone(o)})
	}

	if hasAnc {
		for _, m := range diff3.Diff3MergeIndices(lib, anc, app) {
			l := lib[m.AOffset : m.AOffset+m.ALength]
			a := app[m.BOffset : m.BOffset+m.BLength]
			o := anc[m.OOffset : m.OOffset+m.OLength]
			st := Same
			switch m.Side {
			case diff3.SideA:
				st = AutoLibrary
			case diff3.SideB:
				st = AutoApp
			case diff3.SideConflict:
				if !slices.Equal(l, a) {
					st = Conflict
				}
			}
			add(st, l, a, o)
		}
		return out
	}

	used := 0 // DiffComm omits the trailing common run, so track lib consumed
	for _, d := range diff3.DiffComm(lib, app) {
		used += len(d.Common) + len(d.File1)
		switch {
		case len(d.File1) == 0 && len(d.File2) == 0:
			add(Same, d.Common, d.Common, nil)
		case len(d.File2) == 0:
			add(LibraryOnly, d.File1, nil, nil)
		case len(d.File1) == 0:
			add(AppOnly, nil, d.File2, nil)
		default:
			add(Conflict, d.File1, d.File2, nil)
		}
	}
	add(Same, lib[used:], lib[used:], nil)
	return out
}
