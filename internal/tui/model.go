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
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/clitorhea/sagittarius-astar.git/internal/llm"
	"github.com/clitorhea/sagittarius-astar.git/internal/logger"
	"github.com/clitorhea/sagittarius-astar.git/internal/sandbox"
	"github.com/clitorhea/sagittarius-astar.git/internal/session"
	"github.com/clitorhea/sagittarius-astar.git/internal/workspace"
)

// state represents the TUI's current operational mode.
type state int

const (
	stateInput         state = iota // Waiting for user to type and submit.
	stateStreaming                  // LLM is streaming tokens.
	stateConfirmingCmd              // Awaiting y/n for command execution.
	stateExecuting                  // Sandbox command is running.
	stateExecutingTool              // Agent tool is executing autonomously.
)

// execLangs are the code fence tags that trigger the sandbox prompt.
var execLangPattern = regexp.MustCompile("(?m)^```(bash|sh|ps1|powershell)\n((?s).*?)\n```")

// Model is the Bubble Tea application model for aig.
type Model struct {
	// Core state
	appState state
	history  []llm.Message // full conversation history
	provider llm.Provider

	// Session management
	activeSession       *session.Session
	defaultSystemPrompt string

	// Streaming state
	tokenChan    chan string
	streamBuffer string // accumulates tokens during streaming
	cancelStream context.CancelFunc

	// Pending command execution
	pendingCmd  string
	pendingLang string

	// UI components
	textarea    textarea.Model
	cmdTextarea textarea.Model
	viewport    viewport.Model
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

	// App version string
	appVersion string
}

// NewModel constructs a new TUI model wired to the given LLM provider and session.
func NewModel(provider llm.Provider, activeSession *session.Session, defaultSystemPrompt string, appVersion string) (*Model, error) {
	// Text area
	ta := textarea.New()
	ta.Placeholder = "Ask anything… (Enter to send, Shift+Enter for newline, /help for commands)"
	ta.Focus()
	ta.CharLimit = 8000
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetKeys("shift+enter")

	cta := textarea.New()
	cta.Placeholder = "Edit command before running... (Enter to execute, Esc to cancel)"
	cta.CharLimit = 8000
	cta.SetHeight(5)
	cta.ShowLineNumbers = true
	cta.KeyMap.InsertNewline.SetKeys("shift+enter")

	// Viewport
	vp := viewport.New(80, 20)

	// Spinner
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = spinnerStyle

	// Glamour renderer
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(0), // let viewport handle wrapping
	)
	if err != nil {
		return nil, fmt.Errorf("tui: failed to create glamour renderer: %w", err)
	}

	m := &Model{
		appState:            stateInput,
		history:             activeSession.History,
		provider:            provider,
		textarea:            ta,
		cmdTextarea:         cta,
		viewport:            vp,
		spinner:             sp,
		renderer:            renderer,
		activeSession:       activeSession,
		defaultSystemPrompt: defaultSystemPrompt,
		appVersion:          appVersion,
	}

	return m, nil
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

	// ── Window Resized ─────────────────────────────────────────────────────
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 8 // account for header, input area, margins
		m.textarea.SetWidth(msg.Width - 4) // account for border padding
		m.cmdTextarea.SetWidth(msg.Width - 4)
		if m.renderer != nil {
			m.renderer, _ = glamour.NewTermRenderer(
				glamour.WithStandardStyle("dark"),
				glamour.WithWordWrap(m.viewport.Width-4),
			)
		}
		m.loadHistory(m.history)

	// ── Spinner tick ───────────────────────────────────────────────────────
	case spinner.TickMsg:
		if m.appState == stateStreaming || m.appState == stateExecuting || m.appState == stateExecutingTool {
			var spCmd tea.Cmd
			m.spinner, spCmd = m.spinner.Update(msg)
			return m, spCmd
		}

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

				// Slash Commands
				if strings.HasPrefix(input, "/") {
					m.textarea.Reset()

					// Exit commands
					if input == "/quit" || input == "/exit" || input == "/q" {
						return m, tea.Quit
					}

					// Clear display (does not wipe history in db, just display)
					if input == "/clear" {
						m.outputLines = nil
						m.history = m.history[:min(1, len(m.history))] // keep system prompt
						m.saveSession()
						m.refreshViewport()
						break
					}

					// Help manual
					if input == "/help" || input == "/?" {
						m.appendOutput(lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("Available commands:"))
						m.appendOutput("  /history       List all saved conversations")
						m.appendOutput("  /load <id>     Resume a conversation by its ID")
						m.appendOutput("  /save <name>   Give the current conversation a friendly name")
						m.appendOutput("  /new           Start a fresh conversation thread")
						m.appendOutput("  /clear         Clear display output (keeps system prompt)")
						m.appendOutput("  /quit, /q      Exit the application")
						m.refreshViewport()
						break
					}

					// Session history list
					if input == "/history" {
						sessions, err := session.List()
						if err != nil {
							m.appendOutput(errorStyle.Render("✗ Failed to list sessions: " + err.Error()))
						} else if len(sessions) == 0 {
							m.appendOutput(lipgloss.NewStyle().Foreground(colorYellow).Render("No saved conversations found."))
						} else {
							m.appendOutput(lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("Saved Conversations:"))
							for _, s := range sessions {
								name := s.Name
								if name == "" {
									name = "Untitled"
								}
								line := fmt.Sprintf("  • %s  (%s)  [%s]", s.ID, name, s.UpdatedAt.Format("2006-01-02 15:04"))
								if s.ID == m.activeSession.ID {
									line += lipgloss.NewStyle().Foreground(colorGreen).Render(" (active)")
								}
								m.appendOutput(lipgloss.NewStyle().Foreground(colorText).Render(line))
							}
							m.appendOutput(lipgloss.NewStyle().Foreground(colorSubtext).Render("Use /load <id> to resume a thread, or /new to start fresh."))
						}
						m.refreshViewport()
						break
					}

					// Create new conversation
					if input == "/new" {
						m.activeSession = &session.Session{
							ID:        session.GenerateID(),
							Name:      "Untitled",
							CreatedAt: time.Now(),
							UpdatedAt: time.Now(),
							Provider:  m.activeSession.Provider,
							Model:     m.activeSession.Model,
						}
						if m.defaultSystemPrompt != "" {
							m.activeSession.History = []llm.Message{{
								Role:    llm.RoleSystem,
								Content: m.defaultSystemPrompt,
							}}
						}
						m.history = m.activeSession.History
						m.outputLines = nil
						m.saveSession()
						m.appendOutput(lipgloss.NewStyle().Foreground(colorGreen).Render("Started fresh session: " + m.activeSession.ID))
						m.refreshViewport()
						break
					}

					// Rename/Save session
					if strings.HasPrefix(input, "/save ") || strings.HasPrefix(input, "/rename ") {
						name := strings.TrimSpace(strings.TrimPrefix(input, "/save "))
						if strings.HasPrefix(input, "/rename ") {
							name = strings.TrimSpace(strings.TrimPrefix(input, "/rename "))
						}
						if name == "" {
							m.appendOutput(errorStyle.Render("✗ Please provide a name (e.g. /save my-topic)"))
							m.refreshViewport()
							break
						}
						m.activeSession.Name = name
						if err := session.Save(m.activeSession); err != nil {
							m.appendOutput(errorStyle.Render("✗ Failed to save: " + err.Error()))
						} else {
							m.appendOutput(lipgloss.NewStyle().Foreground(colorGreen).Render("Conversation renamed to: " + name))
						}
						m.refreshViewport()
						break
					}

					// Workspace map
					if strings.HasPrefix(input, "/map ") || input == "/map" {
						dir := strings.TrimSpace(strings.TrimPrefix(input, "/map"))
						if dir == "" {
							dir = "."
						}
						tree, err := workspace.MapDirectory(dir)
						if err != nil {
							m.appendOutput(errorStyle.Render("✗ Failed to map directory: " + err.Error()))
						} else {
							m.history = append(m.history, llm.Message{
								Role:    llm.RoleSystem,
								Content: fmt.Sprintf("Workspace Map for '%s':\n```\n%s\n```", dir, tree),
							})
							m.saveSession()
							m.appendOutput(lipgloss.NewStyle().Foreground(colorSubtext).Render(fmt.Sprintf("🗺️ Mapped workspace %s", dir)))
						}
						m.refreshViewport()
						break
					}

					// Load session
					if strings.HasPrefix(input, "/load ") {
						id := strings.TrimSpace(strings.TrimPrefix(input, "/load "))
						if id == "" {
							m.appendOutput(errorStyle.Render("✗ Please specify a session ID (e.g. /load 20260521-120000)"))
							m.refreshViewport()
							break
						}
						s, err := session.Load(id)
						if err != nil {
							m.appendOutput(errorStyle.Render("✗ Failed to load session: " + err.Error()))
						} else {
							m.activeSession = s
							m.loadHistory(s.History)
							m.appendOutput(lipgloss.NewStyle().Foreground(colorGreen).Render("Loaded session: " + s.ID))
						}
						m.refreshViewport()
						break
					}

					// Unknown command fallback
					m.appendOutput(errorStyle.Render("✗ Unknown command. Type /help to see available commands."))
					m.refreshViewport()
					break
				}

				// Standard user prompt submission
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
			switch msg.Type {
			case tea.KeyEsc:
				logger.L.Info("sandbox: user declined execution", "lang", m.pendingLang)
				m.appState = stateInput
				m.cmdTextarea.Blur()
				m.textarea.Focus()
				m.appendOutput(confirmStyle.Render("✗ Command skipped."))
				m.refreshViewport()
				return m, nil
			case tea.KeyEnter:
				cmd := strings.TrimSpace(m.cmdTextarea.Value())
				if cmd == "" {
					return m, nil
				}
				logger.L.Info("sandbox: user approved execution",
					"lang", m.pendingLang,
					"command", cmd,
				)
				m.pendingCmd = cmd
				m.appState = stateExecuting
				m.cmdTextarea.Blur()
				m.appendOutput(confirmStyle.Render("⚡ Executing…"))
				m.refreshViewport()
				return m, m.runSandboxCmd()
			case tea.KeyCtrlC:
				return m, tea.Quit
			}

			var cmd tea.Cmd
			m.cmdTextarea, cmd = m.cmdTextarea.Update(msg)
			return m, cmd

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
		m.saveSession()

		// Render the full response through glamour.
		rendered, err := m.renderer.Render(raw)
		if err != nil {
			rendered = raw // fallback to plain text
		}

		// Replace the streaming preview with the final rendered output.
		if len(m.outputLines) > 0 && strings.Contains(m.outputLines[len(m.outputLines)-1], "…") {
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

			// Highlight the block and prepare confirmation textarea.
			block := codeBlockStyle.Render("```" + lang + "\n" + cmd + "\n```")
			m.outputLines = append(m.outputLines, block)
			
			m.cmdTextarea.SetValue(cmd)
			m.textarea.Blur()
			m.cmdTextarea.Focus()
			
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
			m.saveSession()
		}
		m.pendingCmd = ""
		m.pendingLang = ""
		m.appState = stateInput
		m.refreshViewport()
		return m, nil

	// ── Tool execution result ──────────────────────────────────────────────
	case toolCallMsg:
		m.appState = stateExecutingTool

		if len(m.outputLines) > 0 && strings.Contains(m.outputLines[len(m.outputLines)-1], "…") {
			m.outputLines = m.outputLines[:len(m.outputLines)-1]
		}

		m.appendOutput(lipgloss.NewStyle().Foreground(colorYellow).Render(fmt.Sprintf("⚙ Executing tool: %s", msg.Call.Name)))
		m.refreshViewport()

		m.history = append(m.history, llm.Message{
			Role:      llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{msg.Call},
		})

		call := msg.Call
		return m, func() tea.Msg {
			resultStr, err := ExecuteTool(call)
			if err != nil {
				return toolCallResultMsg{CallID: call.ID, Name: call.Name, Result: "", Error: err}
			}
			return toolCallResultMsg{CallID: call.ID, Name: call.Name, Result: resultStr, Error: nil}
		}

	case toolCallResultMsg:
		resContent := msg.Result
		if msg.Error != nil {
			resContent = fmt.Sprintf("Error: %s", msg.Error.Error())
			m.appendOutput(errorStyle.Render(fmt.Sprintf("✗ Tool %s failed", msg.Name)))
		} else {
			m.appendOutput(lipgloss.NewStyle().Foreground(colorGreen).Render(fmt.Sprintf("✓ Tool %s succeeded", msg.Name)))
		}
		m.refreshViewport()

		m.history = append(m.history, llm.Message{
			Role:       llm.RoleTool,
			Content:    resContent,
			ToolCallID: msg.CallID,
			ToolName:   msg.Name,
		})
		m.saveSession()

		return m, tea.Batch(m.startStreaming()...)
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
	b.WriteString(headerBanner(m.appVersion))
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
		b.WriteString(inputActiveStyle.Render(
			lipgloss.JoinVertical(lipgloss.Left,
				confirmStyle.Render(fmt.Sprintf("Review %s block (Enter to run, Esc to cancel, Shift+Enter for newline):", m.pendingLang)),
				m.cmdTextarea.View(),
			),
		))
	case stateExecuting:
		b.WriteString(inputStyle.Render(
			m.spinner.View() + " " + lipgloss.NewStyle().Foreground(colorSubtext).Render("running command…"),
		))
	case stateExecutingTool:
		b.WriteString(inputStyle.Render(
			m.spinner.View() + " " + lipgloss.NewStyle().Foreground(colorSubtext).Render("executing tool…"),
		))
	}

	return b.String()
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// loadHistory updates the model's history and fully renders it to the viewport display.
func (m *Model) loadHistory(history []llm.Message) {
	m.outputLines = nil
	m.history = history

	for i, msg := range m.history {
		if msg.Role == llm.RoleSystem {
			if i == 0 {
				// Initial system prompt is not rendered in output
				continue
			}
			// Command execution feedback from sandboxing
			m.outputLines = append(m.outputLines,
				systemLabelStyle.Render("⚙ System Context (Execution Feedback):"),
				systemContentStyle.Render(msg.Content),
				divider(m.width),
			)
			continue
		}

		if msg.Role == llm.RoleUser {
			label := userLabelStyle.Render("You")
			m.outputLines = append(m.outputLines,
				label,
				lipgloss.NewStyle().Foreground(colorText).Render(msg.Content),
				divider(m.width),
			)
		} else if msg.Role == llm.RoleAssistant {
			rendered, err := m.renderer.Render(msg.Content)
			if err != nil {
				rendered = msg.Content
			}
			label := assistantLabelStyle.Render("aig")
			m.outputLines = append(m.outputLines,
				label,
				strings.TrimRight(rendered, "\n"),
				divider(m.width),
			)
		}
	}
}

// saveSession persists the active session history to disk.
func (m *Model) saveSession() {
	if m.activeSession == nil {
		return
	}
	m.activeSession.History = m.history
	if err := session.Save(m.activeSession); err != nil {
		logger.L.Error("failed to auto-save session", "id", m.activeSession.ID, "error", err)
	}
}

// submitPrompt adds the user's message to history and renders it in the viewport.
func (m Model) submitPrompt(input string) Model {
	// 1. Process /read(file) macros
	reRead := regexp.MustCompile(`\/read\(([^)]+)\)`)
	matches := reRead.FindAllStringSubmatch(input, -1)
	for _, match := range matches {
		filename := match[1]
		content, err := os.ReadFile(filename)
		if err != nil {
			m.appendOutput(errorStyle.Render(fmt.Sprintf("✗ Could not read %s: %v", filename, err)))
		} else {
			m.history = append(m.history, llm.Message{
				Role:    llm.RoleSystem,
				Content: fmt.Sprintf("File Context: %s\n```\n%s\n```", filename, string(content)),
			})
			m.appendOutput(lipgloss.NewStyle().Foreground(colorSubtext).Render(fmt.Sprintf("📎 Attached %s", filename)))
		}
	}

	m.history = append(m.history, llm.Message{
		Role:    llm.RoleUser,
		Content: input,
	})
	m.saveSession()

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
		// Prune to a default context limit (100k tokens approx ~400k chars)
		prunedHistory := llm.PruneHistory(m.history, 100000)
		toolCall, err := m.provider.StreamChat(ctx, prunedHistory, AgentTools(), ch)
		if err != nil {
			return streamErrMsg{err: err}
		}
		if toolCall != nil {
			return toolCallMsg{Call: *toolCall}
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
			// Channel closed — stream done.
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
	if len(lines) > 0 && strings.Contains(lines[len(lines)-1], "…") {
		lines = lines[:len(lines)-1]
	}
	label := assistantLabelStyle.Render("aig")
	content := strings.Join(lines, "\n\n") + "\n\n" + label + "\n" + preview
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
		statusText = "⚙ executing command"
	case stateExecutingTool:
		statusText = "⚙ executing tool"
	default:
		statusText = "● ready"
	}

	// Show active session ID in status bar
	sessionInfo := ""
	if m.activeSession != nil {
		name := m.activeSession.Name
		if name == "" {
			name = "Untitled"
		}
		sessionInfo = fmt.Sprintf("session: %s (%s)", m.activeSession.ID, name)
	}

	leftText := statusText
	if sessionInfo != "" {
		leftText = fmt.Sprintf("%s · %s", statusText, sessionInfo)
	}

	padding := m.width - lipgloss.Width(leftText) - 4
	if padding < 0 {
		padding = 0
	}
	spacer := strings.Repeat(" ", padding)
	help := lipgloss.NewStyle().Foreground(colorSubtext).Render("ctrl+c quit · /help menu")
	return statusBarStyle.Width(m.width).Render(leftText + spacer + help)
}
