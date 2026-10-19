package main

import (
	"cmp"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/inkandale/innkeeper/cardhash"
	"github.com/inkandale/innkeeper/protocol"
)

// The fake catalog. Each mode breaks one rule the contract kit checks:
// bad-catalog describes nothing, catalog-repeat serves the first page for
// every cursor, catalog-lax accepts any filter, catalog-no-notfound answers
// unknown IDs with -32000, and catalog-wrong-hash reports a content_hash
// that doesn't match the data. catalog-sourceless checks params, then
// answers like a catalog whose source isn't configured.
// Config down fails every call but describe with a retryable error, and
// failing fails every call but describe with an error that names a server path.
var catalogInfo = protocol.CatalogInfo{
	Kinds:   []string{protocol.KindCard, protocol.KindLorebook},
	Filters: []protocol.CatalogFilter{{Name: "tag", Label: "Tag", Type: protocol.FilterText}},
	Sorts:   []protocol.CatalogOption{{Value: "name", Label: "Name"}},
}

var noSource = protocol.Fail("config.source is not set", false)

type catalogEntry struct {
	id, kind string
	tags     []string
	// versions holds each version's data, oldest first; version n is
	// versions[n-1].
	versions []string
}

// Sorted by name, the only sort.
var catalogEntries = []catalogEntry{
	{"brakka", protocol.KindCard, []string{"dwarf"}, []string{
		`{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Brakka", "description": "A dwarf blacksmith."}}`,
		`{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Brakka", "description": "A grumpy dwarf blacksmith who runs the forge at the edge of town.", "extensions": {"fav": true}, "character_book": {"name": "Forge Songs", "entries": [{"keys": ["forge"], "content": "Hammer songs keep the forge lit."}]}}}`,
	}},
	{"carrow", protocol.KindCard, []string{"elf"}, []string{
		`{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Carrow", "description": "A cheerful elven bard who sings of the sea."}}`,
	}},
	{"forge-lore", protocol.KindLorebook, []string{"dwarf"}, []string{
		`{"name": "Forge Lore", "entries": [{"keys": ["forge"], "content": "The forge at the edge of town never cools."}]}`,
	}},
}

// hashed is the part of an item's data the library hashes: a card's data
// as cardhash.CardData returns it, or the whole lorebook.
func hashed(kind, raw string) json.RawMessage {
	if kind != protocol.KindCard {
		return json.RawMessage(raw)
	}
	data, _, _ := cardhash.CardData(json.RawMessage(raw))
	return data
}

func (e catalogEntry) item(mode string) protocol.CatalogItem {
	data := hashed(e.kind, e.versions[len(e.versions)-1])
	if mode == "catalog-wrong-hash" {
		data = json.RawMessage(`{"name": "Wrong"}`)
	}
	var named struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(data, &named)
	hash, _ := cardhash.ContentHash(data)
	it := protocol.CatalogItem{ID: e.id, Kind: e.kind, Name: cmp.Or(named.Name, e.id), Tags: e.tags,
		LatestVersion: strconv.Itoa(len(e.versions)), Hashes: protocol.CatalogHashes{ContentHash: hash}}
	simhash := cardhash.CardSimhash
	if e.kind == protocol.KindLorebook {
		simhash = cardhash.LorebookSimhash
	}
	if h, ok, _ := simhash(data); ok {
		it.Hashes.TextSimhash = &h
	}
	return it
}

func catalogSearch(params json.RawMessage, mode string) (any, error) {
	var p protocol.CatalogSearchParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	if mode != "catalog-lax" {
		if err := catalogInfo.CheckSearch(p); err != nil {
			return nil, protocol.InvalidParams(err)
		}
	}
	if mode == "catalog-sourceless" {
		return nil, noSource
	}
	var tag string
	_ = json.Unmarshal(p.Filters["tag"], &tag)
	items := []protocol.CatalogItem{}
	for _, e := range catalogEntries {
		it := e.item(mode)
		if (p.Kind == "" || p.Kind == e.kind) && (tag == "" || slices.Contains(e.tags, tag)) &&
			strings.Contains(strings.ToLower(it.Name), strings.ToLower(p.Q)) {
			items = append(items, it)
		}
	}
	start, _ := strconv.Atoi(p.Page.Cursor)
	if mode == "catalog-repeat" {
		start = 0
	}
	start = min(max(start, 0), len(items))
	end := min(start+cmp.Or(p.Page.Size, protocol.MaxCatalogPage), len(items))
	res := protocol.CatalogSearchResult{Items: items[start:end]}
	if end < len(items) {
		res.Next = strconv.Itoa(end)
	}
	return res, nil
}

func catalogGet(blobTmp string, config map[string]any, params json.RawMessage, mode string) (any, error) {
	var p protocol.CatalogGetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	if mode == "catalog-sourceless" {
		return nil, noSource
	}
	i := slices.IndexFunc(catalogEntries, func(e catalogEntry) bool { return e.id == p.ID })
	if i < 0 {
		code := protocol.CodeRemoteNotFound
		if mode == "catalog-no-notfound" {
			code = -32000
		}
		return nil, protocol.NewError(code, "no item "+p.ID, false)
	}
	e := catalogEntries[i]
	n := len(e.versions)
	if p.Version != "" {
		var err error
		if n, err = strconv.Atoi(p.Version); err != nil || n < 1 || n > len(e.versions) {
			return nil, protocol.NewError(protocol.CodeRemoteNotFound, "no version "+p.Version, false)
		}
	}
	res := protocol.CatalogGetResult{Item: e.item(mode), Version: strconv.Itoa(n), Data: json.RawMessage(e.versions[n-1])}
	for v := len(e.versions); v >= 1; v-- {
		res.Versions = append(res.Versions, protocol.CatalogVersion{Version: strconv.Itoa(v)})
	}
	if p.Avatar && e.kind == protocol.KindCard {
		// The core moves the file, so each call writes a new one.
		f, err := os.CreateTemp(blobTmp, "catalog-*.png")
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
			return nil, err
		}
		res.Avatar = f.Name()
	}
	return res, nil
}
