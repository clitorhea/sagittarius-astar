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
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/clitorhea/sagittarius-astar.git/internal/config"
	"github.com/clitorhea/sagittarius-astar.git/internal/llm"
	"github.com/clitorhea/sagittarius-astar.git/internal/logger"
	"github.com/clitorhea/sagittarius-astar.git/internal/sandbox"
	"github.com/clitorhea/sagittarius-astar.git/internal/session"
	"github.com/clitorhea/sagittarius-astar.git/internal/workspace"
)

// state represents the TUI's current operational mode.
type state int

const (
	stateInput             state = iota // Waiting for user to type and submit.
	stateStreaming                      // LLM is streaming tokens.
	stateConfirmingCmd                  // Awaiting y/n for command execution.
	stateExecuting                      // Sandbox command is running.
	stateExecutingTool                  // Agent tool is executing autonomously.
	stateExecutingTools                 // Multiple agent tools executing concurrently.
	stateConfirmingWrite                // Awaiting y/n for write_file tool.
	stateSelecting                      // Selecting options (models, history, providers, personas)
	stateInputSudoPassword              // Awaiting user input for sudo password.
	stateRuntimeInput                   // Sending a stdin line to an already-running process.
)

// execLangs are the code fence tags that trigger the sandbox prompt.
var execLangPattern = regexp.MustCompile("(?m)^```(bash|sh|ps1|powershell)\n((?s).*?)\n```")

type selectionItem struct {
	ID    string
	Label string
}

type sessionTitleMsg string

// Model is the Bubble Tea application model for aig.
type Model struct {
	// Core state
	appState state
	history  []llm.Message // full conversation history
	provider llm.Provider

	// Session management
	activeSession       *session.Session
	defaultSystemPrompt string
	titleGenerated      bool

	// Selection state
	selectionItems []selectionItem
	selectionIdx   int
	selectionType  string // "model", "history", "provider", "persona"

	// Provider / model identity (for status bar)
	activeProvider string
	activeModel    string
	activePersona  string

	// Runtime switching — API keys per provider, personas from config
	apiKeys    map[string]string  // provider name → API key
	fileConfig *config.FileConfig // parsed config, for persona resolution

	// autoApproveCommands skips the confirmation dialog for run_command tool calls.
	autoApproveCommands bool

	// pendingToolCalls holds the full batch returned by the LLM for sequential processing.
	pendingToolCalls []llm.ToolCall
	pendingToolIdx   int // index of the next call to process in pendingToolCalls

	// Streaming state
	tokenChan       chan string
	streamBuffer    string // accumulates tokens during streaming
	reasoningBuffer string // accumulates DeepSeek reasoning_content tokens (not displayed)
	cancelStream   context.CancelFunc

	// Pending command execution
	pendingCmd  string
	pendingLang string

	// cancelExec cancels a running sandbox child process without quitting the app.
	cancelExec context.CancelFunc

	// stdinPipe is the write end of the pipe connected to the running process's stdin.
	// Non-nil only while stateExecuting or stateExecutingTool with a pipe-based command.
	stdinPipe io.WriteCloser

	// runtimeInput is the single-line input box shown in stateRuntimeInput.
	runtimeInput textinput.Model

	// Pending write_file confirmation
	pendingWrite *PendingWrite
	pendingWriteCallID string

	// UI components
	textarea      textarea.Model
	cmdTextarea   textarea.Model
	sudoTextInput textinput.Model
	viewport      viewport.Model
	spinner       spinner.Model
	progressBar   progress.Model
	execStart     time.Time
	execTimeout        time.Duration
	sudoPassword       string
	autocompletePrefix string
	autocompleteIdx    int

	// Layout
	width             int
	height            int
	lastRendererWidth int

	// Rendered display content
	outputLines []string // rendered lines in the viewport

	// Glamour renderer
	renderer *glamour.TermRenderer

	// Error message to display
	lastErr string

	// App version string
	appVersion string

	// showHelp displays the keybind/command overlay.
	showHelp bool
}

// NewModel constructs a new TUI model wired to the given LLM provider and session.
func NewModel(provider llm.Provider, activeSession *session.Session, defaultSystemPrompt string, appVersion string, providerName string, modelName string, personaName string, apiKeys map[string]string) (*Model, error) {
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

	// Progress Bar
	pg := progress.New(progress.WithDefaultGradient())
	pg.Width = 40

	// Glamour renderer
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(0), // let viewport handle wrapping
	)
	if err != nil {
		return nil, fmt.Errorf("tui: failed to create glamour renderer: %w", err)
	}

	if apiKeys == nil {
		apiKeys = make(map[string]string)
	}

	ti := textinput.New()
	ti.Placeholder = "Enter sudo password..."
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '*'
	ti.CharLimit = 128

	ri := textinput.New()
	ri.Placeholder = "Type input for the running process and press Enter to send..."
	ri.CharLimit = 1024

	m := &Model{
		appState:            stateInput,
		history:             activeSession.History,
		provider:            provider,
		textarea:            ta,
		cmdTextarea:         cta,
		sudoTextInput:       ti,
		runtimeInput:        ri,
		viewport:            vp,
		spinner:             sp,
		progressBar:         pg,
		renderer:            renderer,
		activeSession:       activeSession,
		defaultSystemPrompt: defaultSystemPrompt,
		appVersion:          appVersion,
		activeProvider:      providerName,
		activeModel:         modelName,
		activePersona:       personaName,
		apiKeys:             apiKeys,
		fileConfig:          config.LoadFileConfig(),
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
	oldState := m.appState
	concreteModel, cmd := m.update(msg)
	if concreteModel.appState != oldState {
		concreteModel.recalculateDimensions()
	}
	return concreteModel, cmd
}

func (m Model) update(msg tea.Msg) (Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	// ── Window Resized ─────────────────────────────────────────────────────
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalculateDimensions()
		m.loadHistory(m.history, true) // preserve scroll position on resize

	// ── Spinner tick ───────────────────────────────────────────────────────
	case spinner.TickMsg:
		if m.appState == stateStreaming || m.appState == stateExecuting || m.appState == stateExecutingTool || m.appState == stateExecutingTools {
			var spCmd tea.Cmd
			m.spinner, spCmd = m.spinner.Update(msg)
			return m, spCmd
		}

	// ── Keyboard input ─────────────────────────────────────────────────────
	case tea.KeyMsg:
		switch m.appState {
		case stateInput:
			if msg.Type != tea.KeyTab {
				m.autocompletePrefix = ""
			}
			switch msg.Type {
			case tea.KeyTab:
				currentText := m.textarea.Value()
				if strings.HasPrefix(currentText, "/") {
					var slashCommands = []string{
						"/approve-tools",
						"/clear",
						"/delete",
						"/exit",
						"/help",
						"/history",
						"/load",
						"/map",
						"/model",
						"/new",
						"/persona",
						"/provider",
						"/quit",
					}

					// Start fresh or filter based on typed prefix
					if m.autocompletePrefix == "" || !strings.HasPrefix(currentText, m.autocompletePrefix) {
						m.autocompletePrefix = currentText
						m.autocompleteIdx = 0
					} else {
						m.autocompleteIdx++
					}

					var matches []string
					for _, cmd := range slashCommands {
						if strings.HasPrefix(cmd, m.autocompletePrefix) {
							matches = append(matches, cmd)
						}
					}

					if len(matches) > 0 {
						idx := m.autocompleteIdx % len(matches)
						m.textarea.SetValue(matches[idx] + " ")
						m.textarea.CursorEnd()
					}
					return m, nil
				}
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

					// Clear display: trim history to system-prompt-only, avoiding
					// orphaned tool messages that would cause API errors on next call.
					if input == "/clear" {
						m.outputLines = nil
						if len(m.history) > 0 && m.history[0].Role == llm.RoleSystem {
							m.history = m.history[:1]
						} else {
							m.history = nil
						}
						m.titleGenerated = false
						m.saveSession()
						m.refreshViewport()
						break
					}

					// Help manual
					if input == "/help" || input == "/?" {
						m.appendOutput(lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("Commands:"))
						m.appendOutput("  /history           List all saved conversations")
						m.appendOutput("  /load <id>         Resume a conversation by its ID")
						m.appendOutput("  /save <name>       Give the current conversation a friendly name")
						m.appendOutput("  /delete <id>       Permanently delete a saved conversation")
						m.appendOutput("  /map [dir]         Inject workspace tree into context")
						m.appendOutput("  /new               Start a fresh conversation thread")
						m.appendOutput("  /clear             Clear display output (keeps system prompt)")
						m.appendOutput("  /model             List available models for current provider")
						m.appendOutput("  /model <id>        Switch active model (keeps history)")
						m.appendOutput("  /provider          List available providers")
						m.appendOutput("  /provider <name>   Switch active provider (keeps history)")
						m.appendOutput("  /persona           List available personas")
						m.appendOutput("  /persona <name>    Switch system prompt persona")
						m.appendOutput("  /approve-tools on  Auto-execute agent run_command calls")
						m.appendOutput("  /approve-tools off Require confirmation per command (default)")
						m.appendOutput("  /quit, /q          Exit the application")
						m.appendOutput(lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("Keyboard shortcuts:"))
						m.appendOutput("  Enter              Send message")
						m.appendOutput("  Shift+Enter        Insert newline")
						m.appendOutput("  Ctrl+C             Cancel stream / quit")
						m.appendOutput("  Ctrl+L             Clear viewport")
						m.appendOutput("  ?                  Toggle this help overlay")
						m.appendOutput("  /read(file)        Attach file content to next message")
						m.refreshViewport()
						break
					}

					// Session history list
					if input == "/history" {
						sessions, err := session.List()
						if err != nil {
							m.appendOutput(errorStyle.Render("✗ Failed to list sessions: " + err.Error()))
							m.refreshViewport()
							break
						}
						if len(sessions) == 0 {
							m.appendOutput(lipgloss.NewStyle().Foreground(colorYellow).Render("No saved conversations found."))
							m.refreshViewport()
							break
						}
						m.selectionItems = nil
						m.selectionIdx = 0
						m.selectionType = "history"
						for _, s := range sessions {
							name := s.Name
							if name == "" {
								name = "Untitled"
							}
							label := fmt.Sprintf("%s · %s (%s)", s.ID, name, s.UpdatedAt.Format("2006-01-02 15:04"))
							if s.ID == m.activeSession.ID {
								label += " (active)"
							}
							m.selectionItems = append(m.selectionItems, selectionItem{
								ID:    s.ID,
								Label: label,
							})
						}
						m.appState = stateSelecting
						m.textarea.Blur()
						break
					}

					// Delete session
					if strings.HasPrefix(input, "/delete ") {
						id := strings.TrimSpace(strings.TrimPrefix(input, "/delete "))
						if id == "" {
							m.appendOutput(errorStyle.Render("✗ Please specify a session ID (e.g. /delete 20260521-120000)"))
							m.refreshViewport()
							break
						}
						if m.activeSession != nil && id == m.activeSession.ID {
							m.appendOutput(errorStyle.Render("✗ Cannot delete the active session. Start a /new session first."))
							m.refreshViewport()
							break
						}
						if err := session.Delete(id); err != nil {
							m.appendOutput(errorStyle.Render("✗ Failed to delete session: " + err.Error()))
						} else {
							m.appendOutput(lipgloss.NewStyle().Foreground(colorGreen).Render("🗑 Deleted session: " + id))
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
						m.titleGenerated = false
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
							m.appendOutput(lipgloss.NewStyle().Foreground(colorSubtext).Render(fmt.Sprintf("🗺️  Mapped workspace %s", dir)))
						}
						m.refreshViewport()
						break
					}

					// Load session
					if strings.HasPrefix(input, "/load") {
						id := strings.TrimSpace(strings.TrimPrefix(input, "/load"))
						if id == "" {
							sessions, err := session.List()
							if err != nil {
								m.appendOutput(errorStyle.Render("✗ Failed to list sessions: " + err.Error()))
								m.refreshViewport()
								break
							}
							if len(sessions) == 0 {
								m.appendOutput(lipgloss.NewStyle().Foreground(colorYellow).Render("No saved conversations found."))
								m.refreshViewport()
								break
							}
							m.selectionItems = nil
							m.selectionIdx = 0
							m.selectionType = "history"
							for _, s := range sessions {
								name := s.Name
								if name == "" {
									name = "Untitled"
								}
								label := fmt.Sprintf("%s · %s (%s)", s.ID, name, s.UpdatedAt.Format("2006-01-02 15:04"))
								if s.ID == m.activeSession.ID {
									label += " (active)"
								}
								m.selectionItems = append(m.selectionItems, selectionItem{
									ID:    s.ID,
									Label: label,
								})
							}
							m.appState = stateSelecting
							m.textarea.Blur()
							break
						}
						s, err := session.Load(id)
						if err != nil {
							m.appendOutput(errorStyle.Render("✗ Failed to load session: " + err.Error()))
						} else {
							m.activeSession = s
							m.loadHistory(s.History, false)
							m.titleGenerated = false
							m.appendOutput(lipgloss.NewStyle().Foreground(colorGreen).Render("Loaded session: " + s.ID))
						}
						m.refreshViewport()
						break
					}

				// ── /model [name] ──────────────────────────────────────────
				if strings.HasPrefix(input, "/model") {
					arg := strings.TrimSpace(strings.TrimPrefix(input, "/model"))

					if arg == "" {
						prov := config.ProviderName(m.activeProvider)
						models, ok := config.KnownModels[prov]
						if !ok || len(models) == 0 {
							m.appendOutput(lipgloss.NewStyle().Foreground(colorSubtext).Render("  (no known models for this provider)"))
							m.refreshViewport()
							break
						}
						m.selectionItems = nil
						m.selectionIdx = 0
						m.selectionType = "model"
						for _, pair := range models {
							label := fmt.Sprintf("%s (%s)", pair[1], pair[0])
							if pair[0] == m.activeModel {
								label += " (active)"
							}
							m.selectionItems = append(m.selectionItems, selectionItem{
								ID:    pair[0],
								Label: label,
							})
						}
						m.appState = stateSelecting
						m.textarea.Blur()
						break
					}

					// Switch to the named model (keep provider, keep history)
					apiKey, hasKey := m.apiKeys[m.activeProvider]
					if !hasKey || apiKey == "" {
						m.appendOutput(errorStyle.Render(fmt.Sprintf("✗ No API key stored for provider %q", m.activeProvider)))
						m.refreshViewport()
						break
					}
					newProvider, err := llm.NewProvider(m.activeProvider, apiKey, arg)
					if err != nil {
						m.appendOutput(errorStyle.Render("✗ Failed to switch model: " + err.Error()))
					} else {
						m.provider = newProvider
						m.activeModel = arg
						if m.activeSession != nil {
							m.activeSession.Model = arg
							m.saveSession()
						}
						m.appendOutput(lipgloss.NewStyle().Foreground(colorGreen).Render(
							fmt.Sprintf("✓ Switched to model: %s", arg)))
					}
					m.refreshViewport()
					break
				}

				// ── /provider [name] ───────────────────────────────────────
				if strings.HasPrefix(input, "/provider") {
					arg := strings.TrimSpace(strings.TrimPrefix(input, "/provider"))

					if arg == "" {
						providers := []string{"gemini", "deepseek"}
						m.selectionItems = nil
						m.selectionIdx = 0
						m.selectionType = "provider"
						for _, p := range providers {
							label := p
							if p == m.activeProvider {
								label += " (active)"
							}
							m.selectionItems = append(m.selectionItems, selectionItem{
								ID:    p,
								Label: label,
							})
						}
						m.appState = stateSelecting
						m.textarea.Blur()
						break
					}

					arg = strings.ToLower(arg)
					apiKey, hasKey := m.apiKeys[arg]
					if !hasKey || apiKey == "" {
						m.appendOutput(errorStyle.Render(fmt.Sprintf(
							"✗ No API key configured for %q. Set it in ~/.config/aig/config.json or via environment variable.", arg)))
						m.refreshViewport()
						break
					}
					defaultModel := string(config.DefaultModel(config.ProviderName(arg)))
					newProvider, err := llm.NewProvider(arg, apiKey, defaultModel)
					if err != nil {
						m.appendOutput(errorStyle.Render("✗ Failed to switch provider: " + err.Error()))
					} else {
						m.provider = newProvider
						m.activeProvider = arg
						m.activeModel = defaultModel
						if m.activeSession != nil {
							m.activeSession.Provider = arg
							m.activeSession.Model = defaultModel
							m.saveSession()
						}
						m.appendOutput(lipgloss.NewStyle().Foreground(colorGreen).Render(
							fmt.Sprintf("✓ Switched to provider: %s  model: %s", arg, defaultModel)))
					}
					m.refreshViewport()
					break
				}

				// ── /persona [name] ────────────────────────────────────────
				if strings.HasPrefix(input, "/persona") {
					arg := strings.TrimSpace(strings.TrimPrefix(input, "/persona"))

					if arg == "" {
						names := config.PersonaList(m.fileConfig)
						m.selectionItems = nil
						m.selectionIdx = 0
						m.selectionType = "persona"
						for _, name := range names {
							label := name
							if name == m.activePersona {
								label += " (active)"
							}
							m.selectionItems = append(m.selectionItems, selectionItem{
								ID:    name,
								Label: label,
							})
						}
						m.appState = stateSelecting
						m.textarea.Blur()
						break
					}

					prompt, ok := config.ResolvePersona(arg, m.fileConfig)
					if !ok {
						m.appendOutput(errorStyle.Render(fmt.Sprintf("✗ Unknown persona %q. Type /persona to list available.", arg)))
						m.refreshViewport()
						break
					}

					m.defaultSystemPrompt = prompt
					m.activePersona = arg
					// Replace or prepend the leading system message in history
					if len(m.history) > 0 && m.history[0].Role == llm.RoleSystem {
						m.history[0].Content = prompt
					} else {
						m.history = append([]llm.Message{{Role: llm.RoleSystem, Content: prompt}}, m.history...)
					}
					m.saveSession()
					m.appendOutput(lipgloss.NewStyle().Foreground(colorGreen).Render(
						fmt.Sprintf("✓ Persona switched to %q (effective next message)", arg)))
					m.refreshViewport()
					break
				}

				// ── /approve-tools on|off ──────────────────────────────────────
				if strings.HasPrefix(input, "/approve-tools") {
					arg := strings.TrimSpace(strings.TrimPrefix(input, "/approve-tools"))
					switch arg {
					case "on", "1", "yes":
						m.autoApproveCommands = true
						m.appendOutput(lipgloss.NewStyle().Foreground(colorYellow).Bold(true).Render(
							"⚡ Auto-approve enabled — run_command will execute without prompting."))
					case "off", "0", "no":
						m.autoApproveCommands = false
						m.appendOutput(lipgloss.NewStyle().Foreground(colorGreen).Render(
							"✓ Auto-approve disabled — commands require confirmation."))
					default:
						status := "off"
						if m.autoApproveCommands {
							status = "on"
						}
						m.appendOutput(lipgloss.NewStyle().Foreground(colorSubtext).Render(
							fmt.Sprintf("Auto-approve is currently: %s. Use /approve-tools on|off", status)))
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

			case tea.KeyCtrlL:
				// Clear viewport (same as /clear) without wiping history
				m.outputLines = nil
				if len(m.history) > 0 && m.history[0].Role == llm.RoleSystem {
					m.history = m.history[:1]
				} else {
					m.history = nil
				}
				m.titleGenerated = false
				m.saveSession()
				m.refreshViewport()
				return m, nil
			}

			// Viewport scroll while in input mode (explicit bindings so that
			// typing normal characters like f/b/j/k/d/u doesn't scroll).
			if msg.Type == tea.KeyPgUp {
				m.viewport.LineUp(m.viewport.Height)
				return m, nil
			}
			if msg.Type == tea.KeyPgDown {
				m.viewport.LineDown(m.viewport.Height)
				return m, nil
			}
			if msg.Type == tea.KeyCtrlU {
				m.viewport.LineUp(m.viewport.Height / 2)
				return m, nil
			}
			if msg.Type == tea.KeyCtrlD {
				m.viewport.LineDown(m.viewport.Height / 2)
				return m, nil
			}
			if msg.String() == "alt+up" {
				m.viewport.LineUp(1)
				return m, nil
			}
			if msg.String() == "alt+down" {
				m.viewport.LineDown(1)
				return m, nil
			}

			// '?' toggles the inline help overlay
			if msg.String() == "?" && m.textarea.Value() == "" {
				m.showHelp = !m.showHelp
				if m.showHelp {
					m.appendOutput(helpOverlay())
				} else {
					// Remove the last help block (trim until we hit a divider)
					for len(m.outputLines) > 0 && !strings.HasPrefix(m.outputLines[len(m.outputLines)-1], "─") {
						m.outputLines = m.outputLines[:len(m.outputLines)-1]
					}
				}
				m.refreshViewport()
				return m, nil
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

		case stateInputSudoPassword:
			// Check for viewport scroll keys first to allow reading long outputs.
			if msg.Type == tea.KeyPgUp {
				m.viewport.LineUp(m.viewport.Height)
				return m, nil
			}
			if msg.Type == tea.KeyCtrlU {
				m.viewport.LineUp(m.viewport.Height / 2)
				return m, nil
			}
			if msg.Type == tea.KeyPgDown {
				m.viewport.LineDown(m.viewport.Height)
				return m, nil
			}
			if msg.Type == tea.KeyCtrlD {
				m.viewport.LineDown(m.viewport.Height / 2)
				return m, nil
			}
			if msg.String() == "alt+up" {
				m.viewport.LineUp(1)
				return m, nil
			}
			if msg.String() == "alt+down" {
				m.viewport.LineDown(1)
				return m, nil
			}

			switch msg.Type {
			case tea.KeyEsc:
				logger.L.Info("sudo: user declined password input")
				m.appState = stateInput
				m.sudoTextInput.Reset()
				m.textarea.Focus()
				m.appendOutput(confirmStyle.Render("✗ Command cancelled (sudo password required)."))
				m.refreshViewport()
				return m, nil
			case tea.KeyEnter:
				passwd := m.sudoTextInput.Value()
				m.sudoPassword = passwd
				m.sudoTextInput.Reset()
				m.sudoTextInput.Blur()
				
				// Now resume command execution
				cmd := m.pendingCmd
				if m.pendingLang == "" && len(m.pendingToolCalls) > 0 {
					// Tool call path
					call := m.pendingToolCalls[m.pendingToolIdx]
					m.appState = stateExecutingTool
					m.execStart = time.Now()
					m.execTimeout = sandbox.DefaultTimeout
					if v, ok := call.Args["timeout_seconds"].(float64); ok && v > 0 {
						m.execTimeout = time.Duration(v) * time.Second
					}
					m.appendOutput(confirmStyle.Render("⚡ Executing…"))
					m.refreshViewport()
					
					// Patch the command
					callCopy := call
					callCopy.Args["command"] = cmd
					ctx, cancel := context.WithCancel(context.Background())
					m.cancelExec = cancel
					pipeR, pipeW := io.Pipe()
					m.stdinPipe = pipeW
					// Pre-seed sudo password if needed.
					if m.sudoPassword != "" {
						_, _ = fmt.Fprintln(pipeW, m.sudoPassword)
					}
					return m, tea.Batch(func() tea.Msg {
						out, err := ExecuteRunCommand(ctx, callCopy, m.sudoPassword, pipeR)
						cancel()
						_ = pipeR.Close()
						if err != nil {
							return toolCallResultMsg{CallID: callCopy.ID, Name: callCopy.Name, Error: err}
						}
						return toolCallResultMsg{CallID: callCopy.ID, Name: callCopy.Name, Result: out}
					}, m.spinner.Tick)
				} else {
					// Code block sandbox path
					m.appState = stateExecuting
					m.execStart = time.Now()
					m.execTimeout = sandbox.DefaultTimeout
					m.appendOutput(confirmStyle.Render("⚡ Executing…"))
					m.refreshViewport()
					return m, tea.Batch(m.runSandboxCmd(), m.spinner.Tick)
				}
			}
			
			var tiCmd tea.Cmd
			m.sudoTextInput, tiCmd = m.sudoTextInput.Update(msg)
			return m, tiCmd

		case stateConfirmingCmd:
			// Check for viewport scroll keys first to allow reading long outputs.
			if msg.Type == tea.KeyPgUp {
				m.viewport.LineUp(m.viewport.Height)
				return m, nil
			}
			if msg.Type == tea.KeyCtrlU {
				m.viewport.LineUp(m.viewport.Height / 2)
				return m, nil
			}
			if msg.Type == tea.KeyPgDown {
				m.viewport.LineDown(m.viewport.Height)
				return m, nil
			}
			if msg.Type == tea.KeyCtrlD {
				m.viewport.LineDown(m.viewport.Height / 2)
				return m, nil
			}
			if msg.String() == "alt+up" {
				m.viewport.LineUp(1)
				return m, nil
			}
			if msg.String() == "alt+down" {
				m.viewport.LineDown(1)
				return m, nil
			}

			switch msg.Type {
			case tea.KeyEsc:
				logger.L.Info("sandbox: user declined execution", "lang", m.pendingLang)
				// If this was a run_command tool call, return an error result
				// so the batch loop can continue with the next tool.
				if m.pendingLang == "" && len(m.pendingToolCalls) > 0 {
					call := m.pendingToolCalls[m.pendingToolIdx]
				m.appState = stateInput
					m.cmdTextarea.Blur()
					m.textarea.Focus()
					m.appendOutput(confirmStyle.Render("✗ Command skipped."))
					m.refreshViewport()
					return m, func() tea.Msg {
						return toolCallResultMsg{CallID: call.ID, Name: call.Name,
							Result: "Command skipped by user."}
					}
				}
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
				// Tool-call run_command path (pendingLang == ""):
				// run the command and feed the output back as a toolCallResultMsg.
				if m.pendingLang == "" && len(m.pendingToolCalls) > 0 {
					call := m.pendingToolCalls[m.pendingToolIdx]
					logger.L.Info("run_command tool: user approved", "command", cmd)
					if m.needsSudo(cmd) && m.sudoPassword == "" {
						m.appState = stateInputSudoPassword
						m.pendingCmd = cmd
						m.sudoTextInput.Focus()
						m.cmdTextarea.Blur()
						m.appendOutput(confirmStyle.Render("🔒 Sudo password required to execute command."))
						m.refreshViewport()
						return m, nil
					}
					m.appState = stateExecutingTool
					m.execStart = time.Now()
					m.execTimeout = sandbox.DefaultTimeout
					if v, ok := call.Args["timeout_seconds"].(float64); ok && v > 0 {
						m.execTimeout = time.Duration(v) * time.Second
					}
					m.cmdTextarea.Blur()
					m.appendOutput(confirmStyle.Render("⚡ Executing…"))
					m.refreshViewport()
					// Patch the command in case the user edited it.
					callCopy := call
					callCopy.Args["command"] = cmd
					ctx, cancel := context.WithCancel(context.Background())
					m.cancelExec = cancel
					pipeR, pipeW := io.Pipe()
					m.stdinPipe = pipeW
					if m.sudoPassword != "" {
						_, _ = fmt.Fprintln(pipeW, m.sudoPassword)
					}
					return m, tea.Batch(func() tea.Msg {
						out, err := ExecuteRunCommand(ctx, callCopy, m.sudoPassword, pipeR)
						cancel()
						_ = pipeR.Close()
						if err != nil {
							return toolCallResultMsg{CallID: callCopy.ID, Name: callCopy.Name, Error: err}
						}
						return toolCallResultMsg{CallID: callCopy.ID, Name: callCopy.Name, Result: out}
					}, m.spinner.Tick)
				}
				// Code-fence sandbox path.
				logger.L.Info("sandbox: user approved execution",
					"command", cmd,
				)
				m.pendingCmd = cmd
				if m.needsSudo(cmd) && m.sudoPassword == "" {
					m.appState = stateInputSudoPassword
					m.sudoTextInput.Focus()
					m.cmdTextarea.Blur()
					m.appendOutput(confirmStyle.Render("🔒 Sudo password required to execute command."))
					m.refreshViewport()
					return m, nil
				}
				m.appState = stateExecuting
				m.execStart = time.Now()
				m.execTimeout = sandbox.DefaultTimeout
				m.cmdTextarea.Blur()
				m.appendOutput(confirmStyle.Render("⚡ Executing…"))
				m.refreshViewport()
				return m, tea.Batch(m.runSandboxCmd(), m.spinner.Tick)
			case tea.KeyCtrlC:
				return m, tea.Quit
			}

			var cmd tea.Cmd
			m.cmdTextarea, cmd = m.cmdTextarea.Update(msg)
			return m, cmd

		case stateExecuting:
			// Ctrl+I opens the runtime stdin prompt.
			if msg.Type == tea.KeyCtrlI {
				if m.stdinPipe != nil {
					m.appState = stateRuntimeInput
					m.runtimeInput.Reset()
					m.runtimeInput.Focus()
					return m, nil
				}
			}
			// Ctrl+C kills the child process without quitting aig.
			if msg.Type == tea.KeyCtrlC {
				m.killExec()
				logger.L.Warn("sandbox: execution killed by user")
				m.appState = stateInput
				m.appendOutput(errorStyle.Render("✗ Command killed."))
				m.refreshViewport()
				return m, nil
			}

		case stateExecutingTool:
			// Ctrl+I opens the runtime stdin prompt when a pipe is active.
			if msg.Type == tea.KeyCtrlI {
				if m.stdinPipe != nil {
					m.appState = stateRuntimeInput
					m.runtimeInput.Reset()
					m.runtimeInput.Focus()
					return m, nil
				}
			}
			// Ctrl+C aborts the tool and sends an error result to unblock the chain.
			if msg.Type == tea.KeyCtrlC {
				m.killExec()
				logger.L.Warn("tool: execution killed by user")
				m.appendOutput(errorStyle.Render("✗ Tool aborted."))
				m.refreshViewport()
				// Send an error result so the pending-tool chain moves on (or the
				// LLM gets an error response instead of hanging forever).
				if len(m.pendingToolCalls) > 0 && m.pendingToolIdx < len(m.pendingToolCalls) {
					call := m.pendingToolCalls[m.pendingToolIdx]
					m.pendingToolIdx++
					if m.pendingToolIdx < len(m.pendingToolCalls) {
						// More tools remain — send error result and continue chain.
						return m, func() tea.Msg {
							return toolCallResultMsg{CallID: call.ID, Name: call.Name, Error: fmt.Errorf("aborted by user")}
						}
					}
					// Last tool — send error result and reset to input.
					m.pendingToolCalls = nil
					m.pendingToolIdx = 0
					m.appState = stateInput
					m.textarea.Focus()
					return m, func() tea.Msg {
						return toolCallResultMsg{CallID: call.ID, Name: call.Name, Error: fmt.Errorf("aborted by user")}
					}
				}
				m.pendingToolCalls = nil
				m.pendingToolIdx = 0
				m.appState = stateInput
				m.textarea.Focus()
				return m, nil
			}

		case stateRuntimeInput:
			switch msg.Type {
			case tea.KeyEsc:
				// Go back to executing state without sending anything.
				m.runtimeInput.Blur()
				m.appState = stateExecuting
				if len(m.pendingToolCalls) > 0 {
					m.appState = stateExecutingTool
				}
				return m, nil
			case tea.KeyEnter:
				line := m.runtimeInput.Value()
				m.runtimeInput.Reset()
				m.runtimeInput.Blur()
				m.appState = stateExecuting
				if len(m.pendingToolCalls) > 0 {
					m.appState = stateExecutingTool
				}
				if m.stdinPipe != nil {
					m.appendOutput(lipgloss.NewStyle().Foreground(colorSubtext).Render("> " + line))
					m.refreshViewport()
					pipe := m.stdinPipe
					return m, func() tea.Msg {
						_, _ = fmt.Fprintln(pipe, line)
						return nil
					}
				}
				return m, nil
			}
			var riCmd tea.Cmd
			m.runtimeInput, riCmd = m.runtimeInput.Update(msg)
			return m, riCmd

		case stateConfirmingWrite:
			// Check for viewport scroll keys first to allow reading long outputs.
			if msg.Type == tea.KeyPgUp {
				m.viewport.LineUp(m.viewport.Height)
				return m, nil
			}
			if msg.Type == tea.KeyCtrlU {
				m.viewport.LineUp(m.viewport.Height / 2)
				return m, nil
			}
			if msg.Type == tea.KeyPgDown {
				m.viewport.LineDown(m.viewport.Height)
				return m, nil
			}
			if msg.Type == tea.KeyCtrlD {
				m.viewport.LineDown(m.viewport.Height / 2)
				return m, nil
			}
			if msg.String() == "alt+up" {
				m.viewport.LineUp(1)
				return m, nil
			}
			if msg.String() == "alt+down" {
				m.viewport.LineDown(1)
				return m, nil
			}

			switch msg.Type {
			case tea.KeyEnter:
				// 'y' + Enter or just Enter when 'y' is pre-filled confirms the write.
				cmd := strings.ToLower(strings.TrimSpace(m.cmdTextarea.Value()))
				if cmd != "y" && cmd != "yes" {
					m.appendOutput(confirmStyle.Render("✗ Write cancelled."))
					m.pendingWrite = nil
					m.pendingWriteCallID = ""
					m.appState = stateInput
					m.cmdTextarea.Blur()
					m.textarea.Focus()
					m.refreshViewport()
					return m, nil
				}
				pw := m.pendingWrite
				callID := m.pendingWriteCallID
				m.pendingWrite = nil
				m.pendingWriteCallID = ""
				m.appState = stateExecutingTool
				m.execStart = time.Now()
				m.execTimeout = 5 * time.Second
				m.cmdTextarea.Blur()
				m.textarea.Focus()
				m.appendOutput(confirmStyle.Render("✍  Writing file…"))
				m.refreshViewport()
				return m, tea.Batch(func() tea.Msg {
					if err := CommitWrite(*pw); err != nil {
						return toolCallResultMsg{CallID: callID, Name: "write_file", Error: err}
					}
					return toolCallResultMsg{CallID: callID, Name: "write_file", Result: fmt.Sprintf("Successfully wrote %s", pw.Path)}
				}, m.spinner.Tick)
			case tea.KeyEsc:
				m.pendingWrite = nil
				m.pendingWriteCallID = ""
				m.appState = stateInput
				m.cmdTextarea.Blur()
				m.textarea.Focus()
				m.appendOutput(confirmStyle.Render("✗ Write cancelled."))
				m.refreshViewport()
				return m, nil
			case tea.KeyCtrlC:
				return m, tea.Quit
			}

			var cmd tea.Cmd
			m.cmdTextarea, cmd = m.cmdTextarea.Update(msg)
			return m, cmd

		case stateSelecting:
			switch msg.Type {
			case tea.KeyUp, tea.KeyPgUp:
				m.selectionIdx--
				if m.selectionIdx < 0 {
					m.selectionIdx = len(m.selectionItems) - 1
				}
				return m, nil
			case tea.KeyDown, tea.KeyPgDown:
				m.selectionIdx++
				if m.selectionIdx >= len(m.selectionItems) {
					m.selectionIdx = 0
				}
				return m, nil
			case tea.KeyEsc:
				m.appState = stateInput
				m.textarea.Focus()
				return m, nil
			case tea.KeyDelete:
				if m.selectionType == "history" && len(m.selectionItems) > 0 {
					selected := m.selectionItems[m.selectionIdx]
					if m.activeSession != nil && selected.ID == m.activeSession.ID {
						m.appendOutput(errorStyle.Render("✗ Cannot delete the active session."))
						m.refreshViewport()
						return m, nil
					}
					if err := session.Delete(selected.ID); err != nil {
						m.appendOutput(errorStyle.Render("✗ Failed to delete session: " + err.Error()))
					} else {
						m.appendOutput(lipgloss.NewStyle().Foreground(colorGreen).Render("🗑 Deleted session: " + selected.ID))
						m.selectionItems = append(m.selectionItems[:m.selectionIdx], m.selectionItems[m.selectionIdx+1:]...)
						if m.selectionIdx >= len(m.selectionItems) {
							m.selectionIdx = len(m.selectionItems) - 1
						}
						if m.selectionIdx < 0 {
							m.selectionIdx = 0
						}
						if len(m.selectionItems) == 0 {
							m.appState = stateInput
							m.textarea.Focus()
						}
					}
					m.refreshViewport()
					return m, nil
				}
			case tea.KeyEnter:
				if len(m.selectionItems) == 0 {
					m.appState = stateInput
					m.textarea.Focus()
					return m, nil
				}
				selected := m.selectionItems[m.selectionIdx]
				m.appState = stateInput
				m.textarea.Focus()
				return m.handleSelection(selected.ID)
			}
			return m, nil
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

		// Add assistant turn to history. ReasoningContent comes from the
		// streamDoneMsg for non-tool turns (DeepSeek thinking models).
		m.history = append(m.history, llm.Message{
			Role:             llm.RoleAssistant,
			Content:          raw,
			ReasoningContent: msg.ReasoningContent,
		})
		m.reasoningBuffer = ""
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

		// Generate a title for fresh sessions
		var titleCmd tea.Cmd
		if m.activeSession != nil && m.activeSession.Name == "Untitled" && !m.titleGenerated {
			hasUser := false
			hasAssistant := false
			for _, msg := range m.history {
				if msg.Role == llm.RoleUser {
					hasUser = true
				} else if msg.Role == llm.RoleAssistant {
					hasAssistant = true
				}
			}
			if hasUser && hasAssistant {
				m.titleGenerated = true
				titleCmd = m.generateSessionTitleCmd()
			}
		}

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
			return m, titleCmd
		}

		m.resetToInput()
		return m, titleCmd

	// ── Stream error ───────────────────────────────────────────────────────
	case streamErrMsg:
		m.streamBuffer = ""
		m.reasoningBuffer = ""
		m.pendingToolCalls = nil
		m.pendingToolIdx = 0
		m.lastErr = msg.err.Error()
		logger.L.Error("stream error received", "error", msg.err)
		m.appendOutput(errorStyle.Render("✗ Error: " + m.lastErr))
		m.resetToInput()
		return m, nil

	// ── Sandbox execution result ───────────────────────────────────────────
	case execResultMsg:
		if msg.err != nil {
			logger.L.Error("sandbox: execution error", "error", msg.err)
			m.appendOutput(errorStyle.Render("✗ Execution error: " + msg.err.Error()))
			errStr := strings.ToLower(msg.err.Error())
			if strings.Contains(errStr, "incorrect password") || strings.Contains(errStr, "permission denied") {
				m.sudoPassword = ""
				m.appendOutput(errorStyle.Render("🔒 Incorrect password or permission denied. Sudo password cache cleared."))
			}
		} else {
			logger.L.Info("sandbox: execution completed",
				"lang", m.pendingLang,
				"output_len", len(msg.output),
			)
			label := lipgloss.NewStyle().Foreground(colorGreen).Bold(true).Render("$ " + m.pendingLang)
			m.appendOutput(label)
			m.appendOutput(execResultStyle.Render(msg.output))
			
			outStr := strings.ToLower(msg.output)
			if strings.Contains(outStr, "incorrect password") || strings.Contains(outStr, "try again") {
				m.sudoPassword = ""
				m.appendOutput(errorStyle.Render("🔒 Sudo authentication failed. Sudo password cache cleared."))
			}

			// Feed output back into conversation as a system message.
			m.history = append(m.history, llm.Message{
				Role:    llm.RoleSystem,
				Content: fmt.Sprintf("Command executed:\n```%s\n%s\n```\n\nOutput:\n%s", m.pendingLang, m.pendingCmd, msg.output),
			})
			m.saveSession()
		}
		m.pendingCmd = ""
		m.pendingLang = ""
		// Close and discard the stdin pipe now that the command has finished.
		if m.stdinPipe != nil {
			_ = m.stdinPipe.Close()
			m.stdinPipe = nil
		}
		m.resetToInput()
		return m, nil

	// ── Tool batch received from LLM ───────────────────────────────────────
	case toolCallBatchMsg:
		// Clear the "…" streaming placeholder.
		if len(m.outputLines) > 0 && strings.Contains(m.outputLines[len(m.outputLines)-1], "…") {
			m.outputLines = m.outputLines[:len(m.outputLines)-1]
		}

		calls := msg.Calls
		if len(calls) == 0 {
			m.resetToInput()
			return m, nil
		}

		// Record the full assistant turn (all tool calls in one message).
		// ReasoningContent comes atomically from the toolCallBatchMsg itself.
		assistantTCs := make([]llm.ToolCall, len(calls))
		copy(assistantTCs, calls)
		m.history = append(m.history, llm.Message{
			Role:             llm.RoleAssistant,
			ToolCalls:        assistantTCs,
			ReasoningContent: msg.ReasoningContent,
		})
		m.reasoningBuffer = ""

		// Store the batch and start processing from index 0.
		m.pendingToolCalls = calls
		m.pendingToolIdx = 0
		return m, m.processNextToolCall()

	// ── Legacy single-tool path (kept for safety) ──────────────────────────
	case toolCallMsg:
		return m, func() tea.Msg {
			return toolCallBatchMsg{Calls: []llm.ToolCall{msg.Call}}
		}

	case toolCallResultMsg:
		resContent := msg.Result
		if msg.Error != nil {
			resContent = fmt.Sprintf("Error: %s", msg.Error.Error())
			m.appendOutput(errorStyle.Render(fmt.Sprintf("✗ Tool %s failed", msg.Name)))
			if msg.Name == "run_command" {
				m.appendOutput(errorStyle.Render("✗ " + msg.Error.Error()))
				errStr := strings.ToLower(msg.Error.Error())
				if strings.Contains(errStr, "incorrect password") || strings.Contains(errStr, "permission denied") {
					m.sudoPassword = ""
					m.appendOutput(errorStyle.Render("🔒 Incorrect password or permission denied. Sudo password cache cleared."))
				}
			}
		} else {
			m.appendOutput(lipgloss.NewStyle().Foreground(colorGreen).Render(fmt.Sprintf("✓ Tool %s done", msg.Name)))
			if msg.Name == "run_command" {
				m.appendOutput(execResultStyle.Render(msg.Result))
				outStr := strings.ToLower(msg.Result)
				if strings.Contains(outStr, "incorrect password") || strings.Contains(outStr, "try again") {
					m.sudoPassword = ""
					m.appendOutput(errorStyle.Render("🔒 Sudo authentication failed. Sudo password cache cleared."))
				}
			}
		}
		// Close the stdin pipe for this tool (if it had one).
		if m.stdinPipe != nil {
			_ = m.stdinPipe.Close()
			m.stdinPipe = nil
		}
		m.refreshViewport()

		m.history = append(m.history, llm.Message{
			Role:       llm.RoleTool,
			Content:    resContent,
			ToolCallID: msg.CallID,
			ToolName:   msg.Name,
		})
		m.saveSession()

		// Advance to the next tool in the batch, or re-stream if all done.
		m.pendingToolIdx++
		if m.pendingToolIdx < len(m.pendingToolCalls) {
			return m, m.processNextToolCall()
		}
		// All tools in this batch are done — give the LLM the full results.
		m.pendingToolCalls = nil
		m.pendingToolIdx = 0
		return m, tea.Batch(m.startStreaming()...)

	case sessionTitleMsg:
		if string(msg) != "" && m.activeSession != nil {
			m.activeSession.Name = string(msg)
			m.saveSession()
			m.refreshViewport()
		}
		return m, nil
	}

	// ── Delegate to sub-components ─────────────────────────────────────────
	if m.appState == stateInput {
		var taCmd tea.Cmd
		m.textarea, taCmd = m.textarea.Update(msg)
		cmds = append(cmds, taCmd)
	} else if m.appState == stateInputSudoPassword {
		var tiCmd tea.Cmd
		m.sudoTextInput, tiCmd = m.sudoTextInput.Update(msg)
		cmds = append(cmds, tiCmd)
	}
	// Only delegate to the viewport when the textarea is NOT focused.
	// This prevents the viewport's built-in key bindings (arrows, pgup/pgdn)
	// from stealing keystrokes meant for the text input, which causes
	// scroll position jumps on Windows when the user types while scrolled up.
	_, isKeyMsg := msg.(tea.KeyMsg)
	if !isKeyMsg || (m.appState != stateInput && m.appState != stateInputSudoPassword) {
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
		var label string
		if m.pendingLang != "" {
			label = fmt.Sprintf("Review %s block (Enter to run, Esc to cancel, Shift+Enter for newline):", m.pendingLang)
		} else {
			label = "Review command (Enter to run, Esc to cancel, Shift+Enter for newline):"
		}
		b.WriteString(inputActiveStyle.Render(
			lipgloss.JoinVertical(lipgloss.Left,
				confirmStyle.Render(label),
				m.cmdTextarea.View(),
			),
		))
	case stateConfirmingWrite:
		path := ""
		if m.pendingWrite != nil {
			path = m.pendingWrite.Path
		}
		b.WriteString(inputActiveStyle.Render(
			lipgloss.JoinVertical(lipgloss.Left,
				confirmStyle.Render(fmt.Sprintf("💾 Write to %s? Type 'y' and Enter to confirm, Esc to cancel:", path)),
				m.cmdTextarea.View(),
			),
		))
	case stateInputSudoPassword:
		b.WriteString(inputActiveStyle.Render(
			lipgloss.JoinVertical(lipgloss.Left,
				confirmStyle.Render("🔒 This command requires sudo. Enter password (Esc to cancel):"),
				m.sudoTextInput.View(),
			),
		))
	case stateExecuting:
		icon, desc := m.getExecutionIndicator(m.pendingCmd)
		elapsed := time.Since(m.execStart)
		cmdMsg := desc
		if m.pendingCmd != "" {
			trimmedCmd := m.pendingCmd
			if len(trimmedCmd) > 50 {
				trimmedCmd = trimmedCmd[:47] + "..."
			}
			cmdMsg = fmt.Sprintf("%s: %s", desc, trimmedCmd)
		}
		var execHints []string
		execHints = append(execHints, fmt.Sprintf("%s %s %s (elapsed: %.1fs)", m.spinner.View(), icon, cmdMsg, elapsed.Seconds()))
		if m.stdinPipe != nil {
			execHints = append(execHints, lipgloss.NewStyle().Foreground(colorSubtext).Render(
				"Ctrl+I to send stdin · Ctrl+C to abort"))
		} else {
			execHints = append(execHints, lipgloss.NewStyle().Foreground(colorSubtext).Render(
				"Ctrl+C to abort"))
		}
		b.WriteString(inputStyle.Render(
			lipgloss.JoinVertical(lipgloss.Left, execHints...),
		))
	case stateExecutingTool:
		elapsed := time.Since(m.execStart)
		toolName := "tool"
		command := ""
		if len(m.pendingToolCalls) > 0 && m.pendingToolIdx < len(m.pendingToolCalls) {
			call := m.pendingToolCalls[m.pendingToolIdx]
			toolName = call.Name
			if toolName == "run_command" {
				command, _ = call.Args["command"].(string)
			}
		}
		var icon, desc string
		if toolName == "run_command" && command != "" {
			icon, desc = m.getExecutionIndicator(command)
			if len(command) > 50 {
				command = command[:47] + "..."
			}
			desc = fmt.Sprintf("%s: %s", desc, command)
		} else {
			icon = "⚙"
			desc = fmt.Sprintf("executing tool: %s…", toolName)
		}
		b.WriteString(inputStyle.Render(
			lipgloss.JoinVertical(lipgloss.Left,
				fmt.Sprintf("%s %s %s (elapsed: %.1fs)", m.spinner.View(), icon, desc, elapsed.Seconds()),
				lipgloss.NewStyle().Foreground(colorSubtext).Render(
					func() string {
						if m.stdinPipe != nil {
							return "Ctrl+I to send stdin \u00b7 Ctrl+C to abort"
						}
						return "Ctrl+C to abort"
					}()),
			),
		))
	case stateExecutingTools:
		remaining := len(m.pendingToolCalls) - m.pendingToolIdx
		b.WriteString(inputStyle.Render(
			m.spinner.View() + " " + lipgloss.NewStyle().Foreground(colorSubtext).Render(
				fmt.Sprintf("executing tools… (%d remaining)", remaining)),
		))
	case stateSelecting:
		var items []string
		title := fmt.Sprintf("Select %s (Up/Down arrows to navigate, Enter to select, Esc to cancel):", m.selectionType)
		items = append(items, confirmStyle.Render(title))

		start := 0
		end := len(m.selectionItems)
		if len(m.selectionItems) > 4 {
			start = m.selectionIdx - 1
			if start < 0 {
				start = 0
			}
			end = start + 4
			if end > len(m.selectionItems) {
				end = len(m.selectionItems)
				start = end - 4
			}
		}

		for i := start; i < end; i++ {
			item := m.selectionItems[i]
			marker := "  "
			style := lipgloss.NewStyle().Foreground(colorText)
			if i == m.selectionIdx {
				marker = "▶ "
				style = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
			}
			items = append(items, style.Render(fmt.Sprintf("%s%s", marker, item.Label)))
		}
		b.WriteString(inputActiveStyle.Render(
			lipgloss.JoinVertical(lipgloss.Left, items...),
		))
	case stateRuntimeInput:
		hint := lipgloss.NewStyle().Foreground(colorSubtext).Render(
			"\u2328\ufe0f  Type a line to send to the running process (Enter to send \u00b7 Esc to return)")
		b.WriteString(inputActiveStyle.Render(
			lipgloss.JoinVertical(lipgloss.Left,
				hint,
				m.runtimeInput.View(),
			),
		))
	}

	return b.String()
}

// handleSelection processes the interactive list selection.
func (m Model) handleSelection(id string) (Model, tea.Cmd) {
	switch m.selectionType {
	case "model":
		m.activeModel = id
		if m.activeSession != nil {
			m.activeSession.Model = id
			m.saveSession()
		}
		apiKey, hasKey := m.apiKeys[m.activeProvider]
		if !hasKey || apiKey == "" {
			m.appendOutput(errorStyle.Render(fmt.Sprintf("✗ No API key stored for provider %q", m.activeProvider)))
			m.refreshViewport()
			return m, nil
		}
		newProvider, err := llm.NewProvider(m.activeProvider, apiKey, id)
		if err != nil {
			m.appendOutput(errorStyle.Render("✗ Failed to switch model: " + err.Error()))
		} else {
			m.provider = newProvider
			m.appendOutput(lipgloss.NewStyle().Foreground(colorGreen).Render(
				fmt.Sprintf("✓ Model switched to: %s", id)))
		}
		m.refreshViewport()
		return m, nil

	case "history":
		s, err := session.Load(id)
		if err != nil {
			m.appendOutput(errorStyle.Render("✗ Failed to load session: " + err.Error()))
			m.refreshViewport()
			return m, nil
		}
		m.activeSession = s
		if s.Provider != "" {
			m.activeProvider = s.Provider
		}
		if s.Model != "" {
			m.activeModel = s.Model
		}
		apiKey, hasKey := m.apiKeys[m.activeProvider]
		if hasKey && apiKey != "" {
			newProvider, err := llm.NewProvider(m.activeProvider, apiKey, m.activeModel)
			if err == nil {
				m.provider = newProvider
			}
		}
		m.loadHistory(s.History, false)
		m.titleGenerated = false
		m.appendOutput(lipgloss.NewStyle().Foreground(colorGreen).Render("Loaded session: " + s.ID))
		return m, nil

	case "provider":
		apiKey, hasKey := m.apiKeys[id]
		if !hasKey || apiKey == "" {
			m.appendOutput(errorStyle.Render(fmt.Sprintf(
				"✗ No API key configured for %q. Set it in ~/.config/aig/config.json or via environment variable.", id)))
			m.refreshViewport()
			return m, nil
		}
		defaultModel := string(config.DefaultModel(config.ProviderName(id)))
		newProvider, err := llm.NewProvider(id, apiKey, defaultModel)
		if err != nil {
			m.appendOutput(errorStyle.Render("✗ Failed to switch provider: " + err.Error()))
		} else {
			m.provider = newProvider
			m.activeProvider = id
			m.activeModel = defaultModel
			if m.activeSession != nil {
				m.activeSession.Provider = id
				m.activeSession.Model = defaultModel
				m.saveSession()
			}
			m.appendOutput(lipgloss.NewStyle().Foreground(colorGreen).Render(
				fmt.Sprintf("✓ Switched to provider: %s  model: %s", id, defaultModel)))
		}
		m.refreshViewport()
		return m, nil

	case "persona":
		prompt, ok := config.ResolvePersona(id, m.fileConfig)
		if !ok {
			m.appendOutput(errorStyle.Render(fmt.Sprintf("✗ Unknown persona %q.", id)))
			m.refreshViewport()
			return m, nil
		}
		m.defaultSystemPrompt = prompt
		m.activePersona = id
		if len(m.history) > 0 && m.history[0].Role == llm.RoleSystem {
			m.history[0].Content = prompt
		} else {
			m.history = append([]llm.Message{{Role: llm.RoleSystem, Content: prompt}}, m.history...)
		}
		m.saveSession()
		m.appendOutput(lipgloss.NewStyle().Foreground(colorGreen).Render(
			fmt.Sprintf("✓ Persona switched to %q (effective next message)", id)))
		m.refreshViewport()
		return m, nil
	}
	return m, nil
}

// generateSessionTitleCmd calls the LLM in the background to generate a summary title.
func (m Model) generateSessionTitleCmd() tea.Cmd {
	var userQuery string
	for _, msg := range m.history {
		if msg.Role == llm.RoleUser {
			userQuery = msg.Content
			break
		}
	}
	if userQuery == "" {
		return nil
	}

	provider := m.provider
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		prompt := fmt.Sprintf("Summarize the following topic/query into a short, concise conversation title of 3-5 words. Return ONLY the title, with no quotation marks, no markdown formatting, and no extra text:\n\n%s", userQuery)

		tokenChan := make(chan string, 100)
		go func() {
			_, _, err := provider.StreamChat(ctx, []llm.Message{
				{Role: llm.RoleUser, Content: prompt},
			}, nil, tokenChan)
			if err != nil {
				logger.L.Error("title generation background stream failed", "error", err)
			}
		}()

		var sb strings.Builder
		for token := range tokenChan {
			sb.WriteString(token)
		}

		title := strings.TrimSpace(sb.String())
		title = strings.Trim(title, "\"`'*")
		logger.L.Info("generated session title", "title", title)
		if title == "" {
			return sessionTitleMsg("")
		}
		if len(title) > 60 {
			title = title[:57] + "..."
		}
		return sessionTitleMsg(title)
	}
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// loadHistory updates the model's history and fully renders it to the viewport display.
// When keepScroll is true, the viewport's current scroll offset is preserved
// (used during window resize). When false, the viewport scrolls to the bottom
// (used when loading a new session).
func (m *Model) loadHistory(history []llm.Message, keepScroll bool) {
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
			if msg.Content != "" {
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
			// If there are tool calls in this assistant turn, render the completed ones.
			for _, call := range msg.ToolCalls {
				// Look ahead in history to see if there is a corresponding tool result.
				hasResult := false
				for _, searchMsg := range history {
					if searchMsg.Role == llm.RoleTool && searchMsg.ToolCallID == call.ID {
						hasResult = true
						break
					}
				}
				if hasResult {
					// Render the tool call start line.
					if call.Name == "run_command" {
						command, _ := call.Args["command"].(string)
						m.outputLines = append(m.outputLines, lipgloss.NewStyle().Foreground(colorYellow).Render(
							fmt.Sprintf("⚙ Tool: run_command: %s", command)))
					} else if call.Name == "write_file" || call.Name == "edit_file" {
						path, _ := call.Args["path"].(string)
						icon := "💾"
						if call.Name == "edit_file" {
							icon = "✏️ "
						}
						m.outputLines = append(m.outputLines, lipgloss.NewStyle().Foreground(colorYellow).Bold(true).Render(
							fmt.Sprintf("%s %s: %s", icon, call.Name, path)))
					} else {
						m.outputLines = append(m.outputLines, lipgloss.NewStyle().Foreground(colorYellow).Render(
							fmt.Sprintf("⚙ Tool: %s", call.Name)))
					}
				}
			}
		} else if msg.Role == llm.RoleTool {
			if strings.HasPrefix(msg.Content, "Error:") {
				m.outputLines = append(m.outputLines, errorStyle.Render(fmt.Sprintf("✗ Tool %s failed", msg.ToolName)))
				if msg.ToolName == "run_command" {
					m.outputLines = append(m.outputLines, errorStyle.Render(msg.Content))
				}
			} else {
				m.outputLines = append(m.outputLines, lipgloss.NewStyle().Foreground(colorGreen).Render(fmt.Sprintf("✓ Tool %s done", msg.ToolName)))
				if msg.ToolName == "run_command" {
					m.outputLines = append(m.outputLines, execResultStyle.Render(msg.Content))
				}
			}
			// Add divider if next is a user message
			nextIsUser := false
			if i+1 < len(m.history) && m.history[i+1].Role == llm.RoleUser {
				nextIsUser = true
			}
			if nextIsUser {
				m.outputLines = append(m.outputLines, divider(m.width))
			}
		}
	}

	// Restore active/in-flight states which are not yet in m.history
	switch m.appState {
	case stateStreaming:
		if m.streamBuffer == "" {
			m.outputLines = append(m.outputLines, assistantLabelStyle.Render("aig")+" "+spinnerStyle.Render("…"))
			m.refreshViewport()
		} else {
			m.refreshViewportStreaming()
		}
		return

	case stateConfirmingCmd:
		if m.pendingLang != "" {
			block := codeBlockStyle.Render("```" + m.pendingLang + "\n" + m.pendingCmd + "\n```")
			m.outputLines = append(m.outputLines, block)
		} else {
			m.outputLines = append(m.outputLines, confirmStyle.Render(fmt.Sprintf("🔧 run_command: %s", m.pendingCmd)))
			m.outputLines = append(m.outputLines, lipgloss.NewStyle().Foreground(colorSubtext).Render(
				"Enter to execute · Esc to cancel · edit command if needed"))
		}

	case stateConfirmingWrite:
		if m.pendingWrite != nil {
			icon := "💾"
			toolName := "write_file"
			if len(m.pendingToolCalls) > 0 && m.pendingToolIdx < len(m.pendingToolCalls) {
				toolName = m.pendingToolCalls[m.pendingToolIdx].Name
				if toolName == "edit_file" {
					icon = "✏️ "
				}
			}
			m.outputLines = append(m.outputLines, lipgloss.NewStyle().Foreground(colorYellow).Bold(true).Render(
				fmt.Sprintf("%s %s: %s", icon, toolName, m.pendingWrite.Path)))

			if m.pendingWrite.Diff != "" && m.pendingWrite.Diff != "(no changes)" {
				m.outputLines = append(m.outputLines, lipgloss.NewStyle().Foreground(colorSubtext).Render("Diff preview:"))
				m.outputLines = append(m.outputLines, codeBlockStyle.Render(m.pendingWrite.Diff))
			} else {
				preview := m.pendingWrite.Content
				if len(preview) > 400 {
					preview = preview[:400] + "\n...(truncated)"
				}
				m.outputLines = append(m.outputLines, lipgloss.NewStyle().Foreground(colorSubtext).Render(
					fmt.Sprintf("Content preview (%d bytes):", len(m.pendingWrite.Content))))
				m.outputLines = append(m.outputLines, codeBlockStyle.Render(preview))
			}
		}

	case stateExecuting:
		if m.pendingLang != "" {
			block := codeBlockStyle.Render("```" + m.pendingLang + "\n" + m.pendingCmd + "\n```")
			m.outputLines = append(m.outputLines, block)
		} else {
			m.outputLines = append(m.outputLines, confirmStyle.Render(fmt.Sprintf("🔧 run_command: %s", m.pendingCmd)))
		}

	case stateExecutingTool, stateExecutingTools:
		if len(m.pendingToolCalls) > 0 && m.pendingToolIdx < len(m.pendingToolCalls) {
			call := m.pendingToolCalls[m.pendingToolIdx]
			if call.Name == "run_command" {
				command, _ := call.Args["command"].(string)
				if m.autoApproveCommands {
					m.outputLines = append(m.outputLines, lipgloss.NewStyle().Foreground(colorYellow).Render(
						fmt.Sprintf("⚡ run_command (auto): %s", command)))
				} else {
					m.outputLines = append(m.outputLines, confirmStyle.Render(
						fmt.Sprintf("🔧 run_command: %s", command)))
				}
			} else if call.Name == "write_file" || call.Name == "edit_file" {
				path, _ := call.Args["path"].(string)
				icon := "💾"
				if call.Name == "edit_file" {
					icon = "✏️ "
				}
				m.outputLines = append(m.outputLines, lipgloss.NewStyle().Foreground(colorYellow).Bold(true).Render(
					fmt.Sprintf("%s %s: %s", icon, call.Name, path)))
			} else {
				m.outputLines = append(m.outputLines, lipgloss.NewStyle().Foreground(colorYellow).Render(
					fmt.Sprintf("⚙ Tool: %s", call.Name)))
			}
		}
	}

	if keepScroll {
		m.refreshViewportKeepPosition()
	} else {
		m.refreshViewport()
	}
}

func (m *Model) needsSudo(cmd string) bool {
	matched, _ := regexp.MatchString(`\bsudo\b`, cmd)
	return matched
}

func (m *Model) getExecutionIndicator(cmd string) (string, string) {
	cmdLower := strings.ToLower(cmd)
	isSudo := m.needsSudo(cmd)
	isDownload := false
	downloadKeywords := []string{
		"curl", "wget", "git clone", "apt-get install", "apt install",
		"npm install", "npm i ", "pip install", "go get", "docker pull",
		"yarn add", "wget ", "download",
	}
	for _, kw := range downloadKeywords {
		if strings.Contains(cmdLower, kw) {
			isDownload = true
			break
		}
	}
	if isDownload {
		if isSudo {
			return "📥", "downloading & installing with sudo…"
		}
		return "📥", "downloading…"
	}
	if isSudo {
		return "🔒", "running with sudo…"
	}
	return "⚙", "running command…"
}

// recalculateDimensions calculates dynamic layout heights and widths to prevent overflow or underflow.
func (m *Model) recalculateDimensions() {
	if m.width == 0 || m.height == 0 {
		return
	}
	m.viewport.Width = m.width
	m.textarea.SetWidth(m.width - 4)
	m.cmdTextarea.SetWidth(m.width - 4)
	m.sudoTextInput.Width = m.width - 4
	m.runtimeInput.Width = m.width - 4

	overhead := 7

	inputHeight := 0
	switch m.appState {
	case stateInput:
		inputHeight = 5
	case stateStreaming:
		inputHeight = 3
	case stateConfirmingCmd:
		inputHeight = 8
	case stateConfirmingWrite:
		inputHeight = 8
	case stateInputSudoPassword:
		inputHeight = 4
	case stateExecuting:
		inputHeight = 4
	case stateExecutingTool:
		inputHeight = 4
	case stateExecutingTools:
		inputHeight = 3
	case stateRuntimeInput:
		inputHeight = 4
	case stateSelecting:
		itemsCount := len(m.selectionItems)
		if itemsCount > 4 {
			itemsCount = 4
		}
		inputHeight = itemsCount + 3
	default:
		inputHeight = 5
	}

	m.viewport.Height = m.height - (overhead + inputHeight)
	if m.viewport.Height < 5 {
		m.viewport.Height = 5
	}

	if m.renderer != nil && m.width != m.lastRendererWidth {
		m.renderer, _ = glamour.NewTermRenderer(
			glamour.WithStandardStyle("dark"),
			glamour.WithWordWrap(m.viewport.Width - 4),
		)
		m.lastRendererWidth = m.width
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
		toolCalls, reasoningContent, err := m.provider.StreamChat(ctx, prunedHistory, AgentTools(), ch)
		if err != nil {
			return streamErrMsg{err: err}
		}
		if len(toolCalls) > 0 {
			return toolCallBatchMsg{Calls: toolCalls, ReasoningContent: reasoningContent}
		}
		return streamDoneMsg{ReasoningContent: reasoningContent}
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

// killExec cancels any in-flight sandbox child process and cleans up the stdin pipe.
func (m *Model) killExec() {
	if m.stdinPipe != nil {
		_ = m.stdinPipe.Close()
		m.stdinPipe = nil
	}
	if m.cancelExec != nil {
		m.cancelExec()
		m.cancelExec = nil
	}
}

// resetToInput transitions the TUI back to the interactive input state.
// It must be called from every path that returns to stateInput to ensure
// the main textarea is re-focused and any in-flight streams are cancelled.
func (m *Model) resetToInput() {
	// Cancel any in-flight LLM stream context so the goroutine exits cleanly.
	if m.cancelStream != nil {
		m.cancelStream()
		m.cancelStream = nil
	}
	m.appState = stateInput
	// Re-focus the main input area so the user can type immediately.
	m.cmdTextarea.Blur()
	m.textarea.Focus()
	m.refreshViewport()
}

// runSandboxCmd executes the pending command in the sandbox.
// It creates a cancellable context, an io.Pipe for stdin (enabling runtime
// input injection via Ctrl+I), and stores both so the TUI can kill or feed
// the child process without quitting the whole app.
func (m *Model) runSandboxCmd() tea.Cmd {
	cmd := m.pendingCmd
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelExec = cancel

	// Create a pipe: the TUI writes to pipeW, the subprocess reads from pipeR.
	pipeR, pipeW := io.Pipe()
	m.stdinPipe = pipeW

	// Pre-seed the sudo password if needed, then keep the write end open so
	// the process can receive further input via Ctrl+I.
	if m.sudoPassword != "" {
		_, _ = fmt.Fprintln(pipeW, m.sudoPassword)
	}

	return func() tea.Msg {
		result, err := sandbox.ExecuteWithStdin(ctx, cmd, pipeR, sandbox.Options{
			SudoPassword: m.sudoPassword,
		})
		_ = pipeR.Close()
		if err != nil {
			return execResultMsg{err: err}
		}
		return execResultMsg{output: result.Combined()}

	}
}

// processNextToolCall picks up the next call in pendingToolCalls and routes it:
//   - run_command   → auto-approve (execute now) or stateConfirmingCmd
//   - write_file / edit_file → stateConfirmingWrite with diff preview
//   - everything else → execute immediately, return toolCallResultMsg
func (m *Model) processNextToolCall() tea.Cmd {
	if m.pendingToolIdx >= len(m.pendingToolCalls) {
		return func() tea.Msg { return toolCallResultMsg{} }
	}
	call := m.pendingToolCalls[m.pendingToolIdx]

	switch call.Name {
	case "run_command":
		command, _ := call.Args["command"].(string)

		if m.autoApproveCommands {
			if m.needsSudo(command) && m.sudoPassword == "" {
				m.appState = stateInputSudoPassword
				m.pendingCmd = command
				m.pendingLang = "" // tool call
				m.sudoTextInput.Focus()
				m.textarea.Blur()
				m.appendOutput(confirmStyle.Render("🔒 Sudo password required to execute auto-approved command."))
				m.refreshViewport()
				return nil
			}

			// Execute immediately without asking.
			m.appState = stateExecutingTool
			m.execStart = time.Now()
			m.execTimeout = sandbox.DefaultTimeout
			if v, ok := call.Args["timeout_seconds"].(float64); ok && v > 0 {
				m.execTimeout = time.Duration(v) * time.Second
			}
			m.appendOutput(lipgloss.NewStyle().Foreground(colorYellow).Render(
				fmt.Sprintf("⚡ run_command (auto): %s", command)))
			m.refreshViewport()
			callCopy := call
			ctx, cancel := context.WithCancel(context.Background())
			m.cancelExec = cancel
			pipeR, pipeW := io.Pipe()
			m.stdinPipe = pipeW
			if m.sudoPassword != "" {
				_, _ = fmt.Fprintln(pipeW, m.sudoPassword)
			}
			return tea.Batch(func() tea.Msg {
				out, err := ExecuteRunCommand(ctx, callCopy, m.sudoPassword, pipeR)
				cancel()
				_ = pipeR.Close()
				if err != nil {
					return toolCallResultMsg{CallID: callCopy.ID, Name: callCopy.Name, Error: err}
				}
				return toolCallResultMsg{CallID: callCopy.ID, Name: callCopy.Name, Result: out}
			}, m.spinner.Tick)
		}

		// Prompt the user — reuse stateConfirmingCmd.
		m.appState = stateConfirmingCmd
		m.pendingCmd = command
		m.pendingLang = "" // signals "tool call, not a code-fence"
		m.cmdTextarea.SetValue(command)
		m.textarea.Blur()
		m.cmdTextarea.Focus()
		m.appendOutput(confirmStyle.Render(fmt.Sprintf("🔧 run_command: %s", command)))
		m.appendOutput(lipgloss.NewStyle().Foreground(colorSubtext).Render(
			"Enter to execute · Esc to cancel · edit command if needed"))
		m.refreshViewport()
		// Return nil — execution continues when the user presses Enter (handled in
		// stateConfirmingCmd KeyEnter, which calls runSandboxCmd → execResultMsg).
		// But we need to deliver the result back as a toolCallResultMsg, not execResultMsg.
		// So we set a special pending state marker here and intercept in execResultMsg handler.
		// Simpler: in stateConfirmingCmd Enter, detect pendingLang=="" to know it's a tool call.
		return nil

	case "write_file", "edit_file":
		_, pw, _ := ExecuteToolWithWrite(call)
		if pw == nil {
			// Shouldn't happen, but guard.
			return func() tea.Msg {
				return toolCallResultMsg{CallID: call.ID, Name: call.Name, Error: fmt.Errorf("%s: failed to prepare write", call.Name)}
			}
		}
		m.pendingWrite = pw
		m.pendingWriteCallID = call.ID

		icon := "💾"
		if call.Name == "edit_file" {
			icon = "✏️ "
		}
		m.appendOutput(lipgloss.NewStyle().Foreground(colorYellow).Bold(true).Render(
			fmt.Sprintf("%s %s: %s", icon, call.Name, pw.Path)))

		// Show diff if available, otherwise show content preview.
		if pw.Diff != "" && pw.Diff != "(no changes)" {
			m.appendOutput(lipgloss.NewStyle().Foreground(colorSubtext).Render("Diff preview:"))
			m.appendOutput(codeBlockStyle.Render(pw.Diff))
		} else {
			preview := pw.Content
			if len(preview) > 400 {
				preview = preview[:400] + "\n...(truncated)"
			}
			m.appendOutput(lipgloss.NewStyle().Foreground(colorSubtext).Render(
				fmt.Sprintf("Content preview (%d bytes):", len(pw.Content))))
			m.appendOutput(codeBlockStyle.Render(preview))
		}

		m.appState = stateConfirmingWrite
		m.cmdTextarea.SetValue("y")
		m.textarea.Blur()
		m.cmdTextarea.Focus()
		m.refreshViewport()
		return nil

	default:
		// Safe read-only or web tools: execute immediately.
		m.appState = stateExecutingTool
		m.execStart = time.Now()
		m.execTimeout = 5 * time.Second
		m.appendOutput(lipgloss.NewStyle().Foreground(colorYellow).Render(
			fmt.Sprintf("⚙ Tool: %s", call.Name)))
		m.refreshViewport()
		callCopy := call
		return tea.Batch(func() tea.Msg {
			resultStr, _, err := ExecuteToolWithWrite(callCopy)
			if err != nil {
				return toolCallResultMsg{CallID: callCopy.ID, Name: callCopy.Name, Error: err}
			}
			return toolCallResultMsg{CallID: callCopy.ID, Name: callCopy.Name, Result: resultStr}
		}, m.spinner.Tick)
	}
}


// refreshViewport re-renders all output lines into the viewport and scrolls
// to the bottom. Use this when new content has been appended (e.g. a new
// message, command output, tool result).
func (m *Model) refreshViewport() {
	content := strings.Join(m.outputLines, "\n")
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
}

// refreshViewportKeepPosition re-renders output lines into the viewport while
// preserving the current scroll offset. Use this during resize and re-layout
// operations where no new content was added, so the user's reading position
// is not lost.
func (m *Model) refreshViewportKeepPosition() {
	prevOffset := m.viewport.YOffset
	content := strings.Join(m.outputLines, "\n")
	m.viewport.SetContent(content)
	// Clamp the offset so it doesn't exceed the new content length.
	maxOffset := m.viewport.TotalLineCount() - m.viewport.VisibleLineCount()
	if maxOffset < 0 {
		maxOffset = 0
	}
	if prevOffset > maxOffset {
		prevOffset = maxOffset
	}
	m.viewport.SetYOffset(prevOffset)
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

// statusBar renders the bottom status bar with state, session, token count, and model info.
func (m Model) statusBar() string {
	var statusText string
	switch m.appState {
	case stateStreaming:
		statusText = "● streaming"
	case stateConfirmingCmd:
		statusText = "⚠ awaiting confirmation"
	case stateExecuting:
		statusText = "⛔ executing command"
	case stateExecutingTool:
		statusText = "⚙ executing tool"
	case stateConfirmingWrite:
		statusText = "💾 write pending"
	case stateInputSudoPassword:
		statusText = "🔒 entering sudo password"
	default:
		statusText = "● ready"
	}

	// Session name
	sessionInfo := ""
	if m.activeSession != nil {
		name := m.activeSession.Name
		if name == "" {
			name = "Untitled"
		}
		sessionInfo = fmt.Sprintf("[%s]", name)
	}

	// Estimated token count
	tokenCount := llm.EstimateHistoryTokens(m.history)
	tokenInfo := fmt.Sprintf("~%dk tokens", tokenCount/1000)
	if tokenCount < 1000 {
		tokenInfo = fmt.Sprintf("~%d tokens", tokenCount)
	}

	// Provider + model info
	modelInfo := ""
	if m.activeProvider != "" || m.activeModel != "" {
		modelInfo = fmt.Sprintf("%s/%s", m.activeProvider, m.activeModel)
	}

	// Persona info
	personaInfo := ""
	if m.activePersona != "" {
		personaInfo = fmt.Sprintf("🎨 %s", m.activePersona)
	}

	// Auto-approve badge
	autoTag := ""
	if m.autoApproveCommands {
		autoTag = lipgloss.NewStyle().Foreground(colorYellow).Bold(true).Render("⚡auto")
	}

	// Left side: status · session
	leftText := statusText
	if sessionInfo != "" {
		leftText = fmt.Sprintf("%s · %s", statusText, sessionInfo)
	}

	// Right side: tokens · model · persona · auto badge · help hint
	rightParts := []string{}
	if tokenInfo != "" {
		rightParts = append(rightParts, lipgloss.NewStyle().Foreground(colorSubtext).Render(tokenInfo))
	}
	if modelInfo != "" {
		rightParts = append(rightParts, lipgloss.NewStyle().Foreground(colorOverlay).Render(modelInfo))
	}
	if personaInfo != "" {
		rightParts = append(rightParts, lipgloss.NewStyle().Foreground(colorAccent).Render(personaInfo))
	}
	if autoTag != "" {
		rightParts = append(rightParts, autoTag)
	}
	rightParts = append(rightParts, lipgloss.NewStyle().Foreground(colorSubtext).Render("? help"))
	rightText := strings.Join(rightParts, " · ")

	padding := m.width - lipgloss.Width(leftText) - lipgloss.Width(rightText) - 4
	if padding < 0 {
		padding = 0
	}
	spacer := strings.Repeat(" ", padding)
	return statusBarStyle.Width(m.width).Render(leftText + spacer + rightText)
}

// helpOverlay returns a formatted help block to append to outputLines.
func helpOverlay() string {
	header := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("❓ Keyboard Shortcuts & Commands")
	lines := []string{
		header,
		lipgloss.NewStyle().Foreground(colorAccent).Render("  Shortcuts"),
		"  Enter           Send message",
		"  Shift+Enter     Insert newline",
		"  Ctrl+C          Cancel stream / kill command / quit",
		"  Ctrl+L          Clear viewport",
		"  ?               Toggle this help",
		lipgloss.NewStyle().Foreground(colorAccent).Render("  Session Commands"),
		"  /history        List saved sessions",
		"  /load <id>      Resume session",
		"  /save <name>    Rename session",
		"  /delete <id>    Delete session",
		"  /map [dir]      Map workspace into context",
		"  /new            Fresh session",
		"  /clear          Clear viewport",
		"  /quit           Exit",
		lipgloss.NewStyle().Foreground(colorAccent).Render("  Runtime Switching"),
		"  /model          List models for current provider",
		"  /model <id>     Switch model (keeps history)",
		"  /provider       List providers",
		"  /provider <n>   Switch provider (keeps history)",
		"  /persona        List personas",
		"  /persona <n>    Switch system prompt persona",
		lipgloss.NewStyle().Foreground(colorAccent).Render("  Agent Execution"),
		"  /approve-tools on   Auto-execute run_command without prompting",
		"  /approve-tools off  Require confirmation for every command (default)",
		lipgloss.NewStyle().Foreground(colorAccent).Render("  File macros"),
		"  /read(path)     Attach file content to next message",
	}
	return strings.Join(lines, "\n")
}
