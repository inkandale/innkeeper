package protocol

import (
	"reflect"
	"strings"
	"testing"
)

func uiManifest(ui *UI, caps ...string) *Manifest {
	return &Manifest{Name: "panel", Version: "1", Protocol: Version, Command: []string{"panel"}, Capabilities: caps, UI: ui}
}

func TestManifestAcceptsCornerUI(t *testing.T) {
	m := uiManifest(&UI{Title: "AI Assistant", Entry: "ui/index.html", Slot: SlotCorner,
		Permissions: []string{PermissionLibraryRead, PermissionCardsEdit}}, CapabilityUI)
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestManifestRejectsBadUI(t *testing.T) {
	good := UI{Title: "Panel", Entry: "ui/index.html", Slot: SlotCorner}
	for name, tt := range map[string]struct {
		m    *Manifest
		want string
	}{
		"no capability":  {uiManifest(&good, CapabilityAction), "ui needs the ui capability"},
		"no table":       {uiManifest(nil, CapabilityUI), "the ui capability needs a [ui] table"},
		"no title":       {uiManifest(&UI{Title: " ", Entry: "ui/index.html", Slot: SlotCorner}, CapabilityUI), "ui needs a title"},
		"panel listed":   {uiManifest(&UI{Title: "P", Entry: "a/b.html", Slot: SlotCorner, Permissions: []string{PermissionPanel}}, CapabilityUI), `permission "panel"`},
		"no entry":       {uiManifest(&UI{Slot: SlotCorner}, CapabilityUI), "ui needs an entry"},
		"entry escapes":  {uiManifest(&UI{Entry: "../x.html", Slot: SlotCorner}, CapabilityUI), "outside the plugin folder"},
		"entry absolute": {uiManifest(&UI{Entry: "/x.html", Slot: SlotCorner}, CapabilityUI), "outside the plugin folder"},
		"entry in root":  {uiManifest(&UI{Entry: "index.html", Slot: SlotCorner}, CapabilityUI), `entry "index.html" must be in a subfolder, since that whole folder is served`},
		"unknown slot":   {uiManifest(&UI{Entry: "a.html", Slot: "side"}, CapabilityUI), `slot "side"`},
		"unknown grant":  {uiManifest(&UI{Entry: "a.html", Slot: SlotCorner, Permissions: []string{"root"}}, CapabilityUI), `permission "root"`},
		"repeated grant": {uiManifest(&UI{Entry: "a.html", Slot: SlotCorner, Permissions: []string{PermissionCardsPush, PermissionCardsPush}}, CapabilityUI), "listed twice"},
	} {
		t.Run(name, func(t *testing.T) {
			err := tt.m.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
		})
	}
}

func TestManifestPermissions(t *testing.T) {
	action := &Manifest{Capabilities: []string{CapabilityAction}}
	if got := action.Permissions(); !reflect.DeepEqual(got, []string{PermissionLibraryRead}) {
		t.Errorf("an action plugin: %v", got)
	}
	both := &Manifest{Capabilities: []string{CapabilityAction, CapabilityUI},
		UI: &UI{Permissions: []string{PermissionLibraryRead, PermissionCardsPush}}}
	if got := both.Permissions(); !reflect.DeepEqual(got, []string{PermissionPanel, PermissionLibraryRead, PermissionCardsPush}) {
		t.Errorf("an action and ui plugin: %v", got)
	}
	if got := (&Manifest{Capabilities: []string{CapabilityPush}}).Permissions(); len(got) != 0 {
		t.Errorf("a target: %v", got)
	}
}
