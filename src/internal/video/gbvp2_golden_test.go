package video

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// GBVP2_GOLDEN_WRITE=<dir> stores encoder outputs, GBVP2_GOLDEN_CHECK=<dir>
// compares the current encoder against them byte for byte.

type goldenCase struct {
	name    string
	frames  []image.Image
	fps     float64
	maxMB   int
	quality []int
}

func solid(c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 160, 144))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, 255
	}
	return img
}

func goldenCases() []goldenCase {
	rng := rand.New(rand.NewSource(42))

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

	var noise []image.Image
	for f := 0; f < 8; f++ {
		img := image.NewRGBA(image.Rect(0, 0, 160, 144))
		rng.Read(img.Pix)
		for i := 3; i < len(img.Pix); i += 4 {
			img.Pix[i] = 255
		}
		noise = append(noise, img)
	}

	// Mostly static frames: exercises the "same frame" counter path and the
	// count > 255 split, plus flat images where most palette slots are empty.
	var static []image.Image
	base := solid(color.RGBA{10, 200, 90, 255})
	for f := 0; f < 20; f++ {
		if f%7 == 6 {
			static = append(static, solid(color.RGBA{uint8(f * 12), 30, uint8(250 - f*10), 255}))
		} else {
			static = append(static, base)
		}
	}

	// Large stream of noise for the truncation path with a 1 MB cap.
	var long []image.Image
	for f := 0; f < 200; f++ {
		img := image.NewRGBA(image.Rect(0, 0, 160, 144))
		rng.Read(img.Pix)
		for i := 3; i < len(img.Pix); i += 4 {
			img.Pix[i] = 255
		}
		long = append(long, img)
	}

	return []goldenCase{
		{"motion24", motion, 24, 8, []int{0, 7, 20, 64}},
		{"motion60", motion, 60, 8, []int{0, 20}},
		{"noise10", noise, 10, 8, []int{0, 64}},
		{"static1", static, 1, 8, []int{0, 20}},
		{"static30", static, 30, 8, []int{7}},
		{"long1mb", long, 30, 1, []int{0, 64}},
	}
}

func goldenAudio() []byte {
	rng := rand.New(rand.NewSource(7))
	a := make([]byte, 30000)
	rng.Read(a)
	return a
}

func buildGolden(t *testing.T, gc goldenCase, q int, cache *GBVP2Cache) GBVP2Result {
	t.Helper()
	dir := t.TempDir()
	player := filepath.Join(dir, "player.gbc")
	os.WriteFile(player, bytes.Repeat([]byte{0x11}, 0x8000), 0644)
	res, err := BuildGBVP2ROM(dir, player, gc.frames, goldenAudio(), gc.fps, q, gc.maxMB, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func goldenName(gc goldenCase, q int) string { return fmt.Sprintf("%s_q%d.bin", gc.name, q) }

func TestGBVP2Golden(t *testing.T) {
	wdir, cdir := os.Getenv("GBVP2_GOLDEN_WRITE"), os.Getenv("GBVP2_GOLDEN_CHECK")
	if wdir == "" && cdir == "" {
		t.Skip("set GBVP2_GOLDEN_WRITE or GBVP2_GOLDEN_CHECK")
	}
	for _, gc := range goldenCases() {
		for _, q := range gc.quality {
			res := buildGolden(t, gc, q, nil)
			sum := sha256.Sum256(res.ROM)
			t.Logf("%-10s q%-2d frames %d/%d banks %d sha %x", gc.name, q, res.FramesUsed, res.FramesTotal, res.Banks, sum[:8])
			if wdir != "" {
				os.WriteFile(filepath.Join(wdir, goldenName(gc, q)), res.ROM, 0644)
				continue
			}
			want, err := os.ReadFile(filepath.Join(cdir, goldenName(gc, q)))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(want, res.ROM) {
				t.Errorf("%s q%d: ROM differs from golden", gc.name, q)
			}
		}
	}
	if cdir == "" {
		return
	}
	// One cache shared by every build of a clip, qualities visited in a mixed
	// order and twice, as fitQuality does.
	for _, gc := range goldenCases() {
		cache := NewGBVP2Cache()
		var order []int
		for i := len(gc.quality) - 1; i >= 0; i-- {
			order = append(order, gc.quality[i])
		}
		order = append(order, gc.quality...)
		for _, q := range order {
			res := buildGolden(t, gc, q, cache)
			want, _ := os.ReadFile(filepath.Join(cdir, goldenName(gc, q)))
			if !bytes.Equal(want, res.ROM) {
				t.Errorf("%s q%d (cached): ROM differs from golden", gc.name, q)
			}
		}
		t.Logf("%-10s cached order %v ok, %d palette entries", gc.name, order, len(cache.entries))
	}
}
