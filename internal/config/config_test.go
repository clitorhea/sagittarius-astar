package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePersonaWithFile(t *testing.T) {
	// Create a temp directory for test files
	tmpDir, err := os.MkdirTemp("", "aig_config_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Test file content
	promptContent := "This is a custom prompt file content."
	filePath := filepath.Join(tmpDir, "test_prompt.md")
	if err := os.WriteFile(filePath, []byte(promptContent), 0o600); err != nil {
		t.Fatalf("failed to write test prompt file: %v", err)
	}

	// 1. Resolve existing file path
	fc := &FileConfig{}
	resolved, ok := ResolvePersona(filePath, fc)
	if !ok {
		t.Errorf("expected ResolvePersona to succeed for file path %s", filePath)
	}
	if resolved != promptContent {
		t.Errorf("expected resolved prompt to be %q, got %q", promptContent, resolved)
	}

	// 2. Resolve non-existing file path
	nonExisting := filepath.Join(tmpDir, "non_existent.md")
	resolved, ok = ResolvePersona(nonExisting, fc)
	if ok {
		t.Errorf("expected ResolvePersona to fail for non-existing path %s, but got prompt: %q", nonExisting, resolved)
	}

	// 3. Fallback to standard persona when file doesn't exist
	resolved, ok = ResolvePersona("coder", fc)
	if !ok {
		t.Errorf("expected fallback to default coder persona to succeed")
	}
	if resolved == "" {
		t.Errorf("expected non-empty fallback prompt for coder")
	}
}

func TestLoadWithLocalAigMd(t *testing.T) {
	// Backup environment variables
	origKey := os.Getenv("GEMINI_API_KEY")
	defer os.Setenv("GEMINI_API_KEY", origKey)
	os.Setenv("GEMINI_API_KEY", "mock-gemini-key")

	// Create a local aig.md in the current directory (which is internal/config during go test)
	localPrompt := "Local aig.md prompt content"
	err := os.WriteFile("aig.md", []byte(localPrompt), 0o600)
	if err != nil {
		t.Fatalf("failed to write local aig.md: %v", err)
	}
	defer os.Remove("aig.md")

	// Load config with empty persona
	cfg, err := Load("", "", "")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Persona != "aig.md" {
		t.Errorf("expected Persona to be 'aig.md', got %q", cfg.Persona)
	}

	if cfg.SystemPrompt != localPrompt {
		t.Errorf("expected SystemPrompt to be %q, got %q", localPrompt, cfg.SystemPrompt)
	}
}
