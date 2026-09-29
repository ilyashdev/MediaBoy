package deps

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Status describes which external tools were found. Empty fields are missing.
type Status struct {
	GBDKHome string
	FFmpeg   string
	FFprobe  string
}

func (s Status) HaveGBDK() bool   { return s.GBDKHome != "" }
func (s Status) HaveFFmpeg() bool { return s.FFmpeg != "" && s.FFprobe != "" }
func (s Status) Complete() bool   { return s.HaveGBDK() && s.HaveFFmpeg() }

// Check looks for GBDK and ffmpeg/ffprobe and puts any known tool directories
// on the process PATH so later exec calls find them. preferredGBDK (e.g. the
// path saved from a previous session) is tried first.
func Check(preferredGBDK string) Status {
	for _, dir := range ffmpegDirs() {
		if fileExists(filepath.Join(dir, exeName("ffmpeg"))) {
			addToPATH(dir)
		}
	}
	var st Status
	st.GBDKHome = findGBDK(preferredGBDK)
	st.FFmpeg, _ = exec.LookPath("ffmpeg")
	st.FFprobe, _ = exec.LookPath("ffprobe")
	return st
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// gbdkTools is the full toolchain lcc drives. GBDK ships its own C compiler
// (SDCC), so no host gcc/clang is needed to build ROMs.
var gbdkTools = []string{"lcc", "sdcc", "sdcpp", "sdasgb", "sdldgb", "makebin", "png2hicolorgb"}

func missingGBDKTools(home string) []string {
	var missing []string
	for _, t := range gbdkTools {
		if !fileExists(filepath.Join(home, "bin", exeName(t))) {
			missing = append(missing, t)
		}
	}
	return missing
}

// isGBDKHome requires the whole toolchain, so an unrelated "lcc" compiler on
// PATH or a half-extracted install is not mistaken for GBDK.
func isGBDKHome(home string) bool {
	return home != "" && len(missingGBDKTools(home)) == 0
}

func findGBDK(preferred string) string {
	candidates := append([]string{preferred, depsGBDKHome()}, knownGBDKDirs()...)
	for _, c := range candidates {
		if isGBDKHome(c) {
			return c
		}
	}
	if p, err := exec.LookPath("lcc"); err == nil {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		if home := filepath.Dir(filepath.Dir(p)); isGBDKHome(home) {
			return home
		}
	}
	return ""
}

// ffmpegDirs are places ffmpeg may live without being on the inherited PATH:
// our portable install, winget's link dir, Homebrew prefixes.
func ffmpegDirs() []string {
	dirs := []string{depsBinDir()}
	switch runtime.GOOS {
	case "windows":
		if base := os.Getenv("LOCALAPPDATA"); base != "" {
			dirs = append(dirs, filepath.Join(base, "Microsoft", "WinGet", "Links"))
		}
	case "darwin":
		dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin")
	}
	return dirs
}

// LocalDir is where "Install locally" puts the tools.
func LocalDir() string { return depsDir() }

// knownGBDKDirs are common places a user may have unpacked GBDK by hand.
// MediaBoy itself only ever installs GBDK into the local deps folder.
func knownGBDKDirs() []string {
	if runtime.GOOS == "windows" {
		return []string{`C:\gbdk`, `C:\Bin\gbdk`}
	}
	dirs := []string{"/opt/gbdk", "/usr/local/gbdk", "/usr/share/gbdk"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "gbdk"))
	}
	return dirs
}
