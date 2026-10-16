package targets

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/inkandale/innkeeper/internal/merge"
	"github.com/inkandale/innkeeper/protocol"
)

func TestPlanBind(t *testing.T) {
	info := protocol.TargetInfo{Fields: []string{"name", "description"}}
	head := headRow{id: 1, data: json.RawMessage(`{"name":"Brakka","description":"A dwarf.","tags":["smith"]}`),
		overrides: json.RawMessage(`{}`), avatar: "lib"}
	pinned := head
	pinned.overrides = json.RawMessage(`{"7":{"description":"A dwarf of Tor."}}`)
	appData := json.RawMessage(`{"description":"A tall dwarf.","name":"Brakka"}`)
	_, appHash, err := Project(appData, info.Fields)
	if err != nil {
		t.Fatal(err)
	}
	tall := json.RawMessage(`{"name":"Brakka","description":"A tall dwarf.","tags":["smith"]}`)
	tests := []struct {
		name          string
		head          headRow
		def           merge.Choice
		appAvatar     string
		takeAppAvatar bool
		sameAvatar    bool
		want          bindPlan
	}{
		{name: "library default changes nothing and leaves the app behind", head: head, def: merge.ChooseLibrary,
			appAvatar: "lib", want: bindPlan{base: head.data, avatar: "lib", appBehind: true}},
		{name: "app default revises the base and keeps fields the target lacks", head: head, def: merge.ChooseApp,
			appAvatar: "lib", want: bindPlan{base: tall, overrides: json.RawMessage(`{}`), avatar: "lib", revise: true}},
		{name: "keep in app writes the override, not the base", head: head, def: merge.ChooseKeepInApp,
			appAvatar: "lib", want: bindPlan{base: head.data,
				overrides: json.RawMessage(`{"7":{"description":"A tall dwarf."}}`), avatar: "lib", revise: true}},
		{name: "app on an overridden field changes the override", head: pinned, def: merge.ChooseApp,
			appAvatar: "lib", want: bindPlan{base: head.data,
				overrides: json.RawMessage(`{"7":{"description":"A tall dwarf."}}`), avatar: "lib", revise: true}},
		{name: "taking a different app avatar revises", head: head, def: merge.ChooseApp, appAvatar: "app",
			takeAppAvatar: true, want: bindPlan{base: tall, overrides: json.RawMessage(`{}`), avatar: "app", revise: true}},
		{name: "taking the same picture keeps the library's", head: head, def: merge.ChooseLibrary, appAvatar: "app",
			takeAppAvatar: true, sameAvatar: true, want: bindPlan{base: head.data, avatar: "lib", appBehind: true}},
		{name: "leaving a different app avatar puts the app behind", head: head, def: merge.ChooseApp, appAvatar: "app",
			want: bindPlan{base: tall, overrides: json.RawMessage(`{}`), avatar: "lib", appBehind: true, revise: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := appItem{data: appData, hash: appHash, avatar: tt.appAvatar}
			got, err := planBind(cardKind, info, bindInput{head: tt.head, app: app, instanceID: 7, def: tt.def,
				takeAppAvatar: tt.takeAppAvatar, sameAvatar: tt.sameAvatar})
			if err != nil {
				t.Fatal(err)
			}
			if !sameJSON(t, got.base, tt.want.base) || !sameJSON(t, got.overrides, tt.want.overrides) ||
				got.avatar != tt.want.avatar || got.appBehind != tt.want.appBehind || got.revise != tt.want.revise {
				t.Errorf("planBind = {base %s, overrides %s, avatar %q, appBehind %v, revise %v}, want {%s, %s, %q, %v, %v}",
					got.base, got.overrides, got.avatar, got.appBehind, got.revise,
					tt.want.base, tt.want.overrides, tt.want.avatar, tt.want.appBehind, tt.want.revise)
			}
		})
	}
}

func sameJSON(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(x, y)
}
