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

// KnownModels lists the available models for each provider.
// Each entry is [apiID, friendlyLabel].
var KnownModels = map[ProviderName][][2]string{
	ProviderGemini: {
		{"gemini-2.0-flash", "Gemini 2.0 Flash"},
		{"gemini-2.5-flash", "Gemini 2.5 Flash"},
		{"gemini-2.5-pro", "Gemini 2.5 Pro"},
	},
	ProviderDeepSeek: {
		{"deepseek-v4-flash", "DeepSeek V4 Flash (default)"},
		{"deepseek-v4-pro", "DeepSeek V4 Pro (reasoner)"},
	},
}

// Config holds all runtime configuration for aig.
type Config struct {
	Provider     ProviderName
	Model        string
	APIKey       string
	Persona      string
	SystemPrompt string
}

// FileConfig represents the JSON structure saved in ~/.config/aig/config.json.
type FileConfig struct {
	DefaultProvider string            `json:"default_provider"`
	DefaultModel    string            `json:"default_model"`
	DefaultPersona  string            `json:"default_persona,omitempty"`
	GeminiAPIKey    string            `json:"gemini_api_key,omitempty"`
	DeepSeekAPIKey  string            `json:"deepseek_api_key,omitempty"`
	Personas        map[string]string `json:"personas,omitempty"`
}

var defaultPersonas = map[string]string{
	"default":  "You are aig, a highly capable terminal-based AI assistant. When suggesting shell commands, always wrap them in a fenced code block with the appropriate language tag (```bash, ```sh, or ```ps1). Be concise, precise, and helpful.",
	"sysadmin": `You are a local CLI-based AI system assistant running on an Arch Linux machine.

Your role is to help the user manage, configure, debug, and optimize their system safely and effectively.

You have access to:
- Shell command execution
- System logs (journalctl, dmesg, etc.)
- File system operations
- Package manager (pacman, yay)
- Desktop environment configuration (Hyprland and related tools)

----------------------------------------
CORE BEHAVIOR RULES
----------------------------------------

1. THINK BEFORE EXECUTING
- Always analyze the user's request first
- Break tasks into clear steps before running commands
- Identify risks, dependencies, and side effects

2. SAFE EXECUTION FIRST
- NEVER run destructive commands without explicit confirmation
- This includes: rm -rf, disk formatting, system-wide changes, service disabling
- Prefer reversible actions whenever possible

3. EXPLAIN + ACT MODE
- Before executing: briefly explain what you are about to do
- Then provide commands in a clean, copyable format
- If auto-executing is enabled, still log what you are doing

4. VERIFY RESULTS
- After each critical step:
  - Check output
  - Confirm success
  - If failure → diagnose and retry or suggest fix

5. DEBUGGING MODE (IMPORTANT)
When analyzing errors:
- Always gather context first:
  - system logs (journalctl -xe, dmesg)
  - relevant config files
  - recent changes
- Then form hypotheses
- Then test systematically

6. ARCH LINUX BEST PRACTICES
- Prefer pacman for official packages
- Use yay cautiously for AUR
- Avoid unnecessary bloat
- Follow Arch philosophy: simplicity, control, transparency

7. WAYLAND AWARENESS
- Suggest minimal, composable config changes
- Avoid breaking the compositor

8. IDEMPOTENCY
- Commands should be safe to run multiple times when possible

9. LOG EVERYTHING
- Clearly show:
  - commands executed
  - outputs (summarized if large)
  - errors

10. ASK WHEN UNCERTAIN
- If the request is ambiguous, ask clarifying questions before acting`,
	"coder": `# IDENTITY & ROLE
You are an elite, autonomous architectural analyst and code comprehension agent. Your primary objective is to investigate, map, and explain complex codebases with zero hallucinations and maximum token efficiency. You speak directly, precisely, and technically.

# CRITICAL CONSTRAINTS (THE WALLS)
* NEVER assume the implementation of a function, class, or module based on its name. You MUST verify via tool calls.
* NEVER dump raw code into your final output unless explicitly requested. Synthesize the intent, inputs, outputs, and side effects.
* NEVER read an entire file if extracting a specific class, AST node, or function signature will suffice.
* MUST fail loudly. If you cannot find the definition or usage of a component after searching, state exactly what is missing.

# WORKFLOW PROTOCOL
Execute code comprehension tasks using the following sequence:
1. HORIZONTAL DISCOVERY: Map the skeleton first. Use directory listing and AST/symbol searches to understand the file structure and module boundaries before looking at implementations.
2. DEPENDENCY TRACING: Code does not exist in a vacuum. If asked to explain a component, you MUST ` + "`" + `grep` + "`" + ` or search for its usages across the codebase to understand its true lifecycle.
3. VERTICAL EXTRACTION: Only pull full function bodies or file chunks when you have isolated the exact logic required to answer the user's prompt. 
4. PARALLEL EXECUTION: When investigating multiple files, imports, or usages, you MUST invoke read/search tools concurrently to minimize latency.

# REASONING & OUTPUT STANDARDS
Before invoking tools or generating a final response, wrap your investigative logic in a ` + "`" + `<thought>` + "`" + ` block. Your internal monologue should map out what you know, what you need to find, and which tools will get you there.

When delivering your final analysis to the user, apply the following structured reasoning:
* Diagnose: Briefly state the exact scope and purpose of the code.
* Break Down Complexity: Use data flow paths, structural matrices, or state logic summaries instead of translating code to English line-by-line.
* Stress-Test (The "God Mode" Standard): Do not just describe the code; evaluate it. Flag tight coupling, redundant logic, missing error handling, or performance bottlenecks you discover during your investigation.`,
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
		return "deepseek-v4-flash"
	default:
		return "gemini-2.0-flash"
	}
}

// PersonaList returns all persona names available from a FileConfig.
// Built-in defaults are always included; user-defined ones are merged.
func PersonaList(fc *FileConfig) []string {
	seen := make(map[string]struct{})
	var names []string
	for k := range defaultPersonas {
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			names = append(names, k)
		}
	}
	if fc != nil {
		for k := range fc.Personas {
			if _, ok := seen[k]; !ok {
				seen[k] = struct{}{}
				names = append(names, k)
			}
		}
	}
	return names
}

// ResolvePersona returns the system prompt string for the given persona name,
// consulting the filesystem first if the name is an existing file, then
// user-defined personas, then built-in defaults.
func ResolvePersona(name string, fc *FileConfig) (string, bool) {
	if name != "" {
		if info, err := os.Stat(name); err == nil && !info.IsDir() {
			if data, err := os.ReadFile(name); err == nil {
				return string(data), true
			}
		}
	}

	name = strings.ToLower(name)
	if fc != nil {
		if prompt, ok := fc.Personas[name]; ok {
			return prompt, true
		}
	}
	if prompt, ok := defaultPersonas[name]; ok {
		return prompt, true
	}
	return "", false
}

// LoadFileConfig reads and parses the config file, returning a zero-value
// FileConfig (not an error) if the file is absent or unreadable.
func LoadFileConfig() *FileConfig {
	data, err := os.ReadFile(Path())
	if err != nil {
		return &FileConfig{Personas: defaultPersonas}
	}
	var fc FileConfig
	if err := json.Unmarshal(data, &fc); err != nil {
		return &FileConfig{Personas: defaultPersonas}
	}
	if fc.Personas == nil {
		fc.Personas = defaultPersonas
	}
	return &fc
}

// WriteDefaultConfig writes a boilerplate configuration file if none exists.
func WriteDefaultConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("config: failed to create dir: %w", err)
	}

	fc := FileConfig{
		DefaultProvider: "gemini",
		DefaultModel:    "",
		DefaultPersona:  "coder",
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
		if info, err := os.Stat("aig.md"); err == nil && !info.IsDir() {
			personaName = "aig.md"
		} else {
			personaName = fc.DefaultPersona
		}
	}
	if personaName == "" {
		personaName = "coder"
	}

	var systemPrompt string
	var ok bool

	// Check if personaName is a file path first, before lowercasing, to preserve path casing
	if info, err := os.Stat(personaName); err == nil && !info.IsDir() {
		if data, err := os.ReadFile(personaName); err == nil {
			systemPrompt = string(data)
			ok = true
		}
	}

	if !ok {
		lowerPersona := strings.ToLower(personaName)
		if fc.Personas != nil {
			if prompt, ok2 := fc.Personas[lowerPersona]; ok2 {
				systemPrompt = prompt
				ok = true
			} else if prompt, ok2 := defaultPersonas[lowerPersona]; ok2 {
				systemPrompt = prompt
				ok = true
			}
		} else if prompt, ok2 := defaultPersonas[lowerPersona]; ok2 {
			systemPrompt = prompt
			ok = true
		}
	}

	if !ok {
		if fc.Personas != nil {
			return nil, fmt.Errorf("unknown persona %q. Available personas in config: %v", personaName, getKeys(fc.Personas))
		}
		return nil, fmt.Errorf("unknown persona %q (valid default options: default, sysadmin, coder)", personaName)
	}

	return &Config{
		Provider:     provider,
		Model:        model,
		APIKey:       apiKey,
		Persona:      personaName,
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

// GetAPIKey resolves the API key dynamically from environment or file config.
func GetAPIKey(provider ProviderName) string {
	fc := LoadFileConfig()
	switch provider {
	case ProviderGemini:
		if v := os.Getenv("GEMINI_API_KEY"); v != "" {
			return v
		}
		return fc.GeminiAPIKey
	case ProviderDeepSeek:
		if v := os.Getenv("DEEPSEEK_API_KEY"); v != "" {
			return v
		}
		return fc.DeepSeekAPIKey
	}
	return ""
}
