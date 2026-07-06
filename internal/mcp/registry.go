package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/clitorhea/sagittarius-astar.git/internal/llm"
)

// Tool is the MCP wire representation of a tool as returned by a tools/list RPC.
// We preserve the raw JSON Schema in InputSchema for protocol fidelity before
// translating into provider-specific types in LLMTools().
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"` // raw JSON Schema object
}

// toolsListResult is the unmarshalling target for a successful tools/list response.
type toolsListResult struct {
	Tools []Tool `json:"tools"`
}

// toolCallParams is the JSON-RPC params object for a tools/call request.
type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// toolCallResult is the unmarshalling target for a successful tools/call response.
type toolCallResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError"`
}

// toolContent is a single content block from a tool response.
// MCP defines several content types; we handle "text" and ignore the rest for now.
type toolContent struct {
	Type string `json:"type"` // "text" | "image" | "resource"
	Text string `json:"text,omitempty"`
}

// Registry discovers and indexes tools across all connected MCP servers.
// It is the single source of truth for:
//   - Which tools are available (LLMTools for LLM injection)
//   - Which server owns each tool (for dispatch in Call)
//   - Whether a call name refers to an MCP tool (IsMCPTool for TUI routing)
type Registry struct {
	clients map[string]*Client // logical server name → JSON-RPC client
	toolMap map[string]string  // MCP tool name → owning server name
	tools   []Tool             // all discovered tools, in server-then-discovery order

	mu sync.RWMutex
}

// NewRegistry builds a Registry by creating a Client for each live server in m.
// Call Discover() after NewRegistry() to populate the tool index.
func NewRegistry(m *Manager) *Registry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	clients := make(map[string]*Client, len(m.servers))
	for name, srv := range m.servers {
		clients[name] = NewClient(srv)
	}

	return &Registry{
		clients: clients,
		toolMap: make(map[string]string),
	}
}

// Discover runs the full MCP discovery sequence:
//  1. Sends the initialize handshake to every server.
//  2. Calls tools/list on each server.
//  3. Builds the internal tool → server routing table.
//
// It is safe to call multiple times; subsequent calls refresh the index.
// The caller should invoke this once at startup, after Manager.Start().
func (r *Registry) Discover(ctx context.Context) error {
	// Phase 1: Initialize all servers before any tool calls.
	// We do these serially to keep startup logs readable; they are fast.
	for name, c := range r.clients {
		if err := c.Initialize(ctx); err != nil {
			return fmt.Errorf("mcp: registry discover: %w", err)
		}
		_ = name
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Reset the index so re-discovery is clean.
	r.tools = r.tools[:0]
	r.toolMap = make(map[string]string)

	// Phase 2: Enumerate tools from each server.
	for serverName, client := range r.clients {
		raw, err := client.Call(ctx, "tools/list", nil)
		if err != nil {
			return fmt.Errorf("mcp: registry discover: server %q: tools/list: %w", serverName, err)
		}

		var result toolsListResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return fmt.Errorf("mcp: registry discover: server %q: parse tools/list response: %w", serverName, err)
		}

		for _, t := range result.Tools {
			// Last-write-wins on name collision across servers.
			// This mirrors the Claude Desktop convention.
			r.tools = append(r.tools, t)
			r.toolMap[t.Name] = serverName
		}
	}

	return nil
}

// LLMTools translates all discovered MCP tools into the []llm.Tool slice
// that is injected into Provider.StreamChat() alongside the native agent tools.
//
// The MCP InputSchema (a raw JSON Schema object) is unmarshalled into
// map[string]any, which is the common denominator accepted by both:
//   - Gemini: genai.FunctionDeclaration.ParametersJsonSchema (type any)
//   - DeepSeek: openai.FunctionDefinition.Parameters (type any)
//
// If schema unmarshalling fails for a tool, a bare {"type":"object"} is
// substituted so the tool remains callable with untyped arguments.
func (r *Registry) LLMTools() []llm.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]llm.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		var schema map[string]any
		if err := json.Unmarshal(t.InputSchema, &schema); err != nil || schema == nil {
			schema = map[string]any{"type": "object"}
		}
		out = append(out, llm.Tool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  schema,
		})
	}
	return out
}

// Call invokes a named MCP tool on its registered server and returns the
// concatenated text from all "text" content blocks in the response.
//
// This function is designed to be wrapped in a tea.Cmd goroutine:
//
//	return func() tea.Msg {
//	    result, err := registry.Call(ctx, call.Name, call.Args)
//	    if err != nil {
//	        return toolCallResultMsg{..., Error: err}
//	    }
//	    return toolCallResultMsg{..., Result: result}
//	}
func (r *Registry) Call(ctx context.Context, name string, args map[string]any) (string, error) {
	r.mu.RLock()
	serverName, ok := r.toolMap[name]
	client := r.clients[serverName]
	r.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("mcp: tool %q is not registered", name)
	}
	if client == nil {
		return "", fmt.Errorf("mcp: tool %q: server %q has no active client", name, serverName)
	}

	params := toolCallParams{
		Name:      name,
		Arguments: args,
	}
	if params.Arguments == nil {
		params.Arguments = make(map[string]any)
	}

	raw, err := client.Call(ctx, "tools/call", params)
	if err != nil {
		return "", fmt.Errorf("mcp: tool %q: rpc call failed: %w", name, err)
	}

	var result toolCallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("mcp: tool %q: parse response: %w", name, err)
	}

	// Collect all text content blocks into a single string.
	var sb strings.Builder
	for _, c := range result.Content {
		if c.Type == "text" && c.Text != "" {
			sb.WriteString(c.Text)
		}
	}

	if result.IsError {
		// The server signalled a logical error via the IsError flag.
		// We return it as a Go error so processNextToolCall surfaces it correctly.
		errText := sb.String()
		if errText == "" {
			errText = "tool reported an error with no detail"
		}
		return "", fmt.Errorf("mcp: tool %q error: %s", name, errText)
	}

	return sb.String(), nil
}

// IsMCPTool returns true if name matches a tool discovered from any MCP server.
// Used by processNextToolCall in the TUI to route dispatch away from the
// native sandbox tool switch.
func (r *Registry) IsMCPTool(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.toolMap[name]
	return ok
}

// ToolCount returns the total number of MCP tools discovered across all servers.
// Useful for startup log messages and status bar display.
func (r *Registry) ToolCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// Shutdown closes all JSON-RPC client read loops. Call this before Manager.Shutdown()
// to ensure all in-flight RPC calls are drained before the pipes are closed.
func (r *Registry) Shutdown() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.clients {
		c.Close()
	}
}
