// Command launcher is MediaBoy.com, the console front end of MediaBoy.exe on
// Windows. The release MediaBoy.exe is a GUI program, so a terminal neither
// waits for it nor gets its exit code. Typing "MediaBoy" in cmd or PowerShell
// finds MediaBoy.com first (PATHEXT lists .COM before .EXE): this console
// program runs MediaBoy.exe with its own arguments and standard streams, waits
// for it and exits with its code. Explorer still opens MediaBoy.exe.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
)

func main() {
	exe, err := os.Executable()
	if err != nil {
		fail(err)
	}
	gui := filepath.Join(filepath.Dir(exe), "MediaBoy.exe")
	if len(os.Args) == 1 {
		// No command: open the window and give the terminal back.
		if err := exec.Command(gui).Start(); err != nil {
			fail(err)
		}
		return
	}

	cmd := exec.Command(gui, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Ctrl+C reaches MediaBoy.exe through the shared console; it cancels and
	// exits, and this process waits for that.
	signal.Ignore(os.Interrupt)
	err = cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		os.Exit(exit.ExitCode())
	}
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "MediaBoy:", err)
	os.Exit(1)
}
