package video

import (
	"context"
	"image"
	"math/rand"
	"testing"
)

// letterboxed puts noise into the middle 90 lines of a frame, as a 16:9 clip
// letterboxed to 160×90, and leaves the bars black.
func letterboxed(rng *rand.Rand) *image.RGBA {
	img := noiseFrame(rng)
	for y := 0; y < 144; y++ {
		if y >= 27 && y < 117 {
			continue
		}
		row := img.Pix[y*img.Stride : y*img.Stride+160*4]
		for i := 0; i < len(row); i += 4 {
			row[i], row[i+1], row[i+2] = 0, 0, 0
		}
	}
	return img
}

func TestLetterboxBars(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	var boxed, full []image.Image
	for f := 0; f < 24; f++ {
		boxed = append(boxed, letterboxed(rng))
		full = append(full, noiseFrame(rng))
	}
	if !hasBars(boxed) || hasBars(full) {
		t.Fatalf("hasBars: letterboxed %v, full frame %v", hasBars(boxed), hasBars(full))
	}
	audio := testAudio(len(boxed), 24)
	var bytes [2]int
	for k, frames := range [][]image.Image{boxed, full} {
		res, err := BuildROM(context.Background(), frames, audio, 24, 4, 8, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		sim, st := checkROM(t, "letterbox", res, 8)
		bytes[k] = st.videoBytes
		if k == 1 {
			break
		}
		// Every bar pixel of every field shows pure black.
		for i, f := range sim {
			for field := 0; field < 2; field++ {
				for y := 0; y < 144; y++ {
					if y >= 27 && y < 117 {
						continue
					}
					for x := 0; x < 160; x++ {
						if c := f.palette[field][f.fields[field][y][x]]; c != [3]int{} {
							t.Fatalf("frame %d field %d: bar pixel (%d,%d) is %v", i, field, x, y, c)
						}
					}
				}
			}
		}
	}
	// The bars cost next to nothing: the letterboxed clip takes about the
	// picture's 90 of 144 lines.
	t.Logf("video bytes: letterboxed %d, full frame %d (%.0f%%)", bytes[0], bytes[1], float64(bytes[0])/float64(bytes[1])*100)
	if float64(bytes[0]) > 0.7*float64(bytes[1]) {
		t.Errorf("letterboxed clip takes %d bytes, full frame %d", bytes[0], bytes[1])
	}
}
