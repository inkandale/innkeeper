package merge

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Choice resolves a conflict, library_only or app_only hunk.
type Choice string

const (
	ChooseLibrary   Choice = "library"
	ChooseApp       Choice = "app"
	ChooseKeepInApp Choice = "keep_in_app" // library in base, app in the override
	ChooseBoth      Choice = "both"        // library then app; list hunks only
)

var (
	ErrUnknownHunk = errors.New("unknown hunk")
	ErrBadChoice   = errors.New("bad choice")
)

// Apply rebuilds the data from the hunks Compare reports for the same inputs.
// Hunks not in choices take def (empty means library); choices on same and
// auto_* hunks are ignored, since those merge by status. base is the new
// library data; override is the RFC 7386 merge patch that turns base into
// what this app should get, or nil when they are equal.
func Apply(
	s Schema, library, app, ancestor json.RawMessage, choices map[string]Choice, def Choice,
) (base, override json.RawMessage, err error) {
	switch def {
	case "":
		def = ChooseLibrary
	case ChooseLibrary, ChooseApp, ChooseKeepInApp:
	default:
		return nil, nil, fmt.Errorf("%w: default %q", ErrBadChoice, def)
	}
	c := &cmp{choices: choices, def: def}
	fs, v, err := c.walk(s, library, app, ancestor)
	if err != nil {
		return nil, nil, err
	}
	known := map[string]bool{}
	for _, f := range fs {
		for _, h := range f.Hunks {
			known[h.ID] = true
		}
	}
	var unknown []string
	for id, ch := range choices {
		switch {
		case !known[id]:
			unknown = append(unknown, id)
		case ch != ChooseLibrary && ch != ChooseApp && ch != ChooseKeepInApp && ch != ChooseBoth:
			c.bad = append(c.bad, id)
		}
	}
	if err := errors.Join(listed(ErrUnknownHunk, unknown), listed(ErrBadChoice, c.bad)); err != nil {
		return nil, nil, err
	}
	b, a := v.base.(map[string]any), v.app.(map[string]any)
	if p := patch(b, a); p != nil {
		override = enc(p)
	}
	return enc(b), override, nil
}

func listed(err error, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	slices.Sort(ids)
	return fmt.Errorf("%w: %s", err, strings.Join(slices.Compact(ids), ", "))
}

// patch is the merge patch turning from into to, or nil when they are equal.
// ponytail: RFC 7386 reads null as delete, so an app-side null value is lost;
// needs a richer patch format
func patch(from, to map[string]any) map[string]any {
	p := map[string]any{}
	for k := range to {
		f, ok := from[k]
		fm, fok := f.(map[string]any)
		tm, tok := to[k].(map[string]any)
		switch {
		case fok && tok:
			if sub := patch(fm, tm); sub != nil {
				p[k] = sub
			}
		case !ok || !same(f, to[k]):
			p[k] = to[k]
		}
	}
	for k := range from {
		if _, ok := to[k]; !ok {
			p[k] = nil
		}
	}
	if len(p) == 0 {
		return nil
	}
	return p
}
