package gbdk

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"MediaBoy/internal/core"
	"MediaBoy/internal/proc"
)

type CompileResult struct {
	Success bool
	Output  string
	ROMPath string
}

func CompileGB(cfg core.ConvertConfig) CompileResult {
	return CompileGBWithProgress(cfg, nil)
}

func CompileGBWithProgress(cfg core.ConvertConfig, onProgress func(done, total int)) CompileResult {
	dir := cfg.OutputDir
	name := cfg.Name
	gbdkHome := cfg.GBDKHome

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

	if cfg.HiColor && cfg.Mode != core.ModeDMG {
		return compileHiColor(lcc, dir, name)
	}

	upper := strings.ToUpper(name)
	_ = upper

	banks := cfg.ROMBanks
	if banks < 4 {
		banks = 4
	}

	flags := []string{"-Wl-yt0x19", fmt.Sprintf("-Wl-yo%d", banks)}
	if cfg.Mode != core.ModeDMG {
		flags = append([]string{"-Wm-yC"}, flags...)
	}

	var allOutput bytes.Buffer
	var objFiles []string

	srcs, _ := filepath.Glob(filepath.Join(dir, "*.c"))
	for _, srcPath := range srcs {
		src := filepath.Base(srcPath)
		objName := strings.TrimSuffix(src, ".c") + ".o"
		objPath := filepath.Join(objDir, objName)

		args := append([]string{}, flags...)
		args = append(args, "-c", "-o", objPath, srcPath)

		cmd := proc.Command(lcc, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		allOutput.Write(out)
		if err != nil {
			allOutput.WriteString(fmt.Sprintf("\n[FAILED] compiling %s: %v\n", src, err))
			return CompileResult{Output: allOutput.String()}
		}
		allOutput.WriteString(fmt.Sprintf("[OK] compiled %s\n", src))
		objFiles = append(objFiles, objPath)
		if onProgress != nil {
			onProgress(len(objFiles), len(srcs))
		}
	}

	if len(objFiles) == 0 {
		return CompileResult{Output: "no source files found in " + dir}
	}

	romPath := filepath.Join(objDir, name+".gb")
	args := append([]string{}, flags...)
	args = append(args, "-o", romPath)
	args = append(args, objFiles...)

	cmd := proc.Command(lcc, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	allOutput.Write(out)
	if err != nil {
		allOutput.WriteString(fmt.Sprintf("\n[FAILED] link: %v\n", err))
		return CompileResult{Output: allOutput.String()}
	}
	romPath = cleanBuildArtifacts(dir, romPath)
	allOutput.WriteString(fmt.Sprintf("[OK] ROM: %s\n", romPath))

	return CompileResult{
		Success: true,
		Output:  allOutput.String(),
		ROMPath: romPath,
	}
}

func cleanBuildArtifacts(dir, romPath string) string {
	final := romPath
	if romPath != "" {
		if dst := filepath.Join(dir, filepath.Base(romPath)); dst != romPath {
			if os.Rename(romPath, dst) == nil {
				final = dst
			}
		}
	}
	_ = os.RemoveAll(filepath.Join(dir, "obj"))
	_ = os.RemoveAll(filepath.Join(dir, "frames"))
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".gb", ".gbc":
		default:
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return final
}

func compileHiColor(lcc, dir, name string) CompileResult {
	objDir := filepath.Join(dir, "obj")
	_ = os.MkdirAll(objDir, 0755)

	srcs, _ := filepath.Glob(filepath.Join(dir, "*.c"))
	if len(srcs) == 0 {
		return CompileResult{Output: "no source files in " + dir}
	}
	rom := filepath.Join("obj", name+".gbc")

	args := []string{"-Wm-yc", "-Wl-yt0x19", "-autobank", "-Wb-ext=.rel", "-I.", "-o", rom}
	for _, s := range srcs {
		args = append(args, filepath.Base(s))
	}
	cmd := proc.Command(lcc, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	res := CompileResult{Output: string(out)}
	if err != nil {
		res.Output += fmt.Sprintf("\n[FAILED] HiColor build: %v\n", err)
		return res
	}
	res.Success = true
	res.ROMPath = cleanBuildArtifacts(dir, filepath.Join(dir, rom))
	res.Output += fmt.Sprintf("[OK] HiColor ROM: %s\n", res.ROMPath)
	return res
}

// GenerateBatchFile writes a manual compile script next to the exported C
// sources. On Windows it emits compile.bat; on Linux/macOS a POSIX compile.sh.
// (The in-app "Compile ROM" button does not use these — it invokes lcc directly.)
func GenerateBatchFile(cfg core.ConvertConfig) error {
	if runtime.GOOS == "windows" {
		return generateBatchWindows(cfg)
	}
	return generateBatchUnix(cfg)
}

func generateBatchWindows(cfg core.ConvertConfig) error {
	dir := cfg.OutputDir
	name := cfg.Name
	gbdkHome := cfg.GBDKHome

	lcc := filepath.Join(gbdkHome, "bin", "lcc")

	var b bytes.Buffer
	fmt.Fprintf(&b, "@echo off\n")
	fmt.Fprintf(&b, "set LCC=%s\n", lcc)
	fmt.Fprintf(&b, "mkdir obj 2>nul\n\n")

	if cfg.HiColor && cfg.Mode != core.ModeDMG {

		fmt.Fprintf(&b, "%%LCC%% -Wm-yc -Wl-yt0x19 -autobank -Wb-ext=.rel -I. -o obj\\%s.gbc *.c\n", name)
		fmt.Fprintf(&b, "echo Done! ROM: obj\\%s.gbc\n", name)
		return os.WriteFile(filepath.Join(dir, "compile.bat"), b.Bytes(), 0644)
	}

	banks := cfg.ROMBanks
	if banks < 4 {
		banks = 4
	}
	flags := fmt.Sprintf("-Wl-yt0x19 -Wl-yo%d", banks)
	if cfg.Mode != core.ModeDMG {
		flags = "-Wm-yC " + flags
	}
	fmt.Fprintf(&b, "set FLAGS=%s\n", flags)
	fmt.Fprintf(&b, "for %%%%f in (*.c) do %%LCC%% %%FLAGS%% -c -o obj\\%%%%~nf.o %%%%f\n")
	fmt.Fprintf(&b, "%%LCC%% %%FLAGS%% -o obj\\%s.gb obj\\*.o\n", name)
	fmt.Fprintf(&b, "echo Done! ROM: obj\\%s.gb\n", name)

	return os.WriteFile(filepath.Join(dir, "compile.bat"), b.Bytes(), 0644)
}

func generateBatchUnix(cfg core.ConvertConfig) error {
	dir := cfg.OutputDir
	name := cfg.Name
	gbdkHome := cfg.GBDKHome

	lcc := filepath.Join(gbdkHome, "bin", "lcc")

	var b bytes.Buffer
	fmt.Fprintf(&b, "#!/bin/sh\n")
	fmt.Fprintf(&b, "set -e\n")
	fmt.Fprintf(&b, "cd \"$(dirname \"$0\")\"\n")
	fmt.Fprintf(&b, "LCC=\"%s\"\n", lcc)
	fmt.Fprintf(&b, "mkdir -p obj\n\n")

	if cfg.HiColor && cfg.Mode != core.ModeDMG {
		fmt.Fprintf(&b, "\"$LCC\" -Wm-yc -Wl-yt0x19 -autobank -Wb-ext=.rel -I. -o \"obj/%s.gbc\" *.c\n", name)
		fmt.Fprintf(&b, "echo \"Done! ROM: obj/%s.gbc\"\n", name)
		return os.WriteFile(filepath.Join(dir, "compile.sh"), b.Bytes(), 0755)
	}

	banks := cfg.ROMBanks
	if banks < 4 {
		banks = 4
	}
	flags := fmt.Sprintf("-Wl-yt0x19 -Wl-yo%d", banks)
	if cfg.Mode != core.ModeDMG {
		flags = "-Wm-yC " + flags
	}
	fmt.Fprintf(&b, "FLAGS=\"%s\"\n", flags)
	fmt.Fprintf(&b, "for f in *.c; do \"$LCC\" $FLAGS -c -o \"obj/${f%%.c}.o\" \"$f\"; done\n")
	fmt.Fprintf(&b, "\"$LCC\" $FLAGS -o \"obj/%s.gb\" obj/*.o\n", name)
	fmt.Fprintf(&b, "echo \"Done! ROM: obj/%s.gb\"\n", name)

	return os.WriteFile(filepath.Join(dir, "compile.sh"), b.Bytes(), 0755)
}
