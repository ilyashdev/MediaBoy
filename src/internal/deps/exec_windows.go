package deps

import (
	"os/exec"
	"syscall"
)

// hideConsole keeps console tools (winget) from flashing a terminal window
// when launched from the GUI build.
func hideConsole(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return cmd
}
