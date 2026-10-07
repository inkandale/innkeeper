package main

import (
	"slices"
	"testing"
)

func TestSyncPush(t *testing.T) {
	got := stubAPI(t, nil)
	if _, err := runInk(t, "sync", "push", "3", "12"); err != nil {
		t.Fatal(err)
	}
	if _, err := runInk(t, "sync", "push", "3", `{"filter": {"tag": "fantasy"}}`, "--new-copy"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`POST /cards/12/push {"instance_id":3,"new_copy":false}`,
		`POST /pushes {"instance_id":3,"new_copy":true,"query":{"filter":{"tag":"fantasy"}}}`,
	}
	if !slices.Equal(*got, want) {
		t.Fatalf("requests = %q, want %q", *got, want)
	}
	for _, args := range [][]string{{"3"}, {"3", "12", "--force"}, {"x", "12"}, {"3", "{bad"}} {
		if _, err := runInk(t, append([]string{"sync", "push"}, args...)...); err == nil {
			t.Errorf("push %q succeeded", args)
		}
	}
}
