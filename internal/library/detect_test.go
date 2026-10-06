package library

import (
	"errors"
	"testing"
)

func TestDetect(t *testing.T) {
	const sample = `{"extensions": {"chub": {"id": 3604386, "full_path": "frerena/the-demon-of-desire-6bf0cd2c0915"}}}`
	tests := []struct {
		name, data string
		lorebook   bool
		want       *Source
	}{
		{"card", sample, false, &Source{Kind: "chub", Ref: "frerena/the-demon-of-desire-6bf0cd2c0915", ExternalID: "3604386", URL: "https://chub.ai/characters/frerena/the-demon-of-desire-6bf0cd2c0915"}},
		{"lorebook", sample, true, &Source{Kind: "chub", Ref: "frerena/the-demon-of-desire-6bf0cd2c0915", ExternalID: "3604386", URL: "https://chub.ai/lorebooks/frerena/the-demon-of-desire-6bf0cd2c0915"}},
		{"large id", `{"extensions":{"chub":{"id":12345678901234567,"full_path":"a/b"}}}`, false, &Source{Kind: "chub", Ref: "a/b", ExternalID: "12345678901234567", URL: "https://chub.ai/characters/a/b"}},
		{"no id", `{"extensions":{"chub":{"full_path":"a/b"}}}`, false, &Source{Kind: "chub", Ref: "a/b", URL: "https://chub.ai/characters/a/b"}},
		{"empty path", `{"extensions":{"chub":{"id":1,"full_path":""}}}`, false, nil},
		{"no extensions", `{"name":"x"}`, false, nil},
		{"chub not object", `{"extensions":{"chub":"x"}}`, false, nil},
		{"not json", `nope`, false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Detect([]byte(tt.data), tt.lorebook)
			if tt.want == nil {
				if len(got) != 0 {
					t.Fatalf("got %v, want none", got)
				}
				return
			}
			if len(got) != 1 || got[0] != *tt.want {
				t.Fatalf("got %+v, want %+v", got, *tt.want)
			}
		})
	}
}

func TestParseSourceURL(t *testing.T) {
	const ref = "frerena/the-demon-of-desire-6bf0cd2c0915"
	for _, u := range []string{
		"https://chub.ai/characters/" + ref,
		"https://www.chub.ai/characters/" + ref,
		"https://chub.ai/characters/" + ref + "/",
		"https://chub.ai/characters/" + ref + "?tab=chats",
		"https://chub.ai/lorebooks/" + ref,
	} {
		s, err := ParseSourceURL(u)
		if err != nil || s.Kind != "chub" || s.Ref != ref {
			t.Errorf("%s: got %+v, %v", u, s, err)
		}
	}
	for _, u := range []string{"https://example.com/x", "https://chub.ai/characters/onlyone", "https://chub.ai/characters/a/b/c", "not a url"} {
		if _, err := ParseSourceURL(u); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", u, err)
		}
	}
}
