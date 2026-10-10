package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// runInk runs ink with args and returns everything it printed.
func runInk(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := newRoot()
	root.SetArgs(args)
	root.SetIn(strings.NewReader(""))
	root.SetOut(&out)
	root.SetErr(&out)
	err := root.Execute()
	return out.String(), err
}

// stubAPI points ink at a server that answers POST with job 9 and anything
// else with the reply for its path, and records every request as
// "METHOD URI BODY".
func stubAPI(t *testing.T, replies map[string]string) *[]string {
	var got []string
	srv := httptest.NewServer(http.StripPrefix("/api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.RequestURI()+" "+string(body))
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id": 9}`))
			return
		}
		_, _ = w.Write([]byte(replies[r.URL.RequestURI()]))
	})))
	t.Cleanup(srv.Close)
	t.Setenv("INNKEEPER_URL", srv.URL)
	t.Setenv("INNKEEPER_TOKEN", "tok")
	return &got
}

func TestCall(t *testing.T) {
	got := stubAPI(t, map[string]string{"/settings": `{"theme": "dusk"}` + "\n"})
	out, err := runInk(t, "call", "get", "/settings")
	if err != nil || out != `{"theme": "dusk"}`+"\n" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	if _, err := runInk(t, "call", "POST", "/jobs", `{"x": 1}`); err != nil {
		t.Fatal(err)
	}
	if want := []string{"GET /settings ", `POST /jobs {"x": 1}`}; len(*got) != 2 || (*got)[0] != want[0] || (*got)[1] != want[1] {
		t.Fatalf("requests = %q, want %q", *got, want)
	}
	for _, args := range [][]string{{"call", "GET"}, {"call", "POST", "/jobs", "{bad"}} {
		if _, err := runInk(t, args...); err == nil {
			t.Errorf("%q succeeded", args)
		}
	}
}
