package video

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
)

// player is the GBVP3 player ROM, assembled from player/video3.asm.
//
//go:embed video3.gbc
var player []byte

func GIFFPS(delays []int) float64 {
	sum := 0
	for _, d := range delays {
		if d <= 0 {
			d = 10
		}
		sum += d
	}
	if sum == 0 || len(delays) == 0 {
		return 24
	}
	avg := float64(sum) / float64(len(delays))
	if avg <= 0 {
		return 24
	}
	return 100.0 / avg
}

var nintendoLogo = [48]byte{
	0xCE, 0xED, 0x66, 0x66, 0xCC, 0x0D, 0x00, 0x0B, 0x03, 0x73, 0x00, 0x83, 0x00, 0x0C, 0x00, 0x0D,
	0x00, 0x08, 0x11, 0x1F, 0x88, 0x89, 0x00, 0x0E, 0xDC, 0xCC, 0x6E, 0xE6, 0xDD, 0xDD, 0xD9, 0x99,
	0xBB, 0xBB, 0x67, 0x63, 0x6E, 0x0E, 0xEC, 0xCC, 0xDD, 0xDC, 0x99, 0x9F, 0xBB, 0xB9, 0x33, 0x3E,
}

// phase reports progress as the part [lo, hi] of a job with several passes.
func phase(progress func(done, total int), lo, hi float64) func(done, total int) {
	if progress == nil {
		return nil
	}
	return func(done, total int) {
		progress(int(lo*float64(total)+(hi-lo)*float64(done)), total)
	}
}

// cartSize is the MBC5 ROM size that holds n bytes: a power of two, at least
// 2 banks.
func cartSize(n int) int {
	size := 2 * 0x4000
	for size < n {
		size <<= 1
	}
	return size
}

func fixGBHeader(rom []byte) []byte {
	size := cartSize(len(rom))
	if len(rom) < size {
		rom = append(rom, bytes.Repeat([]byte{0xFF}, size-len(rom))...)
	}

	copy(rom[0x104:0x134], nintendoLogo[:])
	rom[0x143] = 0xC0
	rom[0x147] = 0x19
	code := byte(0)
	for n := 2 * 0x4000; n < size; n <<= 1 {
		code++
	}
	rom[0x148] = code
	rom[0x149] = 0x00

	var hc byte
	for i := 0x134; i <= 0x14C; i++ {
		hc = hc - rom[i] - 1
	}
	rom[0x14D] = hc

	var gc uint16
	for i := range rom {
		if i == 0x14E || i == 0x14F {
			continue
		}
		gc += uint16(rom[i])
	}
	rom[0x14E], rom[0x14F] = byte(gc>>8), byte(gc)
	return rom
}

// MinPercent is the lowest quality offered.
const MinPercent = 30

// MaxQuality is the loosest compression tolerance, at MinPercent; 0 is the
// sharpest.
const MaxQuality = 31

// QualityFromPercent maps a quality in percent (MinPercent-100) to the
// encoder's tolerance. The curve is quadratic because the visible differences
// are at the sharp end: 100 % is 0, 75 % is 4 (the default), 50 % is 16,
// 30 % is 31.
func QualityFromPercent(p int) int {
	d := float64(100-max(0, min(100, p))) / 100
	return min(int(math.Round(64*d*d)), MaxQuality)
}

// PercentFromQuality is the inverse of QualityFromPercent.
func PercentFromQuality(q int) int {
	q = max(0, min(MaxQuality, q))
	return int(math.Round(100 - 100*math.Sqrt(float64(q)/64)))
}

// Result is an encoded video ROM.
type Result struct {
	ROM         []byte
	Used        int // bytes of ROM in use, before the padding to the cartridge size
	FramesUsed  int
	FramesTotal int
	Banks       int
}

func (r Result) Truncated() bool { return r.FramesUsed < r.FramesTotal }

// WriteROM writes rom as dir/name.gbc.
func WriteROM(dir, name string, rom []byte) error {
	if abs, e := filepath.Abs(dir); e == nil {
		dir = abs
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name+".gbc"), rom, 0644)
}

// BuildROM encodes 160×144 frames shown at fps, with u8 stereo PCM at
// audioRate, into a ROM of at most maxMB (0: 8 MB). Quality 0 is the sharpest
// (see QualityFromPercent). Frames that do not fit are dropped. cache may be
// nil; a non-nil cache must only be shared between builds of the same frames.
func BuildROM(frames []image.Image, audio []byte, fps float64, quality, maxMB int, cache *PaletteCache, progress func(done, total int)) (Result, error) {
	if len(frames) == 0 {
		return Result{}, fmt.Errorf("no frames")
	}
	e := newGBVP3Enc(BanksFromMB(maxMB))
	e.cache = cache
	data, used, err := e.Encode(fps, quality, audio, frames, progress)
	if err != nil {
		return Result{}, err
	}
	return newResult(data, used, len(frames)), nil
}

// BuildROMFit encodes the whole clip into maxMB, steering the quality frame
// by frame so the ROM ends up nearly full; it returns the average quality.
// Frames are dropped only when even the loosest quality cannot hold them.
//
// The quality starts where hint, an estimate of the same frames (with cache
// shared), says the clip fits; without one, a first pass at the default
// quality measures the clip. The palette fits do not depend on the quality,
// so with the cache the steered pass costs a fraction of the first.
func BuildROMFit(frames []image.Image, audio []byte, fps float64, maxMB int, cache *PaletteCache, hint *Estimate, progress func(done, total int)) (Result, int, error) {
	return buildFit(context.Background(), frames, audio, fps, maxMB, cache, hint, progress)
}

func buildFit(ctx context.Context, frames []image.Image, audio []byte, fps float64, maxMB int, cache *PaletteCache, hint *Estimate, progress func(done, total int)) (Result, int, error) {
	if len(frames) == 0 {
		return Result{}, 0, fmt.Errorf("no frames")
	}
	if cache == nil {
		cache = NewPaletteCache()
	}
	run := func(q int, lo, hi float64) (*Estimate, error) {
		est, err := estimate(ctx, frames, audio, fps, q, cache, phase(progress, lo, hi))
		return &est, err
	}
	if hint == nil {
		var err error
		if hint, err = run(4, 0, 0.6); err != nil {
			return Result{}, 0, err
		}
	}
	e := newGBVP3Enc(BanksFromMB(maxMB))
	budget := e.maxBanks*0x4000 - gbvp3FitMargin - hint.videoBank*0x4000
	// One more measure near the guess pins the quality down (the palette fits
	// are cached by now, so it is cheap) and plans from a closer encode.
	plan := hint
	q, slope := fitQuality(budget, hint)
	if qi := int(math.Round(q)); abs8(qi-hint.Quality) > max(1, hint.Quality/4) {
		est, err := run(qi, 0.6, 0.8)
		if err != nil {
			return Result{}, 0, err
		}
		plan = est
		q, slope = fitQuality(budget, hint, est)
	}
	e.cache = cache
	e.stop = func() bool { return ctx.Err() != nil }
	e.rate = newGBVP3Fit(plan, len(frames), e.maxBanks, q, slope)
	data, used, err := e.Encode(fps, 0, audio, frames, phase(progress, 0.8, 1))
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, 0, ctx.Err()
		}
		return Result{}, 0, err
	}
	return newResult(data, used, len(frames)), e.rate.quality(), nil
}

func newResult(data []byte, used, total int) Result {
	rom := fixGBHeader(append(append([]byte{}, player...), data...))
	return Result{ROM: rom, Used: len(player) + len(data), FramesUsed: used, FramesTotal: total, Banks: len(rom) / 0x4000}
}

// FitEstimate is what BuildROMFit makes of a clip; the encode is
// deterministic, so its ROM is the one the compile would write.
type FitEstimate struct {
	Result
	FPS     float64
	MaxMB   int
	Quality int // average
}

// Seconds is the length of the clip.
func (e FitEstimate) Seconds() float64 { return float64(e.FramesTotal) / e.FPS }

// EstimateFit runs BuildROMFit; it stops early when ctx is cancelled.
func EstimateFit(ctx context.Context, frames []image.Image, audio []byte, fps float64, maxMB int, cache *PaletteCache, hint *Estimate) (FitEstimate, error) {
	res, q, err := buildFit(ctx, frames, audio, fps, maxMB, cache, hint, nil)
	if err != nil {
		return FitEstimate{}, err
	}
	return FitEstimate{Result: res, FPS: fps, MaxMB: maxMB, Quality: q}, nil
}

// Estimate is how a clip encodes into the largest cartridge. It answers for
// any Max ROM size how much of the clip fits, without encoding again.
type Estimate struct {
	Quality int     // the quality encoded at
	Frames  int     // frames of the clip
	FPS     float64 // their rate
	Bytes   int     // ROM bytes in use (with the player, before padding)
	Cart    int     // cartridge size for Bytes
	Fits8MB int     // frames that fit into 8 MB
	// frameEnds[i] is the stream position after the i-th encoded frame,
	// frameIdx[i] the index of that frame in the clip.
	frameEnds, frameIdx []int
	videoBank           int
	result              Result
}

// Seconds is the length of the clip.
func (e Estimate) Seconds() float64 { return float64(e.Frames) / e.FPS }

// ROM is the ROM BuildROM makes of the clip at this quality for maxMB, when
// all of it fits there: the encode is deterministic and only a limit it
// reaches changes it. ok is false otherwise.
func (e Estimate) ROM(maxMB int) (res Result, ok bool) {
	if e.FramesFor(maxMB) != e.Frames {
		return Result{}, false
	}
	return e.result, true
}

// FramesFor is how many frames fit into maxMB (0: 8 MB), as BuildROM would
// keep them; -1 when the audio alone does not fit. A frame is dropped, with
// everything after it, when it would end past the limit.
func (e Estimate) FramesFor(maxMB int) int {
	banks := BanksFromMB(maxMB)
	if e.videoBank >= banks-1 {
		return -1
	}
	for i, end := range e.frameEnds {
		if endBank(end) >= banks {
			return e.frameIdx[i]
		}
	}
	return e.Fits8MB
}

// EstimateROM encodes the clip into an unlimited stream to see how large the
// ROM gets. It stops early when ctx is cancelled.
func EstimateROM(ctx context.Context, frames []image.Image, audio []byte, fps float64, quality int, cache *PaletteCache) (Estimate, error) {
	return estimate(ctx, frames, audio, fps, quality, cache, nil)
}

func estimate(ctx context.Context, frames []image.Image, audio []byte, fps float64, quality int, cache *PaletteCache, progress func(done, total int)) (Estimate, error) {
	if len(frames) == 0 {
		return Estimate{}, fmt.Errorf("no frames")
	}
	e := newGBVP3Enc(mbc5MaxBanks)
	e.cache = cache
	est := Estimate{Quality: quality}
	e.stop = func() bool { return ctx.Err() != nil }
	e.onFrame = func(fi int) {
		est.frameEnds = append(est.frameEnds, e.pos)
		est.frameIdx = append(est.frameIdx, fi)
	}
	data, used, err := e.Encode(fps, quality, audio, frames, progress)
	if err != nil {
		if ctx.Err() != nil {
			return Estimate{}, ctx.Err()
		}
		return Estimate{}, err
	}
	est.videoBank = int(data[0])
	est.Frames, est.FPS, est.Fits8MB = len(frames), fps, used
	est.result = newResult(data, used, len(frames))
	est.Bytes, est.Cart = est.result.Used, len(est.result.ROM)
	return est, nil
}
