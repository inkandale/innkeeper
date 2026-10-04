package targets

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestMergePatch(t *testing.T) {
	// Cases from RFC 7386, appendix A.
	tests := []struct{ doc, patch, want string }{
		{`{"a":"b"}`, `{"a":"c"}`, `{"a":"c"}`},
		{`{"a":"b"}`, `{"b":"c"}`, `{"a":"b","b":"c"}`},
		{`{"a":"b"}`, `{"a":null}`, `{}`},
		{`{"a":{"b":"c"}}`, `{"a":{"b":"d","c":null}}`, `{"a":{"b":"d"}}`},
		{`{"a":[{"b":"c"}]}`, `{"a":[1]}`, `{"a":[1]}`},
		{`{"e":null}`, `{"a":1}`, `{"a":1,"e":null}`},
		{`{"n":12345678901234567890}`, `{}`, `{"n":12345678901234567890}`},
		{`{"a":"foo"}`, `null`, `null`},
		{`{"a":"foo"}`, `"bar"`, `"bar"`},
		{`[1,2]`, `{"a":"b","c":null}`, `{"a":"b"}`},
	}
	for _, tt := range tests {
		got, err := MergePatch(json.RawMessage(tt.doc), json.RawMessage(tt.patch))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tt.want {
			t.Errorf("MergePatch(%s, %s) = %s, want %s", tt.doc, tt.patch, got, tt.want)
		}
	}
}

func TestProject(t *testing.T) {
	data := json.RawMessage(`{"name": "Brakka", "scenario": "A forge.", "first_mes": "Hail."}`)
	got, hash, err := project(data, []string{"name", "first_mes", "description"})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"first_mes":"Hail.","name":"Brakka"}` {
		t.Fatalf("projected %s", got)
	}
	_, same, _ := project(json.RawMessage(`{"name": " Brakka\r\n", "first_mes": "Hail.", "description": ""}`), []string{"name", "first_mes", "description"})
	if same != hash {
		t.Fatal("whitespace and empty fields changed the hash")
	}
}

func TestDiff(t *testing.T) {
	got, err := diff(json.RawMessage(`{"name": "Brakka", "description": "A dwarf.", "tags": [], "first_mes": "Hail."}`),
		json.RawMessage(`{"name": "Brakka ", "first_mes": "Well met.", "scenario": "A forge."}`))
	if err != nil {
		t.Fatal(err)
	}
	var fields []string
	for _, c := range got {
		fields = append(fields, c.Field)
	}
	if !slices.Equal(fields, []string{"description", "first_mes", "scenario"}) {
		t.Fatalf("changed fields = %v", fields)
	}
	if got[0].After != nil || string(got[1].After) != `"Well met."` || got[2].Before != nil {
		t.Fatalf("changes = %+v", got)
	}
}

func TestDroppedFields(t *testing.T) {
	got, err := droppedFields(json.RawMessage(`{"name": "Brakka", "scenario": "A forge.", "tags": [], "system_prompt": " "}`),
		[]string{"name"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"scenario"}) {
		t.Fatalf("dropped = %v", got)
	}
}

func TestAdoptChanges(t *testing.T) {
	changes := []Change{
		{Field: "first_mes", Before: json.RawMessage(`"Hail."`), After: json.RawMessage(`"Well met."`)},
		{Field: "description", Before: json.RawMessage(`"A dwarf."`)},
		{Field: "character_book", After: json.RawMessage(`{"entries": []}`)},
	}
	data, changed, err := applyChanges(json.RawMessage(`{"name": "Brakka", "description": "A dwarf.", "first_mes": "Hail."}`), changes)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if string(data) != `{"first_mes":"Well met.","name":"Brakka"}` {
		t.Fatalf("data = %s", data)
	}
	over, changed, err := overrideChanges(json.RawMessage(`{"name": "Brakka", "description": "A dwarf.", "first_mes": "Hail."}`), json.RawMessage(`{"7": {"name": "Brakka (Fake)"}, "9": {"name": "Other"}}`), "7", changes)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if string(over) != `{"7":{"description":null,"first_mes":"Well met.","name":"Brakka (Fake)"},"9":{"name":"Other"}}` {
		t.Fatalf("overrides = %s", over)
	}
	if _, changed, _ := applyChanges(json.RawMessage(`{}`), changes[2:]); changed {
		t.Fatal("a lorebook-only change was applied")
	}
}

func TestDiffIgnoresVolatileFields(t *testing.T) {
	got, err := diff(json.RawMessage(`{"name": "B", "modification_date": 1}`), json.RawMessage(`{"name": "B", "modification_date": 2}`))
	if err != nil || len(got) != 0 {
		t.Fatalf("changes = %+v, err = %v", got, err)
	}
	dropped, err := droppedFields(json.RawMessage(`{"name": "B", "creation_date": 1}`), []string{"name"})
	if err != nil || len(dropped) != 0 {
		t.Fatalf("dropped = %v, err = %v", dropped, err)
	}
}

func TestOverrideChangesRemovesNestedKeys(t *testing.T) {
	base := json.RawMessage(`{"name":"B","extensions":{"a":1,"b":2}}`)
	changes := []Change{{Field: "extensions", Before: json.RawMessage(`{"a":1,"b":2}`), After: json.RawMessage(`{"a":1}`)}}
	over, changed, err := overrideChanges(base, json.RawMessage(`{}`), "7", changes)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(over, &all); err != nil {
		t.Fatal(err)
	}
	got, err := MergePatch(base, all["7"])
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"extensions":{"a":1},"name":"B"}` {
		t.Fatalf("rendered %s", got)
	}
}

func TestDecodeRejectsTrailingData(t *testing.T) {
	if _, err := MergePatch(json.RawMessage(`{} {}`), json.RawMessage(`{}`)); err == nil {
		t.Fatal("trailing data accepted")
	}
}

func TestClearOverride(t *testing.T) {
	over := json.RawMessage(`{"7":{"first_mes":"a","scenario":"b"},"8":{"first_mes":"c"}}`)
	changes := []Change{{Field: "first_mes"}, {Field: "character_book"}}
	got, changed, err := clearOverride(over, "7", changes)
	if err != nil || !changed || string(got) != `{"7":{"scenario":"b"},"8":{"first_mes":"c"}}` {
		t.Fatalf("got %s, %v, %v", got, changed, err)
	}
	got, changed, err = clearOverride(got, "7", []Change{{Field: "scenario"}})
	if err != nil || !changed || string(got) != `{"8":{"first_mes":"c"}}` {
		t.Fatalf("emptied: %s, %v, %v", got, changed, err)
	}
	if got, changed, err = clearOverride(got, "7", changes); err != nil || changed || string(got) != `{"8":{"first_mes":"c"}}` {
		t.Fatalf("absent: %s, %v, %v", got, changed, err)
	}
	if _, changed, _ := clearOverride(over, "7", []Change{{Field: "character_book"}}); changed {
		t.Fatal("character_book cleared")
	}
}
