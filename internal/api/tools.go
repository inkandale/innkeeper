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

// How long an instance's tool list, or its failure to list them, is reused.
const (
	toolsTTL       = time.Minute
	toolsFailedTTL = 30 * time.Second
)

// errListing means a request is already asking the plugin for its tools.
var errListing = errors.New("still listing this instance's tools")

type cachedTools struct {
	version string
	at      time.Time
	tools   []protocol.Tool
	err     error
}

// instanceTools returns an instance's valid tools, from the cache if its
// settings haven't changed in the last minute.
func (s *Server) instanceTools(ctx context.Context, id int64) (pluginhost.Instance, []protocol.Tool, error) {
	inst, err := s.Registry.Instance(ctx, id)
	if err != nil {
		return inst, nil, err
	}
	if c, ok := s.tools.Load(id); ok {
		c := c.(cachedTools)
		ttl := toolsTTL
		if c.err != nil {
			ttl = toolsFailedTTL
		}
		if c.version == inst.Version && time.Since(c.at) < ttl {
			return inst, c.tools, c.err
		}
	}
	// Requests meanwhile skip the instance rather than wait for it.
	s.tools.Store(id, cachedTools{inst.Version, time.Now(), nil, errListing})
	// Detached from ctx, so a caller giving up doesn't cache a failure.
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var res protocol.MCPToolsResult
	err = s.Registry.Call(callCtx, s.Plugins, inst, protocol.MethodMCPTools, nil, &res)
	var tools []protocol.Tool
	seen := map[string]bool{}
	for _, t := range res.Tools {
		if err := t.Validate(); err != nil || seen[t.Name] {
			s.Log.Warn("skip plugin tool", "instance", id, "tool", t.Name, "err", err)
			continue
		}
		seen[t.Name] = true
		tools = append(tools, t)
	}
	s.tools.Store(id, cachedTools{inst.Version, time.Now(), tools, err})
	return inst, tools, err
}

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
			// Instances answer in parallel. The listing stops waiting after 3
			// seconds, leaving out slow ones; their calls finish in the
			// background and fill the cache.
			var mu sync.Mutex
			out := []PluginTool{}
			var wg sync.WaitGroup
			for _, i := range insts {
				if i.BrokenError != nil {
					continue
				}
				wg.Go(func() {
					_, tools, err := s.instanceTools(ctx, i.ID)
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
			}
			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
			}
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
			inst, tools, err := s.instanceTools(ctx, in.ID)
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
