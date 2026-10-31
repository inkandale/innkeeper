package main

import (
	"context"
	"log/slog"
	"testing"
)

func TestOpenDBTurnsJITOffUnlessTheURLSetsIt(t *testing.T) {
	_, dbURL := pluginDB(t)
	ctx := context.Background()
	for _, c := range []struct{ query, want string }{{"", "off"}, {"?jit=on", "on"}} {
		t.Setenv("INNKEEPER_DATABASE_URL", dbURL+c.query)
		pool, err := openDB(ctx, slog.Default(), 0)
		if err != nil {
			t.Fatal(err)
		}
		var jit string
		err = pool.QueryRow(ctx, `SHOW jit`).Scan(&jit)
		pool.Close()
		if err != nil || jit != c.want {
			t.Errorf("URL %q: jit = %q, %v; want %q", c.query, jit, err, c.want)
		}
	}
}

func TestOpenDBCapsStatementsUnlessTheURLSetsATimeout(t *testing.T) {
	_, dbURL := pluginDB(t)
	ctx := context.Background()
	for _, c := range []struct{ query, want string }{{"", "15min"}, {"?statement_timeout=0", "0"}} {
		t.Setenv("INNKEEPER_DATABASE_URL", dbURL+c.query)
		pool, err := openDB(ctx, slog.Default(), 0)
		if err != nil {
			t.Fatal(err)
		}
		var timeout string
		err = pool.QueryRow(ctx, `SHOW statement_timeout`).Scan(&timeout)
		pool.Close()
		if err != nil || timeout != c.want {
			t.Errorf("URL %q: statement_timeout = %q, %v; want %q", c.query, timeout, err, c.want)
		}
	}
}
