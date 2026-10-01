package video

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"os"
	"path/filepath"

	"MediaBoy/internal/core"
)

// EnsureGBVP2 locates the video.gbc player ROM: first in the working dir, then
// next to the executable (the working dir is usually $HOME when the app is
// started from a desktop launcher).
func EnsureGBVP2() (player string, err error) {
	candidates := []string{"video.gbc"}
	if exe, e := os.Executable(); e == nil {
		if real, e := filepath.EvalSymlinks(exe); e == nil {
			exe = real
		}
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "video.gbc"))
	}
	for _, p := range candidates {
		if _, e := os.Stat(p); e == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("video.gbc (GBVP2 player) not found in the working dir or next to the executable")
}

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

func writeTGA(img image.Image, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	var hdr [18]byte
	hdr[2] = 2
	hdr[12], hdr[13] = byte(core.ScreenW&0xFF), byte(core.ScreenW>>8)
	hdr[14], hdr[15] = byte(core.ScreenH&0xFF), byte(core.ScreenH>>8)
	hdr[16] = 24
	hdr[17] = 0x20
	w.Write(hdr[:])
	b := img.Bounds()
	for y := 0; y < core.ScreenH; y++ {
		for x := 0; x < core.ScreenW; x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			w.WriteByte(byte(bl >> 8))
			w.WriteByte(byte(g >> 8))
			w.WriteByte(byte(r >> 8))
		}
	}
	return w.Flush()
}

func fixGBHeader(rom []byte) []byte {
	banks := len(rom) / 0x4000
	if banks < 2 {
		banks = 2
	}
	pow := 2
	for pow < banks {
		pow <<= 1
	}
	if need := pow * 0x4000; len(rom) < need {
		rom = append(rom, bytes.Repeat([]byte{0xFF}, need-len(rom))...)
	}

	copy(rom[0x104:0x134], nintendoLogo[:])
	rom[0x143] = 0xC0
	rom[0x147] = 0x19
	code := byte(0)
	for n := 2; n < pow; n <<= 1 {
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

type GBVP2Result struct {
	ROM         []byte
	FramesUsed  int
	FramesTotal int
	Banks       int
}

func (r GBVP2Result) Truncated() bool { return r.FramesUsed < r.FramesTotal }

func ExportGBVP2(dir, name, playerPath string, frames []image.Image, audio []byte, fps float64, quality, maxMB int, progress func(done, total int)) (GBVP2Result, error) {
	res, err := BuildGBVP2ROM(dir, playerPath, frames, audio, fps, quality, maxMB, nil, progress)
	if err != nil {
		return res, err
	}
	return res, WriteGBVP2ROM(dir, name, res.ROM)
}

func WriteGBVP2ROM(dir, name string, rom []byte) error {
	if abs, e := filepath.Abs(dir); e == nil {
		dir = abs
	}
	return os.WriteFile(filepath.Join(dir, name+".gbc"), rom, 0644)
}

// BuildGBVP2ROM encodes frames into a ROM image. cache may be nil; a non-nil
// cache must only be shared between builds of the same frames.
func BuildGBVP2ROM(dir, playerPath string, frames []image.Image, audio []byte, fps float64, quality, maxMB int, cache *GBVP2Cache, progress func(done, total int)) (GBVP2Result, error) {
	if len(frames) == 0 {
		return GBVP2Result{}, fmt.Errorf("no frames")
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return GBVP2Result{}, err
	}

	framesDir := filepath.Join(dir, "frames")
	_ = os.RemoveAll(framesDir)
	if err := os.MkdirAll(framesDir, 0755); err != nil {
		return GBVP2Result{}, err
	}
	framePaths := make([]string, len(frames))
	for i, fr := range frames {
		p := filepath.Join(framesDir, fmt.Sprintf("%05d.tga", i))
		if err := writeTGA(fr, p); err != nil {
			return GBVP2Result{}, err
		}
		framePaths[i] = p
	}

	data, framesUsed, err := runGoEncoder(fps, quality, BanksFromMB(maxMB), audio, framePaths, cache, progress)
	if err != nil {
		return GBVP2Result{}, fmt.Errorf("GBVP2 encoder: %w", err)
	}

	_ = os.RemoveAll(framesDir)

	player, err := os.ReadFile(playerPath)
	if err != nil {
		return GBVP2Result{}, fmt.Errorf("player ROM (video.gbc): %v", err)
	}
	rom := fixGBHeader(append(player, data...))
	return GBVP2Result{
		ROM:         rom,
		FramesUsed:  framesUsed,
		FramesTotal: len(frames),
		Banks:       len(rom) / 0x4000,
	}, nil
}
