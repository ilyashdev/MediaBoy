package core

import (
	"image"
	"path/filepath"
	"runtime"
	"strings"
)

type RGB struct {
	R, G, B float64
}

type RGB5 uint16

func ToRGB5(c RGB) RGB5 {
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

const ScreenW = 160
const ScreenH = 144

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

type DitheringType int

const (
	DitheringNone DitheringType = iota
	DitheringBayer
	DitheringFloydSteinberg
	DitheringAtkinson
)

type MusicCodec int

const (
	CodecPCM MusicCodec = iota
	CodecChiptuneUGE
	CodecChiptuneMOD
	CodecChiptuneAuto
)

func (c MusicCodec) String() string {
	switch c {
	case CodecChiptuneUGE:
		return "Chiptune (.uge)"
	case CodecChiptuneMOD:
		return "Chiptune (.mod)"
	case CodecChiptuneAuto:
		return "Chiptune (auto)"
	default:
		return "PCM (3-bit stereo)"
	}
}

func (c MusicCodec) IsChiptune() bool { return c != CodecPCM }

type MusicTarget int

const (
	TargetCGBFast MusicTarget = iota

	TargetDMG
)

func (t MusicTarget) String() string {
	if t == TargetDMG {
		return "DMG (compatible)"
	}
	return "CGB (double-speed)"
}

type Song struct {
	Path      string
	Title     string
	Codec     MusicCodec
	CoverPath string
	Cover     image.Image
}

func (sg *Song) DisplayTitle() string {
	if strings.TrimSpace(sg.Title) != "" {
		return sg.Title
	}
	if sg.Path != "" {
		return strings.TrimSuffix(filepath.Base(sg.Path), filepath.Ext(sg.Path))
	}
	return "(untitled)"
}

type ConvertConfig struct {
	Name            string
	BilateralRadius int
	BilateralSigma  float64
	Scaling         ScalingType
	Upscaler        UpscalerType
	GBCFilter       bool

	BilateralEnabled bool
	SharpenEnabled   bool
	PosterizeEnabled bool
	DitheringEnabled bool

	TargetW, TargetH   int
	KmeansColors       int
	TileficationFilter bool
	SharpenAmount      float64
	OutputScale        int

	PosterizeLevels int

	Dithering         DitheringType
	DitheringStrength float64
	DitheringLevels   int

	Mode        ConvertMode
	CropEnabled bool
	CropRect    image.Rectangle
	// Letterbox fits the whole crop (or frame) into the screen with black
	// bars instead of cropping it to the screen's shape. GIF and video only.
	Letterbox bool
	GBDKHome  string
	OutputDir string

	Quality     int
	PCMRate     int
	MusicTarget MusicTarget
	HiColor     bool
	ROMBanks    int
	MaxVideoMB  int
}

func DefaultConfig() ConvertConfig {
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
		GBDKHome:          defaultGBDKHome(),
		OutputDir:         "out",
		Quality:           4,
		PCMRate:           4096,
		MaxVideoMB:        8,
		HiColor:           true,
	}
}

func defaultGBDKHome() string {
	if runtime.GOOS == "windows" {
		return `C:\Bin\gbdk`
	}
	return "/opt/gbdk"
}
