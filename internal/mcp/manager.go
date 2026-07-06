// Package mcp implements a Model Context Protocol (MCP) host over the stdio
// transport layer. It manages the lifecycle of MCP server subprocesses,
// provides a JSON-RPC 2.0 client for communication, and maintains a registry
// of discovered tools for injection into the LLM provider layer.
package mcp

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// ServerConfig describes a single MCP server process to spawn.
// It maps 1:1 to one entry in the config's mcp_servers map.
type ServerConfig struct {
	Command string            `json:"command"`          // executable path or name, e.g. "npx"
	Args    []string          `json:"args,omitempty"`   // CLI arguments, e.g. ["-y", "@modelcontextprotocol/server-filesystem", "."]
	Env     map[string]string `json:"env,omitempty"`    // extra env vars merged on top of os.Environ()
}

// Server represents a live MCP subprocess with its bidirectional stdio pipes.
// One Server is created per entry in the mcp_servers config map.
type Server struct {
	Name   string       // logical name from the config key, e.g. "filesystem"
	Config ServerConfig

	cmd    *exec.Cmd
	stdin  io.WriteCloser // pipe to child's stdin  — we write JSON-RPC requests here
	stdout io.ReadCloser  // pipe from child's stdout — we read JSON-RPC responses here

	mu     sync.Mutex
	closed bool
}

// Manager owns the lifecycle of all MCP server subprocesses.
// It is constructed once at startup, populated by Start(), and torn down by Shutdown().
type Manager struct {
	servers map[string]*Server // keyed by logical server name
	mu      sync.RWMutex
}

// NewManager allocates an empty Manager. Call Start() to spawn server processes.
func NewManager() *Manager {
	return &Manager{
		servers: make(map[string]*Server),
	}
}

// Start spawns a subprocess for each ServerConfig in the provided map.
// If any server fails to start, all previously started servers are shut down
// before returning the error — no half-initialised state is left behind.
//
// The context governs subprocess lifetime: cancelling it delivers os.Kill to
// every child via exec.CommandContext semantics.
func (m *Manager) Start(ctx context.Context, configs map[string]ServerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	started := make([]string, 0, len(configs))
	for name, cfg := range configs {
		srv, err := spawnServer(ctx, name, cfg)
		if err != nil {
			// Roll back cleanly: kill any server we already started.
			m.mu.Unlock()
			m.Shutdown(context.Background())
			m.mu.Lock()
			return fmt.Errorf("mcp: failed to start server %q: %w", name, err)
		}
		m.servers[name] = srv
		started = append(started, name)
	}
	return nil
}

// spawnServer forks a single MCP server process and wires up its stdio pipes.
func spawnServer(ctx context.Context, name string, cfg ServerConfig) (*Server, error) {
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)

	// Merge caller-supplied env vars on top of the current process's environment.
	// MCP servers often rely on PATH and other ambient vars, so we inherit them.
	if len(cfg.Env) > 0 {
		env := os.Environ()
		for k, v := range cfg.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: stdin pipe: %w", name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("mcp: %s: stdout pipe: %w", name, err)
	}
	// Discard stderr to prevent child log output from blocking the process.
	// MCP servers commonly emit startup messages there.
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("mcp: %s: exec start: %w", name, err)
	}

	return &Server{
		Name:   name,
		Config: cfg,
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
	}, nil
}

// Server returns the named live server, or nil if it is not found.
func (m *Manager) Server(name string) *Server {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.servers[name]
}

// Servers returns a snapshot of all live server names.
func (m *Manager) Servers() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	return names
}

// Shutdown gracefully terminates all child processes concurrently.
// It sends SIGINT and waits up to 5 seconds; processes that survive
// are forcibly killed. The method blocks until all children have exited
// or the provided context is cancelled.
func (m *Manager) Shutdown(ctx context.Context) {
	m.mu.Lock()
	servers := make([]*Server, 0, len(m.servers))
	for _, s := range m.servers {
		servers = append(servers, s)
	}
	// Clear the map immediately so re-entrant calls are no-ops.
	m.servers = make(map[string]*Server)
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, s := range servers {
		wg.Add(1)
		s := s
		go func() {
			defer wg.Done()
			shutdownServer(s)
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
}

// shutdownServer terminates a single server process. Idempotent.
func shutdownServer(s *Server) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()

	// Closing stdin signals EOF to the child, which triggers a clean exit
	// for well-behaved MCP servers that read until stdin closes.
	_ = s.stdin.Close()

	if s.cmd.Process == nil {
		_ = s.stdout.Close()
		return
	}

	// Give the process a grace period to exit cleanly after EOF.
	exited := make(chan error, 1)
	go func() { exited <- s.cmd.Wait() }()

	select {
	case <-exited:
		// Process exited on its own — clean shutdown.
	case <-time.After(5 * time.Second):
		// Grace period elapsed: escalate to SIGKILL.
		_ = s.cmd.Process.Kill()
		<-exited
	}

	_ = s.stdout.Close()
}
