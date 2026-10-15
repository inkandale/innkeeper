package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/internal/tier"
	"github.com/davidgibbons/innkeeper/protocol"
)

// PluginTool is an MCP tool a plugin instance offers.
type PluginTool struct {
	InstanceID   int64           `json:"instance_id"`
	InstanceName string          `json:"instance_name"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	Tier         string          `json:"tier" enum:"read,write,destructive"`
	// MCPName is the tool's name at /mcp: <instance>_<tool>, with the
	// instance ID added when another instance's name makes the same prefix.
	MCPName string `json:"mcp_name"`
}

type pluginToolsOut struct{ Body []PluginTool }

type callToolIn struct {
	ID   int64  `path:"id"`
	Name string `path:"name"`
	Body struct {
		Arguments map[string]any `json:"arguments,omitempty"`
	}
}

type callToolOut struct{ Body protocol.MCPCallResult }

// mcpTools lists an instance's MCP tools.
var mcpTools = listMethod[protocol.MCPToolsResult, protocol.Tool]{name: protocol.MethodMCPTools,
	items: func(r protocol.MCPToolsResult) []protocol.Tool { return r.Tools },
	key:   func(t protocol.Tool) string { return t.Name }, validate: protocol.Tool.Validate}

func (s *Server) toolRoutes(api huma.API) {
	tags := []string{"Plugin tools"}
	huma.Register(api, huma.Operation{OperationID: "list-plugin-tools", Method: http.MethodGet, Path: "/plugin-tools",
		Summary: "List the MCP tools plugin instances offer", Tags: tags,
		Description: "Instances that are broken, or whose plugin doesn't answer, are left out."},
		func(ctx context.Context, _ *struct{}) (*pluginToolsOut, error) {
			insts, err := s.Registry.InstancesWith(ctx, "mcp")
			if err != nil {
				return nil, s.fail(err)
			}
			// Prefixes come from every instance's name, working or not, so a
			// tool's name doesn't change when another instance goes down.
			prefixes := map[string]int{}
			for _, i := range insts {
				prefixes[toolPrefix(i.Name)]++
			}
			var mu sync.Mutex
			out := []PluginTool{}
			eachWorking(insts, func(i plugins.InstanceInfo) {
				_, tools, err := instanceList(ctx, s, &s.tools, mcpTools, i.ID)
				if err != nil {
					s.Log.Warn("list plugin tools", "instance", i.ID, "err", err)
					return
				}
				prefix := toolPrefix(i.Name)
				if prefixes[prefix] > 1 {
					prefix = fmt.Sprintf("%s_%d", prefix, i.ID)
				}
				mu.Lock()
				defer mu.Unlock()
				for _, t := range tools {
					out = append(out, PluginTool{i.ID, i.Name, t.Name, t.Description, t.InputSchema,
						t.EffectiveTier(), prefix + "_" + t.Name})
				}
			})
			mu.Lock()
			defer mu.Unlock()
			out = slices.Clone(out)
			slices.SortFunc(out, func(a, b PluginTool) int { return strings.Compare(a.MCPName, b.MCPName) })
			return &pluginToolsOut{Body: out}, nil
		})

	// The route needs only read; the tool's own tier is checked once known.
	huma.Register(api, huma.Operation{Metadata: needs(tier.Read), OperationID: "call-plugin-tool", Method: http.MethodPost,
		Path: "/instances/{id}/tools/{name}", Summary: "Run a plugin instance's MCP tool", Tags: tags,
		Description: "Needs the tier the tool declares. A failure the tool reports comes back with is_error set."},
		func(ctx context.Context, in *callToolIn) (*callToolOut, error) {
			if err := s.usable(ctx, in.ID, "mcp"); err != nil {
				return nil, err
			}
			inst, tools, err := instanceList(ctx, s, &s.tools, mcpTools, in.ID)
			if errors.Is(err, errListing) {
				return nil, huma.Error503ServiceUnavailable("the plugin is busy listing its tools; try again shortly")
			}
			if err != nil {
				return nil, s.fail(err)
			}
			var tool *protocol.Tool
			for i := range tools {
				if tools[i].Name == in.Name {
					tool = &tools[i]
				}
			}
			if tool == nil {
				return nil, huma.Error404NotFound(fmt.Sprintf("instance %d has no tool %s", in.ID, in.Name))
			}
			need, _ := tier.Parse(tool.EffectiveTier())
			if caller := callerTier(ctx); !caller.Allows(need) {
				return nil, huma.Error403Forbidden(fmt.Sprintf("this token's tier is %s; tool %s needs %s", caller, in.Name, need))
			}
			args, err := json.Marshal(in.Body.Arguments)
			if err != nil {
				return nil, err
			}
			callCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			var res protocol.MCPCallResult
			err = s.Registry.Call(callCtx, s.Plugins, inst, protocol.MethodMCPCall,
				protocol.MCPCallParams{Name: in.Name, Arguments: args}, &res)
			var rpcErr *protocol.Error
			if errors.As(err, &rpcErr) {
				// The plugin answered with an error the model should see.
				s.Log.Info("plugin tool failed", "instance", in.ID, "tool", in.Name, "err", err)
				res = protocol.MCPCallResult{IsError: true, Content: []protocol.MCPContent{{Type: "text", Text: rpcErr.Message}}}
			} else if err != nil {
				return nil, s.fail(err)
			}
			return &callToolOut{Body: res}, nil
		})
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// toolPrefix is the part of a plugin tool's MCP name that names its instance.
func toolPrefix(instance string) string {
	p := strings.Trim(nonWord.ReplaceAllString(strings.ToLower(instance), "_"), "_")
	return strings.TrimRight(p[:min(len(p), 20)], "_")
}
