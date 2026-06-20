package main

import (
	"fmt"
	"image"
	"image/jpeg"
	"os"

	xdraw "golang.org/x/image/draw"
)

// ensureGBSize forces an image to exactly 160×144. The crop + integer-factor
// downscale can land a few pixels short (e.g. 160×138) when the source isn't a
// clean multiple of the GB screen — and png2hicolorgb / tilefication require an
// exact 160×144 frame, otherwise the ROM renders garbage.
func ensureGBSize(img image.Image) image.Image {
	b := img.Bounds()
	if b.Dx() == gbW && b.Dy() == gbH && b.Min.X == 0 && b.Min.Y == 0 {
		return img
	}
	dst := image.NewRGBA(image.Rect(0, 0, gbW, gbH))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	return dst
}

type BilCache struct {
	origSrc  image.Image
	cropRect image.Rectangle
	radius   int
	sigma    float64
	result   image.Image
}

func (c *BilCache) get(origSrc image.Image, cr image.Rectangle, radius int, sigma float64) (image.Image, bool) {
	if c.result != nil && c.origSrc == origSrc && c.cropRect == cr && c.radius == radius && c.sigma == sigma {
		return c.result, true
	}
	return nil, false
}

func (c *BilCache) put(origSrc image.Image, cr image.Rectangle, radius int, sigma float64, result image.Image) {
	c.origSrc, c.cropRect, c.radius, c.sigma, c.result = origSrc, cr, radius, sigma, result
}

type ConvertResult struct {
	ProcessedImage image.Image
	Tiles          [][]Tile
	Palettes       [8]Palette

	FullColor image.Image
}

func runPixelArtPipeline(src image.Image, cfg ConvertConfig, cache *BilCache) (ConvertResult, error) {
	if src == nil {
		return ConvertResult{}, fmt.Errorf("no source image")
	}

	var effectiveCrop image.Rectangle
	var cropped image.Image
	if cfg.CropEnabled && !cfg.CropRect.Empty() {
		effectiveCrop = cfg.CropRect
		cropped = cropToRect(src, effectiveCrop)
	} else {

		cropped = src
	}

	targetW := cfg.TargetW
	if targetW <= 0 {
		targetW = gbW
	}
	scaleFactor := cropped.Bounds().Dx() / targetW
	if scaleFactor < 1 {
		scaleFactor = 1
	}

	filtered := getBilateral(src, effectiveCrop, cropped, cfg, cache)

	processed := applyDownscaleAndSharpen(filtered, scaleFactor, cfg)

	if cfg.PosterizeEnabled {
		processed = applyPosterize(processed, cfg.PosterizeLevels)
	}

	if cfg.DitheringEnabled {
		processed = applyDither(processed, cfg)
	}

	if cfg.TileficationFilter {
		var tiles [][]Tile
		var pals [8]Palette
		if cfg.Mode == ModeDMG {
			tiles, pals = tileficationDMG(processed)
		} else {
			tiles, pals = tilefication(processed)
		}
		processed = tilesToImage(tiles, pals)
	} else if cfg.GBCFilter {
		n := cfg.KmeansColors
		if n < 2 {
			n = 2
		}
		processed = quantizeKmeans(processed, n)
	}

	outScale := cfg.OutputScale
	if outScale < 1 {
		outScale = 1
	}
	if outScale > 1 {
		processed = upscaleWithMode(processed, outScale, cfg.Upscaler)
	}

	return ConvertResult{ProcessedImage: processed}, nil
}

func runGBPipeline(src image.Image, cfg ConvertConfig, cache *BilCache) (ConvertResult, error) {
	if src == nil {
		return ConvertResult{}, fmt.Errorf("no source image")
	}

	filtered := getBilateral(src, src.Bounds(), src, cfg, cache)

	var effectiveCrop image.Rectangle
	if cfg.CropEnabled && !cfg.CropRect.Empty() {
		effectiveCrop = cfg.CropRect
	} else {
		effectiveCrop = autoGBCropRect(src)
	}
	cropped := cropToRect(filtered, effectiveCrop)

	scaleFactor := cropped.Bounds().Dx() / gbW
	if scaleFactor < 1 {
		scaleFactor = 1
	}

	sharpened := applyDownscaleAndSharpen(cropped, scaleFactor, cfg)

	if cfg.PosterizeEnabled {
		sharpened = applyPosterize(sharpened, cfg.PosterizeLevels)
	}

	if cfg.DitheringEnabled {
		sharpened = applyDither(sharpened, cfg)
	}

	sharpened = ensureGBSize(sharpened)

	return ConvertResult{
		ProcessedImage: sharpened,
		FullColor:      sharpened,
	}, nil
}

func runGBPipelineFrame(src image.Image, cfg ConvertConfig) (image.Image, error) {
	if src == nil {
		return nil, fmt.Errorf("nil frame")
	}

	var cropped image.Image
	if cfg.CropEnabled && !cfg.CropRect.Empty() {
		cropped = cropToRect(src, cfg.CropRect)
	} else {
		cropped = cropToRect(src, autoGBCropRect(src))
	}

	scaleFactor := cropped.Bounds().Dx() / gbW
	if scaleFactor < 1 {
		scaleFactor = 1
	}

	var filtered image.Image
	if cfg.BilateralEnabled && cfg.BilateralRadius > 0 {
		filtered = bilateralFilter(cropped, cfg.BilateralRadius, cfg.BilateralSigma)
	} else {
		filtered = cropped
	}

	processed := applyDownscaleAndSharpen(filtered, scaleFactor, cfg)

	if cfg.PosterizeEnabled {
		processed = applyPosterize(processed, cfg.PosterizeLevels)
	}
	if cfg.DitheringEnabled {
		processed = applyDither(processed, cfg)
	}

	return ensureGBSize(processed), nil
}

func getBilateral(origSrc image.Image, effectiveCrop image.Rectangle, cropped image.Image, cfg ConvertConfig, cache *BilCache) image.Image {
	if !cfg.BilateralEnabled || cfg.BilateralRadius <= 0 {
		return cropped
	}
	if cached, ok := cache.get(origSrc, effectiveCrop, cfg.BilateralRadius, cfg.BilateralSigma); ok {
		return cached
	}
	result := bilateralFilter(cropped, cfg.BilateralRadius, cfg.BilateralSigma)
	cache.put(origSrc, effectiveCrop, cfg.BilateralRadius, cfg.BilateralSigma, result)
	return result
}

func applyDownscaleAndSharpen(filtered image.Image, scaleFactor int, cfg ConvertConfig) image.Image {
	var scaled image.Image
	switch cfg.Scaling {
	case ScalingBilinear:
		scaled = downscaleBilinear(filtered, scaleFactor)
	case ScalingNearest:
		scaled = downscaleNearest(filtered, scaleFactor)
	default:
		scaled = downscaleMedian(filtered, scaleFactor)
	}
	if !cfg.SharpenEnabled {
		return scaled
	}
	return sharpenWithAmount(scaled, cfg.SharpenAmount)
}

func autoGBCropRect(src image.Image) image.Rectangle {
	b := src.Bounds()
	scaleW := b.Dx() / gbW
	scaleH := b.Dy() / gbH
	scale := scaleW
	if scaleH < scale {
		scale = scaleH
	}
	if scale < 1 {
		scale = 1
	}
	w := gbW * scale
	h := gbH * scale
	cx := b.Min.X + b.Dx()/2
	cy := b.Min.Y + b.Dy()/2
	return image.Rect(cx-w/2, cy-h/2, cx+w/2, cy+h/2).Intersect(b)
}

func cropGB(src image.Image, scale int) image.Image {
	return cropToRect(src, autoGBCropRect(src))
}

func saveImage(img image.Image, path string) error {
	if img == nil {
		return fmt.Errorf("no image to save")
	}
	fo, err := os.Create(path)
	if err != nil {
		return err
	}
	defer fo.Close()
	return jpeg.Encode(fo, img, &jpeg.Options{Quality: 100})
}
