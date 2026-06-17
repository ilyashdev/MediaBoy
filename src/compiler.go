package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CompileResult holds the output of a GBDK compilation.
type CompileResult struct {
	Success bool
	Output  string
	ROMPath string
}

// CompileGB compiles the exported GBDK project using lcc.
// dir must contain main.c and {name}.c + {name}.h (produced by Export functions).
func CompileGB(cfg ConvertConfig) CompileResult {
	dir := cfg.OutputDir
	name := cfg.Name
	gbdkHome := cfg.GBDKHome

	// Resolve to absolute path so lcc receives absolute paths regardless of CWD.
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}

	lcc := filepath.Join(gbdkHome, "bin", "lcc")
	if _, err := os.Stat(lcc + ".exe"); os.IsNotExist(err) {
		if _, err2 := os.Stat(lcc); os.IsNotExist(err2) {
			return CompileResult{Output: fmt.Sprintf("lcc not found at %s\nCheck GBDK Home path in settings.", lcc)}
		}
	}

	objDir := filepath.Join(dir, "obj")
	if err := os.MkdirAll(objDir, 0755); err != nil {
		return CompileResult{Output: fmt.Sprintf("failed to create obj dir: %v", err)}
	}

	upper := strings.ToUpper(name)
	_ = upper

	banks := cfg.ROMBanks
	if banks < 4 {
		banks = 4
	}
	// -Wm-yC flags the cartridge header CGB-only. Omit it for DMG so the ROM
	// boots on an original Game Boy. MBC5 (0x19) works on both.
	flags := []string{"-Wl-yt0x19", fmt.Sprintf("-Wl-yo%d", banks)}
	if cfg.Mode != ModeDMG {
		flags = append([]string{"-Wm-yC"}, flags...)
	}

	var allOutput bytes.Buffer
	var objFiles []string

	// Compile every .c file in dir (data banks, tables, player).
	srcs, _ := filepath.Glob(filepath.Join(dir, "*.c"))
	for _, srcPath := range srcs {
		src := filepath.Base(srcPath)
		objName := strings.TrimSuffix(src, ".c") + ".o"
		objPath := filepath.Join(objDir, objName)

		args := append([]string{}, flags...)
		args = append(args, "-c", "-o", objPath, srcPath)

		cmd := exec.Command(lcc, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		allOutput.Write(out)
		if err != nil {
			allOutput.WriteString(fmt.Sprintf("\n[FAILED] compiling %s: %v\n", src, err))
			return CompileResult{Output: allOutput.String()}
		}
		allOutput.WriteString(fmt.Sprintf("[OK] compiled %s\n", src))
		objFiles = append(objFiles, objPath)
	}

	if len(objFiles) == 0 {
		return CompileResult{Output: "no source files found in " + dir}
	}

	// Link
	romPath := filepath.Join(objDir, name+".gb")
	args := append([]string{}, flags...)
	args = append(args, "-o", romPath)
	args = append(args, objFiles...)

	cmd := exec.Command(lcc, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	allOutput.Write(out)
	if err != nil {
		allOutput.WriteString(fmt.Sprintf("\n[FAILED] link: %v\n", err))
		return CompileResult{Output: allOutput.String()}
	}
	allOutput.WriteString(fmt.Sprintf("[OK] ROM: %s\n", romPath))

	return CompileResult{
		Success: true,
		Output:  allOutput.String(),
		ROMPath: romPath,
	}
}

// GenerateBatchFile writes a Windows .bat script that compiles the project
// without requiring make to be installed.
func GenerateBatchFile(cfg ConvertConfig) error {
	dir := cfg.OutputDir
	name := cfg.Name
	gbdkHome := cfg.GBDKHome

	lcc := filepath.Join(gbdkHome, "bin", "lcc")

	banks := cfg.ROMBanks
	if banks < 4 {
		banks = 4
	}
	flags := fmt.Sprintf("-Wl-yt0x19 -Wl-yo%d", banks)
	if cfg.Mode != ModeDMG {
		flags = "-Wm-yC " + flags
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, "@echo off\n")
	fmt.Fprintf(&b, "set LCC=%s\n", lcc)
	fmt.Fprintf(&b, "set FLAGS=%s\n", flags)
	fmt.Fprintf(&b, "mkdir obj 2>nul\n\n")
	fmt.Fprintf(&b, "for %%%%f in (*.c) do %%LCC%% %%FLAGS%% -c -o obj\\%%%%~nf.o %%%%f\n")
	fmt.Fprintf(&b, "%%LCC%% %%FLAGS%% -o obj\\%s.gb obj\\*.o\n", name)
	fmt.Fprintf(&b, "echo Done! ROM: obj\\%s.gb\n", name)

	return os.WriteFile(filepath.Join(dir, "compile.bat"), b.Bytes(), 0644)
}
