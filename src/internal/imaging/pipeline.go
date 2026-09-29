package imaging

import (
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"sync"

	"MediaBoy/internal/core"

	xdraw "golang.org/x/image/draw"
)

// EnsureGBSize forces an image to exactly 160×144. The crop + integer-factor
// downscale can land a few pixels short (e.g. 160×138) when the source isn't a
// clean multiple of the GB screen — and png2hicolorgb / tilefication require an
// exact 160×144 frame, otherwise the ROM renders garbage.
func EnsureGBSize(img image.Image) image.Image {
	b := img.Bounds()
	if b.Dx() == core.ScreenW && b.Dy() == core.ScreenH && b.Min.X == 0 && b.Min.Y == 0 {
		return img
	}
	dst := image.NewRGBA(image.Rect(0, 0, core.ScreenW, core.ScreenH))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	return dst
}

// BilCache memoises the last bilateral filter result; safe for concurrent use.
type BilCache struct {
	mu       sync.Mutex
	origSrc  image.Image
	cropRect image.Rectangle
	radius   int
	sigma    float64
	result   image.Image
}

func (c *BilCache) get(origSrc image.Image, cr image.Rectangle, radius int, sigma float64) (image.Image, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.result != nil && c.origSrc == origSrc && c.cropRect == cr && c.radius == radius && c.sigma == sigma {
		return c.result, true
	}
	return nil, false
}

func (c *BilCache) put(origSrc image.Image, cr image.Rectangle, radius int, sigma float64, result image.Image) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.origSrc, c.cropRect, c.radius, c.sigma, c.result = origSrc, cr, radius, sigma, result
}

type ConvertResult struct {
	ProcessedImage image.Image
	Tiles          [][]core.Tile
	Palettes       [8]core.Palette

	FullColor image.Image
}

func RunPixelArtPipeline(src image.Image, cfg core.ConvertConfig, cache *BilCache) (ConvertResult, error) {
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
		targetW = core.ScreenW
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
		var tiles [][]core.Tile
		var pals [8]core.Palette
		if cfg.Mode == core.ModeDMG {
			tiles, pals = TileficationDMG(processed)
		} else {
			tiles, pals = Tilefication(processed)
		}
		processed = TilesToImage(tiles, pals)
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

func RunGBPipeline(src image.Image, cfg core.ConvertConfig, cache *BilCache) (ConvertResult, error) {
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

	scaleFactor := cropped.Bounds().Dx() / core.ScreenW
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

	sharpened = EnsureGBSize(sharpened)

	return ConvertResult{
		ProcessedImage: sharpened,
		FullColor:      sharpened,
	}, nil
}

func RunGBPipelineFrame(src image.Image, cfg core.ConvertConfig) (image.Image, error) {
	if src == nil {
		return nil, fmt.Errorf("nil frame")
	}

	var cropped image.Image
	if cfg.CropEnabled && !cfg.CropRect.Empty() {
		cropped = cropToRect(src, cfg.CropRect)
	} else {
		cropped = cropToRect(src, autoGBCropRect(src))
	}

	scaleFactor := cropped.Bounds().Dx() / core.ScreenW
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

	return EnsureGBSize(processed), nil
}

func getBilateral(origSrc image.Image, effectiveCrop image.Rectangle, cropped image.Image, cfg core.ConvertConfig, cache *BilCache) image.Image {
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

func applyDownscaleAndSharpen(filtered image.Image, scaleFactor int, cfg core.ConvertConfig) image.Image {
	var scaled image.Image
	switch cfg.Scaling {
	case core.ScalingBilinear:
		scaled = downscaleBilinear(filtered, scaleFactor)
	case core.ScalingNearest:
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
	scaleW := b.Dx() / core.ScreenW
	scaleH := b.Dy() / core.ScreenH
	scale := scaleW
	if scaleH < scale {
		scale = scaleH
	}
	if scale < 1 {
		scale = 1
	}
	w := core.ScreenW * scale
	h := core.ScreenH * scale
	cx := b.Min.X + b.Dx()/2
	cy := b.Min.Y + b.Dy()/2
	return image.Rect(cx-w/2, cy-h/2, cx+w/2, cy+h/2).Intersect(b)
}

func SaveImage(img image.Image, path string) error {
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

// Reset drops the cached result.
func (c *BilCache) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.origSrc, c.result = nil, nil
}
