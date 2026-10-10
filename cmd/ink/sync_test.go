package main

import (
	"slices"
	"strings"
	"testing"
)

func TestSyncPush(t *testing.T) {
	got := stubAPI(t, map[string]string{
		"/cards/12/push-preview?instance_id=3": `{"replaces": true, "changes": [{"field": "first_mes", "before": "Well met.", "after": "Hail."}], "confirm": "c1"}`,
		"/cards/13/push-preview?instance_id=3": `{"replaces": false, "changes": [], "confirm": "c2"}`,
		"POST /search": `{"hits": [], "total": 5, "facets": {"targets": [{"instance": 3, "is": "ahead", "cards": 2},
			{"instance": 3, "is": "diverged", "cards": 1}, {"instance": 4, "is": "in_sync", "cards": 5}]}}`,
	})
	if _, err := runInkIn(t, "", false, "sync", "push", "3", "12"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("push without a terminal: err = %v", err)
	}
	if _, err := runInkIn(t, "n\n", true, "sync", "push", "3", "12"); err == nil {
		t.Fatal("push answered no succeeded")
	}
	out, err := runInkIn(t, "", false, "sync", "push", "3", "12", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "app:    Well met.") {
		t.Errorf("no preview in:\n%s", out)
	}
	if _, err := runInkIn(t, "", false, "sync", "push", "3", "13"); err != nil {
		t.Fatalf("push that replaces nothing: %v", err)
	}
	out, err = runInkIn(t, "y\n", true, "sync", "push", "3", `{"filter": {"tag": "fantasy"}}`, "--new-copy")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "5 cards: 2 new, 2 ahead, 1 drifted") || !strings.Contains(out, "Replace 3 copies") {
		t.Errorf("no counts in:\n%s", out)
	}
	preview := "GET /cards/12/push-preview?instance_id=3 "
	want := []string{
		preview, preview, preview,
		`POST /cards/12/push {"confirm":"c1","instance_id":3,"new_copy":false}`,
		"GET /cards/13/push-preview?instance_id=3 ",
		`POST /cards/13/push {"confirm":"c2","instance_id":3,"new_copy":false}`,
		`POST /search {"facets":["targets"],"filter":{"tag":"fantasy"},"page":{"size":1}}`,
		`POST /pushes {"instance_id":3,"new_copy":true,"query":{"filter":{"tag":"fantasy"}}}`,
	}
	if !slices.Equal(*got, want) {
		t.Fatalf("requests = %q, want %q", *got, want)
	}
	for _, args := range [][]string{{"3"}, {"3", "12", "--force"}, {"x", "12"}, {"3", "{bad"}, {"3", "[]"}} {
		if _, err := runInk(t, append([]string{"sync", "push"}, args...)...); err == nil {
			t.Errorf("push %q succeeded", args)
		}
	}
}

func TestSyncPushQueryNoMatch(t *testing.T) {
	got := stubAPI(t, map[string]string{"POST /search": `{"hits": [], "total": 0}`})
	if _, err := runInkIn(t, "", false, "sync", "push", "3", `{"text": "nobody"}`, "--yes"); err == nil {
		t.Fatal("push of a query that matches nothing succeeded")
	}
	if len(*got) != 1 {
		t.Errorf("requests = %q", *got)
	}
}
