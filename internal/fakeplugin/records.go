package main

import (
	"encoding/json"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/davidgibbons/innkeeper/protocol"
)

// note is the fake's one record type, notes. A note's ID is its title.
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

func recordsCall(method string, params json.RawMessage, mode string) (any, error) {
	// Every records method's params are a subset of these.
	var p struct {
		Type, ID, URL, Path, Name string
		Record                    json.RawMessage
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalid(err.Error())
	}
	if p.Type != "notes" {
		return nil, invalid("no record type " + p.Type)
	}
	id := p.ID
	notes.Lock()
	defer notes.Unlock()
	_, found := notes.byID[id]
	switch method {
	case protocol.MethodRecordsList:
		if mode == "bad-records" {
			return protocol.RecordsListResult{Records: []protocol.RecordSummary{{Name: "No ID"}}}, nil
		}
		res := protocol.RecordsListResult{Records: []protocol.RecordSummary{}}
		for _, k := range slices.Sorted(maps.Keys(notes.byID)) {
			res.Records = append(res.Records, summary(notes.byID[k]))
		}
		return res, nil
	case protocol.MethodRecordsGet:
		if !found {
			return nil, protocol.NewError(protocol.CodeRemoteNotFound, "no note "+id, false)
		}
		raw, _ := json.Marshal(notes.byID[id])
		return protocol.RecordsGetResult{Record: raw}, nil
	case protocol.MethodRecordsDelete:
		if !found {
			return nil, protocol.NewError(protocol.CodeRemoteNotFound, "no note "+id, false)
		}
		delete(notes.byID, id)
		return struct{}{}, nil
	case protocol.MethodRecordsPut:
		if id != "" && !found {
			return nil, protocol.NewError(protocol.CodeRemoteNotFound, "no note "+id, false)
		}
		var n note
		if err := json.Unmarshal(p.Record, &n); err != nil || n.Title == "" || strings.Contains(n.Title, "/") {
			return nil, invalid("a note needs a title without /")
		}
		if _, taken := notes.byID[n.Title]; taken && id == "" {
			return nil, invalid("a note titled " + n.Title + " already exists")
		}
		delete(notes.byID, id)
		notes.byID[n.Title] = n
		return protocol.RecordsPutResult{ID: n.Title}, nil
	case protocol.MethodRecordsImport:
		// The body says where the note came from, so tests can see the path.
		// A URL's last segment is the title; one ending in / has none.
		title, _, _ := strings.Cut(p.URL[strings.LastIndex(p.URL, "/")+1:], "?")
		n := note{Title: title, Body: p.URL}
		if p.Path != "" {
			text, err := os.ReadFile(p.Path)
			if err != nil {
				return nil, err
			}
			n = note{Title: p.Name, Body: p.Path + "\n" + string(text)}
		}
		if n.Title == "" {
			return nil, invalid("nothing to import")
		}
		notes.byID[n.Title] = n
		return protocol.RecordsImportResult{Records: []protocol.RecordSummary{summary(n)}}, nil
	}
	return nil, protocol.MethodNotFound(method)
}

func summary(n note) protocol.RecordSummary {
	return protocol.RecordSummary{ID: n.Title, Name: n.Title, Description: n.Body}
}
