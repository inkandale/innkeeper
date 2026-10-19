package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/protocol"
)

const (
	checkMCPToolsName = "mcp.tools returns valid tools"
	checkMCPCallsName = "mcp.call of each tool with empty arguments returns a result or a plugin error"
)

// emptyArguments is what the check calls each tool with. A tool that needs
// arguments answers with an error.
var emptyArguments = json.RawMessage(`{}`)

// checkMCP lists the plugin's tools, then calls each.
func checkMCP(ctx context.Context, p *pluginhost.Process) []Result {
	var res protocol.MCPToolsResult
	err := call(ctx, p, protocol.MethodMCPTools, nil, &res)
	if err == nil {
		seen := map[string]bool{}
		var errs []error
		for _, t := range res.Tools {
			if seen[t.Name] {
				errs = append(errs, fmt.Errorf("tool name %q repeats", t.Name))
			}
			seen[t.Name] = true
			errs = append(errs, t.Validate())
		}
		err = errors.Join(errs...)
	}
	results := []Result{{checkMCPToolsName, err}}
	if err != nil || dead(err, p) {
		return results
	}
	return append(results, Result{checkMCPCallsName, checkMCPCalls(ctx, p, res.Tools)})
}

// checkMCPCalls calls each tool. The core shows the model a plugin error as
// the tool's failure, so an error other than -32601 counts as an answer.
func checkMCPCalls(ctx context.Context, p *pluginhost.Process, tools []protocol.Tool) error {
	for _, t := range tools {
		var res protocol.MCPCallResult
		err := call(ctx, p, protocol.MethodMCPCall, protocol.MCPCallParams{Name: t.Name, Arguments: emptyArguments}, &res)
		if answered(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("%s: %w", t.Name, err)
		}
	}
	return nil
}

// driveMCP calls each listed tool with empty arguments.
func driveMCP(ctx context.Context, call caller) []func(context.Context) error {
	var res protocol.MCPToolsResult
	if call(ctx, protocol.MethodMCPTools, nil, &res) != nil {
		return nil
	}
	var out []func(context.Context) error
	for _, t := range res.Tools {
		params := protocol.MCPCallParams{Name: t.Name, Arguments: emptyArguments}
		out = append(out, func(ctx context.Context) error { return call(ctx, protocol.MethodMCPCall, params, nil) })
	}
	return out
}
