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
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	iauth "github.com/inkandale/innkeeper/internal/auth"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/tier"
	"github.com/inkandale/innkeeper/protocol"
)

// tool is one MCP tool: what it needs, how it adds itself to a server, and
// how Call runs it.
type tool struct {
	tier tier.Tier
	add  func(s *sdk.Server, api http.Handler, t *sdk.Tool)
	call func(ctx context.Context, c caller, args json.RawMessage) (*sdk.CallToolResult, error)
	meta sdk.Tool
}

// def makes a tool whose input is In. run turns the input into one or more
// API calls through c.
func def[In any](name, description string, t tier.Tier, run func(ctx context.Context, c caller, in In) (*sdk.CallToolResult, error)) tool {
	return tool{tier: t, meta: sdk.Tool{Name: name, Description: description, InputSchema: inputSchema[In]()},
		call: func(ctx context.Context, c caller, args json.RawMessage) (*sdk.CallToolResult, error) {
			var in In
			if len(args) > 0 {
				if err := json.Unmarshal(args, &in); err != nil {
					return nil, fmt.Errorf("arguments: %w", err)
				}
			}
			return run(ctx, c, in)
		},
		add: func(s *sdk.Server, api http.Handler, meta *sdk.Tool) {
			sdk.AddTool(s, meta, func(ctx context.Context, req *sdk.CallToolRequest, in In) (*sdk.CallToolResult, any, error) {
				// The token the SDK verified, in the form the API takes.
				token, _ := req.Extra.TokenInfo.Extra["token"].(string)
				res, err := run(ctx, caller{api: api, auth: "Bearer " + token}, in)
				return res, nil, err
			})
		}}
}

// inputSchema is In's JSON Schema. A kind argument lists the library's kinds,
// so a new kind reaches every tool that takes one.
func inputSchema[In any]() *jsonschema.Schema {
	schema, err := jsonschema.For[In](nil)
	if err != nil {
		panic(err)
	}
	if k, ok := schema.Properties["kind"]; ok {
		k.Description = "Default " + library.Card.Name
		for _, kind := range library.Kinds {
			k.Enum = append(k.Enum, kind.Name)
		}
	}
	return schema
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

// do sends body as JSON, or a []byte body as a file.
func (c caller) do(ctx context.Context, method, path string, body any) (reply, error) {
	var b []byte
	contentType := "application/json"
	switch body := body.(type) {
	case nil:
	case []byte:
		b, contentType = body, "application/octet-stream"
	default:
		var err error
		if b, err = json.Marshal(body); err != nil {
			return reply{}, err
		}
	}
	req := httptest.NewRequestWithContext(iauth.ViaMCP(ctx), method, path, bytes.NewReader(b))
	if c.auth != "" {
		req.Header.Set("Authorization", c.auth)
	}
	if body != nil {
		req.Header.Set("Content-Type", contentType)
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

// Call runs the built-in tool name in process through api, with tier t and
// no credentials. A tool that needs more than t, or doesn't exist, is an
// error; a failure the model should see comes back with IsError.
func Call(ctx context.Context, api http.Handler, t tier.Tier, name string, args json.RawMessage) (protocol.MCPCallResult, error) {
	i := slices.IndexFunc(tools, func(tl tool) bool { return tl.meta.Name == name })
	if i < 0 {
		return protocol.MCPCallResult{}, fmt.Errorf("no tool %s", name)
	}
	if !t.Allows(tools[i].tier) {
		return protocol.MCPCallResult{}, fmt.Errorf("tool %s needs %s; this caller has %s", name, tools[i].tier, t)
	}
	res, err := tools[i].call(iauth.InProcess(ctx, t), caller{api: api}, args)
	if err != nil {
		res = &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: err.Error()}}}
	}
	out := protocol.MCPCallResult{IsError: res.IsError, Content: []protocol.MCPContent{}}
	for _, c := range res.Content {
		if text, ok := c.(*sdk.TextContent); ok {
			out.Content = append(out.Content, protocol.MCPContent{Type: "text", Text: text.Text})
		}
	}
	return out, nil
}

// New returns the /mcp handler. api is the API's handler, which tools call,
// and jobs are its routes that queue a job, for start_job. Clients
// authenticate with an API token or an OAuth access token; each sees the
// built-in and plugin tools its tier allows. resourceMetadata, if set, is
// named in the 401 so clients can find the OAuth server.
func New(api http.Handler, jobs []JobRoute, a *iauth.Auth, log *slog.Logger, resourceMetadata string) http.Handler {
	// Stateless servers are built per request; the cache keeps that cheap.
	cache := sdk.NewSchemaCache()
	// Built once per tier, so the cache sees the same schemas each request.
	startJobs := map[tier.Tier]tool{}
	for _, t := range []tier.Tier{tier.Read, tier.Write, tier.Destructive} {
		if tl, ok := startJob(jobs, t); ok {
			startJobs[t] = tl
		}
	}
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
		taken := map[string]bool{"start_job": true}
		all := tools
		if tl, ok := startJobs[t]; ok {
			all = append(slices.Clip(tools), tl)
		}
		for _, tl := range all {
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
