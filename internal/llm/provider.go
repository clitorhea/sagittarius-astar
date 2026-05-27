// Package llm defines the LLM provider abstraction layer used by aig.
package llm

import (
	"context"
	"fmt"
)

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
	Parameters  any // JSON Schema object
}

// Message represents a single turn in a conversation.
type Message struct {
	Role             string     // RoleUser | RoleAssistant | RoleSystem | RoleTool
	Content          string
	ReasoningContent string     // DeepSeek thinking models: chain-of-thought that must be echoed back
	ToolCalls        []ToolCall // If the assistant called tools
	ToolCallID       string     // If the role is tool, the ID of the call
	ToolName         string     // Name of the tool called
}

// Provider is the interface all LLM backends must satisfy.
//
// StreamChat streams token chunks into tokenChan. If the model invokes one or
// more tools it stops streaming and returns the ToolCalls slice. The caller
// must execute them, append the results to messages, and call StreamChat again.
//
// reasoningContent is non-empty when a DeepSeek thinking model emits
// chain-of-thought content; callers must store it in the assistant Message and
// echo it back on the next call or the API will return HTTP 400.
type Provider interface {
	StreamChat(ctx context.Context, messages []Message, tools []Tool, tokenChan chan<- string) (toolCalls []ToolCall, reasoningContent string, err error)
}

// NewProvider constructs a Provider from a provider name, API key, and model string.
// This is used for runtime provider/model switching inside the TUI.
func NewProvider(providerName, apiKey, model string) (Provider, error) {
	switch providerName {
	case "gemini":
		return NewGeminiProvider(apiKey, model)
	case "deepseek":
		return NewDeepSeekProvider(apiKey, model)
	default:
		return nil, fmt.Errorf("llm: unknown provider %q (valid: gemini, deepseek)", providerName)
	}
}
