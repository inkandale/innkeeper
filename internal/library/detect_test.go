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
		{"string id", `{"extensions":{"chub":{"id":"42","full_path":"a/b"}}}`, false, &Source{Kind: "chub", Ref: "a/b", ExternalID: "42", URL: "https://chub.ai/characters/a/b"}},
		{"dotdot", `{"extensions":{"chub":{"full_path":"../b"}}}`, false, nil},
		{"space", `{"extensions":{"chub":{"full_path":"a/b c"}}}`, false, nil},
		{"query", `{"extensions":{"chub":{"full_path":"a/b?x=1"}}}`, false, nil},
		{"three segments", `{"extensions":{"chub":{"full_path":"a/b/c"}}}`, false, nil},
		{"one segment", `{"extensions":{"chub":{"full_path":"a"}}}`, false, nil},
		{"nul", "{\"extensions\":{\"chub\":{\"full_path\":\"a/b\\u0000\"}}}", false, nil},
		{"nbsp", "{\"extensions\":{\"chub\":{\"full_path\":\"a/b\u00a0c\"}}}", false, nil},
		{"not json", `nope`, false, nil},
		{"chub data and source", `{"extensions":{"chub":{"id":7,"full_path":"a/b"}},"source":["https://chub.ai/characters/a/b"]}`, false, &Source{Kind: "chub", Ref: "a/b", ExternalID: "7", URL: "https://chub.ai/characters/a/b"}},
		{"risu source", `{"source":["risurealm:976E92F8-2C7E-4BF9-AEF4-586A7A753338","https://realm.risuai.net/character/976e92f8-2c7e-4bf9-aef4-586a7a753338"]}`, false, &Source{Kind: "risu", Ref: "976e92f8-2c7e-4bf9-aef4-586a7a753338", URL: "https://realm.risuai.net/character/976e92f8-2c7e-4bf9-aef4-586a7a753338"}},
		{"tavern source", `{"source":["https://character-tavern.com/character/ann/brakka"]}`, false, &Source{Kind: "character-tavern", Ref: "ann/brakka", URL: "https://character-tavern.com/character/ann/brakka"}},
		{"unknown source", `{"source":["https://example.com/x", 5, null]}`, false, nil},
		{"source not a list", `{"source":"https://chub.ai/characters/a/b"}`, false, nil},
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
		"http://chub.ai/characters/" + ref,
		"https://CHUB.AI/characters/" + ref,
		"https://chub.ai:443/characters/" + ref,
	} {
		s, err := ParseSourceURL(u)
		if err != nil || s.Kind != "chub" || s.Ref != ref {
			t.Errorf("%s: got %+v, %v", u, s, err)
		}
	}
	for _, u := range []string{"https://example.com/x", "https://chub.ai/characters/onlyone", "https://chub.ai/characters/a/b/c", "not a url",
		"https://chub.ai.evil.com/characters/a/b", "https://evilchub.ai/characters/a/b", "https://chub.ai@evil.com/characters/a/b",
		"ftp://chub.ai/characters/a/b", "https://chub.ai/characters/../b", "https://chub.ai/characters/%2e%2e/b", "https://chub.ai/characters/a%2Fb/c", "https://chub.ai/characters/a/b%00", "https://chub.ai/characters/a/b%C2%A0c", "//chub.ai/characters/a/b", "https://chub.ai/characters/a/%20b", "https://chub.ai/characters/a/b%3Fx", "https://chub.ai/characters/a/%5Cb"} {
		if _, err := ParseSourceURL(u); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", u, err)
		}
	}
}

func TestParseSourceURLLorebook(t *testing.T) {
	s, err := ParseSourceURL("https://www.chub.ai/lorebooks/a/b?x=1")
	if err != nil || s.Ref != "a/b" || s.URL != "https://chub.ai/lorebooks/a/b" {
		t.Fatalf("got %+v, %v", s, err)
	}
}

func TestValidateSource(t *testing.T) {
	got, err := ValidateSource(Source{Kind: "chub", Ref: "a/b", ExternalID: "5", URL: "https://evil.example"}, true)
	if err != nil || got.URL != "https://chub.ai/lorebooks/a/b" || got.ExternalID != "5" {
		t.Fatalf("got %+v, %v", got, err)
	}
	for _, s := range []Source{{Kind: "chub", Ref: "a"}, {Kind: "chub", Ref: "../b"}, {Kind: "chub", Ref: "a/b c"}, {Kind: "other", Ref: "a/b"}, {}} {
		if _, err := ValidateSource(s, false); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: got %v, want ErrInvalid", s, err)
		}
	}
}

func TestParseSourceURLSites(t *testing.T) {
	const risuID = "976e92f8-2c7e-4bf9-aef4-586a7a753338"
	for u, want := range map[string]Source{
		"https://characterhub.org/characters/a/b":           {Kind: "chub", Ref: "a/b", URL: "https://chub.ai/characters/a/b"},
		"https://www.characterhub.org/lorebooks/a/b":        {Kind: "chub", Ref: "a/b", URL: "https://chub.ai/lorebooks/a/b"},
		"https://venus.chub.ai/characters/a/b":              {Kind: "chub", Ref: "a/b", URL: "https://chub.ai/characters/a/b"},
		"https://chub.ai/a/b":                               {Kind: "chub", Ref: "a/b", URL: "https://chub.ai/characters/a/b"},
		"https://realm.risuai.net/character/" + risuID:      {Kind: "risu", Ref: risuID, URL: "https://realm.risuai.net/character/" + risuID},
		"risurealm:" + risuID:                               {Kind: "risu", Ref: risuID, URL: "https://realm.risuai.net/character/" + risuID},
		"https://character-tavern.com/character/ann/brakka": {Kind: "character-tavern", Ref: "ann/brakka", URL: "https://character-tavern.com/character/ann/brakka"},
	} {
		if got, err := ParseSourceURL(u); err != nil || got != want {
			t.Errorf("%s: got %+v, %v", u, got, err)
		}
	}
	for _, u := range []string{
		"https://realm.risuai.net/character/not-a-uuid", "risurealm:x", "https://evil.example/character/" + risuID,
		"https://character-tavern.com/character/ann", "https://character-tavern.com/ann/brakka", "https://character-tavern.com/character/../x",
		"https://character-tavern.com/character/ann/b%20c",
	} {
		if _, err := ParseSourceURL(u); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", u, err)
		}
	}
	if _, err := ValidateSource(Source{Kind: "risu", Ref: risuID}, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("risu lorebook: got %v, want ErrInvalid", err)
	}
}
