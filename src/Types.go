package main

import "image"

type RGB struct {
	R, G, B float64
}

type RGB5 uint16

func toRGB5(c RGB) RGB5 {
	r := uint16(c.R) >> 3
	g := uint16(c.G) >> 3
	b := uint16(c.B) >> 3
	return RGB5(r | (g << 5) | (b << 10))
}

type Palette struct {
	Colors [4]RGB
}

type Tile struct {
	Pixels  [8][8]uint8
	Palette uint8
}

type Upload struct {
	Slot uint16 // global VRAM slot 0..511; VRAM bank 1 when Slot >= 256 (CGB)
	Tile Tile
}

type Update struct {
	ScreenPos uint16
	TileIndex uint8 // bank-local tile index written to the BG map (Slot & 0xFF)
	Attr      uint8 // CGB attribute byte: palette(0..7) | VRAM-bank bit (0x08)
}

type FrameData struct {
	Palette    [8]Palette
	NewPalette bool  // palettes differ from previous frame → load on switch
	Keyframe   bool  // full VRAM reload: new tiles don't fit beside the shown frame
	Delay      uint8 // playback hold in vsync frames (≥1)
	Uploads    []Upload
	Updates    []Update
}

type VideoData struct {
	Frames   []FrameData
	ROMBanks int // total ROM banks needed (power of two, ≥4)
}

const gbW = 160
const gbH = 144

type ConvertMode int

const (
	ModeCGB ConvertMode = iota
	ModeDMG
)

type ScalingType int

const (
	ScalingMedian ScalingType = iota
	ScalingBilinear
	ScalingNearest
)

type UpscalerType int

const (
	UpscalerNearest UpscalerType = iota
	UpscalerScale2x
)

type ConvertConfig struct {
	// shared
	Name            string
	BilateralRadius int
	BilateralSigma  float64
	Scaling         ScalingType
	Upscaler        UpscalerType
	GBCFilter       bool // k-means quantize (PA mode)

	// pipeline stage enables
	BilateralEnabled bool
	SharpenEnabled   bool
	PosterizeEnabled bool
	DitheringEnabled bool

	// pixel-art specific
	TargetW, TargetH   int
	KmeansColors       int
	TileficationFilter bool
	SharpenAmount      float64
	OutputScale        int

	// posterization
	PosterizeLevels int // tonal levels per channel (2..16)

	// dithering
	Dithering         DitheringType
	DitheringStrength float64
	DitheringLevels   int

	// GB/GBC specific
	Mode        ConvertMode
	CropEnabled bool
	CropRect    image.Rectangle
	GBDKHome    string
	OutputDir   string

	// video specific
	SceneThreshold float64 // 0..1; palette-scheme distance above which a new scene (palette set) starts
	InterpFrames   int     // interpolated frames inserted between each pair (0 = off)
	ROMBanks       int     // ROM banks required by the last video export (set at export time)
}

func defaultConfig() ConvertConfig {
	return ConvertConfig{
		Name:              "image",
		BilateralRadius:   8,
		BilateralSigma:    80,
		Scaling:           ScalingMedian,
		Upscaler:          UpscalerNearest,
		GBCFilter:         true,
		BilateralEnabled:  true,
		SharpenEnabled:    true,
		PosterizeEnabled:  false,
		DitheringEnabled:  true,
		TargetW:           160,
		TargetH:           144,
		KmeansColors:      32,
		SharpenAmount:     0.1,
		OutputScale:       2,
		PosterizeLevels:   4,
		Dithering:         DitheringBayer,
		DitheringStrength: 0.1,
		DitheringLevels:   4,
		Mode:              ModeCGB,
		GBDKHome:          `C:\Bin\gbdk`,
		OutputDir:         "gbdk_out",
		SceneThreshold:    0.06,
		InterpFrames:      0,
	}
}
