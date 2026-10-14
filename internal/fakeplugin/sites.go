package main

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/davidgibbons/innkeeper/protocol"
)

// The fake site is https://fake.example/{a}/{b}, kind fake, ref a/b. Config
// changes one instance: sites_fail (retryable or fatal), sites_hang,
// sites_bad (sends an invalid source), and sites_tag (the external_id it
// sends). The sites- modes each break one rule the contract kit checks.

func fakeSource(raw string, config map[string]any) (protocol.Source, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "fake.example" {
		return protocol.Source{}, false
	}
	return fakeRef(strings.Trim(u.Path, "/"), config)
}

func fakeRef(ref string, config map[string]any) (protocol.Source, bool) {
	a, b, ok := strings.Cut(ref, "/")
	if !ok || a == "" || b == "" || strings.Contains(b, "/") {
		return protocol.Source{}, false
	}
	tag, _ := config["sites_tag"].(string)
	s := protocol.Source{Kind: "fake", Ref: ref, ExternalID: tag, URL: "https://fake.example/" + ref}
	if config["sites_bad"] == true {
		s.Kind = "Not A Kind"
	}
	return s, true
}

// sitesTrouble is the failure config asks for, or nil.
func sitesTrouble(config map[string]any) error {
	if config["sites_hang"] == true {
		time.Sleep(time.Minute)
	}
	switch config["sites_fail"] {
	case "retryable":
		return protocol.NewError(-32000, "sites unavailable", true)
	case "fatal":
		return protocol.NewError(-32000, "sites broke", false)
	}
	return nil
}

func validEntity(e string) bool { return e == protocol.KindCard || e == protocol.KindLorebook }

func sitesDetect(config map[string]any, params json.RawMessage, mode string) (any, error) {
	if err := sitesTrouble(config); err != nil {
		return nil, err
	}
	var p protocol.SitesDetectParams
	_ = json.Unmarshal(params, &p)
	var data struct {
		Source []string `json:"source"`
	}
	if json.Unmarshal(p.Data, &map[string]any{}) != nil || !validEntity(p.Entity) {
		if mode != "sites-lax-data" {
			return nil, protocol.NewError(protocol.CodeInvalidParams, "data must be an object", false)
		}
	}
	_ = json.Unmarshal(p.Data, &data)
	out := []protocol.Source{}
	switch mode {
	case "sites-noisy":
		out = append(out, protocol.Source{Kind: "fake", Ref: "always/here", URL: "https://fake.example/always/here"})
	case "sites-bad-source":
		out = append(out, protocol.Source{Kind: "fake", Ref: "a/b", URL: "http://fake.example/a/b"})
	}
	for _, raw := range data.Source {
		if s, ok := fakeSource(raw, config); ok {
			out = append(out, s)
		}
	}
	return protocol.SitesDetectResult{Sources: out}, nil
}

func sitesParse(config map[string]any, params json.RawMessage, mode string) (any, error) {
	if err := sitesTrouble(config); err != nil {
		return nil, err
	}
	var p protocol.SitesParseParams
	_ = json.Unmarshal(params, &p)
	byURL, byRef := p.URL != "", p.Kind != "" || p.Ref != ""
	if (byURL == byRef || !validEntity(p.Entity)) && mode != "sites-lax-params" {
		return nil, protocol.NewError(protocol.CodeInvalidParams, "send a url, or a kind and ref", false)
	}
	if mode == "sites-claims" {
		return protocol.SitesParseResult{Source: &protocol.Source{Kind: "fake", Ref: "any/thing", URL: "https://fake.example/any/thing"}}, nil
	}
	if byURL {
		if s, ok := fakeSource(p.URL, config); ok {
			return protocol.SitesParseResult{Source: &s}, nil
		}
		return protocol.SitesParseResult{}, nil
	}
	if p.Kind != "fake" {
		return protocol.SitesParseResult{}, nil
	}
	s, ok := fakeRef(p.Ref, config)
	if !ok {
		return nil, protocol.NewError(protocol.CodeInvalidParams, "a fake ref is <a>/<b>", false)
	}
	return protocol.SitesParseResult{Source: &s}, nil
}
