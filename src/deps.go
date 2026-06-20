package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const gbdkRelease = "https://github.com/gbdk-2020/gbdk-2020/releases/latest/download/"

func gbdkAsset() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "windows/amd64":
		return "gbdk-win64.zip"
	case "windows/386":
		return "gbdk-win32.zip"
	case "linux/amd64":
		return "gbdk-linux64.tar.gz"
	case "linux/arm64":
		return "gbdk-linux-arm64.tar.gz"
	case "darwin/amd64":
		return "gbdk-macos.tar.gz"
	case "darwin/arm64":
		return "gbdk-macos-arm64.tar.gz"
	}
	return ""
}

func ffmpegURLs() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{"https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip"}
	case "darwin":
		return []string{
			"https://evermeet.cx/ffmpeg/getrelease/ffmpeg/zip",
			"https://evermeet.cx/ffmpeg/getrelease/ffprobe/zip",
		}
	case "linux":
		if runtime.GOARCH == "arm64" {
			return []string{"https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-arm64-static.tar.xz"}
		}
		return []string{"https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz"}
	}
	return nil
}

func depsDir() string {
	exe, err := os.Executable()
	base := "."
	if err == nil {
		base = filepath.Dir(exe)
	}
	return filepath.Join(base, "deps")
}

func depsGBDKHome() string { return filepath.Join(depsDir(), "gbdk") }
func depsBinDir() string   { return filepath.Join(depsDir(), "bin") }

func lccPath(home string) string {
	p := filepath.Join(home, "bin", "lcc")
	if runtime.GOOS == "windows" {
		p += ".exe"
	}
	return p
}

func haveGBDKDeps() bool {
	_, err := os.Stat(lccPath(depsGBDKHome()))
	return err == nil
}

func haveFFmpegDeps() bool {
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	_, err := os.Stat(filepath.Join(depsBinDir(), name))
	return err == nil
}

func wireExistingDeps(cfg *ConvertConfig) {
	if haveGBDKDeps() {
		cfg.GBDKHome = depsGBDKHome()
	}
	if haveFFmpegDeps() {
		addToPATH(depsBinDir())
	}
}

func addToPATH(dir string) {
	cur := os.Getenv("PATH")
	if strings.Contains(cur, dir) {
		return
	}
	_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+cur)
}

func downloadAllDeps(status func(string)) (gbdkHome string, err error) {
	base := depsDir()
	if err = os.MkdirAll(depsBinDir(), 0755); err != nil {
		return "", err
	}
	gbdkHome, err = ensureGBDK(base, status)
	if err != nil {
		return "", fmt.Errorf("GBDK: %w", err)
	}
	if err = installFFmpeg(depsBinDir(), status); err != nil {
		return gbdkHome, fmt.Errorf("ffmpeg: %w", err)
	}
	addToPATH(depsBinDir())
	status("Dependencies ready.")
	return gbdkHome, nil
}

func ensureGBDK(base string, status func(string)) (string, error) {
	asset := gbdkAsset()
	if asset == "" {
		return "", fmt.Errorf("no GBDK build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	tmp := filepath.Join(base, asset)
	if err := httpDownload(gbdkRelease+asset, tmp, status); err != nil {
		return "", err
	}
	defer os.Remove(tmp)
	status("Extracting GBDK…")
	var err error
	if strings.HasSuffix(asset, ".zip") {
		err = unzipTo(tmp, base)
	} else {
		err = untarGzTo(tmp, base)
	}
	if err != nil {
		return "", err
	}
	home := depsGBDKHome()
	if _, e := os.Stat(lccPath(home)); e != nil {
		return "", fmt.Errorf("lcc missing after extract (expected %s)", lccPath(home))
	}
	return home, nil
}

func installFFmpeg(binDir string, status func(string)) error {
	urls := ffmpegURLs()
	if len(urls) == 0 {
		return fmt.Errorf("no ffmpeg build for %s", runtime.GOOS)
	}
	tmpDir, err := os.MkdirTemp("", "mb_ffmpeg_*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	for _, url := range urls {
		archive := filepath.Join(tmpDir, filepath.Base(url))
		if strings.HasSuffix(url, "/zip") {
			archive += ".zip"
		}
		if err := httpDownload(url, archive, status); err != nil {
			return err
		}
		status("Extracting ffmpeg…")
		switch {
		case strings.HasSuffix(archive, ".zip"):
			if err := unzipTo(archive, tmpDir); err != nil {
				return err
			}
		case strings.HasSuffix(archive, ".tar.xz"):
			cmd := exec.Command("tar", "-xJf", archive, "-C", tmpDir)
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("tar: %v\n%s", err, out)
			}
		default:
			return fmt.Errorf("unknown ffmpeg archive %s", archive)
		}
	}

	found := 0
	want := map[string]bool{"ffmpeg": true, "ffprobe": true}
	if runtime.GOOS == "windows" {
		want = map[string]bool{"ffmpeg.exe": true, "ffprobe.exe": true}
	}
	_ = filepath.Walk(tmpDir, func(p string, fi os.FileInfo, e error) error {
		if e != nil || fi.IsDir() {
			return nil
		}
		if want[fi.Name()] {
			if copyExec(p, filepath.Join(binDir, fi.Name())) == nil {
				found++
			}
		}
		return nil
	})
	if found < len(want) {
		return fmt.Errorf("ffmpeg/ffprobe not found in download")
	}
	return nil
}

func httpDownload(url, dst string, status func(string)) error {
	if status != nil {
		status("Downloading " + filepath.Base(dst) + "…")
	}
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func copyExec(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func unzipTo(src, dstDir string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		target := filepath.Join(dstDir, f.Name)
		if !strings.HasPrefix(target, filepath.Clean(dstDir)+string(os.PathSeparator)) {
			continue
		}
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(target, 0755)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode()
		if mode == 0 {
			mode = 0644
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0200)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func untarGzTo(src, dstDir string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dstDir, hdr.Name)
		if !strings.HasPrefix(target, filepath.Clean(dstDir)+string(os.PathSeparator)) {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			_ = os.MkdirAll(target, 0755)
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)|0200)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
	return nil
}
