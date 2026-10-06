package merge

import (
	"fmt"
	"slices"
	"strings"
)

// entries matches lorebook entries by identity so reordering is not a change.
func (c *cmp) entries(path string, l, a, o []any) val {
	libIDs, lib := index(l)
	appIDs, app := index(a)
	_, anc := index(o)
	ids := libIDs
	for _, id := range appIDs {
		if _, ok := lib[id]; !ok {
			ids = append(ids, id)
		}
	}
	base, forApp := []any{}, []any{}
	for _, id := range ids {
		p := fmt.Sprintf("%s[%s]", path, id)
		lv, av, ov := get(lib, id), get(app, id), get(anc, id)
		lm, lok := lv.(map[string]any)
		am, aok := av.(map[string]any)
		v := val{map[string]any{}, map[string]any{}}
		if !lok || !aok {
			v = c.scalar(p, lv, av, ov)
		} else {
			om, _ := ov.(map[string]any)
			for _, k := range keys(lm, am) {
				kp, kl, ka, ko := p+"."+esc(k), get(lm, k), get(am, k), get(om, k)
				var kv val
				if k == "keys" || k == "secondary_keys" {
					// apps treat key lists as sets, so only membership is a change
					kv = c.leaf(kp, c.status(sorted(kl), sorted(ka), sorted(ko)), kl, ka, ko)
				} else {
					_, kv = c.field(kp, entry[k], kl, ka, ko)
				}
				put(v.base.(map[string]any), k, kv.base)
				put(v.app.(map[string]any), k, kv.app)
			}
		}
		if _, gone := v.base.(missing); !gone {
			base = append(base, v.base)
		}
		if _, gone := v.app.(missing); !gone {
			forApp = append(forApp, v.app)
		}
	}
	return val{base, forApp}
}

// emptied drops an emptied entries list where one side lacked the key, unless
// the library had it empty (no change, so the library's form stays).
func emptied(v, l, a any) any {
	if len(v.([]any)) > 0 {
		return v
	}
	_, lgone := l.(missing)
	_, agone := a.(missing)
	if lgone || agone && !empty(l) {
		return missing{}
	}
	return v
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
			// ponytail: reordered same-identity entries show as content conflicts; pair them by best content match
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
