package imaging

import (
	"image"
	"image/color"
	"math"
	"sort"

	"MediaBoy/internal/core"
)

func Tilefication(inp image.Image) ([][]core.Tile, [8]core.Palette) {
	b := inp.Bounds()
	tileX := b.Dx() / 8
	tileY := b.Dy() / 8
	if tileX == 0 || tileY == 0 {
		return nil, [8]core.Palette{}
	}
	tilePixels := make([][][]core.RGB, tileY)
	for ty := 0; ty < tileY; ty++ {
		tilePixels[ty] = make([][]core.RGB, tileX)
		for tx := 0; tx < tileX; tx++ {
			pixels := make([]core.RGB, 0, 64)
			for y := 0; y < 8; y++ {
				for x := 0; x < 8; x++ {
					c := core.ToRGBFull(inp.At(b.Min.X+tx*8+x, b.Min.Y+ty*8+y))
					pixels = append(pixels, c)
				}
			}
			tilePixels[ty][tx] = pixels
		}
	}
	tileMeans := make([]core.RGB, tileY*tileX)
	for ty := 0; ty < tileY; ty++ {
		for tx := 0; tx < tileX; tx++ {
			var r, g, bl float64
			for _, p := range tilePixels[ty][tx] {
				r += p.R
				g += p.G
				bl += p.B
			}
			n := float64(len(tilePixels[ty][tx]))
			tileMeans[ty*tileX+tx] = core.RGB{r / n, g / n, bl / n}
		}
	}

	groupCenters := core.KMeans(tileMeans, 8, 20)

	tileAssign := make([][]int, tileY)
	for ty := range tileAssign {
		tileAssign[ty] = make([]int, tileX)
		for tx := 0; tx < tileX; tx++ {
			tileAssign[ty][tx] = core.NearestIdx(tileMeans[ty*tileX+tx], groupCenters)
		}
	}

	var palettes [8]core.Palette
	for pi := range palettes {
		var bucket []core.RGB
		for ty := 0; ty < tileY; ty++ {
			for tx := 0; tx < tileX; tx++ {
				if tileAssign[ty][tx] == pi {
					bucket = append(bucket, tilePixels[ty][tx]...)
				}
			}
		}
		var colors []core.RGB
		switch {
		case len(bucket) == 0:
			colors = []core.RGB{{0, 0, 0}, {85, 85, 85}, {170, 170, 170}, {255, 255, 255}}
		case len(bucket) <= 4:
			colors = make([]core.RGB, 4)
			for j := 0; j < 4; j++ {
				colors[j] = bucket[j%len(bucket)]
			}
		default:
			colors = core.KMeans(bucket, 4, 20)
		}
		copy(palettes[pi].Colors[:], colors[:4])
	}
	for iter := 0; iter < 10; iter++ {
		for ty := 0; ty < tileY; ty++ {
			for tx := 0; tx < tileX; tx++ {
				bestPal, bestErr := 0, math.MaxFloat64
				for pi, pal := range palettes {
					var err float64
					for _, px := range tilePixels[ty][tx] {
						best := math.MaxFloat64
						for _, pc := range pal.Colors {
							if d := core.ColorDist(px, pc); d < best {
								best = d
							}
						}
						err += best
					}
					if err < bestErr {
						bestErr = err
						bestPal = pi
					}
				}
				tileAssign[ty][tx] = bestPal
			}
		}
		for pi := range palettes {
			var bucket []core.RGB
			for ty := 0; ty < tileY; ty++ {
				for tx := 0; tx < tileX; tx++ {
					if tileAssign[ty][tx] == pi {
						bucket = append(bucket, tilePixels[ty][tx]...)
					}
				}
			}
			if len(bucket) == 0 {
				continue
			}
			colors := core.KMeans(bucket, 4, 10)
			copy(palettes[pi].Colors[:], colors[:4])
		}
	}

	for pi := range palettes {
		for ci := range palettes[pi].Colors {
			palettes[pi].Colors[ci] = core.RoundRGB555(palettes[pi].Colors[ci])
		}
	}

	tiles := make([][]core.Tile, tileY)
	for ty := 0; ty < tileY; ty++ {
		tiles[ty] = make([]core.Tile, tileX)
		for tx := 0; tx < tileX; tx++ {
			var t core.Tile
			t.Palette = uint8(tileAssign[ty][tx])
			pal := palettes[t.Palette]
			for y := 0; y < 8; y++ {
				for x := 0; x < 8; x++ {
					px := tilePixels[ty][tx][y*8+x]
					best, bestDist := 0, math.MaxFloat64
					for ci, pc := range pal.Colors {
						if d := core.ColorDist(px, pc); d < bestDist {
							bestDist = d
							best = ci
						}
					}
					t.Pixels[y][x] = uint8(best)
				}
			}
			tiles[ty][tx] = t
		}
	}

	return tiles, palettes
}

func DedupTilesForPrint(tiles [][]core.Tile) (uniq []core.Tile, tmap []uint8, w, h int) {
	h = len(tiles)
	if h > 0 {
		w = len(tiles[0])
	}
	idxOf := map[core.Tile]int{}
	tmap = make([]uint8, 0, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			t := tiles[y][x]
			id, ok := idxOf[t]
			if !ok {
				if len(uniq) < 256 {
					id = len(uniq)
					idxOf[t] = id
					uniq = append(uniq, t)
				} else {
					id = nearestTileIdx(t, uniq)
				}
			}
			tmap = append(tmap, uint8(id))
		}
	}
	return uniq, tmap, w, h
}

func nearestTileIdx(t core.Tile, pool []core.Tile) int {
	best, bestD := 0, 1<<30
	for i := range pool {
		d := 0
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				dd := int(t.Pixels[y][x]) - int(pool[i].Pixels[y][x])
				if dd < 0 {
					dd = -dd
				}
				d += dd
			}
		}
		if d < bestD {
			bestD = d
			best = i
		}
	}
	return best
}

func TileficationDMG(inp image.Image) ([][]core.Tile, [8]core.Palette) {
	gray := toGrayscale(inp)
	b := gray.Bounds()
	tileX := b.Dx() / 8
	tileY := b.Dy() / 8

	allPixels := make([]core.RGB, 0, b.Dx()*b.Dy())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			allPixels = append(allPixels, core.ToRGBFull(gray.At(x, y)))
		}
	}

	centers := core.KMeans(allPixels, 4, 30)

	sort.Slice(centers, func(i, j int) bool {
		return centers[i].R > centers[j].R
	})

	for i := range centers {
		centers[i] = core.RoundRGB555(centers[i])
	}

	var palettes [8]core.Palette
	copy(palettes[0].Colors[:], centers)
	for i := 1; i < 8; i++ {
		palettes[i] = palettes[0]
	}

	tiles := make([][]core.Tile, tileY)
	for ty := 0; ty < tileY; ty++ {
		tiles[ty] = make([]core.Tile, tileX)
		for tx := 0; tx < tileX; tx++ {
			var t core.Tile
			t.Palette = 0
			for y := 0; y < 8; y++ {
				for x := 0; x < 8; x++ {
					px := core.ToRGBFull(gray.At(b.Min.X+tx*8+x, b.Min.Y+ty*8+y))
					t.Pixels[y][x] = uint8(core.NearestIdx(px, centers))
				}
			}
			tiles[ty][tx] = t
		}
	}

	return tiles, palettes
}

func quantizeKmeans(inp image.Image, numColors int) image.Image {
	if numColors < 2 {
		numColors = 2
	}
	b := inp.Bounds()
	pixels := make([]core.RGB, 0, b.Dx()*b.Dy())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			pixels = append(pixels, core.ToRGBFull(inp.At(x, y)))
		}
	}
	centers := core.KMeans(pixels, numColors, 20)

	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			px := core.ToRGBFull(inp.At(x, y))
			c := centers[core.NearestIdx(px, centers)]
			out.Set(x-b.Min.X, y-b.Min.Y, color.RGBA{
				R: uint8(c.R), G: uint8(c.G), B: uint8(c.B), A: 255,
			})
		}
	}
	return out
}

func TilesToImage(tiles [][]core.Tile, palettes [8]core.Palette) image.Image {
	tileY := len(tiles)
	if tileY == 0 {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	tileX := len(tiles[0])

	out := image.NewRGBA(image.Rect(0, 0, tileX*8, tileY*8))

	for ty := 0; ty < tileY; ty++ {
		for tx := 0; tx < tileX; tx++ {
			t := tiles[ty][tx]
			pal := palettes[t.Palette]

			for y := 0; y < 8; y++ {
				for x := 0; x < 8; x++ {
					c := pal.Colors[t.Pixels[y][x]]
					out.Set(tx*8+x, ty*8+y, color.RGBA{
						R: uint8(c.R),
						G: uint8(c.G),
						B: uint8(c.B),
						A: 255,
					})
				}
			}
		}
	}

	return out
}
