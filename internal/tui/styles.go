// Package tui — Lipgloss style definitions for the aig TUI.
package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

var (
	// Color palette — cohesive dark-mode aesthetic.
	colorBase    = lipgloss.Color("#1e1e2e") // Catppuccin Mocha base
	colorSurface = lipgloss.Color("#313244") // surface0
	colorAccent  = lipgloss.Color("#cba6f7") // mauve
	colorGreen   = lipgloss.Color("#a6e3a1") // green
	colorRed     = lipgloss.Color("#f38ba8") // red
	colorYellow  = lipgloss.Color("#f9e2af") // yellow
	colorSubtext = lipgloss.Color("#6c7086") // subtext0
	colorText    = lipgloss.Color("#cdd6f4") // text
	colorOverlay = lipgloss.Color("#45475a") // overlay0

	// promptStyle renders the "> " user-input prefix.
	promptStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	// userLabelStyle renders the "You" label.
	userLabelStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true).
			PaddingRight(1)

	// assistantLabelStyle renders the "aig" label.
	assistantLabelStyle = lipgloss.NewStyle().
				Foreground(colorGreen).
				Bold(true).
				PaddingRight(1)

	// codeBlockStyle highlights a detected executable code block.
	codeBlockStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorYellow).
			Foreground(colorText).
			Padding(0, 1)

	// confirmStyle renders the execution confirmation prompt.
	confirmStyle = lipgloss.NewStyle().
			Foreground(colorYellow).
			Bold(true).
			PaddingLeft(2)

	// errorStyle renders error messages.
	errorStyle = lipgloss.NewStyle().
			Foreground(colorRed).
			Bold(true).
			PaddingLeft(2)

	// statusBarStyle renders the bottom status line.
	statusBarStyle = lipgloss.NewStyle().
			Background(colorSurface).
			Foreground(colorSubtext).
			PaddingLeft(2).
			PaddingRight(2)

	// spinnerStyle renders the streaming indicator.
	spinnerStyle = lipgloss.NewStyle().
			Foreground(colorAccent)

	// dividerStyle renders the separator between turns.
	dividerStyle = lipgloss.NewStyle().
			Foreground(colorOverlay)

	// execResultStyle renders the sandbox command output.
	execResultStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(colorGreen).
			Foreground(colorText).
			PaddingLeft(1)

	// systemLabelStyle renders the system messages in history.
	systemLabelStyle = lipgloss.NewStyle().
				Foreground(colorSubtext).
				Italic(true).
				PaddingRight(1)

	// systemContentStyle renders system message content in history.
	systemContentStyle = lipgloss.NewStyle().
				Foreground(colorSubtext).
				Italic(true)

	// thinkingStyle renders model internal reasoning blocks.
	thinkingStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(colorOverlay).
			Foreground(colorSubtext).
			Italic(true).
			PaddingLeft(2)

	// inputStyle wraps the text-input area.
	inputStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorOverlay).
			Padding(0, 1)

	// inputActiveStyle renders the active input border.
	inputActiveStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorAccent).
				Padding(0, 1)
)

// headerBanner returns the styled ASCII art header shown on startup.
func headerBanner(version string) string {
	banner := lipgloss.NewStyle().
		Foreground(colorAccent).
		Bold(true).
		Render("  ✦ aig — Sagittarius A*")

	subtitle := lipgloss.NewStyle().
		Foreground(colorSubtext).
		Render(fmt.Sprintf("  terminal AI agent %s ·  type /help for commands", version))

	return banner + "\n" + subtitle + "\n"
}

// divider returns a horizontal rule string of the given width.
func divider(width int) string {
	if width <= 0 {
		width = 40
	}
	line := ""
	for range width {
		line += "─"
	}
	return dividerStyle.Render(line)
}
