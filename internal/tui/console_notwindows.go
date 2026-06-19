//go:build !windows

package tui

// enableWindowsQuickEdit is a no-op on non-Windows platforms.
func enableWindowsQuickEdit() {}
