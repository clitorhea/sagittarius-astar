// Package tui — custom Bubble Tea message types for the aig TUI.
package tui

import "github.com/clitorhea/sagittarius-astar.git/internal/llm"

// tokenMsg carries a single streamed token chunk from the LLM.
type tokenMsg string

// streamDoneMsg signals that the LLM stream has completed successfully.
type streamDoneMsg struct{}

// streamErrMsg signals that the LLM stream encountered an error.
type streamErrMsg struct{ err error }

func (e streamErrMsg) Error() string { return e.err.Error() }

// execResultMsg carries the combined stdout/stderr from a sandbox execution.
type execResultMsg struct {
	output string
	err    error
}

// execConfirmMsg signals that a code block was found and the TUI should
// ask the user for confirmation before executing.
type execConfirmMsg struct {
	Command string
	Lang    string
}

// toolCallMsg signals that the LLM requested to execute a tool.
type toolCallMsg struct {
	Call llm.ToolCall
}

// windowSizeMsg is re-exported here for clarity (bubbletea sends tea.WindowSizeMsg natively).
// We alias it here so internal code is self-contained.
