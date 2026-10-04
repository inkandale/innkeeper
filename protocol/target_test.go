package protocol

import (
	"strings"
	"testing"
)

func TestTargetInfoValidate(t *testing.T) {
	tests := []struct {
		name    string
		info    TargetInfo
		wantErr string
	}{
		{"valid", TargetInfo{Fields: []string{"name"}, Lorebooks: LorebooksEmbedded}, ""},
		{"no fields", TargetInfo{Lorebooks: LorebooksNone}, "fields is empty"},
		{"bad lorebooks", TargetInfo{Fields: []string{"name"}, Lorebooks: "sometimes"}, `lorebooks "sometimes"`},
		{"missing lorebooks", TargetInfo{Fields: []string{"name"}}, `lorebooks ""`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.info.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}
