// Command aig — Sagittarius A* terminal AI agent.
//
// Usage:
//
//	aig [flags]
//	aig --provider gemini --model gemini-2.0-flash
//	aig --provider deepseek --model deepseek-chat
//	aig --persona sysadmin         # use custom prompt persona
//	aig --resume 20260521-120000   # resume previous session
//	aig --log-level debug          # verbose file logging
package main

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/clitorhea/sagittarius-astar.git/internal/config"
	"github.com/clitorhea/sagittarius-astar.git/internal/llm"
	"github.com/clitorhea/sagittarius-astar.git/internal/logger"
	"github.com/clitorhea/sagittarius-astar.git/internal/session"
	"github.com/clitorhea/sagittarius-astar.git/internal/tui"
)

var (
	flagProvider string
	flagModel    string
	flagPersona  string
	flagResume   string
	flagLogLevel string
	flagLogFile  string

	version = "dev" // Injected by LDFLAGS during build
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "aig",
	Short: "aig — terminal-based AI agent (Sagittarius A*)",
	Long: `aig is a cross-platform, terminal-based AI agent that supports
multiple LLM providers (Gemini, DeepSeek) and can securely execute
shell commands with your explicit consent.

Configuration file is located at: ~/.config/aig/config.json
Sessions are saved to: ~/.local/share/aig/sessions/
Logs are written to: ~/.cache/aig/aig.log

Examples:
  aig
  aig --provider deepseek
  aig --persona sysadmin
  aig --resume 20260521-170000
  aig --log-level debug`,
	RunE: runChat,
}

func init() {
	rootCmd.PersistentFlags().StringVarP(
		&flagProvider, "provider", "p", "",
		"LLM provider to use (gemini | deepseek)",
	)
	rootCmd.PersistentFlags().StringVarP(
		&flagModel, "model", "m", "",
		"Model name (defaults to provider's recommended model)",
	)
	rootCmd.PersistentFlags().StringVarP(
		&flagPersona, "persona", "s", "",
		"System prompt persona to use (defaults to coder, or default_persona from config)",
	)
	rootCmd.PersistentFlags().StringVarP(
		&flagResume, "resume", "r", "",
		"Resume a previous conversation by session ID (defaults to 'latest' if flag is present but no ID is provided)",
	)
	rootCmd.PersistentFlags().Lookup("resume").NoOptDefVal = "latest"
	rootCmd.PersistentFlags().StringVar(
		&flagLogLevel, "log-level", "info",
		"Log verbosity written to file (debug | info | warn | error)",
	)
	rootCmd.PersistentFlags().StringVar(
		&flagLogFile, "log-file", "",
		"Override log file path (default: ~/.cache/aig/aig.log)",
	)
}

func runChat(_ *cobra.Command, _ []string) error {
	// ── Logging ──────────────────────────────────────────────────────────────
	logLevel, err := logger.ParseLevel(flagLogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v — defaulting to info\n", err)
		logLevel = slog.LevelInfo
	}

	cleanupLog, err := logger.Init(logLevel, flagLogFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: logging disabled: %v\n", err)
	}
	defer cleanupLog()

	// ── Configuration ─────────────────────────────────────────────────────────
	cfg, err := config.Load(config.ProviderName(flagProvider), flagModel, flagPersona)
	if err != nil {
		logger.L.Error("config load failed", "error", err)
		return fmt.Errorf("configuration error: %w\n\nRun 'aig --help' for usage", err)
	}

	// ── Session Resolution ───────────────────────────────────────────────────
	var activeSession *session.Session
	if flagResume != "" {
		if flagResume == "latest" {
			activeSession, err = session.LoadLatest()
			if err != nil {
				logger.L.Error("failed to load latest session", "error", err)
				return fmt.Errorf("failed to resume latest session: %w", err)
			}
		} else {
			activeSession, err = session.Load(flagResume)
			if err != nil {
				logger.L.Error("failed to load session to resume", "id", flagResume, "error", err)
				return fmt.Errorf("failed to resume session %q: %w", flagResume, err)
			}
		}
		logger.L.Info("resuming session", "id", activeSession.ID, "turns", len(activeSession.History))

		// Override provider and model from session if not explicitly requested on CLI
		if flagProvider == "" && activeSession.Provider != "" {
			flagProvider = activeSession.Provider
		}
		if flagModel == "" && activeSession.Model != "" {
			flagModel = activeSession.Model
		}
		// Re-load configuration with the updated provider/model
		cfg, err = config.Load(config.ProviderName(flagProvider), flagModel, flagPersona)
		if err != nil {
			logger.L.Error("config reload failed for resumed session", "error", err)
			return fmt.Errorf("configuration error: %w", err)
		}
	} else {
		activeSession = &session.Session{
			ID:        session.GenerateID(),
			Name:      "Untitled",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Provider:  string(cfg.Provider),
			Model:     cfg.Model,
		}
		if cfg.SystemPrompt != "" {
			activeSession.History = []llm.Message{{
				Role:    llm.RoleSystem,
				Content: cfg.SystemPrompt,
			}}
		}
		logger.L.Info("created fresh session", "id", activeSession.ID)
	}

	logger.L.Info("aig starting",
		"provider", cfg.Provider,
		"model", cfg.Model,
		"persona", cfg.Persona,
		"log_file", logger.LogPath,
	)

	// ── Provider ──────────────────────────────────────────────────────────────
	var provider llm.Provider
	switch cfg.Provider {
	case config.ProviderGemini:
		provider, err = llm.NewGeminiProvider(cfg.APIKey, cfg.Model)
	case config.ProviderDeepSeek:
		provider, err = llm.NewDeepSeekProvider(cfg.APIKey, cfg.Model)
	default:
		return fmt.Errorf("unknown provider: %s", cfg.Provider)
	}
	if err != nil {
		logger.L.Error("provider init failed", "provider", cfg.Provider, "error", err)
		return fmt.Errorf("failed to initialize provider: %w", err)
	}
	logger.L.Debug("provider initialised", "provider", cfg.Provider, "model", cfg.Model)

	// ── TUI ───────────────────────────────────────────────────────────────────
	model, err := tui.NewModel(provider, activeSession, cfg.SystemPrompt, version, string(cfg.Provider), cfg.Model, cfg.Persona)
	if err != nil {
		logger.L.Error("TUI init failed", "error", err)
		return fmt.Errorf("failed to initialize TUI: %w", err)
	}

	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
	)

	logger.L.Debug("entering TUI event loop")
	if _, err := p.Run(); err != nil {
		logger.L.Error("TUI exited with error", "error", err)
		return fmt.Errorf("TUI error: %w", err)
	}

	logger.L.Info("aig exited cleanly")
	return nil
}

// coalesceEnv returns the value of the named environment variable if set,
// otherwise returns the fallback string (e.g. from the config file).
func coalesceEnv(envName, fallback string) string {
	if v := os.Getenv(envName); v != "" {
		return v
	}
	return fallback
}
