package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/davidgibbons/innkeeper/internal/tier"
)

// pluginTool is an entry of GET /plugin-tools.
type pluginTool struct {
	InstanceID   int64           `json:"instance_id"`
	InstanceName string          `json:"instance_name"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	Tier         string          `json:"tier"`
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// prefix is the part of a plugin tool's name that names its instance.
func prefix(instance string) string {
	p := strings.Trim(nonWord.ReplaceAllString(strings.ToLower(instance), "_"), "_")
	return strings.TrimRight(p[:min(len(p), 20)], "_")
}

// addPluginTools adds the plugin tools caller's tier allows, named
// <instance>_<tool>, or <instance>_<id>_<tool> where that is taken.
func addPluginTools(ctx context.Context, s *sdk.Server, c caller, t tier.Tier, taken map[string]bool, log *slog.Logger) {
	r, err := c.do(ctx, "GET", "/plugin-tools", nil)
	var list []pluginTool
	if err == nil && r.ok() {
		err = json.Unmarshal(r.body, &list)
	}
	if err != nil || !r.ok() {
		log.Warn("list plugin tools for MCP", "status", r.status, "err", err)
		return
	}
	for _, pt := range list {
		need, err := tier.Parse(pt.Tier)
		if err != nil || !t.Allows(need) {
			continue
		}
		name := prefix(pt.InstanceName) + "_" + pt.Name
		if taken[name] {
			name = fmt.Sprintf("%s_%d_%s", prefix(pt.InstanceName), pt.InstanceID, pt.Name)
		}
		if taken[name] || len(name) > 64 {
			log.Warn("skip plugin tool", "instance", pt.InstanceID, "tool", pt.Name, "as", name)
			continue
		}
		taken[name] = true
		path := fmt.Sprintf("/instances/%d/tools/%s", pt.InstanceID, pt.Name)
		s.AddTool(&sdk.Tool{Name: name, Description: fmt.Sprintf("%s (from %s)", pt.Description, pt.InstanceName),
			InputSchema: pt.InputSchema, Annotations: annotations(need)},
			func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				return callPluginTool(ctx, c, path, req.Params.Arguments)
			})
	}
}

func callPluginTool(ctx context.Context, c caller, path string, args json.RawMessage) (*sdk.CallToolResult, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	r, err := c.do(ctx, "POST", path, map[string]json.RawMessage{"arguments": args})
	if err != nil || !r.ok() {
		return result(r), err
	}
	var res struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"is_error"`
	}
	if err := json.Unmarshal(r.body, &res); err != nil {
		return nil, err
	}
	out := &sdk.CallToolResult{IsError: res.IsError}
	for _, ct := range res.Content {
		out.Content = append(out.Content, &sdk.TextContent{Text: ct.Text})
	}
	return out, nil
}
