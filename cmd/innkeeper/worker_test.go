package main

import (
	"testing"
	"time"
)

func TestEveryEnv(t *testing.T) {
	for _, c := range []struct {
		value   string
		want    time.Duration
		wantErr bool
	}{
		{"", 24 * time.Hour, false},
		{"12h", 12 * time.Hour, false},
		{"0", 0, false},
		{"soon", 0, true},
		{"-1h", 0, true},
	} {
		t.Setenv("INNKEEPER_TEST_EVERY", c.value)
		got, err := everyEnv("INNKEEPER_TEST_EVERY", 24*time.Hour)
		if got != c.want || (err != nil) != c.wantErr {
			t.Errorf("everyEnv(%q) = %v, %v; want %v, error %v", c.value, got, err, c.want, c.wantErr)
		}
	}
}
