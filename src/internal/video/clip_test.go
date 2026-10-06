package video

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"MediaBoy/internal/core"
	"MediaBoy/internal/ffmpeg"
)

// Tests on a real clip, skipped unless MEDIABOY_CLIP names a video:
//
//	MEDIABOY_CLIP        the video file
//	MEDIABOY_CLIP_CACHE  directory for its decoded frames (decoded once)
//	MEDIABOY_CLIP_OUT    where TestClipROM and TestClipPreview write
//	MEDIABOY_CLIP_Q      quality for them (default 4)

const clipFPS = 24

func clipEnv(t *testing.T) (frames []image.Image, audio []byte, out string, q int) {
	mp4, cache := os.Getenv("MEDIABOY_CLIP"), os.Getenv("MEDIABOY_CLIP_CACHE")
	if mp4 == "" || cache == "" {
		t.Skip("set MEDIABOY_CLIP and MEDIABOY_CLIP_CACHE")
	}
	q = 4
	if v, err := strconv.Atoi(os.Getenv("MEDIABOY_CLIP_Q")); err == nil {
		q = v
	}
	frames, audio = clipFrames(t, mp4, cache)
	return frames, audio, os.Getenv("MEDIABOY_CLIP_OUT"), q
}

// clipFrames decodes the clip once into cache/frames.bin + audio.bin so every
// run encodes identical input.
func clipFrames(t *testing.T, mp4, cache string) ([]image.Image, []byte) {
	t.Helper()
	fp, ap := filepath.Join(cache, "frames.bin"), filepath.Join(cache, "audio.bin")
	const fsz = 160 * 144 * 4
	if raw, err := os.ReadFile(fp); err == nil {
		audio, _ := os.ReadFile(ap)
		var frames []image.Image
		for off := 0; off+fsz <= len(raw); off += fsz {
			img := image.NewRGBA(image.Rect(0, 0, 160, 144))
			copy(img.Pix, raw[off:off+fsz])
			frames = append(frames, img)
		}
		return frames, audio
	}
	cfg := core.DefaultConfig()
	cfg.Mode = core.ModeCGB
	cfg.BilateralEnabled, cfg.SharpenEnabled, cfg.PosterizeEnabled, cfg.DitheringEnabled = false, false, false, false
	size, err := ffmpeg.ProbeFrameSize(mp4)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := ExtractGBFrames(mp4, clipFPS, cfg, size, nil)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	for i, fr := range frames {
		img := image.NewRGBA(image.Rect(0, 0, 160, 144))
		r, g, b := make([]uint8, gbPixels), make([]uint8, gbPixels), make([]uint8, gbPixels)
		loadFrame(fr, r, g, b)
		for p := 0; p < gbScreenSize; p++ {
			img.Pix[p*4], img.Pix[p*4+1], img.Pix[p*4+2], img.Pix[p*4+3] = r[p], g[p], b[p], 255
		}
		frames[i] = img
		buf.Write(img.Pix)
	}
	audio := ExtractAudio(mp4)
	os.MkdirAll(cache, 0755)
	os.WriteFile(fp, buf.Bytes(), 0644)
	os.WriteFile(ap, audio, 0644)
	return frames, audio
}

// TestClipROM writes the clip as a ROM to MEDIABOY_CLIP_OUT.
func TestClipROM(t *testing.T) {
	frames, audio, out, q := clipEnv(t)
	if out == "" {
		t.Skip("set MEDIABOY_CLIP_OUT")
	}
	res, err := BuildROM(frames, audio, clipFPS, q, 8, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkROM(t, "clip", res, 8)
	clip := os.Getenv("MEDIABOY_CLIP")
	base := strings.TrimSuffix(filepath.Base(clip), filepath.Ext(clip))
	name := fmt.Sprintf("%s_q%d", base, q)
	if err := WriteROM(out, name, res.ROM); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s.gbc: %d KB, %d/%d frames", name, len(res.ROM)>>10, res.FramesUsed, len(frames))

	// MEDIABOY_CLIP_FIT_MB: also the clip fitted into that many MB.
	if mb, err := strconv.Atoi(os.Getenv("MEDIABOY_CLIP_FIT_MB")); err == nil {
		res, q, err := BuildROMFit(frames, audio, clipFPS, mb, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		checkROM(t, "fit", res, mb)
		name := fmt.Sprintf("%s_fit%dmb", base, mb)
		if err := WriteROM(out, name, res.ROM); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s.gbc: %d KB, %d/%d frames, average quality %d %%", name, len(res.ROM)>>10, res.FramesUsed, len(frames), PercentFromQuality(q))
	}
}

// TestClipPreview reports size and quality at several qualities and, with
// MEDIABOY_CLIP_OUT, writes a side-by-side video (source | Game Boy) with the
// Game Boy audio, WAVs of both audio tracks and a few stills.
func TestClipPreview(t *testing.T) {
	frames, audio, out, previewQ := clipEnv(t)
	t.Logf("clip: %d frames, %.1f s", len(frames), float64(len(frames))/clipFPS)
	t.Logf("q    video KB/s  audio KB/s  PSNR      1 MB     8 MB     max cycles")
	var sim []simFrame
	var levels []uint8
	for _, q := range []int{0, 4, 16, 32} {
		start := time.Now()
		res, err := BuildROM(frames, audio, clipFPS, q, 8, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		s, lv, st := simulateGBVP3(t, res.ROM[0x4000:])
		m := measureSim(frames[:res.FramesUsed], clipFPS, s)
		sec := float64(res.FramesUsed) / clipFPS
		perSec := float64(st.videoBytes+st.audioBytes) / sec
		fits := func(mb int) float64 { return float64(mb<<20-len(player)) / perSec }
		t.Logf("q%-2d  %7.1f     %7.1f     %5.2f dB  %5.1f s  %6.1f s  %d   (raw %d, runs %d, kept palettes %d/%d, %v)",
			q, float64(st.videoBytes)/1024/sec, float64(st.audioBytes)/1024/sec, m.psnrAvg, fits(1), fits(8),
			st.maxCycles, st.rawLines, st.runLines, st.palettesKept, st.frames, time.Since(start).Round(time.Millisecond))
		if q == previewQ {
			sim, levels = s, lv
		}
	}
	if out == "" || sim == nil {
		return
	}
	os.MkdirAll(out, 0755)
	if err := writeWAV(filepath.Join(out, "audio_gb.wav"), levels); err != nil {
		t.Fatal(err)
	}
	if err := writeWAVSource(filepath.Join(out, "audio_source.wav"), audio); err != nil {
		t.Fatal(err)
	}

	const scale = 3
	W, H := 160*scale*2, 144*scale
	src, tl := sourceTimeline(len(frames), clipFPS), simTimeline(sim)
	n := min(len(src), len(tl))
	video := filepath.Join(out, fmt.Sprintf("preview_q%d.mp4", previewQ))
	cmd := exec.Command("ffmpeg", "-y", "-loglevel", "error",
		"-f", "rawvideo", "-pix_fmt", "rgb24", "-s", fmt.Sprintf("%dx%d", W, H),
		"-r", strconv.FormatFloat(gbFPSConst/2, 'f', 4, 64), "-i", "-",
		"-i", filepath.Join(out, "audio_gb.wav"),
		"-c:v", "libx264", "-crf", "16", "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "128k", "-shortest", video)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	buf := make([]byte, W*H*3)
	stills := map[int]bool{n / 5: true, n / 2: true, n * 4 / 5: true}
	for u := 0; u < n; u++ {
		sf := frames[src[u]]
		sb := sf.Bounds()
		for y := 0; y < 144; y++ {
			for x := 0; x < 160; x++ {
				r, g, b, _ := sf.At(sb.Min.X+x, sb.Min.Y+y).RGBA()
				panels := [2][3]int{{int(r >> 8), int(g >> 8), int(b >> 8)}, simPixel(&sim[tl[u]], x, y)}
				for p, c := range panels {
					for dy := 0; dy < scale; dy++ {
						for dx := 0; dx < scale; dx++ {
							X, Y := p*160*scale+x*scale+dx, y*scale+dy
							o := (Y*W + X) * 3
							buf[o], buf[o+1], buf[o+2] = uint8(c[0]), uint8(c[1]), uint8(c[2])
							io := img.PixOffset(X, Y)
							img.Pix[io], img.Pix[io+1], img.Pix[io+2], img.Pix[io+3] = uint8(c[0]), uint8(c[1]), uint8(c[2]), 255
						}
					}
				}
			}
		}
		if _, err := stdin.Write(buf); err != nil {
			t.Fatal(err)
		}
		if stills[u] {
			f, _ := os.Create(filepath.Join(out, fmt.Sprintf("still_q%d_%04d.png", previewQ, u)))
			png.Encode(f, img)
			f.Close()
		}
	}
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s (%d frames), stills and WAVs to %s", video, n, out)
}
