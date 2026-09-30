package deps

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// packageManager describes how to install ffmpeg with the OS package manager.
type packageManager struct {
	name     string
	args     []string // full command line
	elevated bool     // needs root
}

func (p packageManager) String() string { return strings.Join(p.args, " ") }

func findPackageManager() (packageManager, bool) {
	switch runtime.GOOS {
	case "windows":
		if _, err := exec.LookPath("winget"); err == nil {
			return packageManager{"winget", []string{"winget", "install", "-e", "--id", "Gyan.FFmpeg", "--source", "winget",
				"--disable-interactivity", "--accept-source-agreements", "--accept-package-agreements"}, false}, true
		}
	case "darwin":
		if brew := findBrew(); brew != "" {
			return packageManager{"Homebrew", []string{brew, "install", "ffmpeg"}, false}, true
		}
	default:
		for _, pm := range []packageManager{
			{"apt", []string{"apt-get", "install", "-y", "ffmpeg"}, true},
			{"dnf", []string{"dnf", "install", "-y", "ffmpeg-free"}, true},
			{"pacman", []string{"pacman", "-S", "--noconfirm", "--needed", "ffmpeg"}, true},
			{"zypper", []string{"zypper", "--non-interactive", "install", "ffmpeg"}, true},
		} {
			if _, err := exec.LookPath(pm.args[0]); err == nil {
				return pm, true
			}
		}
	}
	return packageManager{}, false
}

func findBrew() string {
	if p, err := exec.LookPath("brew"); err == nil {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew"} {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

func missingPMHint() string {
	switch runtime.GOOS {
	case "windows":
		return "winget is not available"
	case "darwin":
		return "install Homebrew from brew.sh"
	}
	return "apt, dnf, pacman or zypper"
}

// runElevated runs a shell command as root: directly when already root,
// through pkexec (graphical password prompt) on Linux, or through an
// administrator AppleScript prompt on macOS.
func runElevated(script string) ([]byte, error) {
	if os.Geteuid() == 0 {
		return exec.Command("/bin/sh", "-c", script).CombinedOutput()
	}
	switch runtime.GOOS {
	case "darwin":
		as := `do shell script "` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(script) +
			`" with administrator privileges`
		return exec.Command("osascript", "-e", as).CombinedOutput()
	case "linux", "freebsd":
		if _, err := exec.LookPath("pkexec"); err != nil {
			return nil, fmt.Errorf("pkexec not found — run this in a terminal instead:\n  sudo sh -c %s", shellQuote(script))
		}
		return exec.Command("pkexec", "/bin/sh", "-c", script).CombinedOutput()
	}
	return nil, fmt.Errorf("elevation is not supported on %s", runtime.GOOS)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellJoin(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = shellQuote(a)
	}
	return strings.Join(q, " ")
}

func tail(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		s = "…" + s[len(s)-n:]
	}
	return s
}

// SystemPlan describes what "Install system-wide" does for the missing parts
// of st. GBDK is never installed system-wide — it always goes to the local
// deps folder; only ffmpeg/ffprobe use the OS package manager. ok is false
// when no supported package manager is available.
func SystemPlan(st Status) (plan []string, ok bool) {
	ok = true
	if !st.HaveGBDK() {
		plan = append(plan, "GBDK → "+depsGBDKHome()+" (always local)")
	}
	if !st.HaveFFmpeg() {
		pm, found := findPackageManager()
		switch {
		case !found:
			ok = false
			plan = append(plan, "ffmpeg → no supported package manager ("+missingPMHint()+")")
		case pm.elevated:
			plan = append(plan, "ffmpeg → "+pm.String()+" (asks for the administrator password)")
		default:
			plan = append(plan, "ffmpeg → "+pm.String())
		}
	}
	return plan, ok
}

// InstallSystem installs ffmpeg with the OS package manager; a missing GBDK
// is still installed locally.
func InstallSystem(st Status, status func(string)) (Status, error) {
	home := st.GBDKHome
	if !st.HaveGBDK() {
		h, err := installGBDKLocal(status)
		if err != nil {
			return Check(""), fmt.Errorf("GBDK: %w", err)
		}
		home = h
	}
	if !st.HaveFFmpeg() {
		pm, found := findPackageManager()
		if !found {
			return Check(home), fmt.Errorf("ffmpeg: no supported package manager (%s)", missingPMHint())
		}
		status("Installing ffmpeg with " + pm.name + "…")
		var out []byte
		var err error
		if pm.elevated {
			out, err = runElevated(shellJoin(pm.args))
		} else {
			out, err = hideConsole(exec.Command(pm.args[0], pm.args[1:]...)).CombinedOutput()
		}
		now := Check(home)
		if now.HaveFFmpeg() {
			return now, nil
		}
		// The package manager updates PATH itself, but a running process keeps
		// the PATH it started with, so a successful install may only become
		// visible after a restart. winget also exits non-zero when the package
		// is already installed, so ask it directly instead of trusting the code.
		if err == nil || (pm.name == "winget" && wingetHasFFmpeg()) {
			return now, ErrRestartRequired
		}
		return now, fmt.Errorf("ffmpeg (%s): %v\n%s", pm, err, tail(out, 800))
	}
	return Check(home), nil
}

// ErrRestartRequired means ffmpeg was installed system-wide but this process
// won't see it on PATH until MediaBoy is restarted.
var ErrRestartRequired = errors.New("ffmpeg was installed; restart MediaBoy to use it")

func wingetHasFFmpeg() bool {
	cmd := hideConsole(exec.Command("winget", "list", "-e", "--id", "Gyan.FFmpeg",
		"--disable-interactivity", "--accept-source-agreements"))
	return cmd.Run() == nil
}
