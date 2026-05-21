// Package logger initialises a file-backed slog.Logger for aig.
//
// Because the TUI owns the terminal, all log output must go to a file —
// writing to stderr or stdout would corrupt the Bubble Tea rendering.
//
// Log file location (XDG-aligned):
//
//	Linux/macOS : $XDG_CACHE_HOME/aig/aig.log  (~/.cache/aig/aig.log)
//	Windows     : %LocalAppData%\aig\aig.log
//
// Usage:
//
//	cleanup, err := logger.Init(slog.LevelDebug)
//	defer cleanup()
//	logger.L.Info("started", "provider", "gemini")
package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// L is the package-level logger. It is set to a discard-handler until
// Init is called, so any early log calls are silently dropped.
var L = slog.New(slog.NewTextHandler(io.Discard, nil))

// LogPath returns the resolved path to the log file.
// Call after Init to get the actual path used.
var LogPath string

// Init opens (or creates) the log file and configures the package-level
// logger. It returns a cleanup function that flushes and closes the file.
//
// level controls the minimum severity written to the file:
//
//	slog.LevelDebug — everything (verbose, state transitions, API calls)
//	slog.LevelInfo  — startup/shutdown, prompts, stream lifecycle  (default)
//	slog.LevelWarn  — recoverable issues, cancelled operations
//	slog.LevelError — LLM errors, sandbox failures, config problems
func Init(level slog.Level, overridePath string) (cleanup func(), err error) {
	var logFile string

	if overridePath != "" {
		logFile = overridePath
	} else {
		cacheDir, err := os.UserCacheDir()
		if err != nil {
			// Fallback: write next to the binary.
			cacheDir = "."
		}
		logFile = filepath.Join(cacheDir, "aig", "aig.log")
	}

	LogPath = logFile

	if err := os.MkdirAll(filepath.Dir(logFile), 0o750); err != nil {
		return func() {}, fmt.Errorf("logger: cannot create log directory: %w", err)
	}

	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return func() {}, fmt.Errorf("logger: cannot open log file %s: %w", logFile, err)
	}

	handler := slog.NewTextHandler(f, &slog.HandlerOptions{
		Level: level,
		// Include source location in DEBUG builds for easier tracing.
		AddSource: level == slog.LevelDebug,
	})

	L = slog.New(handler)

	// Write a session boundary so log files are easy to scan.
	L.Info("─── session start ───",
		"time", time.Now().Format(time.RFC3339),
		"pid", os.Getpid(),
		"log_level", level.String(),
	)

	return func() {
		L.Info("─── session end ───")
		_ = f.Close()
	}, nil
}

// ParseLevel converts a CLI string ("debug", "info", "warn", "error") to
// a slog.Level. Returns slog.LevelInfo and an error for unknown strings.
func ParseLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unknown log level %q (valid: debug, info, warn, error)", s)
	}
}
