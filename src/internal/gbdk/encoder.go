package gbdk

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"MediaBoy/internal/core"
	"MediaBoy/internal/imaging"
	"MediaBoy/internal/proc"
)

func savePNG(img image.Image, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func CleanOutputDir(dir string) {
	for _, pat := range []string{"*.c", "*.h"} {
		if matches, err := filepath.Glob(filepath.Join(dir, pat)); err == nil {
			for _, m := range matches {
				_ = os.Remove(m)
			}
		}
	}
	_ = os.RemoveAll(filepath.Join(dir, "obj"))
}

func EncodeTile(t core.Tile) [16]byte {
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

func ExportGBDK(dir, name string, tiles [][]core.Tile, palettes [8]core.Palette) error {
	h := len(tiles)
	if h == 0 {
		return fmt.Errorf("empty image")
	}
	w := len(tiles[0])

	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	CleanOutputDir(dir)

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
			raw := EncodeTile(tiles[y][x])
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
			fmt.Fprintf(&src, "%d,", idx%256)
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
				bankBit = 0x08
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
				fmt.Fprintf(&src, "0x%04X,", uint16(core.ToRGB5(p.Colors[i])))
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

	return generateStaticPlayerC(dir, name, core.ModeCGB)
}

func ExportGBDKHiColor(dir, name, gbdkHome string, img image.Image) error {
	if img == nil {
		return fmt.Errorf("no image")
	}

	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	CleanOutputDir(dir)

	pngPath := filepath.Join(dir, name+".png")
	if err := savePNG(img, pngPath); err != nil {
		return err
	}

	tool := filepath.Join(gbdkHome, "bin", "png2hicolorgb")
	if _, err := os.Stat(tool); err != nil {
		if _, err2 := os.Stat(tool + ".exe"); err2 != nil {
			return fmt.Errorf("png2hicolorgb not found at %s(.exe) — check GBDK Home in settings", tool)
		}
	}
	cmd := proc.Command(tool, name+".png", "--csource", "--bank=255", "-o", name, "-s", name)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("png2hicolorgb failed: %v\n%s", err, out)
	}

	if err := writeVendoredLibs(dir); err != nil {
		return fmt.Errorf("writing vendored libs: %v", err)
	}

	dmgTiles, _ := imaging.TileficationDMG(img)
	uniq, tmap, w, h := imaging.DedupTilesForPrint(dmgTiles)
	if err := emitDMGData(dir, name, uniq, tmap, w, h); err != nil {
		return err
	}

	return writeHiColorMain(dir, name)
}

func emitDMGData(dir, name string, uniq []core.Tile, tmap []uint8, w, h int) error {
	upper := strings.ToUpper(name)
	var hdr, src bytes.Buffer
	fmt.Fprintf(&hdr, "#ifndef %s_DMG_H_GUARD\n#define %s_DMG_H_GUARD\n#include <stdint.h>\n#include <gbdk/platform.h>\n\n", upper, upper)
	fmt.Fprintf(&hdr, "#define %s_DMG_W %d\n#define %s_DMG_H %d\n#define %s_DMG_TILES %d\n\n", upper, w, upper, h, upper, len(uniq))
	fmt.Fprintf(&hdr, "BANKREF_EXTERN(%s_dmg)\n", name)
	fmt.Fprintf(&hdr, "extern const uint8_t %s_dmg_tiles[%d];\nextern const uint8_t %s_dmg_map[%d];\n#endif\n", name, len(uniq)*16, name, len(tmap))

	// The HiColor ROM is built with -autobank; this data lands in an
	// auto-assigned bank, so BANKREF lets the player SWITCH_ROM to it before
	// reading (otherwise the DMG fallback reads garbage from the wrong bank).
	fmt.Fprintf(&src, "#include \"%s_dmg.h\"\n\nBANKREF(%s_dmg)\n\n", name, name)
	fmt.Fprintf(&src, "const uint8_t %s_dmg_tiles[] = {", name)
	for _, t := range uniq {
		for _, b := range EncodeTile(t) {
			fmt.Fprintf(&src, "0x%02X,", b)
		}
	}
	fmt.Fprintf(&src, "};\n\nconst uint8_t %s_dmg_map[] = {", name)
	for _, m := range tmap {
		fmt.Fprintf(&src, "%d,", m)
	}
	fmt.Fprintf(&src, "};\n")

	if err := os.WriteFile(filepath.Join(dir, name+"_dmg.h"), hdr.Bytes(), 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name+"_dmg.c"), src.Bytes(), 0644)
}

func writeHiColorMain(dir, name string) error {
	tmpl := `#include <gbdk/platform.h>
#include <stdbool.h>
#include <gbc_hicolor.h>
#include "gbprinter.h"
#include "{{N}}.h"
#include "{{N}}_dmg.h"

static const palette_color_t gray_pal[] = {0x7FFF, 0x5294, 0x294A, 0x0000};
static const uint8_t white_tile[16] = {0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0};

bool printer_check_cancel(void) {
    static uint8_t keys = 0u, old_keys;
    old_keys = keys; keys = joypad();
    return (((old_keys ^ keys) & J_B) & (keys & J_B)) != 0u;
}

static void show_dmg(void) {
    SWITCH_ROM(BANK({{N}}_dmg)); /* the fallback data lives in an autobank bank */
    VBK_REG = 0u;
#if {{U}}_DMG_TILES > 128
    set_bkg_data(0u, 128u, {{N}}_dmg_tiles);
    set_bkg_data(128u, (uint8_t)({{U}}_DMG_TILES - 128u), {{N}}_dmg_tiles + 128u * 16u);
#else
    set_bkg_data(0u, {{U}}_DMG_TILES, {{N}}_dmg_tiles);
#endif
    set_bkg_tiles(0u, 0u, {{U}}_DMG_W, {{U}}_DMG_H, {{N}}_dmg_map);
    if (_cpu == CGB_TYPE) {
        set_bkg_palette(0u, 1u, gray_pal);
        VBK_REG = 1u;
        fill_bkg_rect(0u, 0u, 20u, 18u, 0u);
        VBK_REG = 0u;
    } else {
        BGP_REG = DMG_PALETTE(DMG_WHITE, DMG_LITE_GRAY, DMG_DARK_GRAY, DMG_BLACK);
    }
}

static void enter_hicolor(void) {
    DISPLAY_OFF;
    SCY_REG = 0u;
    hicolor_start(&{{N}}_data, BANK({{N}}));
    DISPLAY_ON;
}

/* Scrolls the image up in step with the printer as each tile-row is sent, so
   it "feeds out" while it actually prints rather than after a long pause. */
static void print_feed(uint8_t row, uint8_t rows) {
    SCY_REG = (uint8_t)(((uint16_t)(row + 1u) * 144u) / rows);
}

/* START leaves HiColor (the beam-racer owns every palette and all 256 tiles),
   drops to the grayscale copy, sends it to the GB Printer feeding the picture
   out on screen in time with the print, then re-enters HiColor. */
static void do_print(void) {
    uint8_t white = ({{U}}_DMG_TILES < 256u) ? (uint8_t){{U}}_DMG_TILES : 255u;
    uint8_t s;

    hicolor_stop();
    /* hicolor_stop() only removes the LCD ISR handler; the per-scanline STAT
       (LY==152) interrupt stays enabled and corrupts the timing-sensitive GB
       Printer serial handshake. Run the print with only VBlank enabled;
       enter_hicolor() re-enables LCD on return. */
    set_interrupts(VBL_IFLAG);
    STAT_REG = 0u;
    DISPLAY_OFF;
    show_dmg();
    /* Blank "paper" below the image so the scroll-out reveals white. */
    VBK_REG = 0u;
    set_bkg_data(white, 1u, white_tile);
    fill_bkg_rect(0u, 18u, 20u, 14u, white);
    if (_cpu == CGB_TYPE) {
        VBK_REG = 1u;
        fill_bkg_rect(0u, 18u, 20u, 14u, 0u);
        VBK_REG = 0u;
    }
    DISPLAY_ON;

    /* A printer that just finished a job briefly reports a non-ready status, so
       a single 10-frame detect would fail on every print after the first.
       Re-detect a few times, letting it settle between tries. print_feed
       scrolls the image out in step with the print itself. */
    uint8_t printed = 0u;
    printer_row_cb = print_feed;
    for (uint8_t t = 0u; t < 8u; t++) {
        if (gbprinter_detect(PRINTER_DETECT_TIMEOUT) == PRN_STATUS_OK) {
            gbprinter_print_image({{N}}_dmg_map, {{N}}_dmg_tiles,
                                  (PRN_TILE_WIDTH - {{U}}_DMG_W) / 2, {{U}}_DMG_W, {{U}}_DMG_H);
            printed = 1u;
            break;
        }
        for (uint8_t w = 0u; w < 10u; w++) vsync();
    }
    printer_row_cb = 0;

    /* No printer connected: still play the feed animation so Start gives feedback. */
    if (!printed)
        for (s = 0u; s < 144u; s += 2u) { SCY_REG = s; vsync(); }

    for (s = 0u; s < 30u; s++) vsync();
    SCY_REG = 0u;

    if (_cpu == CGB_TYPE) enter_hicolor();
    else { DISPLAY_OFF; show_dmg(); DISPLAY_ON; }
}

void main(void) {
    if (_cpu == CGB_TYPE) {
        cpu_fast();
        enter_hicolor();
    } else {
        DISPLAY_OFF;
        show_dmg();
        DISPLAY_ON;
    }
    for (;;) {
        vsync();
        if (joypad() & J_START) {
            do_print();
            waitpadup();
        }
    }
}
`
	out := strings.ReplaceAll(tmpl, "{{N}}", name)
	out = strings.ReplaceAll(out, "{{U}}", strings.ToUpper(name))
	return os.WriteFile(filepath.Join(dir, "main.c"), []byte(out), 0644)
}

func ExportGBDKDMG(dir, name string, tiles [][]core.Tile, palette core.Palette) error {
	if len(tiles) == 0 {
		return fmt.Errorf("empty image")
	}

	// DMG has a single BG tile bank (≤256 addressable tiles) and an 8-bit tile
	// map. Emitting one tile per cell (360) overflows both the VRAM tile budget
	// and the uint8 map, so dedup down to ≤256 unique tiles with a proper map.
	uniq, tmap, w, h := imaging.DedupTilesForPrint(tiles)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	CleanOutputDir(dir)

	guard := strings.ToUpper(name) + "_H"
	upper := strings.ToUpper(name)

	var hdr bytes.Buffer
	var src bytes.Buffer

	fmt.Fprintf(&hdr, "#ifndef %s\n#define %s\n\n", guard, guard)
	fmt.Fprintf(&hdr, "#include <stdint.h>\n")
	fmt.Fprintf(&hdr, "#include <gbdk/platform.h>\n\n")
	fmt.Fprintf(&hdr, "#define %s_WIDTH %d\n", upper, w*8)
	fmt.Fprintf(&hdr, "#define %s_HEIGHT %d\n", upper, h*8)
	fmt.Fprintf(&hdr, "#define %s_TILE_COUNT %d\n\n", upper, len(uniq))
	fmt.Fprintf(&hdr, "BANKREF_EXTERN(%s)\n\n", name)
	fmt.Fprintf(&hdr, "extern const uint8_t %s_tiles[%d];\n", name, len(uniq)*16)
	fmt.Fprintf(&hdr, "extern const uint8_t %s_map[%d];\n\n", name, len(tmap))
	fmt.Fprintf(&hdr, "#endif\n")

	fmt.Fprintf(&src, "#include \"%s.h\"\n\n", name)
	fmt.Fprintf(&src, "BANKREF(%s)\n\n", name)

	fmt.Fprintf(&src, "const uint8_t %s_tiles[] = {\n", name)
	for _, t := range uniq {
		for _, b := range EncodeTile(t) {
			fmt.Fprintf(&src, "0x%02X,", b)
		}
		fmt.Fprintf(&src, "\n")
	}
	fmt.Fprintf(&src, "};\n\n")

	fmt.Fprintf(&src, "const uint8_t %s_map[] = {\n", name)
	pos := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fmt.Fprintf(&src, "%d,", tmap[pos])
			pos++
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

	return generateStaticPlayerC(dir, name, core.ModeDMG)
}

func generateStaticPlayerC(dir, name string, mode core.ConvertMode) error {
	upper := strings.ToUpper(name)
	var b bytes.Buffer

	fmt.Fprintf(&b, "#include <gbdk/platform.h>\n")
	fmt.Fprintf(&b, "#include <stdint.h>\n")
	fmt.Fprintf(&b, "#include \"%s.h\"\n\n", name)

	fmt.Fprintf(&b, "static const palette_color_t black_pal[] = {0x0000,0x0000,0x0000,0x0000};\n\n")

	fmt.Fprintf(&b, "void main(void) {\n")
	// VRAM tile/map uploads only land with the LCD off — at program start the
	// boot ROM leaves the display on, so blank it before touching VRAM.
	fmt.Fprintf(&b, "    DISPLAY_OFF;\n\n")

	if mode == core.ModeDMG {
		fmt.Fprintf(&b, "    BGP_REG = DMG_PALETTE(DMG_BLACK, DMG_BLACK, DMG_BLACK, DMG_BLACK);\n\n")
		// set_bkg_data's count is uint8_t, so load ≤128 tiles per call.
		fmt.Fprintf(&b, "#if %s_TILE_COUNT > 128\n", upper)
		fmt.Fprintf(&b, "    set_bkg_data(0, 128U, %s_tiles);\n", name)
		fmt.Fprintf(&b, "    set_bkg_data(128U, (uint8_t)(%s_TILE_COUNT - 128U), %s_tiles + 128U * 16U);\n", upper, name)
		fmt.Fprintf(&b, "#else\n")
		fmt.Fprintf(&b, "    set_bkg_data(0, %s_TILE_COUNT, %s_tiles);\n", upper, name)
		fmt.Fprintf(&b, "#endif\n")
		fmt.Fprintf(&b, "    set_bkg_tiles(0, 0, %s_WIDTH/8, %s_HEIGHT/8, %s_map);\n\n", upper, upper, name)
		fmt.Fprintf(&b, "    SHOW_BKG;\n")
		fmt.Fprintf(&b, "    DISPLAY_ON;\n")
		fmt.Fprintf(&b, "    vsync();\n\n")
		fmt.Fprintf(&b, "    BGP_REG = DMG_PALETTE(DMG_WHITE, DMG_LITE_GRAY, DMG_DARK_GRAY, DMG_BLACK);\n")
	} else {
		fmt.Fprintf(&b, "    if (_cpu == CGB_TYPE) {\n")
		fmt.Fprintf(&b, "        set_bkg_palette(BKGF_CGB_PAL0, 1U, black_pal);\n")
		fmt.Fprintf(&b, "    } else {\n")
		fmt.Fprintf(&b, "        BGP_REG = DMG_PALETTE(DMG_BLACK, DMG_BLACK, DMG_BLACK, DMG_BLACK);\n")
		fmt.Fprintf(&b, "    }\n\n")

		fmt.Fprintf(&b, "#if %s_TILE_COUNT > 256\n", upper)
		// set_bkg_data's count is uint8_t; loading 256 in one call would wrap to 0.
		fmt.Fprintf(&b, "    set_bkg_data(%s_TILE_ORIGIN, 128U, %s_tiles);\n", upper, name)
		fmt.Fprintf(&b, "    set_bkg_data(%s_TILE_ORIGIN + 128U, 128U, %s_tiles + 128U * 16U);\n", upper, name)
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
		fmt.Fprintf(&b, "    DISPLAY_ON;\n")
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
