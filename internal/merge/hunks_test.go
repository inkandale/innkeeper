package merge

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func statuses(sp []span) []Status {
	var out []Status
	for _, s := range sp {
		out = append(out, s.status)
	}
	return out
}

func check(t *testing.T, lib, app, anc string, hasAnc bool, want ...Status) []span {
	t.Helper()
	sp := hunks(sentences(lib), sentences(app), sentences(anc), hasAnc)
	if got := statuses(sp); !slices.Equal(got, want) {
		t.Errorf("statuses = %v, want %v", got, want)
	}
	var l, a []string
	for _, s := range sp {
		if len(s.lib) == 0 && len(s.app) == 0 {
			t.Errorf("empty span %v", s)
		}
		l = append(l, s.lib...)
		a = append(a, s.app...)
	}
	if strings.Join(l, "") != lib || strings.Join(a, "") != app {
		t.Errorf("sides do not rebuild inputs: lib %q app %q", l, a)
	}
	return sp
}

func TestHunksNoAncestor(t *testing.T) {
	check(t, "One. Two. Three.", "One. Deux. Three.", "", false, Same, Conflict, Same)
	check(t, "One. Two. ", "One. Two. Three.", "", false, Same, AppOnly)
	check(t, "One. Two. Three.", "One. Two. ", "", false, Same, LibraryOnly)
	check(t, "One. Two.", "One. Two.", "", false, Same)
}

func TestHunksAncestor(t *testing.T) {
	anc := "A. B. C. D. "
	check(t, "A2. B. C. D. ", "A. B. C2. D. ", anc, true, AutoLibrary, Same, AutoApp, Same)
	// both sides made the same edit
	check(t, "A. X. C. ", "A. X. C. ", "A. B. C. ", true, Same)
	sp := check(t, "A. L. C. ", "A. R. C. ", "A. B. C. ", true, Same, Conflict, Same)
	if !reflect.DeepEqual(sp[1].anc, []string{"B. "}) {
		t.Errorf("conflict anc = %q", sp[1].anc)
	}
}

func TestHunksEmpty(t *testing.T) {
	check(t, "", "", "", false)
	check(t, "", "One.", "", false, AppOnly)
	check(t, "One.", "", "", false, LibraryOnly)
	check(t, "", "", "", true)
	check(t, "", "One.", "", true, AutoApp)
	check(t, "", "One.", "One.", true, AutoLibrary)
}
