package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/davidgibbons/innkeeper/protocol"
)

// target is the fake push and pull target, set up by the instance config. It
// keeps each card as <dir>/<remote_id>.json, so tests can read and change
// what the app holds. Unset flags default to a full-sync app that embeds
// lorebooks.
type target struct {
	Dir       string   `json:"dir"`
	Fields    []string `json:"fields"`
	Pull      *bool    `json:"pull"`
	Update    *bool    `json:"update"`
	Lorebooks string   `json:"lorebooks"`
	// FailPut makes target.put fail: retryable or fatal.
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
		Lorebooks: cmp.Or(t.Lorebooks, protocol.LorebooksEmbedded)}
	if len(info.Fields) == 0 {
		info.Fields = []string{"name", "description", "first_mes", "character_book"}
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

func (t target) put(params json.RawMessage) (any, error) {
	var p protocol.TargetPutParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
	}
	switch t.FailPut {
	case "retryable":
		return nil, protocol.NewError(-32000, "app unavailable", true)
	case "fatal":
		return nil, protocol.NewError(-32000, "app rejected the card", false)
	}
	if t.Dir == "" {
		return nil, protocol.NewError(-32000, "config.dir is not set", false)
	}
	info := t.describe()
	var card struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(p.Card, &card); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
	}
	// Like a real app, keep only the fields it stores.
	kept := map[string]json.RawMessage{}
	for _, f := range info.Fields {
		if v, ok := card.Data[f]; ok {
			kept[f] = v
		}
	}
	stored := map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": kept}
	if p.Avatar != "" {
		stored["avatar"] = p.Avatar
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return nil, err
	}
	id := p.RemoteID
	if id != "" && info.Update {
		if _, err := os.Stat(t.path(id)); err != nil {
			return nil, protocol.NewError(protocol.CodeRemoteNotFound, "no card "+id, false)
		}
	} else {
		// Concurrent puts each claim their own ID.
		for n := 1; ; n++ {
			id = fmt.Sprintf("r%d", n)
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
	// Write to a temp file and rename, so a concurrent get never sees a partial card.
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
	return protocol.TargetPutResult{RemoteID: id}, nil
}

func (t target) get(params json.RawMessage) (any, error) {
	var p protocol.TargetGetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
	}
	raw, err := os.ReadFile(t.path(p.RemoteID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, protocol.NewError(protocol.CodeRemoteNotFound, "no card "+p.RemoteID, false)
	}
	if err != nil {
		return nil, err
	}
	return protocol.TargetGetResult{Card: raw}, nil
}
