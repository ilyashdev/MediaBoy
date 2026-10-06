package video

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"math/rand"
	"testing"
)

// TestPlayerROM checks the embedded player against the addresses and ops the
// encoder relies on.
func TestPlayerROM(t *testing.T) {
	if len(player) != 0x4000 {
		t.Fatalf("player is %d bytes, want one bank", len(player))
	}
	if player[0] != 0xC3 || player[3] != 0xC3 {
		t.Errorf("RunStub and ReturnStub at 0 and 3 must be jp")
	}
	if !bytes.Equal(player[6:9], []byte{0xD1, 0x6A, 0x73}) { // pop de; ld l, d; ld [hl], e
		t.Errorf("Patches must start at 6")
	}
	for i := gbvp3RunReturn - (gbvp3RunMax - 1); i < gbvp3RunReturn; i++ {
		if player[i] != gbvp3PatchHeader(0) {
			t.Fatalf("RunTable byte at %#x is %#x, want the no-change op", i, player[i])
		}
	}
	if player[gbvp3RunReturn] != gbvp3OpReturn {
		t.Errorf("RunReturn at %#x is %#x, want the return op", gbvp3RunReturn, player[gbvp3RunReturn])
	}
	// The player's NR50 tables (low nibble, then high nibble) give the levels
	// the audio is quantised to.
	var tables []byte
	for _, shift := range []int{0, 4} {
		for x := range 256 {
			k := x >> shift & 15
			if k == 15 {
				k = gbvp3AudioLevels / 2 // never written; plays the middle level
			}
			tables = append(tables, gbvp3NR50(k))
		}
	}
	if !bytes.Contains(player, tables) {
		t.Errorf("the player's NR50 tables do not match gbvp3NR50")
	}
	// "jr n" from $FFFD lands at n-1: the ops are (target+1) << 1.
	if gbvp3OpRun != (0+1)<<1 || gbvp3OpReturn != (3+1)<<1 || gbvp3PatchHeader(gbvp3MaxPatches) != (6+1)<<1 {
		t.Errorf("op codes do not match the stub addresses")
	}
}

func TestQualityPercent(t *testing.T) {
	for p, q := range map[int]int{100: 0, 75: 4, MinPercent: MaxQuality, 0: MaxQuality} {
		if got := QualityFromPercent(p); got != q {
			t.Errorf("QualityFromPercent(%d) = %d, want %d", p, got, q)
		}
	}
	// Near 0 % one percent spans more than one quality step, so the way back
	// is only within one step.
	for q := 0; q <= MaxQuality; q++ {
		if got := QualityFromPercent(PercentFromQuality(q)); abs8(got-q) > 1 {
			t.Errorf("quality %d -> %d %% -> %d", q, PercentFromQuality(q), got)
		}
	}
	for p := 1; p <= 100; p++ {
		if QualityFromPercent(p) > QualityFromPercent(p-1) {
			t.Errorf("quality rises from %d %% to %d %%", p-1, p)
		}
	}
}

type testClip struct {
	name   string
	frames []image.Image
	fps    float64
	maxMB  int
}

func solid(c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 160, 144))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, 255
	}
	return img
}

func noiseFrame(rng *rand.Rand) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 160, 144))
	rng.Read(img.Pix)
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	return img
}

func testClips() []testClip {
	rng := rand.New(rand.NewSource(42))

	// Gradients with moving squares: palettes, patches, raw lines.
	var motion []image.Image
	for f := 0; f < 24; f++ {
		img := image.NewRGBA(image.Rect(0, 0, 160, 144))
		for y := 0; y < 144; y++ {
			for x := 0; x < 160; x++ {
				img.Set(x, y, color.RGBA{uint8(x + f*3), uint8(y + f*5), uint8((x * y / 64) + f*7), 255})
			}
		}
		for k := 0; k < 4; k++ {
			ox, oy := (f*(k+2)*3+k*37)%140, (f*(k+1)*2+k*29)%124
			c := color.RGBA{uint8(60 * k), uint8(255 - 50*k), uint8(30 + 70*k), 255}
			for y := oy; y < oy+20; y++ {
				for x := ox; x < ox+20; x++ {
					img.Set(x, y, c)
				}
			}
		}
		motion = append(motion, img)
	}

	// A still picture with small changes: runs and kept palettes; at 1 fps the
	// holds pass the 127 count limit.
	var static []image.Image
	base := solid(color.RGBA{10, 200, 90, 255})
	for f := 0; f < 20; f++ {
		img := image.NewRGBA(base.Rect)
		copy(img.Pix, base.Pix)
		if f%7 == 6 {
			for y := 50; y < 70; y++ {
				for x := 0; x < 160; x++ {
					img.Set(x, y, color.RGBA{uint8(f * 12), 30, uint8(250 - f*10), 255})
				}
			}
		}
		static = append(static, img)
	}

	var noise, long []image.Image
	for f := 0; f < 8; f++ {
		noise = append(noise, noiseFrame(rng))
	}
	for f := 0; f < 200; f++ {
		long = append(long, noiseFrame(rng))
	}

	return []testClip{
		{"motion", motion, 24, 8},
		{"static", static, 1, 8},
		{"noise", noise, 10, 8},
		{"long1mb", long, 30, 1},
	}
}

func testAudio(frames int, fps float64) []byte {
	rng := rand.New(rand.NewSource(7))
	a := make([]byte, int(float64(frames)/fps*audioRate)*2)
	for i := range a {
		a[i] = uint8(128 + 60*rng.NormFloat64()/3)
	}
	return a
}

// checkROM plays res in the simulator, which fails the test on anything the
// player would not handle, and checks the size limit.
func checkROM(t *testing.T, name string, res Result, maxMB int) ([]simFrame, gbvp3SimStats) {
	t.Helper()
	if len(res.ROM) > maxMB<<20 {
		t.Fatalf("%s: %d bytes, over %d MB", name, len(res.ROM), maxMB)
	}
	if !bytes.Equal(res.ROM[:0x100], player[:0x100]) {
		t.Fatalf("%s: the ROM does not start with the player", name)
	}
	sim, _, st := simulateGBVP3(t, res.ROM[0x4000:])
	return sim, st
}

func TestStreams(t *testing.T) {
	for _, c := range testClips() {
		audio := testAudio(len(c.frames), c.fps)
		for _, q := range []int{0, 4, MaxQuality} {
			res, err := BuildROM(c.frames, audio, c.fps, q, c.maxMB, nil, nil)
			if err != nil {
				t.Fatalf("%s q%d: %v", c.name, q, err)
			}
			sim, st := checkROM(t, c.name, res, c.maxMB)
			shown := 0
			for _, f := range sim {
				shown += f.count
			}
			m := measureSim(c.frames[:res.FramesUsed], c.fps, sim)
			t.Logf("%-8s q%-2d frames %d/%d, %d video frames, %d KB, raw %d, runs %d, kept palettes %d, max %d cycles, PSNR %.1f",
				c.name, q, res.FramesUsed, res.FramesTotal, shown, len(res.ROM)>>10,
				st.rawLines, st.runLines, st.palettesKept, st.maxCycles, m.psnrAvg)
			if c.name == "long1mb" && q == 0 && !res.Truncated() {
				t.Errorf("long1mb q0 should not fit into 1 MB")
			}
			if c.name != "long1mb" && res.Truncated() {
				t.Errorf("%s q%d: truncated", c.name, q)
			}
			if c.name == "static" && (st.runLines == 0 || st.palettesKept == 0) {
				t.Errorf("static q%d: no runs or kept palettes", q)
			}
			if c.name == "motion" && q == 0 && m.psnrAvg < 20 {
				t.Errorf("motion q0: PSNR %.1f", m.psnrAvg)
			}
		}
	}
}

func TestFitModes(t *testing.T) {
	c := testClips()[3] // 200 noise frames, 1 MB
	audio := testAudio(len(c.frames), c.fps)

	tr, err := BuildTrimmed(c.frames, audio, c.fps, 0, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkROM(t, "trimmed", tr, 1)
	if !tr.Truncated() || tr.FramesTotal != len(c.frames) {
		t.Errorf("trim: frames %d/%d", tr.FramesUsed, tr.FramesTotal)
	}
	// The trimmed ROM is the plain encode of its frames with their audio.
	plain, err := BuildROM(c.frames[:tr.FramesUsed], audio[:audioLen(audio, tr.FramesUsed, c.fps)], c.fps, 0, 1, nil, nil)
	if err != nil || plain.Truncated() || !bytes.Equal(plain.ROM, tr.ROM) {
		t.Errorf("trim: not the plain encode of its frames + audio (err %v)", err)
	}

	parts, err := BuildParts(c.frames, audio, c.fps, 0, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	next := 0
	for i, p := range parts {
		checkROM(t, "part", p.Result, 1)
		if p.FirstFrame != next || p.FramesUsed <= 0 {
			t.Errorf("part %d: frames %d+%d, want from %d", i+1, p.FirstFrame, p.FramesUsed, next)
		}
		next = p.FirstFrame + p.FramesUsed
	}
	if next != len(c.frames) || len(parts) < 2 {
		t.Errorf("%d parts cover %d of %d frames", len(parts), next, len(c.frames))
	}
	if !bytes.Equal(parts[0].ROM, tr.ROM) {
		t.Errorf("part 1 differs from the trimmed ROM")
	}
}

// TestEstimate checks that one unlimited encode predicts BuildROM for every
// cartridge size.
func TestEstimate(t *testing.T) {
	for _, c := range testClips() {
		audio := testAudio(len(c.frames), c.fps)
		est, err := EstimateROM(context.Background(), c.frames, audio, c.fps, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		full, err := BuildROM(c.frames, audio, c.fps, 0, 8, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if est.Cart != len(full.ROM) || est.Fits8MB != full.FramesUsed {
			t.Errorf("%s: estimate %d bytes (%d frames), ROM %d (%d)", c.name, est.Cart, est.Fits8MB, len(full.ROM), full.FramesUsed)
		}
		for _, mb := range []int{1, 2, 4, 8} {
			res, err := BuildROM(c.frames, audio, c.fps, 0, mb, nil, nil)
			want := res.FramesUsed
			if errors.Is(err, ErrAudioOverflow) {
				want = -1
			} else if err != nil {
				t.Fatal(err)
			}
			if got := est.FramesFor(mb); got != want {
				t.Errorf("%s %d MB: estimate %d frames, BuildROM %d", c.name, mb, got, want)
			}
			// When the whole clip fits, the estimate's ROM is the compile's.
			r, ok := est.ROM(mb)
			if ok != (want == len(c.frames)) || ok && !bytes.Equal(r.ROM, res.ROM) {
				t.Errorf("%s %d MB: estimate ROM ok=%v, identical %v", c.name, mb, ok, ok && bytes.Equal(r.ROM, res.ROM))
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := testClips()[0]
	if _, err := EstimateROM(ctx, c.frames, nil, c.fps, 0, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled estimate: %v", err)
	}
	if _, err := EstimateFit(ctx, c.frames, nil, c.fps, 1, nil, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled fit estimate: %v", err)
	}
}

// TestFit checks that the steered encode holds the whole clip within the
// limit, makes the ROM its estimate promised, and uses the room.
func TestFit(t *testing.T) {
	for _, c := range testClips() {
		audio := testAudio(len(c.frames), c.fps)
		cache := NewPaletteCache()
		hint, err := EstimateROM(context.Background(), c.frames, audio, c.fps, 4, cache)
		if err != nil {
			t.Fatal(err)
		}
		for _, mb := range []int{1, 2} {
			res, q, err := BuildROMFit(c.frames, audio, c.fps, mb, cache, &hint, nil)
			if err != nil {
				t.Fatal(err)
			}
			checkROM(t, c.name, res, mb)
			est, err := EstimateFit(context.Background(), c.frames, audio, c.fps, mb, cache, &hint)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(est.ROM, res.ROM) || est.Quality != q {
				t.Errorf("%s %d MB: the estimate's ROM differs from the compile's", c.name, mb)
			}
			t.Logf("%-8s %d MB: frames %d/%d, %d KB in use, quality %d %%", c.name, mb, res.FramesUsed, res.FramesTotal,
				res.Used>>10, PercentFromQuality(q))
			// The fit cuts only what does not fit at the lowest quality either.
			if res.Truncated() {
				low, err := BuildROM(c.frames, audio, c.fps, MaxQuality, mb, cache, nil)
				if err != nil {
					t.Fatal(err)
				}
				if !low.Truncated() {
					t.Errorf("%s %d MB: the fit cut the clip, quality %d %% holds it", c.name, mb, MinPercent)
				}
				continue
			}
			// Where the clip needs the room, the fit fills it.
			if q > 0 && res.Used < mb<<20*9/10 {
				t.Errorf("%s %d MB: quality %d but only %d KB used", c.name, mb, q, res.Used>>10)
			}
		}
	}
}
