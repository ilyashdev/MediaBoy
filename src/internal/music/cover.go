package music

import (
	"image"

	"MediaBoy/internal/core"
	"MediaBoy/internal/imaging"

	xdraw "golang.org/x/image/draw"
)

const (
	coverTilesW = 16
	coverTilesH = 14
	coverPxW    = coverTilesW * 8
	coverPxH    = coverTilesH * 8
)

func resizeCoverFill(src image.Image) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, coverPxW, coverPxH))
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw <= 0 || sh <= 0 {
		return dst
	}

	targetAR := float64(coverPxW) / float64(coverPxH)
	srcAR := float64(sw) / float64(sh)

	cw, ch := sw, sh
	if srcAR > targetAR {
		cw = int(float64(sh) * targetAR)
	} else {
		ch = int(float64(sw) / targetAR)
	}
	if cw < 1 {
		cw = 1
	}
	if ch < 1 {
		ch = 1
	}
	ox := sb.Min.X + (sw-cw)/2
	oy := sb.Min.Y + (sh-ch)/2
	srcRect := image.Rect(ox, oy, ox+cw, oy+ch)

	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, srcRect, xdraw.Over, nil)
	return dst
}

func coverTiles(src image.Image) ([][]core.Tile, [8]core.Palette) {
	return imaging.Tilefication(resizeCoverFill(src))
}

func coverTilesDMG(src image.Image) ([][]core.Tile, [8]core.Palette) {
	return imaging.TileficationDMG(resizeCoverFill(src))
}
