// Package mcp serves Innkeeper's MCP tools over Streamable HTTP. Each tool
// is an API request, served in process with the caller's credentials, so
// tools get the API's validation, errors, and tier checks.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	iauth "github.com/davidgibbons/innkeeper/internal/auth"
	"github.com/davidgibbons/innkeeper/internal/tier"
)

// tool is one MCP tool: what it needs, and how it adds itself to a server.
type tool struct {
	tier tier.Tier
	add  func(s *sdk.Server, api http.Handler, t *sdk.Tool)
	meta sdk.Tool
}

// def makes a tool whose input is In. run turns the input into one or more
// API calls through c.
func def[In any](name, description string, t tier.Tier, run func(ctx context.Context, c caller, in In) (*sdk.CallToolResult, error)) tool {
	return tool{tier: t, meta: sdk.Tool{Name: name, Description: description},
		add: func(s *sdk.Server, api http.Handler, meta *sdk.Tool) {
			sdk.AddTool(s, meta, func(ctx context.Context, req *sdk.CallToolRequest, in In) (*sdk.CallToolResult, any, error) {
				// The token the SDK verified, in the form the API takes.
				token, _ := req.Extra.TokenInfo.Extra["token"].(string)
				res, err := run(ctx, caller{api: api, auth: "Bearer " + token}, in)
				return res, nil, err
			})
		}}
}

// caller makes API requests as the MCP client.
type caller struct {
	api  http.Handler
	auth string
}

// reply is an API response.
type reply struct {
	status int
	body   []byte
}

func (r reply) ok() bool { return r.status >= 200 && r.status < 300 }

func (c caller) do(ctx context.Context, method, path string, body any) (reply, error) {
	var b []byte
	if body != nil {
		var err error
		if b, err = json.Marshal(body); err != nil {
			return reply{}, err
		}
	}
	req := httptest.NewRequestWithContext(iauth.ViaMCP(ctx), method, path, bytes.NewReader(b))
	req.Header.Set("Authorization", c.auth)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	c.api.ServeHTTP(rec, req)
	return reply{rec.Code, rec.Body.Bytes()}, nil
}

// call makes one API request and returns its result as the tool's result.
func (c caller) call(ctx context.Context, method, path string, body any) (*sdk.CallToolResult, error) {
	r, err := c.do(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	return result(r), nil
}

// result is the tool result for an API response: its JSON body, or a tool
// error with the status and the API's problem details.
func result(r reply) *sdk.CallToolResult {
	text := strings.TrimSpace(string(r.body))
	if text == "" {
		text = http.StatusText(r.status)
	}
	if !r.ok() {
		text = fmt.Sprintf("%d %s: %s", r.status, http.StatusText(r.status), text)
	}
	return &sdk.CallToolResult{IsError: !r.ok(), Content: []sdk.Content{&sdk.TextContent{Text: text}}}
}

// annotations tell clients what a tool does, so they ask before destructive ones.
func annotations(t tier.Tier) *sdk.ToolAnnotations {
	destructive := t == tier.Destructive
	return &sdk.ToolAnnotations{ReadOnlyHint: t == tier.Read, DestructiveHint: &destructive}
}

// New returns the /mcp handler. api is the API's handler, which tools call.
// Clients authenticate with an API token or an OAuth access token; each sees
// the built-in and plugin tools its tier allows. resourceMetadata, if set, is
// named in the 401 so clients can find the OAuth server.
func New(api http.Handler, a *iauth.Auth, log *slog.Logger, resourceMetadata string) http.Handler {
	// Stateless servers are built per request; the cache keeps that cheap.
	cache := sdk.NewSchemaCache()
	h := sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		info := auth.TokenInfoFromContext(r.Context())
		if info == nil || len(info.Scopes) != 1 {
			return nil
		}
		t, err := tier.Parse(info.Scopes[0])
		if err != nil {
			return nil
		}
		token, _ := info.Extra["token"].(string)
		s := sdk.NewServer(&sdk.Implementation{Name: "innkeeper", Version: "0.1.0"}, &sdk.ServerOptions{SchemaCache: cache})
		taken := map[string]bool{}
		for _, tl := range tools {
			taken[tl.meta.Name] = true
			if t.Allows(tl.tier) {
				meta := tl.meta
				meta.Annotations = annotations(tl.tier)
				tl.add(s, api, &meta)
			}
		}
		addPluginTools(r.Context(), s, caller{api: api, auth: "Bearer " + token}, t, taken, log)
		return s
	}, &sdk.StreamableHTTPOptions{Stateless: true, CrossOriginProtection: http.NewCrossOriginProtection()})
	verify := func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		info := &auth.TokenInfo{Extra: map[string]any{"token": token}}
		t, err := a.CheckToken(ctx, token)
		if err == nil && t == 0 {
			t, info.Expiration, err = a.CheckAccessToken(ctx, token, iauth.ResourceMCP)
		}
		if err != nil {
			return nil, err
		}
		if t == 0 {
			return nil, auth.ErrInvalidToken
		}
		info.Scopes = []string{t.String()}
		return info, nil
	}
	// API tokens don't expire; OAuth access tokens carry their expiry.
	return auth.RequireBearerToken(verify, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true,
		ResourceMetadataURL: resourceMetadata})(h)
}
