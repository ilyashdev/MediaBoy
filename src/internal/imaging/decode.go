package imaging

import (
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

func DecodeImageFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return png.Decode(f)
	case ".jpg", ".jpeg":
		return jpeg.Decode(f)
	default:
		img, _, err := image.Decode(f)
		return img, err
	}
}

func ToRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok {
		return r
	}
	b := img.Bounds()
	r := image.NewRGBA(b)
	draw.Draw(r, b, img, b.Min, draw.Src)
	return r
}
