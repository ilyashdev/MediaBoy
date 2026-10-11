package music

import (
	"image"
	"image/color"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

const (
	titleTilesW = 16
	titleTilesH = 2
	titlePxW    = titleTilesW * 8
	titlePxH    = titleTilesH * 8
)

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
