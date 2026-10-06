package backup

import (
	"maps"
	"strings"
	"testing"
	"time"
)

func TestSettingsFromEnv(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantDir   string
		wantEvery map[Type]time.Duration
		wantKeep  map[Type]int
		wantErr   string // substring naming the variable
	}{
		{name: "nothing set", wantEvery: map[Type]time.Duration{}, wantKeep: map[Type]int{}},
		{
			name:      "assets every with default keep",
			env:       map[string]string{"INNKEEPER_BACKUP_DIR": "/b", "INNKEEPER_BACKUP_ASSETS_EVERY": "24h"},
			wantDir:   "/b",
			wantEvery: map[Type]time.Duration{Assets: 24 * time.Hour},
			wantKeep:  map[Type]int{Assets: 7},
		},
		{
			name:      "config every in days",
			env:       map[string]string{"INNKEEPER_BACKUP_DIR": "/b", "INNKEEPER_BACKUP_CONFIG_EVERY": "7d"},
			wantDir:   "/b",
			wantEvery: map[Type]time.Duration{Config: 168 * time.Hour},
			wantKeep:  map[Type]int{Config: 7},
		},
		{
			name:      "explicit keep",
			env:       map[string]string{"INNKEEPER_BACKUP_DIR": "/b", "INNKEEPER_BACKUP_ASSETS_EVERY": "90m", "INNKEEPER_BACKUP_ASSETS_KEEP": "3"},
			wantDir:   "/b",
			wantEvery: map[Type]time.Duration{Assets: 90 * time.Minute},
			wantKeep:  map[Type]int{Assets: 3},
		},
		{
			name:      "keep without every",
			env:       map[string]string{"INNKEEPER_BACKUP_CONFIG_KEEP": "2"},
			wantEvery: map[Type]time.Duration{},
			wantKeep:  map[Type]int{Config: 2},
		},
		{name: "every without dir", env: map[string]string{"INNKEEPER_BACKUP_ASSETS_EVERY": "24h"}, wantErr: "INNKEEPER_BACKUP_DIR"},
		{name: "keep zero", env: map[string]string{"INNKEEPER_BACKUP_ASSETS_KEEP": "0"}, wantErr: "INNKEEPER_BACKUP_ASSETS_KEEP"},
		{name: "keep not a number", env: map[string]string{"INNKEEPER_BACKUP_CONFIG_KEEP": "x"}, wantErr: "INNKEEPER_BACKUP_CONFIG_KEEP"},
		{name: "every negative", env: map[string]string{"INNKEEPER_BACKUP_DIR": "/b", "INNKEEPER_BACKUP_ASSETS_EVERY": "-1h"}, wantErr: "INNKEEPER_BACKUP_ASSETS_EVERY"},
		{name: "every zero", env: map[string]string{"INNKEEPER_BACKUP_DIR": "/b", "INNKEEPER_BACKUP_CONFIG_EVERY": "0"}, wantErr: "INNKEEPER_BACKUP_CONFIG_EVERY"},
		{name: "every garbage", env: map[string]string{"INNKEEPER_BACKUP_DIR": "/b", "INNKEEPER_BACKUP_ASSETS_EVERY": "abc"}, wantErr: "INNKEEPER_BACKUP_ASSETS_EVERY"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range []string{
				"INNKEEPER_BACKUP_DIR",
				"INNKEEPER_BACKUP_ASSETS_EVERY", "INNKEEPER_BACKUP_CONFIG_EVERY",
				"INNKEEPER_BACKUP_ASSETS_KEEP", "INNKEEPER_BACKUP_CONFIG_KEEP",
			} {
				t.Setenv(k, tc.env[k])
			}
			got, err := SettingsFromEnv()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one naming %s", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Dir != tc.wantDir || !maps.Equal(got.Every, tc.wantEvery) || !maps.Equal(got.Keep, tc.wantKeep) {
				t.Fatalf("got %+v, want dir=%q every=%v keep=%v", got, tc.wantDir, tc.wantEvery, tc.wantKeep)
			}
		})
	}
}
