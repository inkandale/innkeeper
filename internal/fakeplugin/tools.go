package main

import (
	"encoding/json"
	"os"

	"github.com/inkandale/innkeeper/protocol"
)

var object = json.RawMessage(`{"type": "object", "properties": {"text": {"type": "string"}}}`)

// mcpTools are the fake plugin's MCP tools, one per tier, plus one without a
// tier and one that crashes the plugin.
func mcpTools(mode string) protocol.MCPToolsResult {
	if mode == "bad-tools" {
		return protocol.MCPToolsResult{Tools: []protocol.Tool{{Name: "Bad Name", InputSchema: object}}}
	}
	return protocol.MCPToolsResult{Tools: []protocol.Tool{
		{Name: "echo", Description: "Repeats text.", InputSchema: object, Tier: protocol.TierRead},
		{Name: "note", Description: "Pretends to save text.", InputSchema: object, Tier: protocol.TierWrite},
		{Name: "wipe", Description: "Pretends to delete everything.", InputSchema: object, Tier: protocol.TierDestructive},
		{Name: "untiered", Description: "Declares no tier.", InputSchema: object},
		{Name: "crash", Description: "Exits the plugin.", InputSchema: object, Tier: protocol.TierRead},
	}}
}

func mcpCall(params json.RawMessage) (any, error) {
	var p protocol.MCPCallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
	}
	var args struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(p.Arguments, &args)
	switch p.Name {
	case "crash":
		os.Exit(4)
	case "echo", "note", "wipe", "untiered":
		return protocol.MCPCallResult{Content: []protocol.MCPContent{{Type: "text", Text: p.Name + ": " + args.Text}}}, nil
	}
	return protocol.MCPCallResult{IsError: true, Content: []protocol.MCPContent{{Type: "text", Text: "no tool " + p.Name}}}, nil
}
