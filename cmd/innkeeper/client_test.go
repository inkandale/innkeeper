package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientReportsAPIErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"status": 422, "detail": "validation failed", "errors": [{"message": "expected number", "location": "body.instance_id"}]}`)
	}))
	defer srv.Close()
	c := &client{base: srv.URL, token: "tok"}
	_, err := c.do(context.Background(), "POST", "/imports", []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "422 validation failed; body.instance_id: expected number") {
		t.Fatalf("err = %v", err)
	}
	c.token = "wrong"
	if _, err := c.do(context.Background(), "GET", "/jobs", nil); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
}

func TestClientStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: job\ndata: {\"n\": 1}\n\nevent: job\ndata: {\"n\": 2}\n\n")
	}))
	defer srv.Close()
	c := &client{base: srv.URL, token: "tok"}
	var got []string
	err := c.stream(context.Background(), "/jobs/1/events", func(data []byte) error {
		got = append(got, string(data))
		return nil
	})
	if err != nil || strings.Join(got, ",") != `{"n": 1},{"n": 2}` {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestNewClientNeedsSettings(t *testing.T) {
	t.Setenv("INNKEEPER_URL", "http://localhost:8080")
	t.Setenv("INNKEEPER_TOKEN", "")
	if _, err := newClient(); err == nil {
		t.Fatal("no error without a token")
	}
}

func TestParseID(t *testing.T) {
	if _, err := parseID("../tokens"); err == nil {
		t.Fatal("accepted a path as an ID")
	}
	if id, err := parseID("12"); err != nil || id != "12" {
		t.Fatalf("parseID(12) = %q, %v", id, err)
	}
	if id, err := parseID("007"); err != nil || id != "7" {
		t.Fatalf("parseID(007) = %q, %v", id, err)
	}
	for _, bad := range []string{"-1", "+5x", ""} {
		if _, err := parseID(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if id, err := parseID("+5"); err != nil || id != "5" {
		t.Fatalf("parseID(+5) = %q, %v", id, err)
	}
}

func TestWatchJob(t *testing.T) {
	for _, tc := range []struct{ stream, want string }{
		{"event: job\ndata: {\"status\": \"running\", \"progress\": {}}\n\n: ping\n\n", "running"},
		{"event: job\ndata: {\"status\": \"failed\"}\n\n", "job failed"},
		{"event: job\ndata: {\"status\": \"succeeded\"}\n\n", ""},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, tc.stream)
		}))
		err := watchJob(context.Background(), &client{base: srv.URL, token: "tok"}, "1")
		srv.Close()
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Fatalf("stream %q: err = %v, want %q", tc.stream, err, tc.want)
		}
	}
}

func TestClientPlainTextError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "  upstream timed out\n")
	}))
	defer srv.Close()
	_, err := (&client{base: srv.URL, token: "tok"}).do(context.Background(), "GET", "/jobs", nil)
	if err == nil || !strings.Contains(err.Error(), "502 upstream timed out") {
		t.Fatalf("err = %v", err)
	}
}

func TestUsageErrorsNeedNoClient(t *testing.T) {
	t.Setenv("INNKEEPER_URL", "")
	t.Setenv("INNKEEPER_TOKEN", "")
	if err := bulkCmd([]string{"archive"}); err == nil || err.Error() != usage {
		t.Fatalf("bulk err = %v", err)
	}
	if err := jobsCmd([]string{"bogus", "1"}); err == nil || err.Error() != usage {
		t.Fatalf("jobs err = %v", err)
	}
}
