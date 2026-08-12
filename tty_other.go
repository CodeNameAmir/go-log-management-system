//go:build !windows

package main

import "os"

// consoleColorSupported reports whether standard output is an interactive
// terminal (character device). When output is redirected to a file or pipe,
// ANSI escape codes would not be interpreted, so colors are disabled.
func consoleColorSupported() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}