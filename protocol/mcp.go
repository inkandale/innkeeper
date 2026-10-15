package protocol

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// The mcp capability adds MCP tools. The core lists them with mcp.tools and
// runs one with mcp.call.
const (
	MethodMCPTools = "mcp.tools"
	MethodMCPCall  = "mcp.call"
)

// Tool tiers: what a tool may do, and so who may call it.
const (
	TierRead        = "read"
	TierWrite       = "write"
	TierDestructive = "destructive"
)

var toolName = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)

// Tool is one MCP tool a plugin offers.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	// Tier is read if the tool changes nothing, write if it changes only
	// what can be undone, and destructive otherwise. Unset is destructive.
	Tier string `json:"tier,omitempty"`
}

// EffectiveTier is the tool's tier, treating a missing or unknown one as
// destructive.
func (t Tool) EffectiveTier() string {
	switch t.Tier {
	case TierRead, TierWrite:
		return t.Tier
	}
	return TierDestructive
}

// Validate checks the tool's name and input schema.
func (t Tool) Validate() error {
	if !toolName.MatchString(t.Name) {
		return fmt.Errorf("tool name %q must be 1 to 40 lowercase letters, digits, and underscores", t.Name)
	}
	if !objectSchema(t.InputSchema) {
		return fmt.Errorf("tool %s: input_schema must be a JSON Schema with type object", t.Name)
	}
	return nil
}

// objectSchema reports whether raw is a JSON Schema with type object.
func objectSchema(raw json.RawMessage) bool {
	var schema struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(raw, &schema) == nil && schema.Type == "object"
}

// MCPToolsResult is the reply to mcp.tools.
type MCPToolsResult struct {
	Tools []Tool `json:"tools"`
}

// MCPCallParams runs one tool.
type MCPCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// MCPContent is one item of a tool's result. Type is text.
type MCPContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// MCPCallResult is a tool's result. IsError marks a failure the model should
// see, such as bad arguments.
type MCPCallResult struct {
	Content []MCPContent `json:"content"`
	IsError bool         `json:"is_error,omitempty"`
}
