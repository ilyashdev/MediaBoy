//go:build !windows

package cli

// attachConsole is only needed on Windows.
func attachConsole() {}
