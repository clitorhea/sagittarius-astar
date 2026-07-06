package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
)

// jsonRPCRequest is a JSON-RPC 2.0 request envelope.
// ID is a pointer so that notifications (no ID) can omit the field via omitempty.
type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`          // always "2.0"
	ID      *int64 `json:"id,omitempty"`     // nil for one-way notifications
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// jsonRPCResponse is a JSON-RPC 2.0 response envelope.
// Exactly one of Result or Error will be non-zero on a valid response.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

// jsonRPCError carries the structured error payload from a failed RPC call.
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Client is a thread-safe JSON-RPC 2.0 client layered over a Server's stdio pipes.
//
// Design: a single background goroutine (readLoop) reads newline-delimited JSON
// from the server's stdout and dispatches each response to the waiting caller
// via a per-request buffered channel. This keeps the Bubble Tea event loop
// unblocked — callers invoke Call() from within tea.Cmd goroutines.
type Client struct {
	server  *Server

	// enc serialises JSON to the child's stdin. writeOnce guards concurrent writes.
	enc      *json.Encoder
	writeMu  sync.Mutex

	// scanner reads newline-delimited JSON from the child's stdout.
	// The buffer is enlarged to 10 MiB to accommodate large tool responses.
	scanner *bufio.Scanner

	// nextID generates monotonically increasing request IDs starting at 1.
	// ID=0 is reserved as the sentinel for "no ID" (i.e., notifications).
	nextID atomic.Int64

	// pending maps in-flight request IDs to their reply channels.
	pending map[int64]chan jsonRPCResponse
	mu      sync.Mutex

	readLoopDone chan struct{}
	closeOnce    sync.Once
}

// NewClient wraps s in a Client and starts its background read loop.
// Call Initialize() before making any tool calls.
func NewClient(s *Server) *Client {
	scanner := bufio.NewScanner(s.stdout)
	// Enlarge the scanner buffer from the default 64 KiB to 10 MiB.
	// MCP tool responses (e.g. large file reads) can easily exceed the default.
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)

	c := &Client{
		server:       s,
		enc:          json.NewEncoder(s.stdin),
		scanner:      scanner,
		pending:      make(map[int64]chan jsonRPCResponse),
		readLoopDone: make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// readLoop is the single goroutine that owns stdout reads for this client.
// It parses each newline-delimited JSON object and dispatches to waiting callers.
// It exits when the scanner hits EOF (server closed stdout / process exited).
func (c *Client) readLoop() {
	defer close(c.readLoopDone)

	for c.scanner.Scan() {
		line := c.scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var resp jsonRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			// Malformed JSON from the server — skip and keep reading.
			continue
		}

		// Notifications have no id field → resp.ID == 0.
		// We never register a pending channel for ID=0, so they are silently
		// dropped here. This covers "notifications/initialized" acknowledgements etc.
		c.mu.Lock()
		ch, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.mu.Unlock()

		if ok {
			// Non-blocking send: the channel is buffered(1) so this never blocks
			// even if the caller has already timed out and stopped listening.
			select {
			case ch <- resp:
			default:
			}
		}
	}

	// The scanner stopped (server EOF or process death).
	// Drain all pending callers with a synthetic error so they don't hang.
	c.mu.Lock()
	pending := c.pending
	c.pending = make(map[int64]chan jsonRPCResponse)
	c.mu.Unlock()

	for _, ch := range pending {
		ch <- jsonRPCResponse{
			Error: &jsonRPCError{Code: -32000, Message: "mcp: server connection closed"},
		}
	}
}

// notify sends a JSON-RPC notification (no id field, no response expected).
// Safe to call concurrently.
func (c *Client) notify(method string, params any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.enc.Encode(jsonRPCRequest{
		JSONRPC: "2.0",
		// ID is nil → omitted from JSON → peer treats it as a notification.
		Method: method,
		Params: params,
	})
}

// Call sends a JSON-RPC 2.0 request and blocks until the matching response
// arrives or ctx is cancelled. Safe to call concurrently from multiple goroutines.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1) // first call gets ID=1
	ch := make(chan jsonRPCResponse, 1)

	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	// Serialise the write so two concurrent callers don't interleave JSON frames.
	c.writeMu.Lock()
	err := c.enc.Encode(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  params,
	})
	c.writeMu.Unlock()

	if err != nil {
		// Remove from pending — nobody will ever deliver a response.
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp: write request %q: %w", method, err)
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	case <-c.readLoopDone:
		// Server died before responding.
		return nil, fmt.Errorf("mcp: server %q closed while waiting for %q response", c.server.Name, method)
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp: rpc error %d from %q: %s", resp.Error.Code, c.server.Name, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

// initializeParams mirrors the MCP initialize request structure.
type initializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    map[string]any     `json:"capabilities"`
	ClientInfo      initializeClientInfo `json:"clientInfo"`
}

type initializeClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Initialize performs the mandatory MCP handshake:
//  1. Sends the initialize request with the negotiated protocol version.
//  2. Sends the notifications/initialized one-way notification.
//
// This must be called exactly once before any tool discovery or invocation.
func (c *Client) Initialize(ctx context.Context) error {
	params := initializeParams{
		ProtocolVersion: "2024-11-05",
		Capabilities:    map[string]any{},
		ClientInfo: initializeClientInfo{
			Name:    "aig",
			Version: "1.0",
		},
	}

	_, err := c.Call(ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("mcp: %s: initialize handshake failed: %w", c.server.Name, err)
	}

	// Fire the initialized notification — the server uses this to finish its
	// startup sequence. We discard the (non-existent) response.
	if err := c.notify("notifications/initialized", nil); err != nil {
		// Non-fatal: some servers tolerate a missing initialized notification.
		_ = err
	}
	return nil
}

// Close shuts down the read loop by closing the server's stdin, which signals
// EOF to the child process and causes the scanner to stop. Idempotent.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		// Closing stdin is the clean way to stop the server's read loop.
		// The server should then close stdout, which terminates our scanner.
		_ = c.server.stdin.Close()
	})
	<-c.readLoopDone
}
