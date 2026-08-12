//go:build windows

package main

import "golang.org/x/sys/windows"

const enableVirtualTerminalProcessing = 0x0004

// consoleColorSupported enables ANSI virtual terminal processing on the
// standard output console and reports whether color output is available.
// It returns false when output is redirected to a file/pipe or the console
// cannot be switched to VT mode (e.g. legacy consoles without ANSI support).
func consoleColorSupported() bool {
	out, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	if err != nil {
		return false
	}
	if enableVT(out) != nil {
		return false
	}
	// Also enable VT on stderr so slog output is colored when both are consoles.
	if errH, err := windows.GetStdHandle(windows.STD_ERROR_HANDLE); err == nil {
		_ = enableVT(errH)
	}
	return true
}

func enableVT(h windows.Handle) error {
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return err
	}
	if mode&enableVirtualTerminalProcessing != 0 {
		return nil
	}
	return windows.SetConsoleMode(h, mode|enableVirtualTerminalProcessing)
}