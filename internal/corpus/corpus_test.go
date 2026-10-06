package corpus_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/cardhash"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/merge"
	"github.com/davidgibbons/innkeeper/internal/targets"
)

// maxAvatarDistance is reconcile's default avatar_distance.
const maxAvatarDistance = 8

type codecGolden struct {
	Card        json.RawMessage `json:"card"`
	Lorebook    json.RawMessage `json:"lorebook"`
	AvatarPhash string          `json:"avatar_phash"`
}

type targetGolden struct {
	Fields         []string          `json:"fields"`
	LorebookFields []string          `json:"lorebook_fields"`
	Card           json.RawMessage   `json:"card"`
	Lorebooks      []json.RawMessage `json:"lorebooks"`
	AvatarPhash    string            `json:"avatar_phash"`
}

// TestCorpusHashes checks that each app gives back every corpus card, and its
// lorebook and avatar, as the codec read it.
func TestCorpusHashes(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join(corpusDir, "*", "source.*"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no corpus sources: %v", err)
	}
	for _, src := range dirs {
		dir := filepath.Dir(src)
		for _, tg := range servers {
			t.Run(filepath.Base(dir)+"/"+tg.name, func(t *testing.T) {
				if b, err := os.ReadFile(filepath.Join(dir, "codec.error")); err == nil {
					t.Skipf("the codec can't read it: %s", b)
				}
				if b, err := os.ReadFile(filepath.Join(dir, tg.name+".error")); err == nil {
					t.Fatalf("capture failed: %s", b)
				}
				var codec codecGolden
				var app targetGolden
				readJSON(t, filepath.Join(dir, "codec.json"), &codec)
				readJSON(t, filepath.Join(dir, tg.name+".json"), &app)

				if len(app.Fields) == 0 || len(app.LorebookFields) == 0 {
					t.Fatalf("golden has fields %v and lorebook_fields %v; recapture", app.Fields, app.LorebookFields)
				}
				// SplitCard strips these, so check the app never returns them.
				var raw struct {
					Data struct {
						Extensions map[string]json.RawMessage `json:"extensions"`
					} `json:"data"`
				}
				if err := json.Unmarshal(app.Card, &raw); err != nil {
					t.Fatal(err)
				}
				for _, k := range []string{"fav", "world"} {
					if v, ok := raw.Data.Extensions[k]; ok {
						t.Errorf("app card has extensions.%s = %s", k, v)
					}
				}
				_, want, _, err := library.SplitCard(codec.Card)
				if err != nil {
					t.Fatal(err)
				}
				_, got, _, err := library.SplitCard(app.Card)
				if err != nil {
					t.Fatal(err)
				}
				compare(t, "card", merge.Card, want, got, app.Fields)

				var books []json.RawMessage
				if len(codec.Lorebook) > 0 && string(codec.Lorebook) != "null" {
					books = append(books, codec.Lorebook)
				}
				if len(app.Lorebooks) != len(books) {
					t.Fatalf("app has %d lorebooks, codec has %d", len(app.Lorebooks), len(books))
				}
				for i := range books {
					compare(t, "lorebook", merge.Lorebook, books[i], app.Lorebooks[i], app.LorebookFields)
				}

				compareAvatars(t, codec.AvatarPhash, app.AvatarPhash)
			})
		}
	}
}

// compare projects both sides to the target's fields, as sync does, and
// requires the same hash and only same hunks.
func compare(t *testing.T, what string, schema merge.Schema, want, got json.RawMessage, fields []string) {
	t.Helper()
	wantP, wantHash, err := targets.Project(want, fields)
	if err != nil {
		t.Fatal(err)
	}
	gotP, gotHash, err := targets.Project(got, fields)
	if err != nil {
		t.Fatal(err)
	}
	if wantHash != gotHash {
		var w, g map[string]json.RawMessage
		_ = json.Unmarshal(wantP, &w)
		_ = json.Unmarshal(gotP, &g)
		for _, f := range fields {
			if fieldHash(t, f, w[f]) != fieldHash(t, f, g[f]) {
				t.Errorf("%s %s differs:\ncodec: %s\napp:   %s", what, f, w[f], g[f])
			}
		}
		t.Errorf("%s hash: codec %s, app %s", what, wantHash, gotHash)
	}
	diff, err := merge.Compare(schema, wantP, gotP, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range diff {
		for _, h := range f.Hunks {
			if h.Status != merge.Same {
				t.Errorf("%s %s hunk %s is %s:\ncodec: %s\napp:   %s", what, f.Path, h.ID, h.Status, h.Library, h.App)
			}
		}
	}
}

func fieldHash(t *testing.T, field string, v json.RawMessage) string {
	t.Helper()
	raw, err := json.Marshal(map[string]json.RawMessage{field: v})
	if err != nil {
		t.Fatal(err)
	}
	h, err := cardhash.ContentHash(raw)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func compareAvatars(t *testing.T, want, got string) {
	t.Helper()
	if want == "" || got == "" {
		if want != got {
			t.Errorf("avatar phash: codec %q, app %q", want, got)
		}
		return
	}
	w, errW := strconv.ParseUint(want, 16, 64)
	g, errG := strconv.ParseUint(got, 16, 64)
	if err := errors.Join(errW, errG); err != nil {
		t.Fatal(err)
	}
	if d := cardhash.Distance(int64(w), int64(g)); d > maxAvatarDistance {
		t.Errorf("avatar phash distance %d over %d: codec %s, app %s", d, maxAvatarDistance, want, got)
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}
