package main

import (
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/internal/tier"
)

func TestProvisionedTokensReadsEachSecret(t *testing.T) {
	secret := strings.Repeat("s", 32)
	env := map[string]string{
		"INNKEEPER_API_TOKENS":              "agent-gateway:destructive, ci:read",
		"INNKEEPER_API_TOKEN_AGENT_GATEWAY": secret,
		"INNKEEPER_API_TOKEN_CI":            secret + "2",
	}
	toks, err := provisionedTokens(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if len(toks) != 2 || toks[0].Name != "agent-gateway" || toks[0].Tier != tier.Destructive || toks[0].Secret != secret ||
		toks[1].Name != "ci" || toks[1].Tier != tier.Read {
		t.Fatalf("tokens = %+v", toks)
	}
}

func TestProvisionedTokensRejectsBadConfig(t *testing.T) {
	for _, env := range []map[string]string{
		{"INNKEEPER_API_TOKENS": "gw:destructive"},
		{"INNKEEPER_API_TOKENS": "gw:destructive", "INNKEEPER_API_TOKEN_GW": "short"},
		{"INNKEEPER_API_TOKENS": "gw:destructive", "INNKEEPER_API_TOKEN_GW": "%generate:password:length=48;num_symbols=0"},
		{"INNKEEPER_API_TOKENS": "gw:admin", "INNKEEPER_API_TOKEN_GW": strings.Repeat("s", 32)},
		{"INNKEEPER_API_TOKENS": "GW:read", "INNKEEPER_API_TOKEN_GW": strings.Repeat("s", 32)},
	} {
		if _, err := provisionedTokens(func(k string) string { return env[k] }); err == nil {
			t.Errorf("provisionedTokens(%v) succeeded", env)
		}
	}
}
