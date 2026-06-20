package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func savePNG(img image.Image, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

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
	cleanOutputDir(dir)

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
	cmd := exec.Command(tool, name+".png", "--csource", "--bank=255", "-o", name, "-s", name)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("png2hicolorgb failed: %v\n%s", err, out)
	}

	if err := writeVendoredLibs(dir); err != nil {
		return fmt.Errorf("writing vendored libs: %v", err)
	}

	dmgTiles, _ := tileficationDMG(img)
	uniq, tmap, w, h := dedupTilesForPrint(dmgTiles)
	if err := emitDMGData(dir, name, uniq, tmap, w, h); err != nil {
		return err
	}

	return writeHiColorMain(dir, name)
}

func emitDMGData(dir, name string, uniq []Tile, tmap []uint8, w, h int) error {
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
		for _, b := range encodeTile(t) {
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

/* START leaves HiColor (the beam-racer owns every palette and all 256 tiles),
   drops to the grayscale copy, sends it to the GB Printer, then plays the
   classic "paper feeds out" scroll-up animation and re-enters HiColor. */
static void do_print(void) {
    uint8_t white = ({{U}}_DMG_TILES < 256u) ? (uint8_t){{U}}_DMG_TILES : 255u;
    uint8_t s;

    hicolor_stop();
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

    if (gbprinter_detect(PRINTER_DETECT_TIMEOUT) == PRN_STATUS_OK)
        gbprinter_print_image({{N}}_dmg_map, {{N}}_dmg_tiles,
                              (PRN_TILE_WIDTH - {{U}}_DMG_W) / 2, {{U}}_DMG_W, {{U}}_DMG_H);

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

func copyFileBytes(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0644)
}

func ExportGBDKDMG(dir, name string, tiles [][]Tile, palette Palette) error {
	if len(tiles) == 0 {
		return fmt.Errorf("empty image")
	}

	// DMG has a single BG tile bank (≤256 addressable tiles) and an 8-bit tile
	// map. Emitting one tile per cell (360) overflows both the VRAM tile budget
	// and the uint8 map, so dedup down to ≤256 unique tiles with a proper map.
	uniq, tmap, w, h := dedupTilesForPrint(tiles)

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
	fmt.Fprintf(&hdr, "#define %s_TILE_COUNT %d\n\n", upper, len(uniq))
	fmt.Fprintf(&hdr, "BANKREF_EXTERN(%s)\n\n", name)
	fmt.Fprintf(&hdr, "extern const uint8_t %s_tiles[%d];\n", name, len(uniq)*16)
	fmt.Fprintf(&hdr, "extern const uint8_t %s_map[%d];\n\n", name, len(tmap))
	fmt.Fprintf(&hdr, "#endif\n")

	fmt.Fprintf(&src, "#include \"%s.h\"\n\n", name)
	fmt.Fprintf(&src, "BANKREF(%s)\n\n", name)

	fmt.Fprintf(&src, "const uint8_t %s_tiles[] = {\n", name)
	for _, t := range uniq {
		for _, b := range encodeTile(t) {
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

	return generateStaticPlayerC(dir, name, ModeDMG)
}

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

	var src bytes.Buffer
	fmt.Fprintf(&src, "#include \"%s.h\"\n\n", name)

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
	// VRAM tile/map uploads only land with the LCD off — at program start the
	// boot ROM leaves the display on, so blank it before touching VRAM.
	fmt.Fprintf(&b, "    DISPLAY_OFF;\n\n")

	if mode == ModeDMG {
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

func generateVideoPlayerC(dir, name string) error {
	upper := strings.ToUpper(name)

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

/* Tiles GP-DMA'd to VRAM per VBlank (CGB). 64 tiles = 64 blocks ≈ 512 CPU cycles,
   comfortably inside the ~1140-cycle VBlank window with margin. ~4× the old
   16-tiles/VBlank set_bkg_data path, and reliable (deterministic GP-DMA). */
#define UPLOAD_BUDGET 64U

/* 32-wide WRAM shadow of the BG map + CGB attributes. +15 bytes so it can be
   bumped up to the 16-byte boundary the DMA source requires. */
static uint8_t _smraw[SHADOW_SZ + 15U];
static uint8_t _saraw[SHADOW_SZ + 15U];
static uint8_t *shadow_map;
static uint8_t *shadow_attr;

/* 16-aligned WRAM staging buffer for batched GP-DMA tile uploads (CGB). */
static uint8_t _stageraw[UPLOAD_BUDGET * 16U + 15U];
static uint8_t *tile_stage;

/* General-purpose DMA: copy nblk 16-byte blocks src(16-aligned)→dst(VRAM) with
   the CPU halted until done. Valid with display off, or inside VBlank. */
static void gdma_n(const uint8_t *src, uint16_t dst, uint8_t nblk) {
    uint16_t s = (uint16_t)src;
    REG_HDMA1 = (uint8_t)(s >> 8);
    REG_HDMA2 = (uint8_t)s;            /* low nibble ignored → src 16-aligned */
    REG_HDMA3 = (uint8_t)(dst >> 8);   /* high bits masked to VRAM by hardware */
    REG_HDMA4 = (uint8_t)dst;          /* low nibble ignored → dst 16-aligned */
    REG_HDMA5 = (uint8_t)(nblk - 1U);  /* bit7=0 → GP-DMA, runs now */
}

/* Bulk-copy the whole BG-map shadow plane (SHADOW_SZ bytes) WRAM→VRAM. */
static void gdma(const uint8_t *src, uint16_t dst) {
    gdma_n(src, dst, SHADOW_BLKS);
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

/* CGB fast upload: stage a batch of tiles into WRAM during active display (WRAM
   writes are free there), then GP-DMA them to their slots inside one VBlank.
   Consecutive same-bank slots are sent as a single GP-DMA run. ~4× upload_spread.
   Slots are guaranteed off-screen, so the writes can't disturb the shown frame. */
static void upload_gdma(uint16_t uc, const uint16_t *uslots, const uint8_t *udata) {
    uint16_t i = 0U;
    while (i < uc) {
        /* Stage up to UPLOAD_BUDGET tiles into WRAM now (display on → free). */
        uint8_t n = 0U, kk;
        const uint8_t *sp; uint8_t *dp;
        while (((uint16_t)(i + n) < uc) && (n < UPLOAD_BUDGET)) {
            sp = udata + (uint16_t)(i + n) * 16U;
            dp = tile_stage + (uint16_t)n * 16U;
            for (kk = 0U; kk < 16U; kk++) dp[kk] = sp[kk];
            n++;
        }
        /* In VBlank, GP-DMA the staged tiles to VRAM (fast; VRAM is free). */
        vsync();
        {
            uint8_t k = 0U;
            while (k < n) {
                uint16_t slot  = uslots[i + k];
                uint8_t  bank1 = (slot >= 256U) ? 1U : 0U;
                uint16_t base  = bank1 ? (uint16_t)(slot - 256U) : slot;
                uint8_t  run   = 0U;
                while ((k + run < n)) {
                    uint16_t s   = uslots[i + k + run];
                    uint8_t  b1  = (s >= 256U) ? 1U : 0U;
                    uint16_t loc = b1 ? (uint16_t)(s - 256U) : s;
                    if (b1 != bank1) break;
                    if (loc != (uint16_t)(base + run)) break;
                    run++;
                }
                if (bank1) VBK_REG = 1U;
                gdma_n(tile_stage + (uint16_t)k * 16U, (uint16_t)(0x8000U + base * 16U), run);
                if (bank1) VBK_REG = 0U;
                k += run;
            }
        }
        i += n;
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
    uint8_t d, first;

    if (_cpu == CGB_TYPE) cpu_fast();

    /* Bump shadow + staging buffers up to a 16-byte boundary for GDMA sources. */
    shadow_map  = (uint8_t *)(((uint16_t)_smraw + 0x0FU) & ~(uint16_t)0x0FU);
    shadow_attr = (uint8_t *)(((uint16_t)_saraw + 0x0FU) & ~(uint16_t)0x0FU);
    tile_stage  = (uint8_t *)(((uint16_t)_stageraw + 0x0FU) & ~(uint16_t)0x0FU);
    cur_pal = 0;

    /* On CGB, zero all BG palettes before display-on so the boot prologue load
       (frame 0) is invisible — tile slot 0 is displayed everywhere until the
       first GDMA, and uploading with a non-black palette would show corruption.
       The prologue fades in from this black; thereafter the display never blanks. */
    if (_cpu == CGB_TYPE) {
        set_bkg_palette(BKGF_CGB_PAL0, 8U, fade_buf); /* fade_buf is zero-init */
    }

    SHOW_BKG;
    DISPLAY_ON;

    /* Frame 0 is the boot prologue (loads the loop-entry VRAM state once). It runs
       on the first outer iteration only; every later loop runs frames 1..N-1, which
       dissolve cleanly from the previous frame — frame 0 is never replayed, so its
       full reload can't overwrite the still-displayed last frame. */
    first = 1U;
    for (;;) {
        for (f = first ? 0U : 1U; f < {{U}}_FRAME_COUNT; f++) {
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
                    upload_gdma(uc, uslots, udata);
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
                /* Light frame (also each scatter-dissolve sub-frame): new tiles
                   go to off-screen slots while the old frame is shown, then the
                   whole map swaps atomically — clean, display stays on. */
                if (_cpu == CGB_TYPE) {
                    upload_gdma(uc, uslots, udata); /* staged GP-DMA, ~4× faster */
                    vsync();                        /* enter VBlank */
                    if ({{N}}_new_palette[f]) { set_bkg_palette(BKGF_CGB_PAL0, 8U, pal); cur_pal = pal; }
                    blit_map_gdma();
                } else {
                    upload_spread(uc, uslots, udata); /* DMG: no GDMA → set_bkg_data */
                    vsync();                          /* enter VBlank */
                    if ({{N}}_new_palette[f])
                        BGP_REG = DMG_PALETTE(DMG_WHITE, DMG_LITE_GRAY, DMG_DARK_GRAY, DMG_BLACK);
                    blit_updates_dmg(nc, pos, tidx); /* changed cells only, in VBlank */
                }
            }

            /* Hold the frame. */
            d = {{N}}_delays[f];
            do { vsync(); } while (--d);
        }
        first = 0U; /* prologue (frame 0) only plays on the first outer pass */
    }
}
`
	out := strings.ReplaceAll(tmpl, "{{N}}", name)
	out = strings.ReplaceAll(out, "{{U}}", upper)
	return os.WriteFile(filepath.Join(dir, "main.c"), []byte(out), 0644)
}
