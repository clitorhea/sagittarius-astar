//go:build windows

package tui

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableWindowsQuickEdit dynamically re-enables Quick Edit Mode on Windows,
// which bubbletea disables by default. This restores native text selection
// and copy/paste via the mouse without having to hold Shift.
func enableWindowsQuickEdit() {
	handle := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err == nil {
		// Enable Quick Edit Mode (0x0040)
		mode |= windows.ENABLE_QUICK_EDIT_MODE
		_ = windows.SetConsoleMode(handle, mode)
	}
}
