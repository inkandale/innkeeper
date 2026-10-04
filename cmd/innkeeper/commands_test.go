package main

import (
	"io"
	"net/http"
	"net/http/httptest"
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
