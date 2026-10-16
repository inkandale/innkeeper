package protocol

import (
	"strings"
	"testing"
)

func TestSourceValidate(t *testing.T) {
	ok := Source{Kind: "chub", Ref: "a/b", ExternalID: "7", URL: "https://chub.ai/characters/a/b"}
	tests := []struct {
		name    string
		edit    func(*Source)
		wantErr string
	}{
		{"valid", func(*Source) {}, ""},
		{"kind with a dash", func(s *Source) { s.Kind = "character-tavern" }, ""},
		{"no kind", func(s *Source) { s.Kind = "" }, "kind"},
		{"upper-case kind", func(s *Source) { s.Kind = "Chub" }, "kind"},
		{"kind starting with a digit", func(s *Source) { s.Kind = "9chub" }, "kind"},
		{"empty ref", func(s *Source) { s.Ref = "" }, "ref is empty"},
		{"long ref", func(s *Source) { s.Ref = strings.Repeat("a", 513) }, "ref is longer"},
		{"long external_id", func(s *Source) { s.ExternalID = strings.Repeat("1", 129) }, "external_id is longer"},
		{"http url", func(s *Source) { s.URL = "http://chub.ai/a/b" }, "https"},
		{"no url", func(s *Source) { s.URL = "" }, "https"},
		{"url without a host", func(s *Source) { s.URL = "https:///a" }, "https"},
		{"long url", func(s *Source) { s.URL = "https://chub.ai/" + strings.Repeat("a", 2048) }, "url is longer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ok
			tt.edit(&s)
			err := s.Validate()
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
