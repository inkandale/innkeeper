package merge

import (
	"fmt"
	"slices"
	"strings"
)

// entries matches lorebook entries by identity so reordering is not a change.
func (c *cmp) entries(path string, l, a, o []any) {
	lib, app, anc := index(l), index(a), index(o)
	var ids []string
	for _, e := range lib {
		ids = append(ids, e.id)
	}
	for _, e := range app {
		if _, gone := find(lib, e.id).(missing); gone {
			ids = append(ids, e.id)
		}
	}
	for _, id := range ids {
		p := fmt.Sprintf("%s[%s]", path, id)
		lv, av, ov := find(lib, id), find(app, id), find(anc, id)
		lm, lok := lv.(map[string]any)
		am, aok := av.(map[string]any)
		if !lok || !aok {
			c.scalar(p, lv, av, ov)
			continue
		}
		om, _ := ov.(map[string]any)
		for _, k := range keys(lm, am) {
			c.field(p+"."+k, entry[k], get(lm, k), get(am, k), get(om, k))
		}
	}
}

type ident struct {
	id string
	v  any
}

// index names each entry by its comment when unique on this side, else its
// sorted keys; repeats get a counter so every entry stays addressable.
func index(es []any) []ident {
	comments := map[string]int{}
	for _, e := range es {
		m, _ := e.(map[string]any)
		if s, _ := m["comment"].(string); s != "" {
			comments[s]++
		}
	}
	seen := map[string]int{}
	out := make([]ident, 0, len(es))
	for _, e := range es {
		m, _ := e.(map[string]any)
		id, _ := m["comment"].(string)
		if id == "" || comments[id] > 1 {
			ks, _ := m["keys"].([]any)
			var ss []string
			for _, k := range ks {
				if s, ok := k.(string); ok {
					ss = append(ss, s)
				}
			}
			slices.Sort(ss)
			id = strings.Join(ss, "\x1f")
		}
		if seen[id]++; seen[id] > 1 {
			id = fmt.Sprintf("%s\x1f%d", id, seen[id])
		}
		out = append(out, ident{id, e})
	}
	return out
}

func find(es []ident, id string) any {
	for _, e := range es {
		if e.id == id {
			return e.v
		}
	}
	return missing{}
}
