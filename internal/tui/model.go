// Package tui — Bubble Tea model implementing the aig state machine.
//
// State transitions:
//
//	INPUT ──submit──► STREAMING ──done──► INPUT
//	                      │
//	                  code block found
//	                      │
//	                      ▼
//	                 CONFIRMING_CMD ──y──► EXECUTING_CMD ──► INPUT
//	                      │                                   ▲
//	                    n/Esc                                  │
//	                      └──────────────────────────────────┘
package tui

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/clitorhea/sagittarius-astar.git/internal/llm"
	"github.com/clitorhea/sagittarius-astar.git/internal/logger"
	"github.com/clitorhea/sagittarius-astar.git/internal/sandbox"
)

// state represents the TUI's current operational mode.
type state int

const (
	stateInput         state = iota // Waiting for user to type and submit.
	stateStreaming                  // LLM is streaming tokens.
	stateConfirmingCmd              // Awaiting y/n for command execution.
	stateExecuting                  // Sandbox command is running.
)

// execLangs are the code fence tags that trigger the sandbox prompt.
var execLangPattern = regexp.MustCompile("(?m)^```(bash|sh|ps1|powershell)\n((?s).*?)\n```")

// Model is the Bubble Tea application model for aig.
type Model struct {
	// Core state
	appState state
	history  []llm.Message // full conversation history
	provider llm.Provider

	// Streaming state
	tokenChan    chan string
	streamBuffer string // accumulates tokens during streaming
	cancelStream context.CancelFunc

	// Pending command execution
	pendingCmd  string
	pendingLang string

	// UI components
	textarea textarea.Model
	viewport viewport.Model
	spinner  spinner.Model

	// Layout
	width  int
	height int

	// Rendered display content
	outputLines []string // rendered lines in the viewport

	// Glamour renderer
	renderer *glamour.TermRenderer

	// Error message to display
	lastErr string
}

// NewModel constructs a new TUI model wired to the given LLM provider.
// systemPrompt is injected as the first message in history.
func NewModel(provider llm.Provider, systemPrompt string) (*Model, error) {
	// Text area
	ta := textarea.New()
	ta.Placeholder = "Ask anything… (Enter to send, Shift+Enter for newline, /quit to exit)"
	ta.Focus()
	ta.CharLimit = 8000
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetKeys("shift+enter")

	// Viewport
	vp := viewport.New(80, 20)

	// Spinner
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = spinnerStyle

	// Glamour renderer (auto-detects dark/light terminal theme)
	renderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(0), // let viewport handle wrapping
	)
	if err != nil {
		return nil, fmt.Errorf("tui: failed to create glamour renderer: %w", err)
	}

	history := []llm.Message{}
	if systemPrompt != "" {
		history = append(history, llm.Message{Role: llm.RoleSystem, Content: systemPrompt})
	}

	return &Model{
		appState: stateInput,
		history:  history,
		provider: provider,
		textarea: ta,
		viewport: vp,
		spinner:  sp,
		renderer: renderer,
	}, nil
}

// ── Bubble Tea lifecycle ──────────────────────────────────────────────────────

// Init satisfies tea.Model.
func (m Model) Init() tea.Cmd {
	return nil
}


// Update is the central message dispatcher.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	// ── Window resize ──────────────────────────────────────────────────────
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		headerH := 3  // banner
		inputH := 5   // textarea + border
		statusH := 1  // status bar
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - headerH - inputH - statusH - 2
		m.textarea.SetWidth(msg.Width - 4) // account for border padding
		if m.renderer != nil {
			m.renderer, _ = glamour.NewTermRenderer(
				glamour.WithAutoStyle(),
				glamour.WithWordWrap(m.viewport.Width-4),
			)
		}
		m.refreshViewport()

	// ── Keyboard input ─────────────────────────────────────────────────────
	case tea.KeyMsg:
		switch m.appState {
		case stateInput:
			switch msg.Type {
			case tea.KeyEnter:
				input := strings.TrimSpace(m.textarea.Value())
				if input == "" {
					break
				}
				// Built-in commands.
				if input == "/quit" || input == "/exit" || input == "/q" {
					return m, tea.Quit
				}
				if input == "/clear" {
					m.outputLines = nil
					m.history = m.history[:min(1, len(m.history))] // keep system prompt
					m.textarea.Reset()
					m.refreshViewport()
					break
				}
				m.textarea.Reset()
				logger.L.Info("user prompt submitted",
					"prompt_len", len(input),
					"history_turns", len(m.history),
				)
				m = m.submitPrompt(input)
				return m, tea.Batch(m.startStreaming()...)

			case tea.KeyCtrlC:
				return m, tea.Quit
			}

		case stateStreaming:
			// Allow CtrlC to cancel the stream.
			if msg.Type == tea.KeyCtrlC {
				if m.cancelStream != nil {
					m.cancelStream()
				}
				logger.L.Warn("stream cancelled by user")
				m.appState = stateInput
				m.appendOutput(errorStyle.Render("✗ Stream cancelled."))
				m.refreshViewport()
				return m, nil
			}

		case stateConfirmingCmd:
			switch {
			case msg.Type == tea.KeyRunes && (string(msg.Runes) == "y" || string(msg.Runes) == "Y"):
				logger.L.Info("sandbox: user approved execution",
					"lang", m.pendingLang,
					"command", m.pendingCmd,
				)
				m.appState = stateExecuting
				m.appendOutput(confirmStyle.Render("⚡ Executing…"))
				m.refreshViewport()
				return m, m.runSandboxCmd()

			case msg.Type == tea.KeyEscape,
				msg.Type == tea.KeyRunes && (string(msg.Runes) == "n" || string(msg.Runes) == "N"):
				logger.L.Info("sandbox: user declined execution", "lang", m.pendingLang)
				m.appState = stateInput
				m.appendOutput(confirmStyle.Render("✗ Command skipped."))
				m.refreshViewport()

			case msg.Type == tea.KeyCtrlC:
				return m, tea.Quit
			}

		case stateExecuting:
			if msg.Type == tea.KeyCtrlC {
				return m, tea.Quit
			}
		}

	// ── Streaming token received ───────────────────────────────────────────
	case tokenMsg:
		m.streamBuffer += string(msg)
		// Show live streaming text (unformatted, fast) in viewport.
		m.refreshViewportStreaming()
		return m, waitForToken(m.tokenChan)

	// ── Stream completed successfully ──────────────────────────────────────
	case streamDoneMsg:
		raw := m.streamBuffer
		m.streamBuffer = ""

		// Add assistant turn to history.
		m.history = append(m.history, llm.Message{
			Role:    llm.RoleAssistant,
			Content: raw,
		})
		logger.L.Info("stream completed", "response_len", len(raw))

		// Render the full response through glamour.
		rendered, err := m.renderer.Render(raw)
		if err != nil {
			rendered = raw // fallback to plain text
		}

		// Replace the streaming preview with the final rendered output.
		// Pop the last streaming line and replace with rendered content.
		if len(m.outputLines) > 0 {
			m.outputLines = m.outputLines[:len(m.outputLines)-1]
		}
		label := assistantLabelStyle.Render("aig")
		m.outputLines = append(m.outputLines, label)
		m.outputLines = append(m.outputLines, strings.TrimRight(rendered, "\n"))
		m.outputLines = append(m.outputLines, divider(m.width))

		// Check for executable code blocks.
		if match := execLangPattern.FindStringSubmatch(raw); match != nil {
			lang := match[1]
			cmd := match[2]
			m.pendingCmd = cmd
			m.pendingLang = lang
			m.appState = stateConfirmingCmd

			// Highlight the block and show confirmation prompt.
			block := codeBlockStyle.Render("```" + lang + "\n" + cmd + "\n```")
			prompt := confirmStyle.Render(fmt.Sprintf("[Execute this %s block? (y/N)]", lang))
			m.outputLines = append(m.outputLines, block, prompt)
			m.refreshViewport()
			return m, nil
		}

		m.appState = stateInput
		m.refreshViewport()
		return m, nil

	// ── Stream error ───────────────────────────────────────────────────────
	case streamErrMsg:
		m.streamBuffer = ""
		m.appState = stateInput
		m.lastErr = msg.err.Error()
		logger.L.Error("stream error received", "error", msg.err)
		m.appendOutput(errorStyle.Render("✗ Error: " + m.lastErr))
		m.refreshViewport()
		return m, nil

	// ── Sandbox execution result ───────────────────────────────────────────
	case execResultMsg:
		if msg.err != nil {
			logger.L.Error("sandbox: execution error", "error", msg.err)
			m.appendOutput(errorStyle.Render("✗ Execution error: " + msg.err.Error()))
		} else {
			logger.L.Info("sandbox: execution completed",
				"lang", m.pendingLang,
				"output_len", len(msg.output),
			)
			label := lipgloss.NewStyle().Foreground(colorGreen).Bold(true).Render("$ " + m.pendingLang)
			m.appendOutput(label)
			m.appendOutput(execResultStyle.Render(msg.output))

			// Feed output back into conversation as a system message.
			m.history = append(m.history, llm.Message{
				Role:    llm.RoleSystem,
				Content: fmt.Sprintf("Command executed:\n```%s\n%s\n```\n\nOutput:\n%s", m.pendingLang, m.pendingCmd, msg.output),
			})
		}
		m.pendingCmd = ""
		m.pendingLang = ""
		m.appState = stateInput
		m.refreshViewport()
		return m, nil
	}

	// ── Delegate to sub-components ─────────────────────────────────────────
	if m.appState == stateInput {
		var taCmd tea.Cmd
		m.textarea, taCmd = m.textarea.Update(msg)
		cmds = append(cmds, taCmd)
	}
	{
		var vpCmd tea.Cmd
		m.viewport, vpCmd = m.viewport.Update(msg)
		cmds = append(cmds, vpCmd)
	}
	if m.appState == stateStreaming {
		var spCmd tea.Cmd
		m.spinner, spCmd = m.spinner.Update(msg)
		cmds = append(cmds, spCmd)
	}

	return m, tea.Batch(cmds...)
}

// View renders the full TUI to a string.
func (m Model) View() string {
	if m.width == 0 {
		return "Loading…"
	}

	var b strings.Builder

	// Header banner.
	b.WriteString(headerBanner())
	b.WriteString(divider(m.width))
	b.WriteString("\n")

	// Scrollable output viewport.
	b.WriteString(m.viewport.View())
	b.WriteString("\n")

	// Status bar.
	b.WriteString(m.statusBar())
	b.WriteString("\n")

	// Input area (hidden while confirming or executing).
	switch m.appState {
	case stateInput:
		b.WriteString(inputActiveStyle.Render(m.textarea.View()))
	case stateStreaming:
		b.WriteString(inputStyle.Render(
			m.spinner.View() + " " + lipgloss.NewStyle().Foreground(colorSubtext).Render("streaming…"),
		))
	case stateConfirmingCmd:
		b.WriteString(inputStyle.Render(
			confirmStyle.Render("Press y to execute, n / Esc to skip"),
		))
	case stateExecuting:
		b.WriteString(inputStyle.Render(
			m.spinner.View() + " " + lipgloss.NewStyle().Foreground(colorSubtext).Render("running command…"),
		))
	}

	return b.String()
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// submitPrompt adds the user's message to history and renders it in the viewport.
func (m Model) submitPrompt(input string) Model {
	m.history = append(m.history, llm.Message{
		Role:    llm.RoleUser,
		Content: input,
	})
	label := userLabelStyle.Render("You")
	m.outputLines = append(m.outputLines,
		label,
		lipgloss.NewStyle().Foreground(colorText).Render(input),
		divider(m.width),
		assistantLabelStyle.Render("aig")+" "+spinnerStyle.Render("…"),
	)
	return m
}

// startStreaming kicks off the LLM goroutine and the waitForToken relay.
func (m *Model) startStreaming() []tea.Cmd {
	ch := make(chan string, 64)
	m.tokenChan = ch
	m.appState = stateStreaming
	m.streamBuffer = ""

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelStream = cancel

	// Background goroutine — calls the LLM provider.
	streamCmd := func() tea.Msg {
		err := m.provider.StreamChat(ctx, m.history, ch)
		if err != nil {
			return streamErrMsg{err: err}
		}
		return streamDoneMsg{}
	}

	return []tea.Cmd{
		streamCmd,
		waitForToken(ch),
		m.spinner.Tick,
	}
}

// waitForToken is the relay command: it blocks until one token arrives on ch
// (or ch is closed), then returns a tokenMsg. The Update loop re-issues this
// command after each token to achieve continuous streaming.
func waitForToken(ch chan string) tea.Cmd {
	return func() tea.Msg {
		token, ok := <-ch
		if !ok {
			// Channel closed — stream done. The streamCmd goroutine already
			// sent streamDoneMsg or streamErrMsg; returning nil here is safe.
			return nil
		}
		return tokenMsg(token)
	}
}

// runSandboxCmd executes the pending command in the sandbox.
func (m *Model) runSandboxCmd() tea.Cmd {
	cmd := m.pendingCmd
	return func() tea.Msg {
		result, err := sandbox.Execute(context.Background(), cmd)
		if err != nil {
			return execResultMsg{err: err}
		}
		return execResultMsg{output: result.Combined()}
	}
}

// refreshViewport re-renders all output lines into the viewport.
func (m *Model) refreshViewport() {
	content := strings.Join(m.outputLines, "\n")
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
}

// refreshViewportStreaming appends the current streaming buffer as a preview.
func (m *Model) refreshViewportStreaming() {
	preview := strings.TrimRight(m.streamBuffer, "\n")
	lines := m.outputLines
	// Replace the last line (the "…" spinner placeholder) with live text.
	if len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	label := assistantLabelStyle.Render("aig")
	content := strings.Join(lines, "\n") + "\n" + label + "\n" + preview
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
}

// appendOutput adds a rendered line to the output history.
func (m *Model) appendOutput(line string) {
	m.outputLines = append(m.outputLines, line)
}

// statusBar renders the bottom status bar.
func (m Model) statusBar() string {
	var statusText string
	switch m.appState {
	case stateStreaming:
		statusText = "● streaming"
	case stateConfirmingCmd:
		statusText = "⚠ awaiting confirmation"
	case stateExecuting:
		statusText = "⚙ executing"
	default:
		statusText = "● ready"
	}

	padding := m.width - lipgloss.Width(statusText) - 4
	if padding < 0 {
		padding = 0
	}
	spacer := strings.Repeat(" ", padding)
	help := lipgloss.NewStyle().Foreground(colorSubtext).Render("ctrl+c quit · /clear reset")
	return statusBarStyle.Width(m.width).Render(statusText + spacer + help)
}


