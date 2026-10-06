package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestPushCmd(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.URL.Path+" "+string(body))
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id": 7}`))
	}))
	defer srv.Close()
	t.Setenv("INNKEEPER_URL", srv.URL)
	t.Setenv("INNKEEPER_TOKEN", "tok")

	if err := pushCmd([]string{"3", "12"}); err != nil {
		t.Fatal(err)
	}
	if err := pushCmd([]string{"3", `{"filter": {"tag": "fantasy"}}`, "--new-copy"}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`/cards/12/push {"instance_id":3,"new_copy":false}`,
		`/pushes {"instance_id":3,"new_copy":true,"query":{"filter":{"tag":"fantasy"}}}`,
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("requests = %q, want %q", got, want)
	}
	for _, args := range [][]string{{"3"}, {"3", "12", "--force"}, {"x", "12"}, {"3", "{bad"}} {
		if err := pushCmd(args); err == nil {
			t.Errorf("push %q succeeded", args)
		}
	}
}

func TestSourcesCmd(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+string(body))
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[{"id": 4, "kind": "chub", "ref": "u/a", "linked_by": "auto", "url": "https://chub.ai/characters/u/a"}]`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	t.Setenv("INNKEEPER_URL", srv.URL)
	t.Setenv("INNKEEPER_TOKEN", "tok")

	for _, args := range [][]string{{"3"}, {"3", "add", "https://chub.ai/characters/u/a"}, {"3", "remove", "4"}} {
		if err := sourcesCmd(args); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"GET /cards/3/sources ", `PUT /cards/3/sources {"url":"https://chub.ai/characters/u/a"}`, "DELETE /cards/3/sources/4 "}
	if !slices.Equal(got, want) {
		t.Fatalf("requests = %q, want %q", got, want)
	}
	for _, args := range [][]string{{}, {"3", "add"}, {"3", "zap", "x"}, {"x"}, {"3", "remove", "x"}} {
		if err := sourcesCmd(args); err == nil {
			t.Errorf("sources %q succeeded", args)
		}
	}
}
