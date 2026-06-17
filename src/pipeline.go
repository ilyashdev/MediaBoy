package main

import (
	"fmt"
	"image"
	"image/jpeg"
	"os"
)

// ── Bilateral cache ───────────────────────────────────────────────────────────

// BilCache caches the bilateral-filtered crop so that fast params (sharpen,
// scaling mode, resolution) can be changed without re-running the filter.
type BilCache struct {
	origSrc  image.Image
	cropRect image.Rectangle // effective crop rect used (zero = no crop / full image)
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

// ── Result ────────────────────────────────────────────────────────────────────

type ConvertResult struct {
	ProcessedImage image.Image
	Tiles          [][]Tile
	Palettes       [8]Palette
}

// ── Pixel Art pipeline ────────────────────────────────────────────────────────

func runPixelArtPipeline(src image.Image, cfg ConvertConfig, cache *BilCache) (ConvertResult, error) {
	if src == nil {
		return ConvertResult{}, fmt.Errorf("no source image")
	}

	// Determine crop
	var effectiveCrop image.Rectangle
	var cropped image.Image
	if cfg.CropEnabled && !cfg.CropRect.Empty() {
		effectiveCrop = cfg.CropRect
		cropped = cropToRect(src, effectiveCrop)
	} else {
		// effectiveCrop stays zero → full image
		cropped = src
	}

	// Scale factor: crop → target
	targetW := cfg.TargetW
	if targetW <= 0 {
		targetW = gbW
	}
	scaleFactor := cropped.Bounds().Dx() / targetW
	if scaleFactor < 1 {
		scaleFactor = 1
	}

	// Bilateral (cached, optional)
	filtered := getBilateral(src, effectiveCrop, cropped, cfg, cache)

	// Downscale + sharpen
	processed := applyDownscaleAndSharpen(filtered, scaleFactor, cfg)

	// Posterize — before dithering and quantization
	if cfg.PosterizeEnabled {
		processed = applyPosterize(processed, cfg.PosterizeLevels)
	}

	// Dithering
	if cfg.DitheringEnabled {
		processed = applyDither(processed, cfg)
	}

	// Color quantize
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

	// Upscale
	outScale := cfg.OutputScale
	if outScale < 1 {
		outScale = 1
	}
	if outScale > 1 {
		processed = upscaleWithMode(processed, outScale, cfg.Upscaler)
	}

	return ConvertResult{ProcessedImage: processed}, nil
}

// ── GB/GBC pipeline ───────────────────────────────────────────────────────────

func runGBPipeline(src image.Image, cfg ConvertConfig, cache *BilCache) (ConvertResult, error) {
	if src == nil {
		return ConvertResult{}, fmt.Errorf("no source image")
	}

	// Determine effective crop (snap guarantees valid GB multiples if CropEnabled)
	var effectiveCrop image.Rectangle
	var cropped image.Image
	if cfg.CropEnabled && !cfg.CropRect.Empty() {
		effectiveCrop = cfg.CropRect
		cropped = cropToRect(src, effectiveCrop)
	} else {
		effectiveCrop = autoGBCropRect(src)
		cropped = cropToRect(src, effectiveCrop)
	}

	scaleFactor := cropped.Bounds().Dx() / gbW
	if scaleFactor < 1 {
		scaleFactor = 1
	}

	// Bilateral (cached)
	filtered := getBilateral(src, effectiveCrop, cropped, cfg, cache)

	// Downscale + sharpen
	sharpened := applyDownscaleAndSharpen(filtered, scaleFactor, cfg)

	// Posterize — before dithering and quantization
	if cfg.PosterizeEnabled {
		sharpened = applyPosterize(sharpened, cfg.PosterizeLevels)
	}

	// Dithering
	if cfg.DitheringEnabled {
		sharpened = applyDither(sharpened, cfg)
	}

	// Tilefication is always applied in GB/GBC mode
	var tiles [][]Tile
	var palettes [8]Palette
	if cfg.Mode == ModeDMG {
		tiles, palettes = tileficationDMG(sharpened)
	} else {
		tiles, palettes = tilefication(sharpened)
	}
	processed := tilesToImage(tiles, palettes)

	return ConvertResult{
		ProcessedImage: processed,
		Tiles:          tiles,
		Palettes:       palettes,
	}, nil
}

// runGBPipelineFrame runs the GB preprocessing stages (crop → bilateral →
// downscale → sharpen → posterize → dither) on a single frame without a
// bilateral cache. Used for GIF/video where each frame is a different image.
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

	return processed, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// getBilateral returns the bilateral-filtered version of cropped, using cache.
// origSrc and effectiveCrop are the cache key; cropped is the already-cropped image.
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

// applyDownscaleAndSharpen runs the fast (non-cached) part of the pipeline.
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

// autoGBCropRect computes the largest center-fit GB-compatible rect for src.
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

// cropGB crops src to scale×160 × scale×144 centered. (kept for backward compat)
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
