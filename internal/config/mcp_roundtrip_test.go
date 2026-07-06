package config

import (
	"encoding/json"
	"testing"
)

func TestMCPServersRoundTrip(t *testing.T) {
	// ── Case 1: config WITH mcp_servers ──────────────────────────────────────
	withMCP := []byte(`{
		"default_provider": "gemini",
		"default_model": "",
		"mcp_servers": {
			"filesystem": {
				"command": "npx",
				"args": ["-y", "@modelcontextprotocol/server-filesystem", "."],
				"env": {"NODE_ENV": "production"}
			}
		}
	}`)
	var fc1 FileConfig
	if err := json.Unmarshal(withMCP, &fc1); err != nil {
		t.Fatalf("unmarshal with mcp_servers: %v", err)
	}
	srv, ok := fc1.MCPServers["filesystem"]
	if !ok {
		t.Fatal("expected 'filesystem' key in MCPServers")
	}
	if srv.Command != "npx" {
		t.Errorf("command: got %q, want %q", srv.Command, "npx")
	}
	if len(srv.Args) != 3 || srv.Args[0] != "-y" {
		t.Errorf("args: got %v", srv.Args)
	}
	if srv.Env["NODE_ENV"] != "production" {
		t.Errorf("env: got %v", srv.Env)
	}

	// ── Case 2: config WITHOUT mcp_servers — backward compatibility ──────────
	withoutMCP := []byte(`{"default_provider":"gemini","default_model":"gemini-2.0-flash"}`)
	var fc2 FileConfig
	if err := json.Unmarshal(withoutMCP, &fc2); err != nil {
		t.Fatalf("unmarshal without mcp_servers: %v", err)
	}
	if fc2.MCPServers != nil {
		t.Errorf("expected nil MCPServers for missing key, got %v", fc2.MCPServers)
	}

	// ── Case 3: empty map — omitempty drops it just like nil ────────────────
	// Go's encoding/json treats an empty map the same as nil for omitempty;
	// the key will not appear in the output. This is expected behaviour.
	fc3 := FileConfig{MCPServers: map[string]MCPServerConfig{}}
	b3, err := json.Marshal(fc3)
	if err != nil {
		t.Fatalf("marshal empty MCPServers: %v", err)
	}
	var check3 map[string]any
	_ = json.Unmarshal(b3, &check3)
	if _, has := check3["mcp_servers"]; has {
		t.Errorf("expected mcp_servers to be omitted for empty map (omitempty), got: %s", b3)
	}

	// ── Case 4: nil map is omitted by omitempty ───────────────────────────────
	fc4 := FileConfig{}
	b4, _ := json.Marshal(fc4)
	var check4 map[string]any
	_ = json.Unmarshal(b4, &check4)
	if _, has := check4["mcp_servers"]; has {
		t.Errorf("expected mcp_servers to be omitted for nil map, got: %s", b4)
	}
}
