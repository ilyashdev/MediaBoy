package main

import (
	"bytes"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ExportGBDKImageGallery builds a multi-image HiColor ROM: every picture is a
// full HiColor image (per-scanline palettes, like the single-image export) in
// its own auto-assigned bank; ◀▶ reloads the selected image via hicolor_start.
// Each image also carries a deduplicated grayscale copy used both for the
// original-GB (DMG) fallback display and for GB Printer output.
func ExportGBDKImageGallery(cfg ConvertConfig, imgs []image.Image) (int, error) {
	if len(imgs) == 0 {
		return 0, fmt.Errorf("no images")
	}
	dir := cfg.OutputDir
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, err
	}
	cleanGalleryDir(dir)

	tool := filepath.Join(cfg.GBDKHome, "bin", "png2hicolorgb")
	if _, err := os.Stat(tool); err != nil {
		if _, err2 := os.Stat(tool + ".exe"); err2 != nil {
			return 0, fmt.Errorf("png2hicolorgb not found at %s(.exe) — check GBDK Home in settings", tool)
		}
	}

	name := cfg.Name
	n := len(imgs)

	for i, img := range imgs {
		base := fmt.Sprintf("img%02d", i)
		pngPath := filepath.Join(dir, base+".png")
		if err := savePNG(ensureGBSize(img), pngPath); err != nil {
			return 0, err
		}
		// --bank=255 is the autobank "assign me a bank" sentinel; BANKREF then
		// resolves the real bank at link time.
		cmd := exec.Command(tool, base+".png", "--csource", "--bank=255", "-o", base, "-s", base)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return 0, fmt.Errorf("png2hicolorgb failed on image %d: %v\n%s", i, err, out)
		}

		dmg, _ := tileficationDMG(ensureGBSize(img))
		uniq, pmap, _, _ := dedupTilesForPrint(dmg)
		if err := os.WriteFile(filepath.Join(dir, base+"_gray.c"), []byte(emitGalleryGray(base, uniq, pmap)), 0644); err != nil {
			return 0, err
		}
		if err := os.WriteFile(filepath.Join(dir, base+"_gray.h"), []byte(emitGalleryGrayHeader(base, len(uniq), len(pmap))), 0644); err != nil {
			return 0, err
		}
	}

	if err := os.WriteFile(filepath.Join(dir, name+".h"), []byte(galleryHeader(name, n)), 0644); err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(dir, name+"_tables.c"), []byte(galleryTables(name, n)), 0644); err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(dir, "main.c"), []byte(generateGalleryHiColorPlayerC(name, n)), 0644); err != nil {
		return 0, err
	}
	if err := writeVendoredLibs(dir); err != nil {
		return 0, err
	}

	// autobank decides the final bank count; we only report a sensible minimum.
	return nextPow2(firstImageBank + n*2), nil
}

const firstImageBank = 2

func cleanGalleryDir(dir string) {
	cleanOutputDir(dir)
	for _, pat := range []string{"*.til", "*.map", "*.atr", "*.pal", "*.png"} {
		if matches, err := filepath.Glob(filepath.Join(dir, pat)); err == nil {
			for _, m := range matches {
				_ = os.Remove(m)
			}
		}
	}
}

func emitGalleryGrayHeader(base string, tiles, mapLen int) string {
	upper := strings.ToUpper(base)
	var b bytes.Buffer
	fmt.Fprintf(&b, "#ifndef %s_GRAY_H\n#define %s_GRAY_H\n#include <stdint.h>\n#include <gbdk/platform.h>\n\n", upper, upper)
	fmt.Fprintf(&b, "#define %s_GTILES %d\n\n", upper, tiles)
	fmt.Fprintf(&b, "BANKREF_EXTERN(%s_gray)\n", base)
	fmt.Fprintf(&b, "extern const uint8_t %s_gtiles[%d];\n", base, tiles*16)
	fmt.Fprintf(&b, "extern const uint8_t %s_gmap[%d];\n#endif\n", base, mapLen)
	return b.String()
}

func emitGalleryGray(base string, uniq []Tile, pmap []uint8) string {
	var b bytes.Buffer
	// #pragma bank 255 hands each gray module to autobank (same sentinel the
	// color data uses via png2hicolorgb --bank=255). Without it every gray
	// module piles into HOME; a gallery of several images overflows bank 0 and
	// the linker relocates the later ones, so only the first image's grayscale
	// survives intact — which is why printing worked for image 0 only.
	fmt.Fprintf(&b, "#pragma bank 255\n#include \"%s_gray.h\"\n\nBANKREF(%s_gray)\n\n", base, base)
	fmt.Fprintf(&b, "const uint8_t %s_gtiles[] = {", base)
	for _, t := range uniq {
		for _, v := range encodeTile(t) {
			fmt.Fprintf(&b, "0x%02X,", v)
		}
	}
	fmt.Fprintf(&b, "};\n\nconst uint8_t %s_gmap[] = {", base)
	for _, v := range pmap {
		fmt.Fprintf(&b, "%d,", v)
	}
	fmt.Fprintf(&b, "};\n")
	return b.String()
}

func galleryHeader(name string, n int) string {
	upper := strings.ToUpper(name)
	var b bytes.Buffer
	fmt.Fprintf(&b, "#ifndef %s_H\n#define %s_H\n\n", upper, upper)
	fmt.Fprintf(&b, "#include <gbdk/platform.h>\n#include <stdint.h>\n#include <gbc_hicolor.h>\n\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "#include \"img%02d.h\"\n#include \"img%02d_gray.h\"\n", i, i)
	}
	fmt.Fprintf(&b, "\n#define NUM_IMAGES %d\n\n", n)
	fmt.Fprintf(&b, "extern const hicolor_data* const img_data[NUM_IMAGES];\n")
	fmt.Fprintf(&b, "extern const uint8_t* const img_gtiles[NUM_IMAGES];\n")
	fmt.Fprintf(&b, "extern const uint8_t* const img_gmap[NUM_IMAGES];\n")
	fmt.Fprintf(&b, "extern const uint16_t img_gtilecount[NUM_IMAGES];\n\n#endif\n")
	return b.String()
}

func galleryTables(name string, n int) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "#include \"%s.h\"\n\n", name)

	fmt.Fprintf(&b, "const hicolor_data* const img_data[NUM_IMAGES] = {")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "&img%02d_data,", i)
	}
	fmt.Fprintf(&b, "};\n")

	fmt.Fprintf(&b, "const uint8_t* const img_gtiles[NUM_IMAGES] = {")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "img%02d_gtiles,", i)
	}
	fmt.Fprintf(&b, "};\n")

	fmt.Fprintf(&b, "const uint8_t* const img_gmap[NUM_IMAGES] = {")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "img%02d_gmap,", i)
	}
	fmt.Fprintf(&b, "};\n")

	fmt.Fprintf(&b, "const uint16_t img_gtilecount[NUM_IMAGES] = {")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "IMG%02d_GTILES,", i)
	}
	fmt.Fprintf(&b, "};\n")
	return b.String()
}

func generateGalleryHiColorPlayerC(name string, n int) string {
	// BANK(x) is a link-time value, not a constant initializer, so the per-image
	// bank tables are filled at runtime here instead of in a static array.
	var initBanks strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&initBanks, "    img_bank[%d] = BANK(img%02d); img_gbank[%d] = BANK(img%02d_gray);\n", i, i, i, i)
	}

	const tmpl = `#include <gbdk/platform.h>
#include <stdbool.h>
#include <gbc_hicolor.h>
#include "gbprinter.h"
#include "%[1]s.h"

static uint8_t current;
static uint8_t img_bank[NUM_IMAGES];
static uint8_t img_gbank[NUM_IMAGES];
static const palette_color_t gray_pal[] = {0x7FFF, 0x5294, 0x294A, 0x0000};
static const uint8_t white_tile[16] = {0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0};

static void init_banks(void) {
%[2]s}

/* B cancels an in-progress print. */
bool printer_check_cancel(void) {
    static uint8_t keys = 0u, old_keys;
    old_keys = keys; keys = joypad();
    return (((old_keys ^ keys) & J_B) & (keys & J_B)) != 0u;
}

/* Load the grayscale copy of image i into VRAM. Caller must have the LCD off. */
static void load_gray(uint8_t i) {
    uint16_t gc = img_gtilecount[i];
    SWITCH_ROM(img_gbank[i]);
    VBK_REG = 0u;
    if (gc > 128u) {
        set_bkg_data(0u, 128u, img_gtiles[i]);
        set_bkg_data(128u, (uint8_t)(gc - 128u), img_gtiles[i] + 128u * 16u);
    } else {
        set_bkg_data(0u, (uint8_t)gc, img_gtiles[i]);
    }
    set_bkg_tiles(0u, 0u, 20u, 18u, img_gmap[i]);
    if (_cpu == CGB_TYPE) {
        set_bkg_palette(0u, 1u, gray_pal);
        VBK_REG = 1u;
        fill_bkg_rect(0u, 0u, 20u, 18u, 0u);
        VBK_REG = 0u;
    } else {
        BGP_REG = DMG_PALETTE(DMG_WHITE, DMG_LITE_GRAY, DMG_DARK_GRAY, DMG_BLACK);
    }
}

static void show_color(uint8_t i) {
    DISPLAY_OFF;
    SCY_REG = 0u;
    hicolor_start(img_data[i], img_bank[i]);
    DISPLAY_ON;
}

static void show_gray(uint8_t i) {
    DISPLAY_OFF;
    load_gray(i);
    DISPLAY_ON;
}

static void show_current(void) {
    if (_cpu == CGB_TYPE) show_color(current);
    else show_gray(current);
}

/* Scrolls the image up in step with the printer as each tile-row is sent, so
   the picture "feeds out" while it actually prints instead of as a separate
   animation after a long unresponsive pause. */
static void print_feed(uint8_t row, uint8_t rows) {
    SCY_REG = (uint8_t)(((uint16_t)(row + 1u) * 144u) / rows);
}

/* Start: on GBC drop to the grayscale fallback, send the image to the GB
   Printer, feeding the picture out on screen in time with the print. */
static void do_print(void) {
    uint16_t gc = img_gtilecount[current];
    uint8_t white = (gc < 256u) ? (uint8_t)gc : 255u;
    uint8_t s;

    hicolor_stop();
    /* hicolor_stop() only removes the LCD ISR handler; the per-scanline STAT
       (LY==152) interrupt itself stays enabled. The GB Printer serial handshake
       is timing-sensitive, and that stray interrupt corrupts it — which is why
       the DMG path (no LCD interrupt) prints fine but the color path doesn't.
       Run the print with only VBlank enabled; hicolor_start re-enables LCD on
       return. */
    set_interrupts(VBL_IFLAG);
    STAT_REG = 0u;
    DISPLAY_OFF;
    load_gray(current);
    /* Blank "paper" in the off-screen rows below the image so the scroll-out
       reveals white, not stale VRAM. */
    VBK_REG = 0u;
    set_bkg_data(white, 1u, white_tile);
    fill_bkg_rect(0u, 18u, 20u, 14u, white);
    if (_cpu == CGB_TYPE) {
        VBK_REG = 1u;
        fill_bkg_rect(0u, 18u, 20u, 14u, 0u);
        VBK_REG = 0u;
    }
    DISPLAY_ON;

    /* Best-effort print (no-op if no printer is connected). A printer that has
       just finished a previous job briefly reports a non-ready status (motor
       cooldown, residual UNTRAN/FULL bits), so a single 10-frame detect would
       fail on every print after the first. Re-detect a few times, letting it
       settle between tries, so repeated prints work. print_feed scrolls the
       image out in step with the print itself. */
    uint8_t printed = 0u;
    printer_row_cb = print_feed;
    for (uint8_t t = 0u; t < 8u; t++) {
        if (gbprinter_detect(PRINTER_DETECT_TIMEOUT) == PRN_STATUS_OK) {
            gbprinter_print_image(img_gmap[current], img_gtiles[current], 0, 20u, 18u);
            printed = 1u;
            break;
        }
        for (uint8_t w = 0u; w < 10u; w++) vsync();
    }
    printer_row_cb = 0;

    /* No printer connected: still play the feed animation so Start gives feedback. */
    if (!printed)
        for (s = 0u; s < 144u; s += 2u) { SCY_REG = s; vsync(); }

    for (s = 0u; s < 30u; s++) vsync(); /* hold the blank page briefly */
    SCY_REG = 0u;

    show_current();
}

void main(void) {
    current = 0u;
    init_banks();
    gbprinter_set_print_params(PRN_NO_MARGINS, PRN_PALETTE_NORMAL, PRN_EXPOSURE_DEFAULT);
    if (_cpu == CGB_TYPE) cpu_fast();
    SHOW_BKG;
    show_current();

    uint8_t prev = 0u;
    for (;;) {
        vsync();
        uint8_t j = joypad();
        uint8_t pressed = j & ~prev;

        if (pressed & J_LEFT) {
            current = (current == 0u) ? (NUM_IMAGES - 1u) : (current - 1u);
            show_current();
        }
        if (pressed & J_RIGHT) {
            current = (current + 1u) %% NUM_IMAGES;
            show_current();
        }
        if (pressed & J_START) {
            do_print();
            waitpadup();
        }
        prev = j;
    }
}
`
	return fmt.Sprintf(tmpl, name, initBanks.String())
}
