package merge

import (
	"fmt"
	"slices"
	"strings"
)

// entries matches lorebook entries by identity so reordering is not a change.
func (c *cmp) entries(path string, l, a, o []any) {
	libIDs, lib := index(l)
	appIDs, app := index(a)
	_, anc := index(o)
	ids := libIDs
	for _, id := range appIDs {
		if _, ok := lib[id]; !ok {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		p := fmt.Sprintf("%s[%s]", path, id)
		lv, av, ov := get(lib, id), get(app, id), get(anc, id)
		lm, lok := lv.(map[string]any)
		am, aok := av.(map[string]any)
		if !lok || !aok {
			c.scalar(p, lv, av, ov)
			continue
		}
		om, _ := ov.(map[string]any)
		for _, k := range keys(lm, am) {
			kp, kl, ka, ko := p+"."+esc(k), get(lm, k), get(am, k), get(om, k)
			if k == "keys" || k == "secondary_keys" {
				// apps treat key lists as sets, so only membership is a change
				c.leaf(kp, c.status(sorted(kl), sorted(ka), sorted(ko)), kl, ka, ko)
				continue
			}
			c.field(kp, entry[k], kl, ka, ko)
		}
	}
}

// index names each entry c:<comment> when the comment is unique on this side,
// else k:<sorted keys>; repeats get ~n so every entry stays addressable.
func index(es []any) ([]string, map[string]any) {
	comments := map[string]int{}
	for _, e := range es {
		m, _ := e.(map[string]any)
		if s, _ := m["comment"].(string); s != "" {
			comments[s]++
		}
	}
	ids := make([]string, 0, len(es))
	byID := make(map[string]any, len(es))
	for _, e := range es {
		m, _ := e.(map[string]any)
		comment, _ := m["comment"].(string)
		// ponytail: a comment unique on one side only splits into
		// library_only + app_only; match by comment when unique on either side
		id := "c:" + esc(comment)
		if comment == "" || comments[comment] > 1 {
			ks, _ := m["keys"].([]any)
			var ss []string
			for _, k := range ks {
				if s, ok := k.(string); ok {
					ss = append(ss, esc(s))
				}
			}
			slices.Sort(ss)
			id = "k:" + strings.Join(ss, ",")
		}
		base := id
		for n := 2; ; n++ {
			if _, dup := byID[id]; !dup {
				break
			}
			id = fmt.Sprintf("%s~%d", base, n)
		}
		ids = append(ids, id)
		byID[id] = e
	}
	return ids, byID
}

// sorted orders an array's elements by their JSON; other values pass through.
func sorted(v any) any {
	xs, ok := v.([]any)
	if !ok {
		return v
	}
	xs = slices.Clone(xs)
	slices.SortFunc(xs, func(x, y any) int { return strings.Compare(string(enc(x)), string(enc(y))) })
	return xs
}
