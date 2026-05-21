// Package llm defines the LLM provider abstraction layer used by aig.
package llm

import "context"

// Role constants for conversation messages.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
)

// Message represents a single turn in a conversation.
type Message struct {
	Role    string // RoleUser | RoleAssistant | RoleSystem
	Content string
}

// Provider is the interface all LLM backends must satisfy.
// StreamChat sends the conversation history to the LLM and streams token
// chunks into tokenChan. The channel is closed when the stream ends (either
// successfully or with an error). Errors are signalled by returning a non-nil
// error; the channel will still be closed.
type Provider interface {
	StreamChat(ctx context.Context, messages []Message, tokenChan chan<- string) error
}
