package music

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"MediaBoy/internal/core"
	"MediaBoy/internal/imaging"
)

const (
	titleTilesW    = 16
	titleTilesH    = 2
	titlePxW       = titleTilesW * 8
	titlePxH       = titleTilesH * 8
	titleTileCount = titleTilesW * titleTilesH
	uiPaletteIdx   = 7

	titleTileX = 2
	titleTileY = 15
)

var uiPalette = core.Palette{Colors: [4]core.RGB{
	{16, 16, 24}, {248, 248, 248}, {248, 248, 248}, {248, 248, 248},
}}

func renderTitleImage(title string) *image.Gray {
	img := image.NewGray(image.Rect(0, 0, titlePxW, titlePxH))
	face := basicfont.Face7x13
	text := truncateToWidth(title, face, titlePxW-2)
	if text == "" {
		return img
	}
	w := font.MeasureString(face, text).Ceil()
	x := (titlePxW - w) / 2
	if x < 0 {
		x = 0
	}
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(color.Gray{Y: 255}),
		Face: face,
		Dot:  fixed.P(x, 12),
	}
	d.DrawString(text)
	return img
}

func truncateToWidth(s string, face font.Face, maxW int) string {
	if font.MeasureString(face, s).Ceil() <= maxW {
		return s
	}
	r := []rune(s)
	for len(r) > 1 {
		r = r[:len(r)-1]
		cand := string(r) + "…"
		if font.MeasureString(face, cand).Ceil() <= maxW {
			return cand
		}
	}
	return string(r)
}

func titleTiles(title string) [][]core.Tile {
	img := renderTitleImage(title)
	tiles := make([][]core.Tile, titleTilesH)
	for ty := 0; ty < titleTilesH; ty++ {
		tiles[ty] = make([]core.Tile, titleTilesW)
		for tx := 0; tx < titleTilesW; tx++ {
			t := core.Tile{Palette: uiPaletteIdx}
			for y := 0; y < 8; y++ {
				for x := 0; x < 8; x++ {
					if img.GrayAt(tx*8+x, ty*8+y).Y > 127 {

						t.Pixels[y][x] = 3
					}
				}
			}
			tiles[ty][tx] = t
		}
	}
	return tiles
}

func reserveUIPalette(tiles [][]core.Tile, pals *[8]core.Palette) {
	best, bestD := 0, math.MaxFloat64
	for i := 0; i < uiPaletteIdx; i++ {
		if d := paletteDist(pals[i], pals[uiPaletteIdx]); d < bestD {
			bestD, best = d, i
		}
	}
	for y := range tiles {
		for x := range tiles[y] {
			if tiles[y][x].Palette == uiPaletteIdx {
				remapTilePalette(&tiles[y][x], pals[uiPaletteIdx], pals[best], uint8(best))
			}
		}
	}
	pals[uiPaletteIdx] = uiPalette
}

func remapTilePalette(t *core.Tile, from, to core.Palette, toIdx uint8) {
	toColors := to.Colors[:]
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			rgb := from.Colors[t.Pixels[y][x]&3]
			t.Pixels[y][x] = uint8(core.NearestIdx(rgb, toColors))
		}
	}
	t.Palette = toIdx
}

func paletteDist(a, b core.Palette) float64 {
	sum := 0.0
	for _, c := range a.Colors {
		sum += core.ColorDist(c, b.Colors[core.NearestIdx(c, b.Colors[:])])
	}
	return sum
}

func RenderDevicePreview(sg *core.Song) image.Image {
	canvas := image.NewRGBA(image.Rect(0, 0, core.ScreenW, core.ScreenH))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.RGBA{8, 8, 12, 255}), image.Point{}, draw.Src)

	if sg.Cover != nil {
		ct, cp := coverTiles(sg.Cover)
		reserveUIPalette(ct, &cp)
		cov := imaging.TilesToImage(ct, cp)
		draw.Draw(canvas, image.Rect(16, 8, 16+coverPxW, 8+coverPxH), cov, image.Point{}, draw.Src)
	}

	var tpals [8]core.Palette
	tpals[uiPaletteIdx] = uiPalette
	timg := imaging.TilesToImage(titleTiles(sg.DisplayTitle()), tpals)
	draw.Draw(canvas, image.Rect(16, 120, 16+titlePxW, 120+titlePxH), timg, image.Point{}, draw.Src)

	return canvas
}
