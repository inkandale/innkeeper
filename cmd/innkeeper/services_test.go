package main

import (
	"context"
	"testing"
)

func TestOpenDBTurnsJITOffUnlessTheURLSetsIt(t *testing.T) {
	_, dbURL := pluginDB(t)
	ctx := context.Background()
	for _, c := range []struct{ query, want string }{{"", "off"}, {"?jit=on", "on"}} {
		t.Setenv("INNKEEPER_DATABASE_URL", dbURL+c.query)
		pool, err := openDB(ctx, 0)
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
