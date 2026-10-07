package imaging

import (
	"image"
	"image/color"
	"testing"

	"MediaBoy/internal/core"
)

func TestLetterboxRect(t *testing.T) {
	for _, c := range []struct {
		w, h int
		want image.Rectangle
	}{
		{1920, 1080, image.Rect(0, 27, 160, 117)}, // 16:9: bars above and below
		{640, 480, image.Rect(0, 12, 160, 132)},   // 4:3
		{1080, 1920, image.Rect(40, 0, 120, 144)}, // 9:16: bars at the sides
		{160, 144, image.Rect(0, 0, 160, 144)},    // the screen's own shape
		{320, 288, image.Rect(0, 0, 160, 144)},
	} {
		if got := LetterboxRect(c.w, c.h); got != c.want {
			t.Errorf("LetterboxRect(%d, %d) = %v, want %v", c.w, c.h, got, c.want)
		}
	}
}

func TestPipelineFrameLetterbox(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 320, 180))
	for i := 0; i < len(src.Pix); i += 4 {
		src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = 200, 120, 40, 255
	}
	cfg := core.DefaultConfig()
	cfg.Letterbox = true
	cfg.Scaling = core.ScalingBilinear
	out, err := RunGBPipelineFrame(src, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if b := out.Bounds(); b != image.Rect(0, 0, core.ScreenW, core.ScreenH) {
		t.Fatalf("frame is %v", b)
	}
	picture := LetterboxRect(320, 180)
	for y := 0; y < core.ScreenH; y++ {
		for x := 0; x < core.ScreenW; x++ {
			r, g, b, _ := out.At(x, y).RGBA()
			inside := image.Pt(x, y).In(picture)
			if !inside && r|g|b != 0 {
				t.Fatalf("bar pixel (%d,%d) is %v, not black", x, y, out.At(x, y))
			}
			if inside && r>>8 < 100 {
				t.Fatalf("picture pixel (%d,%d) is %v", x, y, color.RGBAModel.Convert(out.At(x, y)))
			}
		}
	}
}
