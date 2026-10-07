package imaging

import (
	"image"
	"image/draw"

	"MediaBoy/internal/core"

	xdraw "golang.org/x/image/draw"
)

// LetterboxRect is where a w×h picture goes when it is fitted whole into the
// GB screen: as large as fits, centred, with black bars around it. Its sides
// are even so the two bars are the same size.
func LetterboxRect(w, h int) image.Rectangle {
	sw, sh := core.ScreenW, core.ScreenH
	if w <= 0 || h <= 0 {
		return image.Rect(0, 0, sw, sh)
	}
	cw, ch := sw, sh
	if w*sh >= h*sw { // wider than the screen: bars above and below
		ch = min(sh, (h*sw+w/2)/w)
	} else {
		cw = min(sw, (w*sh+h/2)/h)
	}
	cw, ch = max(2, cw&^1), max(2, ch&^1)
	x, y := (sw-cw)/2, (sh-ch)/2
	return image.Rect(x, y, x+cw, y+ch)
}

// letterbox draws img scaled into r on a black screen-sized frame.
func letterbox(img image.Image, r image.Rectangle) *image.RGBA {
	dst := blackScreen()
	b := img.Bounds()
	if b.Size() == r.Size() {
		draw.Draw(dst, r, img, b.Min, draw.Src)
	} else {
		xdraw.CatmullRom.Scale(dst, r, img, b, xdraw.Src, nil)
	}
	return dst
}

// clearBars makes everything outside r pure black, so the bars stay exactly
// the same from frame to frame whatever the filters did near their edges. An
// empty r leaves img as it is.
func clearBars(img image.Image, r image.Rectangle) image.Image {
	if r.Empty() {
		return img
	}
	dst := blackScreen()
	draw.Draw(dst, r, img, r.Min, draw.Src)
	return dst
}

func blackScreen() *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, core.ScreenW, core.ScreenH))
	for i := 3; i < len(dst.Pix); i += 4 {
		dst.Pix[i] = 255
	}
	return dst
}
