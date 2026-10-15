package main

import (
	"encoding/json"
	"maps"
	"os"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/davidgibbons/innkeeper/protocol"
)

// note is the fake's one item type, notes. A note's ID is its title.
type note struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

// notes is in memory, so each plugin process starts with only the welcome note.
var notes = struct {
	sync.Mutex
	byID map[string]note
}{byID: map[string]note{"welcome": {Title: "welcome", Body: "Hello."}}}

func invalid(msg string) error { return protocol.NewError(protocol.CodeInvalidParams, msg, false) }

func itemsCall(method string, params json.RawMessage, mode string) (any, error) {
	// Every items method's params are a subset of these.
	var p struct {
		Type, ID, URL, Path, Name string
		Item                      json.RawMessage
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalid(err.Error())
	}
	if p.Type != "notes" {
		return nil, invalid("no item type " + p.Type)
	}
	id := p.ID
	notes.Lock()
	defer notes.Unlock()
	_, found := notes.byID[id]
	switch method {
	case protocol.MethodItemsList:
		if mode == "bad-items" {
			return protocol.ItemsListResult{Items: []protocol.ItemSummary{{Name: "No ID"}}}, nil
		}
		res := protocol.ItemsListResult{Items: []protocol.ItemSummary{}}
		for _, k := range slices.Sorted(maps.Keys(notes.byID)) {
			res.Items = append(res.Items, summary(notes.byID[k]))
		}
		return res, nil
	case protocol.MethodItemsGet:
		if !found {
			return nil, protocol.NewError(protocol.CodeRemoteNotFound, "no note "+id, false)
		}
		raw, _ := json.Marshal(notes.byID[id])
		return protocol.ItemsGetResult{Item: raw}, nil
	case protocol.MethodItemsDelete:
		if !found {
			return nil, protocol.NewError(protocol.CodeRemoteNotFound, "no note "+id, false)
		}
		delete(notes.byID, id)
		return struct{}{}, nil
	case protocol.MethodItemsPut:
		if id != "" && !found {
			return nil, protocol.NewError(protocol.CodeRemoteNotFound, "no note "+id, false)
		}
		var n note
		if err := json.Unmarshal(p.Item, &n); err != nil || n.Title == "" || strings.Contains(n.Title, "/") {
			return nil, invalid("a note needs a title without /")
		}
		delete(notes.byID, id)
		notes.byID[n.Title] = n
		return protocol.ItemsPutResult{ID: n.Title}, nil
	case protocol.MethodItemsImport:
		// The body records where the note came from, so tests can see the path.
		n := note{Title: path.Base(p.URL), Body: p.URL}
		if p.Path != "" {
			text, err := os.ReadFile(p.Path)
			if err != nil {
				return nil, err
			}
			n = note{Title: p.Name, Body: p.Path + "\n" + string(text)}
		}
		if n.Title == "" || n.Title == "." || n.Title == "/" {
			return nil, invalid("nothing to import")
		}
		notes.byID[n.Title] = n
		return protocol.ItemsImportResult{Items: []protocol.ItemSummary{summary(n)}}, nil
	}
	return nil, protocol.MethodNotFound(method)
}

func summary(n note) protocol.ItemSummary {
	return protocol.ItemSummary{ID: n.Title, Name: n.Title, Description: n.Body}
}
