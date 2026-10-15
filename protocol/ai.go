package protocol

import "encoding/json"

// The ai capability offers model services to other plugins. A consumer
// declares the methods it calls in its manifest's uses and sends them to the
// core, which forwards each to the provider instance it names.
const (
	MethodAIChat  = "ai.chat"
	MethodAIEmbed = "ai.embed"
	// NotifyAIDelta is the next piece of an ai.chat reply's text.
	NotifyAIDelta = "ai.delta"
)

// Services maps each service method to the capability that offers it.
var Services = map[string]string{MethodAIChat: "ai", MethodAIEmbed: "ai"}

// CodeAIRejected is a provider refusing a request as invalid, such as tools
// for a model without tool support.
const CodeAIRejected = -32010

// ServiceCall is the part of every service call the core reads: the
// provider instance, and the caller's token for the call's notifications.
type ServiceCall struct {
	Instance int64  `json:"instance,omitempty"`
	Run      string `json:"run,omitempty"`
}

// ChatMessage is one message of an ai.chat conversation. Parts replace
// Content for a message with images.
type ChatMessage struct {
	Role       string        `json:"role" enum:"system,user,assistant,tool"`
	Content    string        `json:"content,omitempty"`
	Parts      []ContentPart `json:"parts,omitempty"`
	ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

// ContentPart is text or an image; ImageURL may be a data: URL.
type ContentPart struct {
	Type     string `json:"type" enum:"text,image_url"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

// ChatTool is a function the model may call. Parameters is a JSON Schema.
type ChatTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolCall is one call the model made. Arguments is a JSON string.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// AIChatParams is one model turn.
type AIChatParams struct {
	ServiceCall
	Messages    []ChatMessage `json:"messages"`
	Tools       []ChatTool    `json:"tools,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
}

// AIChatResult is the model's reply.
type AIChatResult struct {
	Text      string     `json:"text"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// Model is the model that answered, for the consumer's records.
	Model string `json:"model,omitempty"`
}

// AIDelta streams ai.chat's text under the call's Run token.
type AIDelta struct {
	Run  string `json:"run"`
	Text string `json:"text"`
}

// AIEmbedParams asks for one vector per text.
type AIEmbedParams struct {
	ServiceCall
	Texts []string `json:"texts"`
}

// AIEmbedResult holds the vectors in the order of the texts.
type AIEmbedResult struct {
	Vectors [][]float32 `json:"vectors"`
}
