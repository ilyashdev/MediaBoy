// Package proc starts the console tools MediaBoy drives (ffmpeg, lcc, …).
package proc

import "os/exec"

// Command is exec.Command for a console tool: on Windows the GUI build would
// otherwise open a terminal window for every run.
func Command(name string, args ...string) *exec.Cmd {
	return Hide(exec.Command(name, args...))
}
