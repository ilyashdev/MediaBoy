package deps

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ulikunitz/xz"
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

// depsDir is where GBDK and ffmpeg are installed: next to the executable when
// that location is writable (portable install), otherwise in the user config
// dir (e.g. ~/.config/MediaBoy/deps when the binary lives in /usr/bin).
func depsDir() string {
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		local := filepath.Join(filepath.Dir(exe), "deps")
		if _, err := os.Stat(local); err == nil || dirWritable(filepath.Dir(exe)) {
			return local
		}
	}
	if cfg, err := os.UserConfigDir(); err == nil {
		return filepath.Join(cfg, "MediaBoy", "deps")
	}
	return "deps"
}

func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".mb_write_test_*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

func depsGBDKHome() string { return filepath.Join(depsDir(), "gbdk") }
func depsBinDir() string   { return filepath.Join(depsDir(), "bin") }

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// addToPATH puts dir first on the process PATH (our own installs win).
func addToPATH(dir string) {
	if cur := os.Getenv("PATH"); !pathContains(cur, dir) {
		_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+cur)
	}
}

func pathContains(pathList, dir string) bool {
	for _, p := range filepath.SplitList(pathList) {
		if p == dir || (runtime.GOOS == "windows" && strings.EqualFold(filepath.Clean(p), filepath.Clean(dir))) {
			return true
		}
	}
	return false
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
		if err := extractArchive(archive, tmpDir); err != nil {
			return err
		}
	}

	want := map[string]bool{exeName("ffmpeg"): true, exeName("ffprobe"): true}
	found := 0
	_ = filepath.Walk(tmpDir, func(p string, fi os.FileInfo, e error) error {
		if e != nil || fi.IsDir() || !want[fi.Name()] {
			return nil
		}
		if copyExec(p, filepath.Join(binDir, fi.Name())) == nil {
			found++
		}
		return nil
	})
	if found < len(want) {
		return fmt.Errorf("ffmpeg/ffprobe not found in download")
	}
	return nil
}

func extractArchive(archive, dstDir string) error {
	switch {
	case strings.HasSuffix(archive, ".zip"):
		return unzipTo(archive, dstDir)
	case strings.HasSuffix(archive, ".tar.gz"), strings.HasSuffix(archive, ".tgz"):
		return untarFile(archive, dstDir, func(r io.Reader) (io.Reader, error) { return gzip.NewReader(r) })
	case strings.HasSuffix(archive, ".tar.xz"):
		return untarFile(archive, dstDir, func(r io.Reader) (io.Reader, error) { return xz.NewReader(r) })
	}
	return fmt.Errorf("unknown archive format: %s", filepath.Base(archive))
}

var httpClient = &http.Client{Timeout: 15 * time.Minute}

func httpDownload(url, dst string, status func(string)) error {
	name := filepath.Base(dst)
	status("Downloading " + name + "…")
	resp, err := httpClient.Get(url)
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
	pr := &progressReader{r: resp.Body, total: resp.ContentLength, report: func(done, total int64) {
		if total > 0 {
			status(fmt.Sprintf("Downloading %s… %d%% (%.1f / %.1f MB)", name,
				done*100/total, float64(done)/(1<<20), float64(total)/(1<<20)))
		} else {
			status(fmt.Sprintf("Downloading %s… %.1f MB", name, float64(done)/(1<<20)))
		}
	}}
	_, err = io.Copy(f, pr)
	return err
}

type progressReader struct {
	r      io.Reader
	done   int64
	total  int64
	last   time.Time
	report func(done, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if time.Since(p.last) > 200*time.Millisecond || err == io.EOF {
		p.last = time.Now()
		p.report(p.done, p.total)
	}
	return n, err
}

func copyExec(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	_ = os.Remove(dst) // replacing a running binary in place fails with "text file busy"
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// safeJoin resolves name inside dstDir, rejecting paths that escape it.
func safeJoin(dstDir, name string) (string, bool) {
	target := filepath.Join(dstDir, name)
	return target, strings.HasPrefix(target, filepath.Clean(dstDir)+string(os.PathSeparator))
}

func unzipTo(src, dstDir string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		target, ok := safeJoin(dstDir, f.Name)
		if !ok {
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

func untarFile(src, dstDir string, decompress func(io.Reader) (io.Reader, error)) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	r, err := decompress(f)
	if err != nil {
		return err
	}
	if c, ok := r.(io.Closer); ok {
		defer c.Close()
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, ok := safeJoin(dstDir, hdr.Name)
		if !ok {
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
		case tar.TypeSymlink:
			// Only keep links that stay inside the extraction dir.
			if _, ok := safeJoin(dstDir, filepath.Join(filepath.Dir(hdr.Name), hdr.Linkname)); !ok || filepath.IsAbs(hdr.Linkname) {
				continue
			}
			_ = os.MkdirAll(filepath.Dir(target), 0755)
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil && runtime.GOOS != "windows" {
				return err
			}
		}
	}
}

// downloadGBDK fetches the GBDK release for this platform and extracts it
// into parent, returning the resulting GBDK home (parent/gbdk).
func downloadGBDK(parent string, status func(string)) (string, error) {
	asset := gbdkAsset()
	if asset == "" {
		return "", fmt.Errorf("no GBDK build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	tmp := filepath.Join(parent, asset)
	if err := httpDownload(gbdkRelease+asset, tmp, status); err != nil {
		return "", err
	}
	defer os.Remove(tmp)
	status("Extracting GBDK…")
	if err := extractArchive(tmp, parent); err != nil {
		return "", err
	}
	home := filepath.Join(parent, "gbdk")
	if missing := missingGBDKTools(home); len(missing) > 0 {
		return "", fmt.Errorf("GBDK incomplete after extract, missing: %s", strings.Join(missing, ", "))
	}
	return home, nil
}

// installGBDKLocal (re)installs GBDK into the local deps folder.
func installGBDKLocal(status func(string)) (string, error) {
	if err := os.MkdirAll(depsDir(), 0755); err != nil {
		return "", fmt.Errorf("cannot create %s: %w", depsDir(), err)
	}
	_ = os.RemoveAll(depsGBDKHome())
	return downloadGBDK(depsDir(), status)
}

// InstallLocal installs the missing tools into the portable deps folder next
// to the executable (or the user config dir when that isn't writable).
func InstallLocal(st Status, status func(string)) (Status, error) {
	home := st.GBDKHome
	if !st.HaveGBDK() {
		h, err := installGBDKLocal(status)
		if err != nil {
			return Check(""), fmt.Errorf("GBDK: %w", err)
		}
		home = h
	}
	if !st.HaveFFmpeg() {
		if err := os.MkdirAll(depsBinDir(), 0755); err != nil {
			return Check(home), err
		}
		if err := installFFmpeg(depsBinDir(), status); err != nil {
			return Check(home), fmt.Errorf("ffmpeg: %w", err)
		}
		addToPATH(depsBinDir())
	}
	return Check(home), nil
}
