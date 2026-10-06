package imaging

import (
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"math"
	"runtime"
	"sort"

	"MediaBoy/internal/core"
	"MediaBoy/internal/safe"
)

func downscaleMedian(inp image.Image, s int) image.Image {
	if s < 1 {
		s = 1
	}
	b := inp.Bounds()
	outW := b.Dx() / s
	outH := b.Dy() / s
	if outW < 1 {
		outW = 1
	}
	if outH < 1 {
		outH = 1
	}
	out := image.NewRGBA(image.Rect(0, 0, outW, outH))
	for oy := 0; oy < outH; oy++ {
		for ox := 0; ox < outW; ox++ {
			pixels := make([]core.RGB, 0, s*s)
			for y := 0; y < s; y++ {
				for x := 0; x < s; x++ {
					pixels = append(pixels, core.ToRGBFull(inp.At(b.Min.X+ox*s+x, b.Min.Y+oy*s+y)))
				}
			}
			rs := make([]float64, len(pixels))
			gs := make([]float64, len(pixels))
			bs := make([]float64, len(pixels))
			for i, p := range pixels {
				rs[i] = p.R
				gs[i] = p.G
				bs[i] = p.B
			}
			sort.Float64s(rs)
			sort.Float64s(gs)
			sort.Float64s(bs)
			mid := len(pixels) / 2
			out.Set(ox, oy, color.RGBA{
				R: uint8(rs[mid]),
				G: uint8(gs[mid]),
				B: uint8(bs[mid]),
				A: 255,
			})
		}
	}
	return out
}

func downscaleBilinear(inp image.Image, s int) image.Image {
	if s < 1 {
		s = 1
	}
	b := inp.Bounds()
	outW := b.Dx() / s
	outH := b.Dy() / s
	if outW < 1 {
		outW = 1
	}
	if outH < 1 {
		outH = 1
	}
	out := image.NewRGBA(image.Rect(0, 0, outW, outH))
	for oy := 0; oy < outH; oy++ {
		for ox := 0; ox < outW; ox++ {
			var rSum, gSum, bSum float64
			count := 0
			for y := 0; y < s; y++ {
				for x := 0; x < s; x++ {
					c := core.ToRGB(inp.At(b.Min.X+ox*s+x, b.Min.Y+oy*s+y))
					rSum += c.R
					gSum += c.G
					bSum += c.B
					count++
				}
			}
			n := float64(count)
			out.Set(ox, oy, color.RGBA{
				R: uint8(rSum / n),
				G: uint8(gSum / n),
				B: uint8(bSum / n),
				A: 255,
			})
		}
	}
	return out
}

func downscaleNearest(inp image.Image, s int) image.Image {
	if s < 1 {
		s = 1
	}
	b := inp.Bounds()
	outW := b.Dx() / s
	outH := b.Dy() / s
	if outW < 1 {
		outW = 1
	}
	if outH < 1 {
		outH = 1
	}
	out := image.NewRGBA(image.Rect(0, 0, outW, outH))
	for oy := 0; oy < outH; oy++ {
		for ox := 0; ox < outW; ox++ {
			c := core.ToRGB(inp.At(b.Min.X+ox*s, b.Min.Y+oy*s))
			out.Set(ox, oy, color.RGBA{
				R: uint8(c.R),
				G: uint8(c.G),
				B: uint8(c.B),
				A: 255,
			})
		}
	}
	return out
}

func sharpenWithAmount(inp image.Image, amount float64) image.Image {
	if amount <= 0 {
		return inp
	}
	b := inp.Bounds()
	out := image.NewRGBA(b)
	center := 1 + 8*amount
	neigh := -amount
	kernel := [3][3]float64{
		{neigh, neigh, neigh},
		{neigh, center, neigh},
		{neigh, neigh, neigh},
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			var r, g, bl float64
			for ky := -1; ky <= 1; ky++ {
				for kx := -1; kx <= 1; kx++ {
					nx := x + kx
					ny := y + ky
					if nx < b.Min.X {
						nx = b.Min.X
					}
					if nx >= b.Max.X {
						nx = b.Max.X - 1
					}
					if ny < b.Min.Y {
						ny = b.Min.Y
					}
					if ny >= b.Max.Y {
						ny = b.Max.Y - 1
					}
					c := core.ToRGB(inp.At(nx, ny))
					w := kernel[ky+1][kx+1]
					r += c.R * w
					g += c.G * w
					bl += c.B * w
				}
			}
			out.Set(x, y, color.RGBA{
				R: uint8(math.Max(0, math.Min(255, r))),
				G: uint8(math.Max(0, math.Min(255, g))),
				B: uint8(math.Max(0, math.Min(255, bl))),
				A: 255,
			})
		}
	}
	return out
}

func applyPosterize(img image.Image, levels int) image.Image {
	if levels < 2 {
		levels = 2
	}
	b := img.Bounds()
	out := image.NewRGBA(b)
	step := 255.0 / float64(levels-1)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := core.ToRGBFull(img.At(x, y))
			r := math.Round(c.R/step) * step
			g := math.Round(c.G/step) * step
			bv := math.Round(c.B/step) * step
			out.SetRGBA(x, y, color.RGBA{
				R: uint8(math.Min(255, r)),
				G: uint8(math.Min(255, g)),
				B: uint8(math.Min(255, bv)),
				A: 255,
			})
		}
	}
	return out
}

func bilateralFilter(inp image.Image, radius int, sigmaColor float64) image.Image {
	b := inp.Bounds()
	out := image.NewRGBA(b)

	sigmaColor2 := 2 * sigmaColor * sigmaColor
	sigmaSpace2 := 2 * float64(radius*radius)

	numWorkers := runtime.NumCPU()
	rows := make(chan int, b.Dy())
	var wg safe.Group

	for i := 0; i < numWorkers; i++ {
		wg.Go(func() {
			for y := range rows {
				for x := b.Min.X; x < b.Max.X; x++ {
					var rSum, gSum, bSum, wSum float64
					c0 := core.ToRGB(inp.At(x, y))
					r0, g0, b0 := c0.R, c0.G, c0.B

					for dy := -radius; dy <= radius; dy++ {
						ny := y + dy
						if ny < b.Min.Y || ny >= b.Max.Y {
							continue
						}
						for dx := -radius; dx <= radius; dx++ {
							nx := x + dx
							if nx < b.Min.X || nx >= b.Max.X {
								continue
							}
							c1 := core.ToRGB(inp.At(nx, ny))
							r1, g1, b1 := c1.R, c1.G, c1.B

							spaceDist := float64(dx*dx + dy*dy)
							colorDist := (r0-r1)*(r0-r1) + (g0-g1)*(g0-g1) + (b0-b1)*(b0-b1)
							w := math.Exp(-spaceDist/sigmaSpace2 - colorDist/sigmaColor2)

							rSum += r1 * w
							gSum += g1 * w
							bSum += b1 * w
							wSum += w
						}
					}

					out.Set(x, y, color.RGBA{
						R: uint8(rSum / wSum),
						G: uint8(gSum / wSum),
						B: uint8(bSum / wSum),
						A: 255,
					})
				}
			}
		})
	}

	for y := b.Min.Y; y < b.Max.Y; y++ {
		rows <- y
	}
	close(rows)
	wg.Wait()

	return out
}

func cropToRect(inp image.Image, rect image.Rectangle) image.Image {
	rect = rect.Intersect(inp.Bounds())
	if si, ok := inp.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return si.SubImage(rect)
	}
	out := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			out.Set(x-rect.Min.X, y-rect.Min.Y, inp.At(x, y))
		}
	}
	return out
}

func toGrayscale(inp image.Image) image.Image {
	b := inp.Bounds()
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := core.ToRGBFull(inp.At(x, y))
			lum := 0.299*c.R + 0.587*c.G + 0.114*c.B
			gray := uint8(lum)
			out.Set(x, y, color.RGBA{R: gray, G: gray, B: gray, A: 255})
		}
	}
	return out
}

func GIFToFrames(m *gif.GIF) []*image.RGBA {
	width := m.Config.Width
	height := m.Config.Height
	frames := make([]*image.RGBA, len(m.Image))

	canvas := image.NewRGBA(image.Rect(0, 0, width, height))

	for i, frame := range m.Image {
		draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
		snapshot := image.NewRGBA(canvas.Bounds())
		draw.Draw(snapshot, canvas.Bounds(), canvas, canvas.Bounds().Min, draw.Src)
		frames[i] = snapshot
		if i < len(m.Disposal) {
			switch m.Disposal[i] {
			case gif.DisposalBackground:
				draw.Draw(canvas, frame.Bounds(), image.Transparent, image.Point{}, draw.Src)
			case gif.DisposalPrevious:
				if i > 0 {
					draw.Draw(canvas, frame.Bounds(), frames[i-1], frame.Bounds().Min, draw.Src)
				}
			}
		}
	}
	return frames
}

func upscale(img image.Image, factor int) image.Image {
	if factor <= 1 {
		return img
	}
	bounds := img.Bounds()
	newW := bounds.Dx() * factor
	newH := bounds.Dy() * factor
	out := image.NewRGBA(image.Rect(0, 0, newW, newH))

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := img.At(x, y)
			for dy := 0; dy < factor; dy++ {
				for dx := 0; dx < factor; dx++ {
					out.Set((x-bounds.Min.X)*factor+dx, (y-bounds.Min.Y)*factor+dy, c)
				}
			}
		}
	}
	return out
}

func scale2x(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := image.NewRGBA(image.Rect(0, 0, w*2, h*2))

	get := func(x, y int) color.RGBA {
		if x < b.Min.X {
			x = b.Min.X
		}
		if x >= b.Max.X {
			x = b.Max.X - 1
		}
		if y < b.Min.Y {
			y = b.Min.Y
		}
		if y >= b.Max.Y {
			y = b.Max.Y - 1
		}
		r, g, bv, a := img.At(x, y).RGBA()
		return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(bv >> 8), uint8(a >> 8)}
	}

	eqColor := func(a, bv color.RGBA) bool {
		return a.R == bv.R && a.G == bv.G && a.B == bv.B
	}

	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			P := get(x, y)
			B := get(x, y-1)
			D := get(x-1, y)
			F := get(x+1, y)
			H := get(x, y+1)

			ox := (x - b.Min.X) * 2
			oy := (y - b.Min.Y) * 2

			var e0, e1, e2, e3 color.RGBA
			if !eqColor(D, F) && !eqColor(B, H) {
				if eqColor(D, B) {
					e0 = D
				} else {
					e0 = P
				}
				if eqColor(B, F) {
					e1 = F
				} else {
					e1 = P
				}
				if eqColor(D, H) {
					e2 = D
				} else {
					e2 = P
				}
				if eqColor(H, F) {
					e3 = F
				} else {
					e3 = P
				}
			} else {
				e0, e1, e2, e3 = P, P, P, P
			}

			out.Set(ox, oy, e0)
			out.Set(ox+1, oy, e1)
			out.Set(ox, oy+1, e2)
			out.Set(ox+1, oy+1, e3)
		}
	}
	return out
}

func upscaleWithMode(img image.Image, factor int, mode core.UpscalerType) image.Image {
	if factor <= 1 {
		return img
	}
	switch mode {
	case core.UpscalerScale2x:
		doubled := scale2x(img)
		if factor > 2 {
			return upscale(doubled, factor/2)
		}
		return doubled
	default:
		return upscale(img, factor)
	}
}
