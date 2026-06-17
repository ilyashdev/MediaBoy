package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// cleanOutputDir removes previously generated C sources, headers and build
// objects so a fresh export (especially video, which spans many bank files)
// doesn't get mixed with stale files when the whole dir is globbed at compile.
func cleanOutputDir(dir string) {
	for _, pat := range []string{"*.c", "*.h"} {
		if matches, err := filepath.Glob(filepath.Join(dir, pat)); err == nil {
			for _, m := range matches {
				_ = os.Remove(m)
			}
		}
	}
	_ = os.RemoveAll(filepath.Join(dir, "obj"))
}

func encodeTile(t Tile) [16]byte {
	var out [16]byte
	for y := 0; y < 8; y++ {
		var lo byte
		var hi byte
		for x := 0; x < 8; x++ {
			c := t.Pixels[y][x] & 3
			shift := 7 - x
			lo |= (c & 1) << shift
			hi |= ((c >> 1) & 1) << shift
		}
		out[y*2] = lo
		out[y*2+1] = hi
	}
	return out
}

// ExportGBDK exports a static CGB image (tiles + 8 palettes) and a main.c player.
func ExportGBDK(dir, name string, tiles [][]Tile, palettes [8]Palette) error {
	h := len(tiles)
	if h == 0 {
		return fmt.Errorf("empty image")
	}
	w := len(tiles[0])

	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	cleanOutputDir(dir)

	guard := strings.ToUpper(name) + "_H"
	upper := strings.ToUpper(name)

	var hdr bytes.Buffer
	var src bytes.Buffer

	fmt.Fprintf(&hdr, "#ifndef %s\n#define %s\n\n", guard, guard)
	fmt.Fprintf(&hdr, "#include <stdint.h>\n")
	fmt.Fprintf(&hdr, "#include <gbdk/platform.h>\n\n")
	fmt.Fprintf(&hdr, "#define %s_TILE_ORIGIN 0\n", upper)
	fmt.Fprintf(&hdr, "#define %s_TILE_W 8\n", upper)
	fmt.Fprintf(&hdr, "#define %s_TILE_H 8\n", upper)
	fmt.Fprintf(&hdr, "#define %s_WIDTH %d\n", upper, w*8)
	fmt.Fprintf(&hdr, "#define %s_HEIGHT %d\n", upper, h*8)
	totalTiles := w * h
	fmt.Fprintf(&hdr, "#define %s_TILE_COUNT %d\n", upper, totalTiles)
	fmt.Fprintf(&hdr, "#define %s_PALETTE_COUNT %d\n", upper, len(palettes))
	fmt.Fprintf(&hdr, "#define %s_COLORS_PER_PALETTE 4\n", upper)
	fmt.Fprintf(&hdr, "#define %s_TOTAL_COLORS %d\n\n", upper, len(palettes)*4)
	fmt.Fprintf(&hdr, "BANKREF_EXTERN(%s)\n\n", name)
	fmt.Fprintf(&hdr, "extern const palette_color_t %s_palettes[%d];\n", name, len(palettes)*4)
	fmt.Fprintf(&hdr, "extern const uint8_t %s_tiles[%d];\n", name, totalTiles*16)
	fmt.Fprintf(&hdr, "extern const uint8_t %s_map[%d];\n", name, totalTiles)
	fmt.Fprintf(&hdr, "extern const uint8_t %s_attr[%d];\n\n", name, totalTiles)
	fmt.Fprintf(&hdr, "#endif\n")

	fmt.Fprintf(&src, "#include \"%s.h\"\n\n", name)
	fmt.Fprintf(&src, "BANKREF(%s)\n\n", name)

	fmt.Fprintf(&src, "const uint8_t %s_tiles[] = {\n", name)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			raw := encodeTile(tiles[y][x])
			for _, b := range raw {
				fmt.Fprintf(&src, "0x%02X,", b)
			}
			fmt.Fprintf(&src, "\n")
		}
	}
	fmt.Fprintf(&src, "};\n\n")

	fmt.Fprintf(&src, "const uint8_t %s_map[] = {\n", name)
	idx := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fmt.Fprintf(&src, "%d,", idx%256) // bank-local index (0-255)
			idx++
		}
		fmt.Fprintf(&src, "\n")
	}
	fmt.Fprintf(&src, "};\n\n")

	fmt.Fprintf(&src, "const uint8_t %s_attr[] = {\n", name)
	idx = 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			palIdx := tiles[y][x].Palette & 7
			bankBit := uint8(0)
			if idx >= 256 {
				bankBit = 0x08 // CGB attribute bit 3 = VRAM bank 1
			}
			fmt.Fprintf(&src, "%d,", palIdx|bankBit)
			idx++
		}
		fmt.Fprintf(&src, "\n")
	}
	fmt.Fprintf(&src, "};\n\n")

	fmt.Fprintf(&src, "const palette_color_t %s_palettes[] = {\n", name)
	for _, p := range palettes {
		for i := 0; i < 4; i++ {
			if i < len(p.Colors) {
				fmt.Fprintf(&src, "0x%04X,", uint16(toRGB5(p.Colors[i])))
			} else {
				fmt.Fprintf(&src, "0x0000,")
			}
		}
		fmt.Fprintf(&src, "\n")
	}
	fmt.Fprintf(&src, "};\n")

	if err := os.WriteFile(filepath.Join(dir, name+".h"), hdr.Bytes(), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, name+".c"), src.Bytes(), 0644); err != nil {
		return err
	}

	return generateStaticPlayerC(dir, name, ModeCGB)
}

// ExportGBDKDMG exports a static DMG image with a single grayscale palette.
func ExportGBDKDMG(dir, name string, tiles [][]Tile, palette Palette) error {
	h := len(tiles)
	if h == 0 {
		return fmt.Errorf("empty image")
	}
	w := len(tiles[0])

	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	cleanOutputDir(dir)

	guard := strings.ToUpper(name) + "_H"
	upper := strings.ToUpper(name)

	var hdr bytes.Buffer
	var src bytes.Buffer

	fmt.Fprintf(&hdr, "#ifndef %s\n#define %s\n\n", guard, guard)
	fmt.Fprintf(&hdr, "#include <stdint.h>\n")
	fmt.Fprintf(&hdr, "#include <gbdk/platform.h>\n\n")
	fmt.Fprintf(&hdr, "#define %s_WIDTH %d\n", upper, w*8)
	fmt.Fprintf(&hdr, "#define %s_HEIGHT %d\n", upper, h*8)
	totalTiles := w * h
	fmt.Fprintf(&hdr, "#define %s_TILE_COUNT %d\n\n", upper, totalTiles)
	fmt.Fprintf(&hdr, "BANKREF_EXTERN(%s)\n\n", name)
	fmt.Fprintf(&hdr, "extern const uint8_t %s_tiles[%d];\n", name, totalTiles*16)
	fmt.Fprintf(&hdr, "extern const uint8_t %s_map[%d];\n\n", name, totalTiles)
	fmt.Fprintf(&hdr, "#endif\n")

	fmt.Fprintf(&src, "#include \"%s.h\"\n\n", name)
	fmt.Fprintf(&src, "BANKREF(%s)\n\n", name)

	fmt.Fprintf(&src, "const uint8_t %s_tiles[] = {\n", name)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			raw := encodeTile(tiles[y][x])
			for _, b := range raw {
				fmt.Fprintf(&src, "0x%02X,", b)
			}
			fmt.Fprintf(&src, "\n")
		}
	}
	fmt.Fprintf(&src, "};\n\n")

	fmt.Fprintf(&src, "const uint8_t %s_map[] = {\n", name)
	idx := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fmt.Fprintf(&src, "%d,", idx)
			idx++
		}
		fmt.Fprintf(&src, "\n")
	}
	fmt.Fprintf(&src, "};\n")

	if err := os.WriteFile(filepath.Join(dir, name+".h"), hdr.Bytes(), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, name+".c"), src.Bytes(), 0644); err != nil {
		return err
	}

	return generateStaticPlayerC(dir, name, ModeDMG)
}

// roundUpPow2 returns the smallest power of two ≥ n (minimum 4, maximum 512 —
// the MBC5 ROM-bank ceiling).
func roundUpPow2(n int) int {
	v := 4
	for v < n {
		v <<= 1
	}
	if v > 512 {
		v = 512
	}
	return v
}

// ExportGBDKVideo exports animated video data plus a CGB/DMG delta-player main.c.
//
// Layout: the big per-frame arrays are distributed across ROM banks (one file per
// bank, `name_bN.c`, each with `#pragma bank N`). Bank 0 (`name.c`) holds the
// fixed-address lookup tables, palettes and the per-frame bank map. The player
// SWITCH_ROMs to a frame's bank before reading its data.
func ExportGBDKVideo(dir, name string, video *VideoData) error {
	if video == nil || len(video.Frames) == 0 {
		return fmt.Errorf("empty video")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	cleanOutputDir(dir)

	upper := strings.ToUpper(name)
	frameCount := len(video.Frames)

	// ── Bank assignment ──────────────────────────────────────────────────────
	// Bank 0 = tables, bank 1 = code/main; frame data starts at bank 2.
	const bankSize = 16384
	frameBank := make([]int, frameCount)
	curBank, curUsed := 2, 0
	for i, f := range video.Frames {
		sz := len(f.Uploads)*(2+16) + len(f.Updates)*(2+1+1)
		if sz == 0 {
			sz = 1
		}
		if curUsed > 0 && curUsed+sz > bankSize {
			curBank++
			curUsed = 0
		}
		frameBank[i] = curBank
		curUsed += sz
	}
	video.ROMBanks = roundUpPow2(curBank + 1)

	// ── Header ───────────────────────────────────────────────────────────────
	var hdr bytes.Buffer
	fmt.Fprintf(&hdr, "#ifndef %s_H\n#define %s_H\n\n", upper, upper)
	fmt.Fprintf(&hdr, "#include <stdint.h>\n#include <gbdk/platform.h>\n\n")
	fmt.Fprintf(&hdr, "#define %s_FRAME_COUNT %d\n\n", upper, frameCount)
	fmt.Fprintf(&hdr, "extern const palette_color_t %s_palettes[%d];\n", name, frameCount*8*4)
	fmt.Fprintf(&hdr, "extern const uint8_t  %s_frame_banks[%d];\n", name, frameCount)
	fmt.Fprintf(&hdr, "extern const uint8_t  %s_new_palette[%d];\n", name, frameCount)
	fmt.Fprintf(&hdr, "extern const uint8_t  %s_keyframe[%d];\n", name, frameCount)
	fmt.Fprintf(&hdr, "extern const uint8_t  %s_delays[%d];\n", name, frameCount)
	fmt.Fprintf(&hdr, "extern const uint16_t %s_upload_counts[%d];\n", name, frameCount)
	fmt.Fprintf(&hdr, "extern const uint16_t %s_update_counts[%d];\n", name, frameCount)
	fmt.Fprintf(&hdr, "extern const uint16_t* const %s_uslots_table[%d];\n", name, frameCount)
	fmt.Fprintf(&hdr, "extern const uint8_t*  const %s_udata_table[%d];\n", name, frameCount)
	fmt.Fprintf(&hdr, "extern const uint16_t* const %s_pos_table[%d];\n", name, frameCount)
	fmt.Fprintf(&hdr, "extern const uint8_t*  const %s_tidx_table[%d];\n", name, frameCount)
	fmt.Fprintf(&hdr, "extern const uint8_t*  const %s_attr_table[%d];\n\n", name, frameCount)
	fmt.Fprintf(&hdr, "#endif\n")
	if err := os.WriteFile(filepath.Join(dir, name+".h"), hdr.Bytes(), 0644); err != nil {
		return err
	}

	// ── Per-bank data files ──────────────────────────────────────────────────
	// Group frames by bank, emit one file per bank.
	byBank := map[int][]int{}
	for i := 0; i < frameCount; i++ {
		byBank[frameBank[i]] = append(byBank[frameBank[i]], i)
	}
	for bank, frames := range byBank {
		var b bytes.Buffer
		fmt.Fprintf(&b, "#pragma bank %d\n", bank)
		fmt.Fprintf(&b, "#include \"%s.h\"\n\n", name)
		for _, i := range frames {
			f := video.Frames[i]

			fmt.Fprintf(&b, "const uint16_t %s_f%d_uslots[] = {", name, i)
			for _, u := range f.Uploads {
				fmt.Fprintf(&b, "%d,", u.Slot)
			}
			if len(f.Uploads) == 0 {
				fmt.Fprintf(&b, "0")
			}
			fmt.Fprintf(&b, "};\n")

			fmt.Fprintf(&b, "const uint8_t %s_f%d_udata[] = {", name, i)
			for _, u := range f.Uploads {
				for _, raw := range encodeTile(u.Tile) {
					fmt.Fprintf(&b, "0x%02X,", raw)
				}
			}
			if len(f.Uploads) == 0 {
				fmt.Fprintf(&b, "0")
			}
			fmt.Fprintf(&b, "};\n")

			fmt.Fprintf(&b, "const uint16_t %s_f%d_pos[] = {", name, i)
			for _, u := range f.Updates {
				fmt.Fprintf(&b, "%d,", u.ScreenPos)
			}
			if len(f.Updates) == 0 {
				fmt.Fprintf(&b, "0")
			}
			fmt.Fprintf(&b, "};\n")

			fmt.Fprintf(&b, "const uint8_t %s_f%d_tidx[] = {", name, i)
			for _, u := range f.Updates {
				fmt.Fprintf(&b, "%d,", u.TileIndex)
			}
			if len(f.Updates) == 0 {
				fmt.Fprintf(&b, "0")
			}
			fmt.Fprintf(&b, "};\n")

			fmt.Fprintf(&b, "const uint8_t %s_f%d_attr[] = {", name, i)
			for _, u := range f.Updates {
				fmt.Fprintf(&b, "%d,", u.Attr)
			}
			if len(f.Updates) == 0 {
				fmt.Fprintf(&b, "0")
			}
			fmt.Fprintf(&b, "};\n\n")
		}
		fname := fmt.Sprintf("%s_b%d.c", name, bank)
		if err := os.WriteFile(filepath.Join(dir, fname), b.Bytes(), 0644); err != nil {
			return err
		}
	}

	// ── Bank 0: tables, palettes, bank map ───────────────────────────────────
	var src bytes.Buffer
	fmt.Fprintf(&src, "#include \"%s.h\"\n\n", name)

	// Externs for the banked per-frame arrays.
	for i := 0; i < frameCount; i++ {
		fmt.Fprintf(&src, "extern const uint16_t %s_f%d_uslots[];\n", name, i)
		fmt.Fprintf(&src, "extern const uint8_t  %s_f%d_udata[];\n", name, i)
		fmt.Fprintf(&src, "extern const uint16_t %s_f%d_pos[];\n", name, i)
		fmt.Fprintf(&src, "extern const uint8_t  %s_f%d_tidx[];\n", name, i)
		fmt.Fprintf(&src, "extern const uint8_t  %s_f%d_attr[];\n", name, i)
	}
	fmt.Fprintf(&src, "\n")

	fmt.Fprintf(&src, "const palette_color_t %s_palettes[] = {\n", name)
	for _, f := range video.Frames {
		for _, p := range f.Palette {
			for i := 0; i < 4; i++ {
				fmt.Fprintf(&src, "0x%04X,", uint16(toRGB5(p.Colors[i])))
			}
		}
		fmt.Fprintf(&src, "\n")
	}
	fmt.Fprintf(&src, "};\n\n")

	fmt.Fprintf(&src, "const uint8_t %s_frame_banks[] = {", name)
	for i := 0; i < frameCount; i++ {
		fmt.Fprintf(&src, "%d,", frameBank[i])
	}
	fmt.Fprintf(&src, "};\n\n")

	fmt.Fprintf(&src, "const uint8_t %s_new_palette[] = {", name)
	for _, f := range video.Frames {
		if f.NewPalette {
			fmt.Fprintf(&src, "1,")
		} else {
			fmt.Fprintf(&src, "0,")
		}
	}
	fmt.Fprintf(&src, "};\n\n")

	fmt.Fprintf(&src, "const uint8_t %s_keyframe[] = {", name)
	for _, f := range video.Frames {
		if f.Keyframe {
			fmt.Fprintf(&src, "1,")
		} else {
			fmt.Fprintf(&src, "0,")
		}
	}
	fmt.Fprintf(&src, "};\n\n")

	fmt.Fprintf(&src, "const uint8_t %s_delays[] = {", name)
	for _, f := range video.Frames {
		d := f.Delay
		if d == 0 {
			d = 1
		}
		fmt.Fprintf(&src, "%d,", d)
	}
	fmt.Fprintf(&src, "};\n\n")

	fmt.Fprintf(&src, "const uint16_t %s_upload_counts[] = {", name)
	for _, f := range video.Frames {
		fmt.Fprintf(&src, "%d,", len(f.Uploads))
	}
	fmt.Fprintf(&src, "};\n\n")

	fmt.Fprintf(&src, "const uint16_t %s_update_counts[] = {", name)
	for _, f := range video.Frames {
		fmt.Fprintf(&src, "%d,", len(f.Updates))
	}
	fmt.Fprintf(&src, "};\n\n")

	emitTable := func(typ, suffix string) {
		fmt.Fprintf(&src, "const %s const %s_%s_table[] = {", typ, name, suffix)
		for i := 0; i < frameCount; i++ {
			fmt.Fprintf(&src, "%s_f%d_%s,", name, i, suffix)
		}
		fmt.Fprintf(&src, "};\n")
	}
	emitTable("uint16_t*", "uslots")
	emitTable("uint8_t*", "udata")
	emitTable("uint16_t*", "pos")
	emitTable("uint8_t*", "tidx")
	emitTable("uint8_t*", "attr")

	if err := os.WriteFile(filepath.Join(dir, name+".c"), src.Bytes(), 0644); err != nil {
		return err
	}

	return generateVideoPlayerC(dir, name)
}

func generateStaticPlayerC(dir, name string, mode ConvertMode) error {
	upper := strings.ToUpper(name)
	var b bytes.Buffer

	fmt.Fprintf(&b, "#include <gbdk/platform.h>\n")
	fmt.Fprintf(&b, "#include <stdint.h>\n")
	fmt.Fprintf(&b, "#include \"%s.h\"\n\n", name)

	fmt.Fprintf(&b, "static const palette_color_t black_pal[] = {0x0000,0x0000,0x0000,0x0000};\n\n")

	fmt.Fprintf(&b, "void main(void) {\n")

	if mode == ModeDMG {
		fmt.Fprintf(&b, "    BGP_REG = DMG_PALETTE(DMG_BLACK, DMG_BLACK, DMG_BLACK, DMG_BLACK);\n\n")
		fmt.Fprintf(&b, "    set_bkg_data(0, %s_TILE_COUNT, %s_tiles);\n", upper, name)
		fmt.Fprintf(&b, "    set_bkg_tiles(0, 0, %s_WIDTH/8, %s_HEIGHT/8, %s_map);\n\n", upper, upper, name)
		fmt.Fprintf(&b, "    SHOW_BKG;\n")
		fmt.Fprintf(&b, "    vsync();\n\n")
		fmt.Fprintf(&b, "    BGP_REG = DMG_PALETTE(DMG_WHITE, DMG_LITE_GRAY, DMG_DARK_GRAY, DMG_BLACK);\n")
	} else {
		fmt.Fprintf(&b, "    if (_cpu == CGB_TYPE) {\n")
		fmt.Fprintf(&b, "        set_bkg_palette(BKGF_CGB_PAL0, 1U, black_pal);\n")
		fmt.Fprintf(&b, "    } else {\n")
		fmt.Fprintf(&b, "        BGP_REG = DMG_PALETTE(DMG_BLACK, DMG_BLACK, DMG_BLACK, DMG_BLACK);\n")
		fmt.Fprintf(&b, "    }\n\n")
		// CGB has two 256-tile VRAM banks; a full 160x144 image needs 360 tiles.
		// Tiles 0-255 go to bank 0; tiles 256+ go to bank 1 (attr bit 3 set).
		fmt.Fprintf(&b, "#if %s_TILE_COUNT > 256\n", upper)
		fmt.Fprintf(&b, "    set_bkg_data(%s_TILE_ORIGIN, 256U, %s_tiles);\n", upper, name)
		fmt.Fprintf(&b, "    if (_cpu == CGB_TYPE) {\n")
		fmt.Fprintf(&b, "        VBK_REG = 1;\n")
		fmt.Fprintf(&b, "        set_bkg_data(0U, (uint16_t)(%s_TILE_COUNT - 256U), %s_tiles + 256U * 16U);\n", upper, name)
		fmt.Fprintf(&b, "        VBK_REG = 0;\n")
		fmt.Fprintf(&b, "    }\n")
		fmt.Fprintf(&b, "#else\n")
		fmt.Fprintf(&b, "    set_bkg_data(%s_TILE_ORIGIN, %s_TILE_COUNT, %s_tiles);\n", upper, upper, name)
		fmt.Fprintf(&b, "#endif\n")
		fmt.Fprintf(&b, "    set_bkg_tiles(0, 0, %s_WIDTH/8, %s_HEIGHT/8, %s_map);\n\n", upper, upper, name)
		fmt.Fprintf(&b, "    if (_cpu == CGB_TYPE) {\n")
		fmt.Fprintf(&b, "        VBK_REG = VBK_ATTRIBUTES;\n")
		fmt.Fprintf(&b, "        set_bkg_tiles(0, 0, %s_WIDTH/8, %s_HEIGHT/8, %s_attr);\n", upper, upper, name)
		fmt.Fprintf(&b, "        VBK_REG = VBK_TILES;\n")
		fmt.Fprintf(&b, "    }\n\n")
		fmt.Fprintf(&b, "    SHOW_BKG;\n")
		fmt.Fprintf(&b, "    vsync();\n\n")
		fmt.Fprintf(&b, "    if (_cpu == CGB_TYPE) {\n")
		fmt.Fprintf(&b, "        set_bkg_palette(BKGF_CGB_PAL0, %s_PALETTE_COUNT, %s_palettes);\n", upper, name)
		fmt.Fprintf(&b, "    } else {\n")
		fmt.Fprintf(&b, "        BGP_REG = DMG_PALETTE(DMG_WHITE, DMG_LITE_GRAY, DMG_DARK_GRAY, DMG_BLACK);\n")
		fmt.Fprintf(&b, "    }\n")
	}

	fmt.Fprintf(&b, "\n    while(1) { vsync(); }\n")
	fmt.Fprintf(&b, "}\n")

	return os.WriteFile(filepath.Join(dir, "main.c"), b.Bytes(), 0644)
}

func generateVideoPlayerC(dir, name string) error {
	upper := strings.ToUpper(name)

	// Architecture
	// ────────────
	// The BG map is only 32×18 = 576 bytes. GBC General-Purpose DMA (GDMA)
	// copies that WRAM→VRAM in ~0.27 ms; both planes (tile indices + CGB
	// attributes) = ~0.55 ms — comfortably inside one VBlank (~1.09 ms), while
	// the PPU isn't drawing and VRAM is free. So the whole map update lands in
	// a single VBlank with the display ON: no tearing, no flicker, no DISPLAY_OFF
	// and no double-buffering needed (HBlank DMA / two map areas would only help
	// if the payload exceeded a VBlank — it doesn't).
	//
	// Per delta frame:
	//   1. Apply the delta to a 32-wide WRAM shadow (cumulative map state).
	//      ScreenPos is pre-baked to the 32-wide VRAM stride by the encoder, so
	//      the shadow GDMAs straight in without per-row fix-ups.
	//   2. Upload new tiles to fresh, never-displayed slots, spread over vblank
	//      windows (display stays on — those slots aren't on screen yet).
	//   3. vsync() into VBlank, then GDMA both shadow planes to 0x9800.
	//
	// The encoder marks each frame light or keyframe, never partial:
	//
	//   Light frame — all new tiles fit in off-screen slots. Spread-upload them
	//   (display on, old frame still shown), then push the whole map atomically:
	//   CGB GDMAs both planes in one VBlank; DMG writes the changed cells in one
	//   VBlank. The new frame appears cleanly, no flicker, no display-off.
	//
	//   Keyframe — new tiles don't fit beside the shown frame. CGB fades the
	//   palette to black, reloads VRAM while black (display on → no white flash),
	//   GDMAs the map, then fades back in: a clean crossfade. DMG can't colour-fade
	//   and has no GDMA, so its keyframes use a brief DISPLAY_OFF bulk load.
	tmpl := `#include <gbdk/platform.h>
#include <stdint.h>
#include "{{N}}.h"

#define VIS_W       20U
#define VIS_H       18U
#define MAP_W       32U   /* VRAM BG-map row stride */
#define SHADOW_SZ   576U  /* 32 * 18 — laid out to match the VRAM stride */
#define SHADOW_BLKS 36U   /* 576 / 16 */

/* GBC DMA registers (HDMA1-4 = src/dst, HDMA5 = length/mode/start). */
#define REG_HDMA1 (*(volatile uint8_t *)0xFF51U)
#define REG_HDMA2 (*(volatile uint8_t *)0xFF52U)
#define REG_HDMA3 (*(volatile uint8_t *)0xFF53U)
#define REG_HDMA4 (*(volatile uint8_t *)0xFF54U)
#define REG_HDMA5 (*(volatile uint8_t *)0xFF55U)

/* 32-wide WRAM shadow of the BG map + CGB attributes. +15 bytes so it can be
   bumped up to the 16-byte boundary the DMA source requires. */
static uint8_t _smraw[SHADOW_SZ + 15U];
static uint8_t _saraw[SHADOW_SZ + 15U];
static uint8_t *shadow_map;
static uint8_t *shadow_attr;

/* General-purpose DMA: bulk-copy SHADOW_SZ bytes WRAM→VRAM with the CPU halted
   until done. Valid with display off, or inside VBlank where VRAM is free. */
static void gdma(const uint8_t *src, uint16_t dst) {
    uint16_t s = (uint16_t)src;
    REG_HDMA1 = (uint8_t)(s >> 8);
    REG_HDMA2 = (uint8_t)s;            /* low nibble ignored → src 16-aligned */
    REG_HDMA3 = (uint8_t)(dst >> 8);   /* high bits masked to VRAM by hardware */
    REG_HDMA4 = (uint8_t)dst;          /* low nibble ignored → dst 16-aligned */
    REG_HDMA5 = (uint8_t)(SHADOW_BLKS - 1U); /* bit7=0 → GP-DMA, runs now */
}

/* Push both shadow planes to the on-screen BG map at 0x9800 (CGB). */
static void blit_map_gdma(void) {
    gdma(shadow_map, 0x9800U);
    VBK_REG = 1U;
    gdma(shadow_attr, 0x9800U);
    VBK_REG = 0U;
}

/* DMG full blit (no DMA): copy the 20 visible columns of each row from the
   shadow. Used only for the one-time blanked bulk load (display off). */
static void blit_map_dmg(void) {
    uint8_t row;
    for (row = 0U; row < VIS_H; row++)
        set_bkg_tiles(0U, row, VIS_W, 1U, &shadow_map[(uint16_t)row * MAP_W]);
}

/* DMG delta blit: write only the changed cells straight to the BG map at
   0x9800. Must run in VBlank; the encoder caps the count to fit one vblank. */
static void blit_updates_dmg(uint16_t n, const uint16_t *p, const uint8_t *t) {
    uint16_t i;
    for (i = 0U; i < n; i++)
        set_vram_byte((uint8_t *)(0x9800U + p[i]), t[i]);
}

/* Upload new tile data spread across vblank windows while display stays ON.
   Slots are guaranteed fresh (never referenced by the visible frame). */
static void upload_spread(uint16_t uc, const uint16_t *uslots, const uint8_t *udata) {
    uint16_t i = 0U, e, s;
    const uint8_t *d;
    while (i < uc) {
        e = i + 16U; if (e > uc) e = uc;
        vsync();
        for (; i < e; i++) {
            s = uslots[i]; d = udata + (uint16_t)i * 16U;
            if ((_cpu == CGB_TYPE) && (s >= 256U)) {
                VBK_REG = 1U; set_bkg_data((uint8_t)(s - 256U), 1U, d); VBK_REG = 0U;
            } else {
                set_bkg_data((uint8_t)s, 1U, d);
            }
        }
    }
}

/* Bulk tile upload for DMG keyframes (DISPLAY_OFF → VRAM free). */
static void upload_bulk(uint8_t first, uint16_t n, const uint8_t *data, uint8_t bank1) {
    uint16_t chunk;
    if (!n) return;
    if (bank1 && (_cpu == CGB_TYPE)) VBK_REG = 1U;
    while (n) {
        chunk = (n > 128U) ? 128U : n;
        set_bkg_data(first, (uint8_t)chunk, data);
        first = (uint8_t)((uint16_t)first + chunk);
        data += chunk * 16U; n -= chunk;
    }
    if (bank1 && (_cpu == CGB_TYPE)) VBK_REG = 0U;
}

/* ── CGB palette fade (keyframe crossfade, display stays on) ─────────────── */
#define FADE_STEPS 4U
static const palette_color_t *cur_pal; /* palette block currently on screen */
static palette_color_t fade_buf[32];

/* Write all 8 palettes from src, each channel scaled by num/FADE_STEPS. */
static void set_pal_scaled(const palette_color_t *src, uint8_t num) {
    uint8_t i, r, g, bl;
    uint16_t c;
    for (i = 0U; i < 32U; i++) {
        c  = src[i];
        r  = (uint8_t)(((uint16_t)(c & 31U) * num) / FADE_STEPS);
        g  = (uint8_t)(((uint16_t)((c >> 5) & 31U) * num) / FADE_STEPS);
        bl = (uint8_t)(((uint16_t)((c >> 10) & 31U) * num) / FADE_STEPS);
        fade_buf[i] = (palette_color_t)(r | (g << 5) | ((uint16_t)bl << 10));
    }
    set_bkg_palette(BKGF_CGB_PAL0, 8U, fade_buf);
}

/* Fade the on-screen palette down to black over FADE_STEPS vblanks. */
static void fade_out(void) {
    uint8_t s;
    if (!cur_pal) return; /* nothing shown yet (boot) */
    for (s = FADE_STEPS; s != 0U; s--) { set_pal_scaled(cur_pal, s - 1U); vsync(); }
}

/* Fade pal up from black to full over FADE_STEPS vblanks; remember it. */
static void fade_in(const palette_color_t *pal) {
    uint8_t s;
    for (s = 1U; s <= FADE_STEPS; s++) { set_pal_scaled(pal, s); vsync(); }
    cur_pal = pal;
}

void main(void) {
    uint16_t f, uc, nc, i, k;
    const uint16_t *uslots, *pos;
    const uint8_t  *udata, *tidx, *attr;
    const palette_color_t *pal;
    uint8_t d;

    if (_cpu == CGB_TYPE) cpu_fast();

    /* Bump shadow buffers up to a 16-byte boundary for the DMA source. */
    shadow_map  = (uint8_t *)(((uint16_t)_smraw + 0x0FU) & ~(uint16_t)0x0FU);
    shadow_attr = (uint8_t *)(((uint16_t)_saraw + 0x0FU) & ~(uint16_t)0x0FU);
    cur_pal = 0;

    /* On CGB, zero all BG palettes before display-on so the initial
       upload_spread (frame 0 light frame) is invisible — tile slot 0
       is displayed everywhere until the first GDMA, and uploading to it
       with a non-black palette would show corruption for ~18 VBlanks. */
    if (_cpu == CGB_TYPE) {
        set_bkg_palette(BKGF_CGB_PAL0, 8U, fade_buf); /* fade_buf is zero-init */
    }

    SHOW_BKG;
    DISPLAY_ON;

    for (;;) {
        for (f = 0U; f < {{U}}_FRAME_COUNT; f++) {
            SWITCH_ROM({{N}}_frame_banks[f]);

            /* Apply the map delta to the WRAM shadow (instant, no VRAM access).
               pos[] is already in 32-wide VRAM-map coordinates. */
            nc   = {{N}}_update_counts[f];
            pos  = {{N}}_pos_table[f];
            tidx = {{N}}_tidx_table[f];
            attr = {{N}}_attr_table[f];
            for (i = 0U; i < nc; i++) {
                shadow_map[pos[i]]  = tidx[i];
                shadow_attr[pos[i]] = attr[i];
            }

            uc     = {{N}}_upload_counts[f];
            uslots = {{N}}_uslots_table[f];
            udata  = {{N}}_udata_table[f];
            pal    = &{{N}}_palettes[f * 32U];

            if ({{N}}_keyframe[f]) {
                if (_cpu == CGB_TYPE) {
                    /* Crossfade: fade to black, reload VRAM while black (display
                       stays on → no white flash), swap the map, fade back in. */
                    fade_out();
                    upload_spread(uc, uslots, udata);
                    vsync();
                    blit_map_gdma();
                    fade_in(pal);
                } else {
                    /* DMG: no colour fade / no GDMA → brief blanked bulk load. */
                    DISPLAY_OFF;
                    BGP_REG = DMG_PALETTE(DMG_WHITE, DMG_LITE_GRAY, DMG_DARK_GRAY, DMG_BLACK);
                    if (uc) {
                        k = 0U;
                        while ((k < uc) && (uslots[k] < 256U)) k++;
                        upload_bulk((uint8_t)uslots[0U], k, udata, 0U);
                        if (k < uc)
                            upload_bulk((uint8_t)(uslots[k] - 256U), uc - k,
                                        udata + (uint16_t)k * 16U, 1U);
                    }
                    blit_map_dmg();
                    DISPLAY_ON;
                }

            } else {
                /* Light frame: new tiles go to off-screen slots (display on),
                   then the whole map swaps atomically — clean, no flicker. */
                upload_spread(uc, uslots, udata);
                vsync(); /* enter VBlank */
                if (_cpu == CGB_TYPE) {
                    if ({{N}}_new_palette[f]) { set_bkg_palette(BKGF_CGB_PAL0, 8U, pal); cur_pal = pal; }
                    blit_map_gdma();
                } else {
                    if ({{N}}_new_palette[f])
                        BGP_REG = DMG_PALETTE(DMG_WHITE, DMG_LITE_GRAY, DMG_DARK_GRAY, DMG_BLACK);
                    blit_updates_dmg(nc, pos, tidx); /* changed cells only, in VBlank */
                }
            }

            /* Hold the frame. */
            d = {{N}}_delays[f];
            do { vsync(); } while (--d);
        }
    }
}
`
	out := strings.ReplaceAll(tmpl, "{{N}}", name)
	out = strings.ReplaceAll(out, "{{U}}", upper)
	return os.WriteFile(filepath.Join(dir, "main.c"), []byte(out), 0644)
}
