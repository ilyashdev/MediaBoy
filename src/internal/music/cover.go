package music

import (
	"image"
	"image/color"

	xdraw "golang.org/x/image/draw"
)

const (
	coverTilesW = 16
	coverTilesH = 14
	coverPxW    = coverTilesW * 8
	coverPxH    = coverTilesH * 8
)

// resizeCoverFill scales src to fill the cover box, cropping the overflow
// from the middle; transparency shows bg.
func resizeCoverFill(src image.Image, bg color.RGBA) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, coverPxW, coverPxH))
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw <= 0 || sh <= 0 {
		return dst
	}
	src = flatten(src, bg)
	sb = src.Bounds()

	targetAR := float64(coverPxW) / float64(coverPxH)
	srcAR := float64(sw) / float64(sh)

	cw, ch := sw, sh
	if srcAR > targetAR {
		cw = int(float64(sh) * targetAR)
	} else {
		ch = int(float64(sw) / targetAR)
	}
	cw, ch = max(cw, 1), max(ch, 1)
	ox := sb.Min.X + (sw-cw)/2
	oy := sb.Min.Y + (sh-ch)/2
	srcRect := image.Rect(ox, oy, ox+cw, oy+ch)

	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, srcRect, xdraw.Src, nil)
	return dst
}
