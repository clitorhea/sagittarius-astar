// Package config handles loading and validating application configuration
// from environment variables and CLI flags.
package config

import (
	"errors"
	"os"
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
	Provider ProviderName
	Model    string
	APIKey   string

	// SystemPrompt is prepended to every conversation as a system message.
	SystemPrompt string
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

// Load constructs a Config, resolving the API key from the environment.
// It returns an error if the required API key is not set.
func Load(provider ProviderName, model string) (*Config, error) {
	provider = ProviderName(strings.ToLower(string(provider)))

	if model == "" {
		model = DefaultModel(provider)
	}

	var (
		apiKey  string
		envName string
	)

	switch provider {
	case ProviderGemini:
		envName = "GEMINI_API_KEY"
	case ProviderDeepSeek:
		envName = "DEEPSEEK_API_KEY"
	default:
		return nil, errors.New("unknown provider: " + string(provider) + " (valid: gemini, deepseek)")
	}

	apiKey = os.Getenv(envName)
	if apiKey == "" {
		return nil, errors.New("missing required environment variable: " + envName)
	}

	return &Config{
		Provider: provider,
		Model:    model,
		APIKey:   apiKey,
		SystemPrompt: "You are aig, a highly capable terminal-based AI assistant. " +
			"When suggesting shell commands, always wrap them in a fenced code block with the appropriate language tag (```bash, ```sh, or ```ps1). " +
			"Be concise, precise, and helpful.",
	}, nil
}
