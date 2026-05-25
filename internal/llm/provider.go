// Package llm defines the LLM provider abstraction layer used by aig.
package llm

import "context"

// Role constants for conversation messages.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
	RoleTool      = "tool"
)

// ToolCall represents a function call requested by the LLM.
type ToolCall struct {
	ID   string
	Name string
	Args map[string]any
}

// Tool describes a callable function to the LLM.
type Tool struct {
	Name        string
	Description string
	Parameters  any // JSON Schema object (provider specific mapping may be needed, but usually map[string]any works)
}

// Message represents a single turn in a conversation.
type Message struct {
	Role       string // RoleUser | RoleAssistant | RoleSystem | RoleTool
	Content    string
	ToolCalls  []ToolCall // If the assistant called tools
	ToolCallID string     // If the role is tool, the ID of the call
	ToolName   string     // Name of the tool called
}

// Provider is the interface all LLM backends must satisfy.
// StreamChat streams token chunks into tokenChan. If the model invokes a tool,
// it stops streaming and returns the ToolCall. The caller must execute it,
// append the result to messages, and call StreamChat again.
type Provider interface {
	StreamChat(ctx context.Context, messages []Message, tools []Tool, tokenChan chan<- string) (*ToolCall, error)
}
