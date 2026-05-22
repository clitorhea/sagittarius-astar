// Package config handles loading, validating, and writing application configuration
// from file, environment variables, personas, and CLI flags.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ProviderName identifies an LLM backend.
type ProviderName string

const (
	ProviderGemini   ProviderName = "gemini"
	ProviderDeepSeek ProviderName = "deepseek"
)

// Config holds all runtime configuration for aig.
type Config struct {
	Provider     ProviderName
	Model        string
	APIKey       string
	SystemPrompt string
}

// FileConfig represents the JSON structure saved in ~/.config/aig/config.json.
type FileConfig struct {
	DefaultProvider string            `json:"default_provider"`
	DefaultModel    string            `json:"default_model"`
	GeminiAPIKey    string            `json:"gemini_api_key,omitempty"`
	DeepSeekAPIKey  string            `json:"deepseek_api_key,omitempty"`
	Personas        map[string]string `json:"personas,omitempty"`
}

var defaultPersonas = map[string]string{
	"default": "You are aig, a highly capable terminal-based AI assistant. When suggesting shell commands, always wrap them in a fenced code block with the appropriate language tag (```bash, ```sh, or ```ps1). Be concise, precise, and helpful.",
	"sysadmin": "You are an expert Linux and Windows system administrator. Focus on providing clean, secure, and robust shell commands. Explain potential risks before executing commands.",
	"coder": "You are an expert software engineer. Provide high-quality, readable, and well-structured code. Keep explanations minimal and focus on code patterns and correctness.",
}

// Path returns the config file location: ~/.config/aig/config.json
func Path() string {
	home, err := os.UserHomeDir()
	if err != nil {
		configDir, _ := os.UserConfigDir()
		return filepath.Join(configDir, "aig", "config.json")
	}

	if runtime.GOOS == "windows" {
		appData := os.Getenv("AppData")
		if appData != "" {
			return filepath.Join(appData, "aig", "config.json")
		}
		return filepath.Join(home, "AppData", "Roaming", "aig", "config.json")
	}

	return filepath.Join(home, ".config", "aig", "config.json")
}

// DefaultModel returns the sensible default model for a given provider.
func DefaultModel(p ProviderName) string {
	switch p {
	case ProviderDeepSeek:
		return "deepseek-chat"
	default:
		return "gemini-2.0-flash"
	}
}

// WriteDefaultConfig writes a boilerplate configuration file if none exists.
func WriteDefaultConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("config: failed to create dir: %w", err)
	}

	fc := FileConfig{
		DefaultProvider: "gemini",
		DefaultModel:    "",
		GeminiAPIKey:    "",
		DeepSeekAPIKey:  "",
		Personas:        defaultPersonas,
	}

	data, err := json.MarshalIndent(fc, "", "  ")
	if err != nil {
		return err
	}

	// Write only if it doesn't exist
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return fmt.Errorf("config: failed to write default: %w", err)
		}
	}
	return nil
}

// Load constructs a Config by merging file configuration, environment variables,
// CLI overrides, and persona selections.
func Load(providerOverride ProviderName, modelOverride string, personaName string) (*Config, error) {
	cfgPath := Path()
	_ = WriteDefaultConfig(cfgPath) // Ensure the file exists

	data, err := os.ReadFile(cfgPath)
	var fc FileConfig
	if err == nil {
		_ = json.Unmarshal(data, &fc)
	}

	// 1. Resolve Provider (CLI override > File config > Default)
	providerStr := string(providerOverride)
	if providerStr == "" {
		providerStr = fc.DefaultProvider
	}
	if providerStr == "" {
		providerStr = "gemini"
	}
	provider := ProviderName(strings.ToLower(providerStr))

	// 2. Resolve Model (CLI override > File config > Default)
	model := modelOverride
	if model == "" {
		model = fc.DefaultModel
	}
	if model == "" {
		model = DefaultModel(provider)
	}

	// 3. Resolve API Key (Environment variable > File config)
	var (
		apiKey  string
		envName string
	)

	switch provider {
	case ProviderGemini:
		envName = "GEMINI_API_KEY"
		apiKey = os.Getenv(envName)
		if apiKey == "" {
			apiKey = fc.GeminiAPIKey
		}
	case ProviderDeepSeek:
		envName = "DEEPSEEK_API_KEY"
		apiKey = os.Getenv(envName)
		if apiKey == "" {
			apiKey = fc.DeepSeekAPIKey
		}
	default:
		return nil, fmt.Errorf("unknown provider: %q (valid: gemini, deepseek)", provider)
	}

	if apiKey == "" {
		return nil, fmt.Errorf("missing API key for %s. Either set environment variable %s or configure it in %s", provider, envName, cfgPath)
	}

	// 4. Resolve System Prompt (Persona)
	if personaName == "" {
		personaName = "default"
	}
	personaName = strings.ToLower(personaName)

	systemPrompt := defaultPersonas["default"]
	if fc.Personas != nil {
		if prompt, ok := fc.Personas[personaName]; ok {
			systemPrompt = prompt
		} else if prompt, ok := defaultPersonas[personaName]; ok {
			systemPrompt = prompt
		} else {
			return nil, fmt.Errorf("unknown persona %q. Available personas in config: %v", personaName, getKeys(fc.Personas))
		}
	} else if prompt, ok := defaultPersonas[personaName]; ok {
		systemPrompt = prompt
	} else {
		return nil, fmt.Errorf("unknown persona %q (valid default options: default, sysadmin, coder)", personaName)
	}

	return &Config{
		Provider:     provider,
		Model:        model,
		APIKey:       apiKey,
		SystemPrompt: systemPrompt,
	}, nil
}

func getKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
