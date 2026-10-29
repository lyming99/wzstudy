package provider

import "context"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

type ToolSchema struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

type Response struct {
	Text         string
	ToolCalls    []ToolCall
	Usage        Usage
	FinishReason string
}

// Provider is the only model dependency of the agent. Implementations must not
// leak credentials into logs or task events.
type Provider interface {
	Chat(ctx context.Context, model string, messages []Message, tools []ToolSchema) (Response, error)
}
