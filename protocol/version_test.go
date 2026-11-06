package protocol

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestCheckVersion(t *testing.T) {
	current := fmt.Sprintf("%d.%d", Version, Minor)
	for _, tt := range []struct {
		version, wantErr string
	}{
		{"", ""},
		{fmt.Sprintf("%d.0", Version), ""},
		{current, ""},
		{"1", "must be <major>.<minor>"},
		{"1.", "must be <major>.<minor>"},
		{"v1.1", "must be <major>.<minor>"},
		{"1.1.0", "must be <major>.<minor>"},
		{"+1.1", "must be <major>.<minor>"},
		{" 1.1", "must be <major>.<minor>"},
		{"99999999999999999999.1", "must be <major>.<minor>"},
		{"2.0", "doesn't match protocol 1"},
	} {
		t.Run(tt.version, func(t *testing.T) {
			err := CheckVersion(Version, tt.version)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("CheckVersion(%q) = %v", tt.version, err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("CheckVersion(%q) = %v, want %q", tt.version, err, tt.wantErr)
			}
		})
	}
}

func TestCheckVersionReportsANewerMinor(t *testing.T) {
	next := fmt.Sprintf("%d.%d", Version, Minor+1)
	err := CheckVersion(Version, next)
	var newer *NewerProtocolError
	if !errors.As(err, &newer) || newer.Needs != next {
		t.Fatalf("CheckVersion(%q) = %v, want a NewerProtocolError", next, err)
	}
	if want := fmt.Sprintf("needs protocol %s; this Innkeeper speaks %d.%d", next, Version, Minor); err.Error() != want {
		t.Fatalf("message = %q, want %q", err, want)
	}
	if err := CheckVersion(Version, "1"); errors.As(err, &newer) {
		t.Fatalf("a malformed version is a NewerProtocolError: %v", err)
	}
}

func TestLoadManifestReadsProtocolVersion(t *testing.T) {
	current := fmt.Sprintf("%d.%d", Version, Minor)
	body := strings.Replace(validManifest, "protocol = 1", "protocol = 1\nprotocol_version = \""+current+"\"", 1)
	m, err := LoadManifest(writeManifest(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if m.ProtocolVersion != current {
		t.Fatalf("ProtocolVersion = %q, want %q", m.ProtocolVersion, current)
	}
}

// A newer minor may add keys this core doesn't know; the error must still say what the plugin needs.
func TestLoadManifestReportsANewerProtocolBeforeUnknownKeys(t *testing.T) {
	body := strings.Replace(validManifest, "protocol = 1", "protocol = 1\nprotocol_version = \"1.99\"\nfrom_the_future = true", 1)
	_, err := LoadManifest(writeManifest(t, body))
	var newer *NewerProtocolError
	if !errors.As(err, &newer) || newer.Needs != "1.99" {
		t.Fatalf("LoadManifest = %v, want a NewerProtocolError needing 1.99", err)
	}
}
