package video

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"MediaBoy/internal/core"
	"MediaBoy/internal/ffmpeg"
)

// Real-clip reference: GBVP2_REF_MP4=<mp4>, GBVP2_REF_DIR=<dir>.
// The first run decodes the clip once into <dir>/frames.bin + audio.bin so the
// encoder always gets identical input; GBVP2_REF_MODE=write stores ROMs,
// GBVP2_REF_MODE=check compares against them.

func refFrames(t *testing.T, mp4, dir string) ([]image.Image, []byte) {
	t.Helper()
	fp, ap := filepath.Join(dir, "frames.bin"), filepath.Join(dir, "audio.bin")
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
	frames, err := ExtractGBFrames(mp4, 24, cfg, size, nil)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	for i, fr := range frames {
		img := image.NewRGBA(image.Rect(0, 0, 160, 144))
		b := fr.Bounds()
		for y := 0; y < 144; y++ {
			for x := 0; x < 160; x++ {
				r, g, bl, _ := fr.At(b.Min.X+x, b.Min.Y+y).RGBA()
				o := y*img.Stride + x*4
				img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = uint8(r>>8), uint8(g>>8), uint8(bl>>8), 255
			}
		}
		frames[i] = img
		buf.Write(img.Pix)
	}
	audio := ExtractAudio(mp4)
	os.WriteFile(fp, buf.Bytes(), 0644)
	os.WriteFile(ap, audio, 0644)
	return frames, audio
}

// TestGBVP2RealClipFit checks the Trim and Split modes on the real clip.
func TestGBVP2RealClipFit(t *testing.T) {
	mp4, dir := os.Getenv("GBVP2_REF_MP4"), os.Getenv("GBVP2_REF_DIR")
	if mp4 == "" || dir == "" {
		t.Skip("set GBVP2_REF_MP4 and GBVP2_REF_DIR")
	}
	frames, audio := refFrames(t, mp4, dir)
	tmp := t.TempDir()
	player := filepath.Join("..", "..", "video.gbc")

	// A clip that fits is encoded exactly as before by both modes.
	want, err := os.ReadFile(filepath.Join(dir, "rom_q4.gbc"))
	if err != nil {
		t.Fatal(err)
	}
	tr, err := BuildGBVP2Trimmed(tmp, player, frames, audio, 24, 4, 8, nil)
	if err != nil || tr.Truncated() || !bytes.Equal(tr.ROM, want) {
		t.Fatalf("fitting clip, trim: err %v, frames %d/%d, identical %v", err, tr.FramesUsed, tr.FramesTotal, bytes.Equal(tr.ROM, want))
	}
	ps, err := BuildGBVP2Parts(tmp, player, frames, audio, 24, 4, 8, nil)
	if err != nil || len(ps) != 1 || !bytes.Equal(ps[0].ROM, want) {
		t.Fatalf("fitting clip, split: err %v, %d parts", err, len(ps))
	}
	t.Log("8 MB q4: trim and split both give the reference ROM")

	const mb, q = 2, 0
	start := time.Now()
	tr, err = BuildGBVP2Trimmed(tmp, player, frames, audio, 24, q, mb, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("trim %d MB: %d bytes, frames %d/%d (%.1f of %.1f s), %v", mb, len(tr.ROM), tr.FramesUsed, tr.FramesTotal,
		float64(tr.FramesUsed)/24, float64(tr.FramesTotal)/24, time.Since(start).Round(time.Millisecond))
	if len(tr.ROM) > mb<<20 || !tr.Truncated() {
		t.Errorf("trim: bad result")
	}
	// The trimmed ROM must be exactly the plain encode of that many frames
	// with their own stretch of audio, and that encode must not truncate.
	plain, err := BuildGBVP2ROM(tmp, player, frames[:tr.FramesUsed], audio[:audioLen(audio, tr.FramesUsed, 24)], 24, q, mb, nil, nil)
	if err != nil || plain.Truncated() || !bytes.Equal(plain.ROM, tr.ROM) {
		t.Errorf("trim: not the plain encode of its frames + audio")
	}

	start = time.Now()
	type ev struct{ part, pass, done, total int }
	var evs []ev
	ps, err = BuildGBVP2Parts(tmp, player, frames, audio, 24, q, mb, func(part, pass, done, total int) {
		evs = append(evs, ev{part, pass, done, total})
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("split %d MB: %d parts, %v, %d progress events", mb, len(ps), time.Since(start).Round(time.Millisecond), len(evs))
	maxDone, passes := 0, map[int]int{}
	for i, e := range evs {
		if e.total != len(frames) || e.done > e.total || e.part < 1 || e.part > len(ps) || e.done < ps[e.part-1].FirstFrame {
			t.Fatalf("progress event %d out of range: %+v", i, e)
		}
		if i > 0 && e.part < evs[i-1].part {
			t.Fatalf("progress event %d: part went back: %+v after %+v", i, e, evs[i-1])
		}
		maxDone = max(maxDone, e.done)
		passes[e.part] = max(passes[e.part], e.pass)
	}
	t.Logf("progress: furthest frame %d / %d, passes per part %v", maxDone, len(frames), passes)
	if maxDone != len(frames) {
		t.Errorf("progress never reached the last frame")
	}
	next := 0
	for i, p := range ps {
		t.Logf("  part %d: frames %d..%d, %d bytes", i+1, p.FirstFrame, p.FirstFrame+p.FramesUsed-1, len(p.ROM))
		if p.FirstFrame != next || p.FramesUsed <= 0 || len(p.ROM) > mb<<20 {
			t.Errorf("part %d: bad bounds or size", i+1)
		}
		next = p.FirstFrame + p.FramesUsed
	}
	if next != len(frames) {
		t.Errorf("parts cover %d of %d frames", next, len(frames))
	}
	if !bytes.Equal(ps[0].ROM, tr.ROM) {
		t.Errorf("part 1 differs from the trimmed ROM")
	}
}

// TestGBVP2RealClipLimit checks that the ROM never exceeds the Max ROM size.
func TestGBVP2RealClipLimit(t *testing.T) {
	mp4, dir := os.Getenv("GBVP2_REF_MP4"), os.Getenv("GBVP2_REF_DIR")
	if mp4 == "" || dir == "" {
		t.Skip("set GBVP2_REF_MP4 and GBVP2_REF_DIR")
	}
	frames, audio := refFrames(t, mp4, dir)
	tmp := t.TempDir()
	player := filepath.Join("..", "..", "video.gbc")
	cache := NewGBVP2Cache()
	for _, mb := range []int{1, 2, 4, 8} {
		res, err := BuildGBVP2ROM(tmp, player, frames, audio, 24, 0, mb, cache, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%d MB: %d bytes, %d banks, frames %d/%d", mb, len(res.ROM), res.Banks, res.FramesUsed, res.FramesTotal)
		if len(res.ROM) > mb<<20 {
			t.Errorf("%d MB limit: ROM is %d bytes", mb, len(res.ROM))
		}
		want, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("lim%d_q0.gbc", mb)))
		if err != nil {
			continue
		}
		if bytes.Equal(want, res.ROM) {
			t.Logf("%d MB: identical to the original encoder", mb)
			continue
		}
		// Differs: only allowed where the original overflowed the limit. Outside
		// the header's size/checksum bytes the new ROM must match the original
		// up to the dropped frame and be padding (0xFF) after its end marker.
		if len(want) <= mb<<20 {
			t.Errorf("%d MB: original fit the limit but the ROM changed", mb)
			continue
		}
		first := -1
		for i := range res.ROM {
			if i >= 0x148 && i <= 0x14F {
				continue
			}
			if res.ROM[i] != want[i] {
				first = i
				break
			}
		}
		rest := res.ROM[first+1:]
		tail := bytes.TrimRight(rest, "\xff")
		t.Logf("%d MB: original was %d bytes; first difference at bank %d offset %#x (new byte %#x = end marker), %d non-padding bytes after it",
			mb, len(want), first/0x4000, first, res.ROM[first], len(tail))
		if first < 0 || res.ROM[first] != 0 || len(tail) != 0 {
			t.Errorf("%d MB: unexpected difference from the original", mb)
		}
	}
}

func TestGBVP2RealClip(t *testing.T) {
	mp4, dir, mode := os.Getenv("GBVP2_REF_MP4"), os.Getenv("GBVP2_REF_DIR"), os.Getenv("GBVP2_REF_MODE")
	if mp4 == "" || dir == "" {
		t.Skip("set GBVP2_REF_MP4 and GBVP2_REF_DIR")
	}
	frames, audio := refFrames(t, mp4, dir)
	t.Logf("%d frames, %d bytes audio", len(frames), len(audio))

	tmp := t.TempDir()
	player := filepath.Join(tmp, "player.gbc")
	if p, err := EnsureGBVP2(); err == nil {
		b, _ := os.ReadFile(p)
		os.WriteFile(player, b, 0644)
	} else {
		pb, _ := os.ReadFile(filepath.Join("..", "..", "video.gbc"))
		os.WriteFile(player, pb, 0644)
	}

	var cache *GBVP2Cache
	if os.Getenv("GBVP2_REF_CACHE") != "" {
		cache = NewGBVP2Cache()
	}
	// The order fitQuality visits for an 8 MB target, then the default q4.
	for _, q := range []int{32, 15, 7, 3, 1, 0, 64, 4} {
		start := time.Now()
		res, err := BuildGBVP2ROM(tmp, player, frames, audio, 24, q, 8, cache, nil)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(res.ROM)
		t.Logf("q%-2d frames %d/%d banks %d sha %x  %v", q, res.FramesUsed, res.FramesTotal, res.Banks, sum[:8], time.Since(start).Round(time.Millisecond))
		name := filepath.Join(dir, fmt.Sprintf("rom_q%d.gbc", q))
		switch mode {
		case "write":
			os.WriteFile(name, res.ROM, 0644)
		case "check":
			want, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(want, res.ROM) {
				t.Errorf("q%d: ROM differs from reference", q)
			}
		}
	}
}
