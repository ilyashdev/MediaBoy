package main

import (
	"image"
	"image/color"
	"math"
)

type DitheringType int

const (
	DitheringNone           DitheringType = iota
	DitheringBayer                        // 8×8 ordered Bayer matrix
	DitheringFloydSteinberg               // error diffusion, full 16/16
	DitheringAtkinson                     // error diffusion, 6/8 spread
)

// applyDither dispatches to the selected algorithm. Called before any quantization.
func applyDither(img image.Image, cfg ConvertConfig) image.Image {
	if cfg.Dithering == DitheringNone || cfg.DitheringStrength <= 0 {
		return img
	}
	lvl := cfg.DitheringLevels
	if lvl < 2 {
		lvl = 4
	}
	switch cfg.Dithering {
	case DitheringBayer:
		return dithBayer(img, cfg.DitheringStrength)
	case DitheringFloydSteinberg:
		return errorDiffuse(img, lvl, cfg.DitheringStrength, fsOffsets)
	case DitheringAtkinson:
		// Atkinson only propagates 6/8 of the error; strength is scaled accordingly
		return errorDiffuse(img, lvl, cfg.DitheringStrength*0.75, atkOffsets)
	}
	return img
}

// Floyd-Steinberg neighbourhood: [dx, dy, weight]
var fsOffsets = [][3]float64{
	{1, 0, 7.0 / 16},
	{-1, 1, 3.0 / 16},
	{0, 1, 5.0 / 16},
	{1, 1, 1.0 / 16},
}

// Atkinson neighbourhood
var atkOffsets = [][3]float64{
	{1, 0, 1.0 / 8},
	{2, 0, 1.0 / 8},
	{-1, 1, 1.0 / 8},
	{0, 1, 1.0 / 8},
	{1, 1, 1.0 / 8},
	{0, 2, 1.0 / 8},
}

// Bayer 8×8 threshold matrix (values 0..63).
var bayerMatrix8 = [8][8]float64{
	{0, 32, 8, 40, 2, 34, 10, 42},
	{48, 16, 56, 24, 50, 18, 58, 26},
	{12, 44, 4, 36, 14, 46, 6, 38},
	{60, 28, 52, 20, 62, 30, 54, 22},
	{3, 35, 11, 43, 1, 33, 9, 41},
	{51, 19, 59, 27, 49, 17, 57, 25},
	{15, 47, 7, 39, 13, 45, 5, 37},
	{63, 31, 55, 23, 61, 29, 53, 21},
}

// dithBayer applies 8×8 ordered Bayer dithering.
// strength 0..1 → amplitude ±32 px (enough to shift between quantization levels).
func dithBayer(img image.Image, strength float64) image.Image {
	b := img.Bounds()
	out := image.NewRGBA(b)
	amp := strength * 64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			bx := (x - b.Min.X) & 7
			by := (y - b.Min.Y) & 7
			t := (bayerMatrix8[by][bx]/63.0 - 0.5) * amp
			c := toRGBFull(img.At(x, y))
			out.SetRGBA(x, y, color.RGBA{
				R: clampByte(c.R + t),
				G: clampByte(c.G + t),
				B: clampByte(c.B + t),
				A: 255,
			})
		}
	}
	return out
}

// errorDiffuse is the generic error-diffusion engine shared by FS and Atkinson.
func errorDiffuse(img image.Image, levels int, strength float64, offsets [][3]float64) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if levels < 2 {
		levels = 2
	}

	type pix struct{ r, g, bl float64 }
	buf := make([]pix, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := toRGBFull(img.At(b.Min.X+x, b.Min.Y+y))
			buf[y*w+x] = pix{c.R, c.G, c.B}
		}
	}

	step := 255.0 / float64(levels-1)
	qf := func(v float64) float64 { return math.Round(v/step) * step }

	out := image.NewRGBA(b)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			p := buf[y*w+x]
			nr, ng, nb := qf(p.r), qf(p.g), qf(p.bl)
			out.SetRGBA(b.Min.X+x, b.Min.Y+y, color.RGBA{
				R: clampByte(nr), G: clampByte(ng), B: clampByte(nb), A: 255,
			})
			er := (p.r - nr) * strength
			eg := (p.g - ng) * strength
			eb := (p.bl - nb) * strength
			for _, o := range offsets {
				nx, ny := x+int(o[0]), y+int(o[1])
				if nx >= 0 && nx < w && ny >= 0 && ny < h {
					buf[ny*w+nx].r += er * o[2]
					buf[ny*w+nx].g += eg * o[2]
					buf[ny*w+nx].bl += eb * o[2]
				}
			}
		}
	}
	return out
}

func clampByte(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}
