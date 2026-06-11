// Package sandbox provides OS-aware command execution for aig.
// Commands are always gated by explicit user confirmation in the TUI.
package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/clitorhea/sagittarius-astar.git/internal/logger"
)

// DefaultTimeout is the maximum wall-clock time a sandboxed command may run.
// Commands that exceed this are killed automatically.
const DefaultTimeout = 30 * time.Second

// MaxTimeout is the ceiling a caller may request for a single command.
const MaxTimeout = 120 * time.Second

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
	fmt.Fprintf(&sb, "exit_code: %d\n", r.ExitCode)
	if r.Stdout != "" {
		sb.WriteString("--- stdout ---\n")
		sb.WriteString(r.Stdout)
	}
	if r.Stderr != "" {
		sb.WriteString("\n--- stderr ---\n")
		sb.WriteString(r.Stderr)
	}
	return strings.TrimSpace(sb.String())
}

// Options configures a single Execute call.
type Options struct {
	// WorkingDir sets the command's working directory.
	// An empty string inherits the process working directory.
	WorkingDir string

	// Timeout overrides DefaultTimeout for this call.
	// Values ≤ 0 use DefaultTimeout; values > MaxTimeout are clamped to MaxTimeout.
	Timeout time.Duration

	// SudoPassword is the password to use for sudo commands.
	SudoPassword string
}

// Execute runs the given command string using the platform-appropriate shell.
// On Linux/macOS it uses /bin/bash -c; on Windows it uses powershell.exe -Command.
//
// A hard timeout is applied automatically. If the parent context is already
// shorter, that deadline wins.
func Execute(ctx context.Context, command string, opts ...Options) (*Result, error) {
	var stdinBuf bytes.Buffer
	if len(opts) > 0 && opts[0].SudoPassword != "" {
		stdinBuf.WriteString(opts[0].SudoPassword + "\n")
	}
	return ExecuteWithStdin(ctx, command, &stdinBuf, opts...)
}

// ExecuteWithStdin is like Execute but accepts a custom io.Reader for stdin.
// Pass an io.Pipe reader to allow the TUI to inject lines while the process runs.
func ExecuteWithStdin(ctx context.Context, command string, stdin io.Reader, opts ...Options) (*Result, error) {
	var opt Options
	if len(opts) > 0 {
		opt = opts[0]
	}

	timeout := opt.Timeout
	switch {
	case timeout <= 0:
		timeout = DefaultTimeout
	case timeout > MaxTimeout:
		timeout = MaxTimeout
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	shell := Shell()
	logger.L.Info("sandbox: executing command",
		"shell", shell,
		"os", runtime.GOOS,
		"command", command,
		"working_dir", opt.WorkingDir,
		"timeout", timeout,
	)

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(timeoutCtx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command)
	default: // linux, darwin, etc.
		if opt.SudoPassword != "" {
			re := regexp.MustCompile(`\bsudo\b`)
			command = re.ReplaceAllString(command, "sudo -S")
			command = strings.ReplaceAll(command, "sudo -S -S", "sudo -S")
			command = strings.ReplaceAll(command, "sudo -S -s", "sudo -S -s")
		}
		cmd = exec.CommandContext(timeoutCtx, "/bin/bash", "-c", command)
		cmd.Stdin = stdin
	}

	if opt.WorkingDir != "" {
		cmd.Dir = opt.WorkingDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	result := &Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if opt.SudoPassword != "" {
		rePrompt := regexp.MustCompile(`(?m)^\[sudo\] password for .*: \n?`)
		result.Stderr = rePrompt.ReplaceAllString(result.Stderr, "")
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
		if timeoutCtx.Err() == context.DeadlineExceeded {
			logger.L.Error("sandbox: command timed out", "timeout", timeout)
			return result, fmt.Errorf("executor: command timed out after %s", timeout)
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
func Shell() string {
	if runtime.GOOS == "windows" {
		return "powershell.exe"
	}
	return "/bin/bash"
}
