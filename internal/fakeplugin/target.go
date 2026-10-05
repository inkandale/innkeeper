package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/davidgibbons/innkeeper/protocol"
)

// target is the fake push and pull target, set up by the instance config. It
// keeps each card as <dir>/r<n>.json and each lorebook as <dir>/l<n>.json,
// so tests can read and change what the app holds. Unset flags default to a
// full-sync app that embeds lorebooks.
type target struct {
	Dir            string   `json:"dir"`
	Fields         []string `json:"fields"`
	Pull           *bool    `json:"pull"`
	Update         *bool    `json:"update"`
	Lorebooks      string   `json:"lorebooks"`
	LorebookFields []string `json:"lorebook_fields"`
	// FailPut makes target.put fail: retryable, fatal, or crash (the plugin
	// exits after storing the card). Lorebook puts don't fail.
	FailPut string `json:"fail_put"`
}

func newTarget(config map[string]any) target {
	raw, _ := json.Marshal(config)
	var t target
	_ = json.Unmarshal(raw, &t)
	return t
}

func (t target) describe() protocol.TargetInfo {
	info := protocol.TargetInfo{Fields: t.Fields, Pull: true, Update: true,
		Lorebooks: cmp.Or(t.Lorebooks, protocol.LorebooksEmbedded), LorebookFields: t.LorebookFields}
	if len(info.Fields) == 0 {
		info.Fields = []string{"name", "description", "first_mes", "character_book"}
	}
	if info.Lorebooks == protocol.LorebooksStandalone && len(info.LorebookFields) == 0 {
		info.LorebookFields = []string{"name", "description", "entries"}
	}
	if t.Pull != nil {
		info.Pull = *t.Pull
	}
	if t.Update != nil {
		info.Update = *t.Update
	}
	return info
}

func (t target) path(id string) string { return filepath.Join(t.Dir, id+".json") }

func invalidParams(err error) error {
	return protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
}

// keep returns only the fields the app stores, like a real app.
func keep(all map[string]json.RawMessage, fields []string) map[string]json.RawMessage {
	kept := map[string]json.RawMessage{}
	for _, f := range fields {
		if v, ok := all[f]; ok {
			kept[f] = v
		}
	}
	return kept
}

func (t target) put(params json.RawMessage) (any, error) {
	var p protocol.TargetPutParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams(err)
	}
	switch t.FailPut {
	case "retryable":
		return nil, protocol.NewError(-32000, "app unavailable", true)
	case "fatal":
		return nil, protocol.NewError(-32000, "app rejected the card", false)
	}
	var card struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(p.Card, &card); err != nil {
		return nil, invalidParams(err)
	}
	info := t.describe()
	stored := map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": keep(card.Data, info.Fields)}
	if p.Avatar != "" {
		stored["avatar"] = p.Avatar
	}
	if p.LorebookRemoteIDs != nil {
		stored["lorebook_remote_ids"] = p.LorebookRemoteIDs
	}
	return t.store("r", stored, p.RemoteID, p.Key, t.FailPut == "crash")
}

func (t target) putLorebook(params json.RawMessage) (any, error) {
	var p protocol.TargetPutLorebookParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams(err)
	}
	var book map[string]json.RawMessage
	if err := json.Unmarshal(p.Lorebook, &book); err != nil {
		return nil, invalidParams(err)
	}
	return t.store("l", map[string]any{"lorebook": keep(book, t.describe().LorebookFields)}, p.RemoteID, p.Key, false)
}

// store writes one item under a new ID with prefix, or over remoteID. A key
// already stored returns that item instead. With crash, the plugin exits
// after writing.
func (t target) store(prefix string, stored map[string]any, remoteID, key string, crash bool) (any, error) {
	if t.Dir == "" {
		return nil, protocol.NewError(-32000, "config.dir is not set", false)
	}
	if key != "" {
		if id, ok := t.byKey(prefix, key); ok {
			return protocol.TargetPutResult{RemoteID: id}, nil
		}
		stored["key"] = key
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return nil, err
	}
	id := remoteID
	if id != "" && t.describe().Update {
		if _, err := os.Stat(t.path(id)); err != nil {
			return nil, protocol.NewError(protocol.CodeRemoteNotFound, "no item "+id, false)
		}
	} else {
		// Concurrent puts each claim their own ID.
		for n := 1; ; n++ {
			id = fmt.Sprintf("%s%d", prefix, n)
			f, err := os.OpenFile(t.path(id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err == nil {
				f.Close()
				break
			}
			if !errors.Is(err, os.ErrExist) {
				return nil, err
			}
		}
	}
	// Write to a temp file and rename, so a concurrent get never sees a partial item.
	tmp, err := os.CreateTemp(t.Dir, ".put-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.Write(raw)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return nil, werr
	}
	if err := os.Rename(tmp.Name(), t.path(id)); err != nil {
		return nil, err
	}
	if crash {
		os.Exit(1)
	}
	return protocol.TargetPutResult{RemoteID: id}, nil
}

// byKey finds the item with prefix that a put with this key created.
func (t target) byKey(prefix, key string) (string, bool) {
	files, _ := filepath.Glob(filepath.Join(t.Dir, prefix+"*.json"))
	for _, f := range files {
		var c struct{ Key string }
		if raw, err := os.ReadFile(f); err == nil && json.Unmarshal(raw, &c) == nil && c.Key == key {
			return strings.TrimSuffix(filepath.Base(f), ".json"), true
		}
	}
	return "", false
}

// read returns the stored item named by params' remote_id.
func (t target) read(params json.RawMessage) ([]byte, error) {
	var p protocol.TargetGetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams(err)
	}
	raw, err := os.ReadFile(t.path(p.RemoteID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, protocol.NewError(protocol.CodeRemoteNotFound, "no item "+p.RemoteID, false)
	}
	return raw, err
}

func (t target) get(params json.RawMessage) (any, error) {
	raw, err := t.read(params)
	if err != nil {
		return nil, err
	}
	return protocol.TargetGetResult{Card: raw}, nil
}

func (t target) getLorebook(params json.RawMessage) (any, error) {
	raw, err := t.read(params)
	if err != nil {
		return nil, err
	}
	var item struct {
		Lorebook json.RawMessage `json:"lorebook"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, err
	}
	return protocol.TargetGetLorebookResult{Lorebook: item.Lorebook}, nil
}

// list returns the stored items with prefix, named by their name field.
func (t target) list(prefix string) (any, error) {
	if t.Dir == "" {
		return nil, protocol.NewError(-32000, "config.dir is not set", false)
	}
	files, _ := filepath.Glob(filepath.Join(t.Dir, prefix+"*.json"))
	items := []protocol.TargetItem{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var item struct {
			Data     struct{ Name string } `json:"data"`
			Lorebook struct{ Name string } `json:"lorebook"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		items = append(items, protocol.TargetItem{
			RemoteID: strings.TrimSuffix(filepath.Base(f), ".json"),
			Name:     cmp.Or(item.Data.Name, item.Lorebook.Name),
		})
	}
	return protocol.TargetListResult{Items: items}, nil
}
