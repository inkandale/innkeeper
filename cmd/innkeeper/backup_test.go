package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/backup"
)

func TestBackupArgErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"d", "bogus"}, {"d", "assets", "extra"}} {
		if err := backupCmd(args); err == nil {
			t.Errorf("backup %v: want error", args)
		}
	}
}

func TestRestoreArgErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"--replace"}, {"--bogus", "x"}} {
		if err := restoreCmd(args); err == nil {
			t.Errorf("restore %v: want error", args)
		}
	}
	if err := restoreCmd([]string{"--bogus", "x"}); !strings.Contains(err.Error(), "--bogus") {
		t.Errorf("error should name the flag: %v", err)
	}
}

func TestRestoreOrder(t *testing.T) {
	in := []restoreItem{{"c1", backup.Config}, {"a1", backup.Assets}, {"c2", backup.Config}, {"a2", backup.Assets}}
	var got []string
	for _, it := range restoreOrder(in) {
		got = append(got, it.path)
	}
	if want := []string{"a1", "a2", "c1", "c2"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
