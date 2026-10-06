//go:build !windows

package proc

import "os/exec"

// Hide keeps cmd from opening a console window (Windows only).
func Hide(cmd *exec.Cmd) *exec.Cmd { return cmd }
