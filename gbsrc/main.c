#include <gbdk/platform.h>
#include <gb/drawing.h>
#include <stdint.h>
#include "../res/makise.h"

#define CGB_ONE_PAL 1u

const palette_color_t cgb_pal_black[] = {RGB_BLACK, RGB_BLACK, RGB_BLACK, RGB_BLACK};

void main(void)
{
    if (_cpu == CGB_TYPE) {
        set_bkg_palette(BKGF_CGB_PAL0, CGB_ONE_PAL, cgb_pal_black);
    } else {
        BGP_REG = DMG_PALETTE(DMG_BLACK, DMG_BLACK, DMG_BLACK, DMG_BLACK);
    }

    draw_image(makise_tiles);

    if (_cpu == CGB_TYPE) {
        VBK_REG = VBK_ATTRIBUTES;
        set_bkg_tiles(0, 0, MAKISE_WIDTH / 8, MAKISE_HEIGHT / 8, makise_attr);
        VBK_REG = VBK_TILES;
    }

    SHOW_BKG;
    vsync();

    if (_cpu == CGB_TYPE) {
        set_bkg_palette(BKGF_CGB_PAL0, MAKISE_PALETTE_COUNT, makise_palettes);
    } else {
        BGP_REG = DMG_PALETTE(DMG_WHITE, DMG_LITE_GRAY, DMG_DARK_GRAY, DMG_BLACK);
    }

    while(1) {
        vsync();
    }
}