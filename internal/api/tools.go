package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/pluginhost"
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

// toolsTTL is how long an instance's tool list is reused.
const toolsTTL = time.Minute

type cachedTools struct {
	version string
	at      time.Time
	tools   []protocol.Tool
}

// instanceTools returns an instance's valid tools, from the cache if its
// settings haven't changed in the last minute.
func (s *Server) instanceTools(ctx context.Context, id int64) (pluginhost.Instance, []protocol.Tool, error) {
	inst, err := s.Registry.Instance(ctx, id)
	if err != nil {
		return inst, nil, err
	}
	if c, ok := s.tools.Load(id); ok {
		if c := c.(cachedTools); c.version == inst.Version && time.Since(c.at) < toolsTTL {
			return inst, c.tools, nil
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var res protocol.MCPToolsResult
	if err := s.Registry.Call(callCtx, s.Plugins, inst, protocol.MethodMCPTools, nil, &res); err != nil {
		return inst, nil, err
	}
	var tools []protocol.Tool
	for _, t := range res.Tools {
		if err := t.Validate(); err != nil {
			s.Log.Warn("skip plugin tool", "instance", id, "err", err)
			continue
		}
		tools = append(tools, t)
	}
	s.tools.Store(id, cachedTools{inst.Version, time.Now(), tools})
	return inst, tools, nil
}

func (s *Server) toolRoutes(api huma.API) {
	tags := []string{"Plugin tools"}
	huma.Register(api, huma.Operation{OperationID: "list-plugin-tools", Method: http.MethodGet, Path: "/plugin-tools",
		Summary: "List the MCP tools plugin instances offer", Tags: tags,
		Description: "Instances that are broken, or whose plugin doesn't answer, are left out."},
		func(ctx context.Context, _ *struct{}) (*pluginToolsOut, error) {
			insts, err := s.Registry.ListInstances(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			out := []PluginTool{}
			for _, i := range insts {
				if i.BrokenError != nil || !s.Registry.HasCapability(i.Plugin, "mcp") {
					continue
				}
				_, tools, err := s.instanceTools(ctx, i.ID)
				if err != nil {
					s.Log.Warn("list plugin tools", "instance", i.ID, "err", err)
					continue
				}
				for _, t := range tools {
					out = append(out, PluginTool{i.ID, i.Name, t.Name, t.Description, t.InputSchema, t.EffectiveTier()})
				}
			}
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
			inst, tools, err := s.instanceTools(ctx, in.ID)
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
				res = protocol.MCPCallResult{IsError: true, Content: []protocol.MCPContent{{Type: "text", Text: rpcErr.Message}}}
			} else if err != nil {
				return nil, s.fail(err)
			}
			return &callToolOut{Body: res}, nil
		})
}
