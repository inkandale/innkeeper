package merge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

type Hunk struct {
	ID      string          `json:"id"`
	Status  Status          `json:"status"`
	Library json.RawMessage `json:"library,omitempty"`
	App     json.RawMessage `json:"app,omitempty"`
	Base    json.RawMessage `json:"base,omitempty"`
	Words   []Span          `json:"words,omitempty"` // prose conflicts only; display only
}

// Span is a changed byte range in one side's hunk text.
type Span struct {
	Side  string `json:"side"` // library or app
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type Field struct {
	Path  string `json:"path"`
	Kind  Kind   `json:"kind"`
	Hunks []Hunk `json:"hunks"`
}

// missing marks a key absent on one side, as distinct from null.
type missing struct{}

// Compare lists every field present on either side, sorted by path.
// ancestor is nil when there is none.
func Compare(s Schema, library, app, ancestor json.RawMessage) ([]Field, error) {
	fs, _, err := (&cmp{}).walk(s, library, app, ancestor)
	return fs, err
}

// walk diffs every field and rebuilds the data from the hunks c picks.
func (c *cmp) walk(s Schema, library, app, ancestor json.RawMessage) ([]Field, val, error) {
	lib, err := decode(library)
	if err != nil {
		return nil, val{}, fmt.Errorf("library: %w", err)
	}
	ap, err := decode(app)
	if err != nil {
		return nil, val{}, fmt.Errorf("app: %w", err)
	}
	var anc map[string]any
	if len(ancestor) > 0 {
		if anc, err = decode(ancestor); err != nil {
			return nil, val{}, fmt.Errorf("ancestor: %w", err)
		}
	}
	c.hasAnc = anc != nil
	var out []Field
	seen := map[string]bool{}
	base, forApp := map[string]any{}, map[string]any{}
	for _, k := range keys(lib, ap) {
		c.out = []Hunk{}
		kind, v := c.field(esc(k), s[k], get(lib, k), get(ap, k), get(anc, k))
		for _, h := range c.out {
			if seen[h.ID] {
				return nil, val{}, fmt.Errorf("duplicate hunk ID %q", h.ID)
			}
			seen[h.ID] = true
		}
		out = append(out, Field{Path: esc(k), Kind: kind, Hunks: c.out})
		put(base, k, v.base)
		put(forApp, k, v.app)
	}
	return out, val{base, forApp}, nil
}

// esc backslash-escapes the characters that structure a hunk ID, so any key
// or identity yields a unique path.
var esc = strings.NewReplacer(`\`, `\\`, `.`, `\.`, `[`, `\[`, `]`, `\]`, `#`, `\#`, `,`, `\,`, `~`, `\~`).Replace

func decode(raw json.RawMessage) (map[string]any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("trailing data after object")
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("not a JSON object")
	}
	return m, nil
}

type cmp struct {
	hasAnc  bool
	out     []Hunk
	choices map[string]Choice
	def     Choice   // "" takes the library
	inList  bool     // inside a list or entries field, where keep_in_app is refused
	bad     []string // hunks given a choice their kind can't take
}

// val is a value rebuilt for base and for the app; missing{} drops the key.
type val struct{ base, app any }

func put(m map[string]any, k string, v any) {
	if _, gone := v.(missing); !gone {
		m[k] = v
	}
}

// take picks the side(s) whose tokens or value a hunk contributes.
type take uint8

const (
	takeLib take = 1 << iota
	takeApp
)

// add records h and returns what it contributes to base and to the app.
func (c *cmp) add(h Hunk, list bool) (base, app take) {
	c.out = append(c.out, h)
	switch h.Status {
	case Same, AutoLibrary:
		return takeLib, takeLib
	case AutoApp:
		return takeApp, takeApp
	}
	ch, ok := c.choices[h.ID]
	if !ok {
		ch = c.def
	}
	switch ch {
	case ChooseApp:
		return takeApp, takeApp
	case ChooseKeepInApp:
		if !c.inList {
			return takeLib, takeApp
		}
		if ok {
			c.bad = append(c.bad, h.ID)
		}
	case ChooseBoth:
		if list {
			return takeLib | takeApp, takeLib | takeApp
		}
		c.bad = append(c.bad, h.ID)
	}
	return takeLib, takeLib
}

func tokens(t take, lib, app []string) []string {
	var out []string
	if t&takeLib != 0 {
		out = append(out, lib...)
	}
	if t&takeApp != 0 {
		out = append(out, app...)
	}
	return out
}

// field diffs one value as kind k and returns the kind actually used, since
// a value of the wrong type falls back to scalar.
func (c *cmp) field(path string, k Kind, l, a, o any) (Kind, val) {
	if k == List || k == Entries {
		// the override would hold the whole array, pinning it for this app
		defer func(was bool) { c.inList = was }(c.inList)
		c.inList = true
	}
	_, lgone := l.(missing)
	_, agone := a.(missing)
	if (k == Prose || k == List) && lgone != agone {
		// a whole-value hunk keeps "absent" distinct from "empty" for Apply
		if empty(l) && empty(a) {
			return Scalar, c.leaf(path, Same, l, a, o)
		}
		return Scalar, c.scalar(path, l, a, o)
	}
	switch k {
	case Prose:
		if ls, ok := str(l); ok {
			if as, ok := str(a); ok {
				if os, ok := str(o); ok {
					return Prose, c.prose(path, ls, as, os)
				}
			}
		}
	case List:
		if ls, ok := arr(l); ok {
			if as, ok := arr(a); ok {
				if os, ok := arr(o); ok {
					return List, c.list(path, ls, as, os)
				}
			}
		}
	case JSON:
		return JSON, c.json(path, l, a, o)
	case Entries:
		if ls, ok := arr(l); ok {
			if as, ok := arr(a); ok {
				if os, ok := arr(o); ok {
					v := c.entries(path, ls, as, os)
					return Entries, val{emptied(v.base, l, a), emptied(v.app, l, a)}
				}
			}
		}
	}
	return Scalar, c.scalar(path, l, a, o)
}

func (c *cmp) prose(path, l, a, o string) val {
	var base, app strings.Builder
	for i, s := range hunks(sentences(l), sentences(a), sentences(o), c.hasAnc) {
		lt, at, ot := strings.Join(s.lib, ""), strings.Join(s.app, ""), strings.Join(s.anc, "")
		h := Hunk{ID: fmt.Sprintf("%s#%d", path, i), Status: s.status}
		if lt != "" {
			h.Library = enc(lt)
		}
		if at != "" {
			h.App = enc(at)
		}
		if ot != "" {
			h.Base = enc(ot)
		}
		if s.status == Conflict {
			h.Words = wordSpans(lt, at)
		}
		tb, ta := c.add(h, false)
		base.WriteString(strings.Join(tokens(tb, s.lib, s.app), ""))
		app.WriteString(strings.Join(tokens(ta, s.lib, s.app), ""))
	}
	return val{base.String(), app.String()}
}

func wordSpans(l, a string) []Span {
	var out []Span
	lo, ao := 0, 0
	for _, s := range hunks(words(l), words(a), nil, false) {
		ln, an := len(strings.Join(s.lib, "")), len(strings.Join(s.app, ""))
		if s.status != Same {
			if ln > 0 {
				out = append(out, Span{"library", lo, lo + ln})
			}
			if an > 0 {
				out = append(out, Span{"app", ao, ao + an})
			}
		}
		lo, ao = lo+ln, ao+an
	}
	return out
}

func (c *cmp) list(path string, l, a, o []any) val {
	byTok := map[string]any{}
	toks := func(vs []any) []string {
		var out []string
		for _, v := range vs {
			t := string(enc(v))
			byTok[t] = v
			out = append(out, t)
		}
		return out
	}
	values := func(ts []string) []any {
		out := []any{}
		for _, t := range ts {
			out = append(out, byTok[t])
		}
		return out
	}
	array := func(ts []string) json.RawMessage {
		if len(ts) == 0 {
			return nil
		}
		return json.RawMessage("[" + strings.Join(ts, ",") + "]")
	}
	var base, app []string
	for i, s := range hunks(toks(l), toks(a), toks(o), c.hasAnc) {
		tb, ta := c.add(Hunk{
			ID:      fmt.Sprintf("%s#%d", path, i),
			Status:  s.status,
			Library: array(s.lib),
			App:     array(s.app),
			Base:    array(s.anc),
		}, true)
		base = append(base, tokens(tb, s.lib, s.app)...)
		app = append(app, tokens(ta, s.lib, s.app)...)
	}
	return val{values(base), values(app)}
}

// json recurses while both sides are non-empty objects; anything else is a
// leaf so each nested key can be picked separately.
func (c *cmp) json(path string, l, a, o any) val {
	lm, lok := l.(map[string]any)
	am, aok := a.(map[string]any)
	if !lok || !aok || len(lm)+len(am) == 0 {
		return c.scalar(path, l, a, o)
	}
	om, _ := o.(map[string]any)
	base, app := map[string]any{}, map[string]any{}
	for _, k := range keys(lm, am) {
		v := c.json(path+"."+esc(k), get(lm, k), get(am, k), get(om, k))
		put(base, k, v.base)
		put(app, k, v.app)
	}
	return val{base, app}
}

func (c *cmp) scalar(path string, l, a, o any) val {
	return c.leaf(path, c.status(l, a, o), l, a, o)
}

func (c *cmp) leaf(path string, st Status, l, a, o any) val {
	h := Hunk{ID: path + "#0", Status: st}
	if _, gone := l.(missing); !gone {
		h.Library = enc(l)
	}
	if _, gone := a.(missing); !gone {
		h.App = enc(a)
	}
	if _, gone := o.(missing); c.hasAnc && !gone {
		h.Base = enc(o)
	}
	tb, ta := c.add(h, false)
	side := func(t take) any {
		if t == takeApp {
			return a
		}
		return l
	}
	return val{side(tb), side(ta)}
}

func (c *cmp) status(l, a, o any) Status {
	switch {
	case same(l, a):
		return Same
	case c.hasAnc && same(a, o):
		return AutoLibrary
	case c.hasAnc && same(l, o):
		return AutoApp
	}
	_, lgone := l.(missing)
	_, agone := a.(missing)
	if _, ogone := o.(missing); c.hasAnc && !ogone && (lgone || agone) {
		return Conflict // one side deleted what the other changed
	}
	switch {
	case agone:
		return LibraryOnly
	case lgone:
		return AppOnly
	}
	return Conflict
}

func same(x, y any) bool {
	_, xm := x.(missing)
	_, ym := y.(missing)
	if xm || ym {
		return xm == ym
	}
	return bytes.Equal(enc(x), enc(y))
}

// enc is canonical compact JSON (sorted keys) without HTML escaping, which
// card text is full of.
func enc(v any) json.RawMessage {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(v) // decoded JSON always re-encodes
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

func get(m map[string]any, k string) any {
	if v, ok := m[k]; ok {
		return v
	}
	return missing{}
}

func keys(a, b map[string]any) []string {
	ks := slices.Collect(maps.Keys(a))
	for k := range b {
		if _, ok := a[k]; !ok {
			ks = append(ks, k)
		}
	}
	slices.Sort(ks)
	return ks
}

// empty reports a missing value, "" or [].
func empty(v any) bool {
	switch x := v.(type) {
	case missing:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	}
	return false
}

// str and arr read a value as that type, with missing as the empty value.
func str(v any) (string, bool) {
	if _, ok := v.(missing); ok {
		return "", true
	}
	s, ok := v.(string)
	return s, ok
}

func arr(v any) ([]any, bool) {
	if _, ok := v.(missing); ok {
		return nil, true
	}
	s, ok := v.([]any)
	return s, ok
}
