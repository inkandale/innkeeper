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
	lib, err := decode(library)
	if err != nil {
		return nil, fmt.Errorf("library: %w", err)
	}
	ap, err := decode(app)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}
	var anc map[string]any
	if len(ancestor) > 0 {
		if anc, err = decode(ancestor); err != nil {
			return nil, fmt.Errorf("ancestor: %w", err)
		}
	}
	var out []Field
	for _, k := range keys(lib, ap) {
		c := cmp{hasAnc: anc != nil, out: []Hunk{}}
		kind := c.field(k, s[k], get(lib, k), get(ap, k), get(anc, k))
		out = append(out, Field{Path: k, Kind: kind, Hunks: c.out})
	}
	return out, nil
}

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
	hasAnc bool
	out    []Hunk
}

// field diffs one value as kind k and returns the kind actually used, since
// a value of the wrong type falls back to scalar.
func (c *cmp) field(path string, k Kind, l, a, o any) Kind {
	switch k {
	case Prose:
		if ls, ok := str(l); ok {
			if as, ok := str(a); ok {
				if os, ok := str(o); ok {
					c.prose(path, ls, as, os)
					return Prose
				}
			}
		}
	case List:
		if ls, ok := arr(l); ok {
			if as, ok := arr(a); ok {
				if os, ok := arr(o); ok {
					c.list(path, ls, as, os)
					return List
				}
			}
		}
	case JSON:
		c.json(path, l, a, o)
		return JSON
	case Entries:
		if ls, ok := arr(l); ok {
			if as, ok := arr(a); ok {
				if os, ok := arr(o); ok {
					c.entries(path, ls, as, os)
					return Entries
				}
			}
		}
	}
	c.scalar(path, l, a, o)
	return Scalar
}

func (c *cmp) prose(path, l, a, o string) {
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
		c.out = append(c.out, h)
	}
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

func (c *cmp) list(path string, l, a, o []any) {
	toks := func(vs []any) []string {
		var out []string
		for _, v := range vs {
			out = append(out, string(enc(v)))
		}
		return out
	}
	array := func(ts []string) json.RawMessage {
		if len(ts) == 0 {
			return nil
		}
		return json.RawMessage("[" + strings.Join(ts, ",") + "]")
	}
	for i, s := range hunks(toks(l), toks(a), toks(o), c.hasAnc) {
		c.out = append(c.out, Hunk{
			ID:      fmt.Sprintf("%s#%d", path, i),
			Status:  s.status,
			Library: array(s.lib),
			App:     array(s.app),
			Base:    array(s.anc),
		})
	}
}

// json recurses while both sides are non-empty objects; anything else is a
// leaf so each nested key can be picked separately.
func (c *cmp) json(path string, l, a, o any) {
	lm, lok := l.(map[string]any)
	am, aok := a.(map[string]any)
	if !lok || !aok || len(lm)+len(am) == 0 {
		c.scalar(path, l, a, o)
		return
	}
	om, _ := o.(map[string]any)
	for _, k := range keys(lm, am) {
		c.json(path+"."+k, get(lm, k), get(am, k), get(om, k))
	}
}

func (c *cmp) scalar(path string, l, a, o any) {
	h := Hunk{ID: path + "#0", Status: c.status(l, a, o)}
	if _, gone := l.(missing); !gone {
		h.Library = enc(l)
	}
	if _, gone := a.(missing); !gone {
		h.App = enc(a)
	}
	if _, gone := o.(missing); c.hasAnc && !gone {
		h.Base = enc(o)
	}
	c.out = append(c.out, h)
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
	if _, gone := a.(missing); gone {
		return LibraryOnly
	}
	if _, gone := l.(missing); gone {
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
