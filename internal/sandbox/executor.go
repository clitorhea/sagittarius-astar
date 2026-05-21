// Package sandbox provides OS-aware command execution for aig.
// Commands are always gated by explicit user confirmation in the TUI.
package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/clitorhea/sagittarius-astar.git/internal/logger"
)

// Result holds the combined output from an executed command.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Combined returns stdout and stderr as a single annotated string suitable
// for injection into LLM conversation history.
func (r Result) Combined() string {
	var sb strings.Builder
	if r.Stdout != "" {
		sb.WriteString("--- stdout ---\n")
		sb.WriteString(r.Stdout)
	}
	if r.Stderr != "" {
		sb.WriteString("\n--- stderr ---\n")
		sb.WriteString(r.Stderr)
	}
	if r.ExitCode != 0 {
		fmt.Fprintf(&sb, "\n--- exit code: %d ---\n", r.ExitCode)
	}
	return strings.TrimSpace(sb.String())
}

// Execute runs the given command string using the platform-appropriate shell.
// On Linux/macOS it uses /bin/bash -c; on Windows it uses powershell.exe -Command.
// The context is respected for cancellation/timeout.
func Execute(ctx context.Context, command string) (*Result, error) {
	var cmd *exec.Cmd

	shell := Shell()
	logger.L.Info("sandbox: executing command",
		"shell", shell,
		"os", runtime.GOOS,
		"command", command,
	)

	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command)
	default: // linux, darwin, etc.
		cmd = exec.CommandContext(ctx, "/bin/bash", "-c", command)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	result := &Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			logger.L.Warn("sandbox: command exited non-zero",
				"exit_code", result.ExitCode,
				"stderr_len", len(result.Stderr),
			)
			return result, nil
		}
		logger.L.Error("sandbox: failed to start command", "error", err)
		return result, fmt.Errorf("executor: failed to start command: %w", err)
	}

	logger.L.Debug("sandbox: command succeeded",
		"stdout_len", len(result.Stdout),
		"stderr_len", len(result.Stderr),
	)
	return result, nil
}

// Shell returns the shell binary path used on the current OS.
// Useful for display purposes in the TUI.
func Shell() string {
	if runtime.GOOS == "windows" {
		return "powershell.exe"
	}
	return "/bin/bash"
}
