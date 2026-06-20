package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
)

const firstSongBank = 2

func ExportGBDKMusic(cfg ConvertConfig, songs []*Song, progress func(string)) (int, error) {
	if len(songs) == 0 {
		return 0, fmt.Errorf("no songs")
	}
	dir := cfg.OutputDir
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, err
	}
	cleanOutputDir(dir)

	name := cfg.Name
	upper := strings.ToUpper(name)
	n := len(songs)

	rate := cfg.PCMRate
	if rate != 8192 && rate != 9198 {
		rate = defaultPCMRate
	}
	dmg := cfg.MusicTarget == TargetDMG
	doubleSpeed := !dmg

	for i, sg := range songs {
		if progress != nil {
			progress(fmt.Sprintf("Encoding cover %d / %d…", i+1, n))
		}
		cover := sg.cover
		if cover == nil {
			cover = placeholderCover(sg.displayTitle())
		}
		var tiles [][]Tile
		var pals [8]Palette
		if dmg {
			tiles, pals = coverTilesDMG(cover)
		} else {
			tiles, pals = coverTiles(cover)
			reserveUIPalette(tiles, &pals)
		}
		title := titleTiles(sg.displayTitle())
		bankSrc := emitCoverBank(name, i, firstSongBank+i, tiles, pals, title)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s_song%02d.c", name, i)), []byte(bankSrc), 0644); err != nil {
			return 0, err
		}
	}

	nextBank := firstSongBank + n
	var chunkBanks, chunkLens []int
	firstChunk := make([]int, n)
	chunkCount := make([]int, n)
	songCodec := make([]int, n)
	chipFramesArr := make([]int, n)
	songChipFirst := make([]int, n)
	var chipChunkBanks, chipChunkLens []int
	chunkK := 0
	chipChunkK := 0
	for i, sg := range songs {
		firstChunk[i] = chunkK
		songChipFirst[i] = chipChunkK
		switch {
		case sg.Codec == CodecChiptuneAuto && sg.Path != "":
			if progress != nil {
				progress(fmt.Sprintf("Converting track %d → chiptune…", i+1))
			}
			stream, frames, err := encodeSongChiptune(sg.Path, isMIDIFile(sg.Path))
			if err != nil {
				return 0, err
			}
			for _, ch := range chunkChipStream(stream, 0x4000) {
				if nextBank > 511 {
					return 0, fmt.Errorf("project exceeds 512 ROM banks at track %d", i+1)
				}
				if err := emitChipChunkFile(dir, name, chipChunkK, nextBank, ch); err != nil {
					return 0, err
				}
				chipChunkBanks = append(chipChunkBanks, nextBank)
				chipChunkLens = append(chipChunkLens, len(ch))
				nextBank++
				chipChunkK++
			}
			songCodec[i] = 1
			chipFramesArr[i] = frames

		case sg.Codec == CodecPCM && sg.Path != "":
			if progress != nil {
				progress(fmt.Sprintf("Decoding audio %d / %d (ffmpeg)…", i+1, n))
			}
			chunks, _, err := encodeSongPCMChunks(sg.Path, rate)
			if err != nil {
				return 0, err
			}
			for _, ch := range chunks {
				if nextBank > 511 {
					return 0, fmt.Errorf("project exceeds 512 ROM banks (8 MB) at track %d — shorten audio or lower the sample rate", i+1)
				}
				if err := emitPCMChunkFile(dir, name, chunkK, nextBank, ch); err != nil {
					return 0, err
				}
				chunkBanks = append(chunkBanks, nextBank)
				chunkLens = append(chunkLens, len(ch))
				nextBank++
				chunkK++
			}

		default:
			if sg.Codec.chiptune() && sg.Codec != CodecChiptuneAuto && progress != nil {
				progress(fmt.Sprintf("Track %d (%s): needs an external driver — silent for now.", i+1, sg.Codec))
			}
		}
		chunkCount[i] = chunkK - firstChunk[i]
	}
	totalChunks := chunkK
	totalChipChunks := chipChunkK

	var hdr bytes.Buffer
	fmt.Fprintf(&hdr, "#ifndef %s_H\n#define %s_H\n\n", upper, upper)
	fmt.Fprintf(&hdr, "#include <gbdk/platform.h>\n#include <stdint.h>\n\n")
	fmt.Fprintf(&hdr, "#define NUM_SONGS %d\n", n)
	fmt.Fprintf(&hdr, "#define COVER_TILES_W %d\n", coverTilesW)
	fmt.Fprintf(&hdr, "#define COVER_TILES_H %d\n", coverTilesH)
	fmt.Fprintf(&hdr, "#define COVER_TILE_COUNT %d\n\n", coverTilesW*coverTilesH)

	fmt.Fprintf(&hdr, "#define TITLE_BASE_TILE %d\n", coverTilesW*coverTilesH)
	fmt.Fprintf(&hdr, "#define TITLE_TILE_COUNT %d\n", titleTileCount)
	fmt.Fprintf(&hdr, "#define TITLE_X %d\n", titleTileX)
	fmt.Fprintf(&hdr, "#define TITLE_Y %d\n", titleTileY)
	fmt.Fprintf(&hdr, "#define TITLE_W %d\n", titleTilesW)
	fmt.Fprintf(&hdr, "#define TITLE_H %d\n\n", titleTilesH)

	fmt.Fprintf(&hdr, "#define PCM_CHUNK_COUNT %d\n", totalChunks)
	fmt.Fprintf(&hdr, "#define AUDIO_TMA %d\n", audioTimerTMA(rate, doubleSpeed))
	fmt.Fprintf(&hdr, "#define AUDIO_TAC 0x05\n\n")
	for i := range songs {
		fmt.Fprintf(&hdr, "BANKREF_EXTERN(song%02d)\n", i)
		fmt.Fprintf(&hdr, "extern const uint8_t song%02d_tiles[];\n", i)
		fmt.Fprintf(&hdr, "extern const uint8_t song%02d_map[];\n", i)
		fmt.Fprintf(&hdr, "extern const uint8_t song%02d_attr[];\n", i)
		fmt.Fprintf(&hdr, "extern const uint8_t song%02d_title[];\n", i)
		fmt.Fprintf(&hdr, "extern const palette_color_t song%02d_pal[];\n", i)
	}
	fmt.Fprintf(&hdr, "\nextern const uint8_t song_banks[NUM_SONGS];\n")
	fmt.Fprintf(&hdr, "extern const uint8_t* const song_tiles[NUM_SONGS];\n")
	fmt.Fprintf(&hdr, "extern const uint8_t* const song_map[NUM_SONGS];\n")
	fmt.Fprintf(&hdr, "extern const uint8_t* const song_attr[NUM_SONGS];\n")
	fmt.Fprintf(&hdr, "extern const uint8_t* const song_title[NUM_SONGS];\n")
	fmt.Fprintf(&hdr, "extern const palette_color_t* const song_pal[NUM_SONGS];\n")
	fmt.Fprintf(&hdr, "extern const uint8_t title_map[TITLE_TILE_COUNT];\n")
	fmt.Fprintf(&hdr, "extern const uint8_t title_attr[TITLE_TILE_COUNT];\n\n")
	for k := 0; k < totalChunks; k++ {
		fmt.Fprintf(&hdr, "extern const uint8_t pcm_chunk%d[];\n", k)
	}
	fmt.Fprintf(&hdr, "extern const uint16_t pcm_chunk_bank[];\n")
	fmt.Fprintf(&hdr, "extern const uint8_t* const pcm_chunk_ptr[];\n")
	fmt.Fprintf(&hdr, "extern const uint16_t pcm_chunk_len[];\n")
	fmt.Fprintf(&hdr, "extern const uint16_t song_first_chunk[NUM_SONGS];\n")
	fmt.Fprintf(&hdr, "extern const uint16_t song_chunk_count[NUM_SONGS];\n\n")

	for k := 0; k < totalChipChunks; k++ {
		fmt.Fprintf(&hdr, "extern const uint8_t chip_chunk%d[];\n", k)
	}
	fmt.Fprintf(&hdr, "extern const uint8_t song_codec[NUM_SONGS];\n")
	fmt.Fprintf(&hdr, "extern const uint16_t chip_chunk_bank[];\n")
	fmt.Fprintf(&hdr, "extern const uint8_t* const chip_chunk_ptr[];\n")
	fmt.Fprintf(&hdr, "extern const uint16_t chip_chunk_len[];\n")
	fmt.Fprintf(&hdr, "extern const uint16_t song_chip_first[NUM_SONGS];\n")
	fmt.Fprintf(&hdr, "extern const uint16_t song_chip_frames[NUM_SONGS];\n")
	fmt.Fprintf(&hdr, "\n#endif\n")
	if err := os.WriteFile(filepath.Join(dir, name+".h"), hdr.Bytes(), 0644); err != nil {
		return 0, err
	}

	var tbl bytes.Buffer
	fmt.Fprintf(&tbl, "#include \"%s.h\"\n\n", name)
	fmt.Fprintf(&tbl, "const uint8_t song_banks[NUM_SONGS] = {")
	for i := range songs {
		fmt.Fprintf(&tbl, "%d,", firstSongBank+i)
	}
	fmt.Fprintf(&tbl, "};\n")
	emitPtrTable(&tbl, "song_tiles", "uint8_t", n, "tiles")
	emitPtrTable(&tbl, "song_map", "uint8_t", n, "map")
	emitPtrTable(&tbl, "song_attr", "uint8_t", n, "attr")
	emitPtrTable(&tbl, "song_title", "uint8_t", n, "title")
	emitPtrTable(&tbl, "song_pal", "palette_color_t", n, "pal")

	fmt.Fprintf(&tbl, "const uint8_t title_map[TITLE_TILE_COUNT] = {")
	for i := 0; i < titleTileCount; i++ {
		fmt.Fprintf(&tbl, "%d,", coverTilesW*coverTilesH+i)
	}
	fmt.Fprintf(&tbl, "};\n")
	fmt.Fprintf(&tbl, "const uint8_t title_attr[TITLE_TILE_COUNT] = {")
	for i := 0; i < titleTileCount; i++ {
		fmt.Fprintf(&tbl, "%d,", uiPaletteIdx)
	}
	fmt.Fprintf(&tbl, "};\n")

	fmt.Fprintf(&tbl, "const uint16_t pcm_chunk_bank[] = {")
	for _, bk := range chunkBanks {
		fmt.Fprintf(&tbl, "%d,", bk)
	}
	if totalChunks == 0 {
		fmt.Fprintf(&tbl, "0,")
	}
	fmt.Fprintf(&tbl, "};\n")

	fmt.Fprintf(&tbl, "const uint8_t* const pcm_chunk_ptr[] = {")
	for k := 0; k < totalChunks; k++ {
		fmt.Fprintf(&tbl, "pcm_chunk%d,", k)
	}
	if totalChunks == 0 {
		fmt.Fprintf(&tbl, "0,")
	}
	fmt.Fprintf(&tbl, "};\n")

	fmt.Fprintf(&tbl, "const uint16_t pcm_chunk_len[] = {")
	for _, ln := range chunkLens {
		fmt.Fprintf(&tbl, "%d,", ln)
	}
	if totalChunks == 0 {
		fmt.Fprintf(&tbl, "0,")
	}
	fmt.Fprintf(&tbl, "};\n")

	fmt.Fprintf(&tbl, "const uint16_t song_first_chunk[NUM_SONGS] = {")
	for i := range songs {
		fmt.Fprintf(&tbl, "%d,", firstChunk[i])
	}
	fmt.Fprintf(&tbl, "};\n")
	fmt.Fprintf(&tbl, "const uint16_t song_chunk_count[NUM_SONGS] = {")
	for i := range songs {
		fmt.Fprintf(&tbl, "%d,", chunkCount[i])
	}
	fmt.Fprintf(&tbl, "};\n")

	fmt.Fprintf(&tbl, "const uint8_t song_codec[NUM_SONGS] = {")
	for i := range songs {
		fmt.Fprintf(&tbl, "%d,", songCodec[i])
	}
	fmt.Fprintf(&tbl, "};\n")
	fmt.Fprintf(&tbl, "const uint16_t song_chip_first[NUM_SONGS] = {")
	for i := range songs {
		fmt.Fprintf(&tbl, "%d,", songChipFirst[i])
	}
	fmt.Fprintf(&tbl, "};\n")
	fmt.Fprintf(&tbl, "const uint16_t song_chip_frames[NUM_SONGS] = {")
	for i := range songs {
		fmt.Fprintf(&tbl, "%d,", chipFramesArr[i])
	}
	fmt.Fprintf(&tbl, "};\n")
	fmt.Fprintf(&tbl, "const uint16_t chip_chunk_bank[] = {")
	for _, bk := range chipChunkBanks {
		fmt.Fprintf(&tbl, "%d,", bk)
	}
	if totalChipChunks == 0 {
		fmt.Fprintf(&tbl, "0,")
	}
	fmt.Fprintf(&tbl, "};\n")
	fmt.Fprintf(&tbl, "const uint8_t* const chip_chunk_ptr[] = {")
	for k := 0; k < totalChipChunks; k++ {
		fmt.Fprintf(&tbl, "chip_chunk%d,", k)
	}
	if totalChipChunks == 0 {
		fmt.Fprintf(&tbl, "0,")
	}
	fmt.Fprintf(&tbl, "};\n")
	fmt.Fprintf(&tbl, "const uint16_t chip_chunk_len[] = {")
	for _, ln := range chipChunkLens {
		fmt.Fprintf(&tbl, "%d,", ln)
	}
	if totalChipChunks == 0 {
		fmt.Fprintf(&tbl, "0,")
	}
	fmt.Fprintf(&tbl, "};\n")

	if err := os.WriteFile(filepath.Join(dir, name+"_tables.c"), tbl.Bytes(), 0644); err != nil {
		return 0, err
	}

	if err := os.WriteFile(filepath.Join(dir, "main.c"), []byte(generateMusicPlayerC(name, dmg)), 0644); err != nil {
		return 0, err
	}

	cfg.ROMBanks = nextPow2(nextBank)
	cfg.HiColor = false
	if dmg {
		cfg.Mode = ModeDMG
	} else {
		cfg.Mode = ModeCGB
	}
	_ = GenerateBatchFile(cfg)
	if progress != nil {
		progress(fmt.Sprintf("Encoded %d track(s), %d PCM bank(s); player generated (%d ROM banks).",
			n, totalChunks, cfg.ROMBanks))
	}
	return cfg.ROMBanks, nil
}

func isMIDIFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".mid" || ext == ".midi"
}

func emitChipChunkFile(dir, name string, k, bank int, data []byte) error {
	var b bytes.Buffer
	fmt.Fprintf(&b, "#pragma bank %d\n#include <stdint.h>\n\n", bank)
	fmt.Fprintf(&b, "const uint8_t chip_chunk%d[%d] = {\n", k, len(data))
	for i, v := range data {
		fmt.Fprintf(&b, "0x%02X,", v)
		if i%24 == 23 {
			b.WriteByte('\n')
		}
	}
	fmt.Fprintf(&b, "\n};\n")
	return os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s_chip%d.c", name, k)), b.Bytes(), 0644)
}

func emitPCMChunkFile(dir, name string, k, bank int, data []byte) error {
	var b bytes.Buffer
	fmt.Fprintf(&b, "#pragma bank %d\n#include <stdint.h>\n\n", bank)
	fmt.Fprintf(&b, "const uint8_t pcm_chunk%d[%d] = {\n", k, len(data))
	for i, v := range data {
		fmt.Fprintf(&b, "0x%02X,", v)
		if i%32 == 31 {
			b.WriteByte('\n')
		}
	}
	fmt.Fprintf(&b, "\n};\n")
	return os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s_pcm%d.c", name, k)), b.Bytes(), 0644)
}

func emitCoverBank(name string, idx, bank int, tiles [][]Tile, pals [8]Palette, title [][]Tile) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "#pragma bank %d\n", bank)
	fmt.Fprintf(&b, "#include \"%s.h\"\n\n", name)
	fmt.Fprintf(&b, "BANKREF(song%02d)\n\n", idx)

	h := len(tiles)
	w := 0
	if h > 0 {
		w = len(tiles[0])
	}

	fmt.Fprintf(&b, "const uint8_t song%02d_tiles[] = {\n", idx)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			for _, v := range encodeTile(tiles[y][x]) {
				fmt.Fprintf(&b, "0x%02X,", v)
			}
			fmt.Fprintf(&b, "\n")
		}
	}
	fmt.Fprintf(&b, "};\n\n")

	fmt.Fprintf(&b, "const uint8_t song%02d_map[] = {\n", idx)
	i := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fmt.Fprintf(&b, "%d,", i&0xFF)
			i++
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "};\n\n")

	fmt.Fprintf(&b, "const uint8_t song%02d_attr[] = {\n", idx)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fmt.Fprintf(&b, "%d,", tiles[y][x].Palette&7)
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "};\n\n")

	fmt.Fprintf(&b, "const uint8_t song%02d_title[] = {\n", idx)
	for ty := 0; ty < len(title); ty++ {
		for tx := 0; tx < len(title[ty]); tx++ {
			for _, v := range encodeTile(title[ty][tx]) {
				fmt.Fprintf(&b, "0x%02X,", v)
			}
			fmt.Fprintf(&b, "\n")
		}
	}
	fmt.Fprintf(&b, "};\n\n")

	fmt.Fprintf(&b, "const palette_color_t song%02d_pal[] = {\n", idx)
	for _, p := range pals {
		for c := 0; c < 4; c++ {
			fmt.Fprintf(&b, "0x%04X,", uint16(toRGB5(p.Colors[c])))
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "};\n")
	return b.String()
}

func emitPtrTable(b *bytes.Buffer, tblName, elemType string, n int, field string) {
	fmt.Fprintf(b, "const %s* const %s[NUM_SONGS] = {", elemType, tblName)
	for i := 0; i < n; i++ {
		fmt.Fprintf(b, "song%02d_%s,", i, field)
	}
	fmt.Fprintf(b, "};\n")
}

func generateMusicPlayerC(name string, dmg bool) string {

	cgbLoadCover := `static void load_cover(uint8_t i) {
    SWITCH_ROM(song_banks[i]);
    set_bkg_palette(0, 8, song_pal[i]);

    VBK_REG = 0;
    set_bkg_data(0, COVER_TILE_COUNT, song_tiles[i]);
    set_bkg_data(TITLE_BASE_TILE, TITLE_TILE_COUNT, song_title[i]);
    set_bkg_tiles(COVER_X, COVER_Y, COVER_TILES_W, COVER_TILES_H, song_map[i]);
    set_bkg_tiles(TITLE_X, TITLE_Y, TITLE_W, TITLE_H, title_map);

    VBK_REG = 1; // CGB attribute plane
    set_bkg_tiles(COVER_X, COVER_Y, COVER_TILES_W, COVER_TILES_H, song_attr[i]);
    set_bkg_tiles(TITLE_X, TITLE_Y, TITLE_W, TITLE_H, title_attr);
    VBK_REG = 0;
}`
	dmgLoadCover := `static void load_cover(uint8_t i) {
    SWITCH_ROM(song_banks[i]);
    BGP_REG = DMG_PALETTE(DMG_WHITE, DMG_LITE_GRAY, DMG_DARK_GRAY, DMG_BLACK);
    set_bkg_data(0, COVER_TILE_COUNT, song_tiles[i]);
    set_bkg_data(TITLE_BASE_TILE, TITLE_TILE_COUNT, song_title[i]);
    set_bkg_tiles(COVER_X, COVER_Y, COVER_TILES_W, COVER_TILES_H, song_map[i]);
    set_bkg_tiles(TITLE_X, TITLE_Y, TITLE_W, TITLE_H, title_map);
}`
	loadCover := cgbLoadCover
	cpuInit := "    cpu_fast(); // CGB double-speed: 2x CPU budget per audio sample\n"
	if dmg {
		loadCover = dmgLoadCover
		cpuInit = ""
	}
	freqTables := cU16Table("pulse_freq", gbFreqValues(false)) + cU16Table("wave_freq", gbFreqValues(true))

	const tmpl = `#include <gbdk/platform.h>
#include <gb/gb.h>
#include <gb/isr.h>
#include <stdint.h>
#include "%[1]s.h"

#define COVER_X 2
#define COVER_Y 1

static uint8_t current;
static uint8_t cur_codec; // codec of the currently loaded track (0 PCM, 1 chiptune)
static volatile uint8_t playing;
static volatile const uint8_t* pcm_ptr;
static volatile const uint8_t* pcm_end;
static volatile uint16_t pcm_chunk;
static volatile uint16_t pcm_chunk_end;

void switch_track(uint8_t i);

// pcm_load_chunk pages in the current chunk's ROM bank and points the cursor at
// its data (end = ptr + length).
static void pcm_load_chunk(void) {
    SWITCH_ROM(pcm_chunk_bank[pcm_chunk]);
    pcm_ptr = pcm_chunk_ptr[pcm_chunk];
    pcm_end = pcm_ptr + pcm_chunk_len[pcm_chunk];
}

// pcm_advance is called from the ISR at a chunk boundary: step to the next chunk
// (loop at the track's end) and page it in.
static void pcm_advance(void) {
    pcm_chunk++;
    if (pcm_chunk >= pcm_chunk_end) pcm_chunk = song_first_chunk[current];
    pcm_load_chunk();
}

// Raw timer ISR, hand-written in assembly for speed (installed via ISR_VECTOR,
// bypassing the GBDK dispatcher). The hot path writes one sample to NR50 and
// advances the 16-bit cursor (SM83 has no ld hl,(nn) — pointers move byte-wise);
// only at a chunk boundary does it call the C pcm_advance to page the next bank.
// A heavy C ISR here was too slow even at 4096 Hz (pitched-down, distorted).
void audio_isr(void) __naked {
__asm
        push af
        push hl
        ld a, (_playing)
        or a
        jr z, 0002$
        ld a, (_pcm_ptr)
        ld l, a
        ld a, (_pcm_ptr+1)
        ld h, a
        ld a, (_pcm_end)
        cp l
        jr nz, 0001$
        ld a, (_pcm_end+1)
        cp h
        jr z, 0003$
0001$:
        ld a, (hl)
        ldh (0x24), a
        inc hl
        ld a, l
        ld (_pcm_ptr), a
        ld a, h
        ld (_pcm_ptr+1), a
0002$:
        pop hl
        pop af
        reti
0003$:
        push bc
        push de
        call _pcm_advance
        pop de
        pop bc
        ld a, (_pcm_ptr)
        ld l, a
        ld a, (_pcm_ptr+1)
        ld h, a
        ld a, (hl)
        ldh (0x24), a
        inc hl
        ld a, l
        ld (_pcm_ptr), a
        ld a, h
        ld (_pcm_ptr+1), a
        pop hl
        pop af
        reti
__endasm;
}
ISR_VECTOR(VECTOR_TIMER, audio_isr)

// pcm_setup arms CH3 as a constant-DC carrier; NR50 modulation becomes the PCM
// waveform (volume-PWM). Re-armed on each PCM play (chiptune borrows CH3).
static void pcm_setup(void) {
    NR51_REG = 0x44;
    NR50_REG = 0x00;
    NR30_REG = 0x00;
    for (uint8_t k = 0; k < 16; k++) AUD3WAVE[k] = 0xFF;
    NR30_REG = 0x80;
    NR32_REG = 0x20;
    NR31_REG = 0x00;
    NR33_REG = 0x00;
    NR34_REG = 0x87;
}

// ── Chiptune engine: a 60 Hz note stream on the GB's native channels ──────────
static volatile uint8_t chip_playing;
static volatile const uint8_t* chip_ptr;
static volatile const uint8_t* chip_chunk_endp;
static volatile uint16_t chip_chunk;
static volatile uint16_t chip_frame;
static volatile uint16_t chip_total;

static void chip_load_chunk(void) {
    SWITCH_ROM(chip_chunk_bank[chip_chunk]);
    chip_ptr = chip_chunk_ptr[chip_chunk];
    chip_chunk_endp = chip_ptr + chip_chunk_len[chip_chunk];
}

%[4]s
// Triangle waveform for the CH3 bass voice.
static const uint8_t chip_wave[16] = {
    0x01,0x23,0x45,0x67,0x89,0xAB,0xCD,0xEF,0xFE,0xDC,0xBA,0x98,0x76,0x54,0x32,0x10
};

static void chip_pulse1(uint8_t note) {
    if (note == 0) { NR12_REG = 0x00; return; }
    uint16_t f = pulse_freq[note];
    NR11_REG = 0x80; NR12_REG = 0xF0;
    NR13_REG = (uint8_t)f; NR14_REG = 0x80 | (uint8_t)(f >> 8);
}
static void chip_pulse2(uint8_t note) {
    if (note == 0) { NR22_REG = 0x00; return; }
    uint16_t f = pulse_freq[note];
    NR21_REG = 0x80; NR22_REG = 0xF0;
    NR23_REG = (uint8_t)f; NR24_REG = 0x80 | (uint8_t)(f >> 8);
}
static void chip_wavech(uint8_t note) {
    if (note == 0) { NR30_REG = 0x00; return; }
    uint16_t f = wave_freq[note];
    NR30_REG = 0x80; NR32_REG = 0x20;
    NR33_REG = (uint8_t)f; NR34_REG = 0x80 | (uint8_t)(f >> 8);
}
static void chip_noise(uint8_t v) {
    if (v == 0) return;
    NR41_REG = 0x00; NR42_REG = 0xF1; NR43_REG = 0x33; NR44_REG = 0x80;
}

// chip_vbl plays one note-stream record per frame (control byte + changed values).
void chip_vbl(void) {
    uint8_t ctrl;
    if (!chip_playing) return;
    if (chip_frame >= chip_total) {            // loop: back to the track's first chunk
        chip_chunk = song_chip_first[current];
        chip_load_chunk();
        chip_frame = 0;
    } else if (chip_ptr >= chip_chunk_endp) {  // chunk/bank boundary
        chip_chunk++;
        chip_load_chunk();
    }
    ctrl = *chip_ptr++;
    if (ctrl & 1) chip_pulse1(*chip_ptr++);
    if (ctrl & 2) chip_pulse2(*chip_ptr++);
    if (ctrl & 4) chip_wavech(*chip_ptr++);
    if (ctrl & 8) chip_noise(*chip_ptr++);
    chip_frame++;
}

static void chip_setup(void) {
    NR51_REG = 0xFF;
    NR50_REG = 0x77;
    NR30_REG = 0x00;
    for (uint8_t k = 0; k < 16; k++) AUD3WAVE[k] = chip_wave[k];
}

static void audio_silence(void) {
    NR50_REG = 0x00;
    NR12_REG = 0x00; NR22_REG = 0x00; NR30_REG = 0x00; NR42_REG = 0x00;
}

// audio_pause mutes and stops feeding samples but keeps the playback position
// (pcm cursor / chip frame) so audio_resume continues from where it left off.
static void audio_pause(void) {
    playing = 0;
    chip_playing = 0;
    audio_silence();
}

// audio_resume continues the current track from its saved position. Each engine
// uses one interrupt source (chiptune = VBL, PCM = timer ISR) so they don't fight
// over the audio registers.
static void audio_resume(void) {
    if (cur_codec) {
        if (song_chip_frames[current] == 0) return;
        SWITCH_ROM(chip_chunk_bank[chip_chunk]);
        chip_setup();
        __critical {
            set_interrupts(VBL_IFLAG);
            chip_playing = 1;
            playing = 1;
        }
        return;
    }
    if (song_chunk_count[current] == 0) return;
    SWITCH_ROM(pcm_chunk_bank[pcm_chunk]);
    pcm_setup();
    __critical {
        set_interrupts(TIM_IFLAG);
        playing = 1;
    }
}

// audio_start loads track i and plays it from the very beginning.
static void audio_start(uint8_t i) {
    audio_pause();
    current = i;
    cur_codec = song_codec[i];
    if (cur_codec) {
        if (song_chip_frames[i] == 0) return;
        chip_chunk = song_chip_first[i];
        chip_load_chunk();
        chip_frame = 0;
        chip_total = song_chip_frames[i];
    } else {
        if (song_chunk_count[i] == 0) return;
        pcm_chunk = song_first_chunk[i];
        pcm_chunk_end = pcm_chunk + song_chunk_count[i];
        SWITCH_ROM(pcm_chunk_bank[pcm_chunk]);
        pcm_ptr = pcm_chunk_ptr[pcm_chunk];
        pcm_end = pcm_ptr + pcm_chunk_len[pcm_chunk];
    }
    audio_resume();
}

%[2]s

// switch_track loads the new cover and starts that track from the beginning,
// immediately (◀▶ jump straight to the next track, no gap).
void switch_track(uint8_t i) {
    audio_pause();
    load_cover(i);
    audio_start(i);
}

void main(void) {
    current = 0;
    playing = 0;
%[3]s
    DISPLAY_OFF;
    load_cover(current);
    SHOW_BKG;
    DISPLAY_ON;

    NR52_REG = 0x80;       // APU on
    rTMA = AUDIO_TMA;
    rTAC = AUDIO_TAC;
    add_VBL(chip_vbl);     // chiptune playback; PCM uses the raw timer ISR
    enable_interrupts();   // audio_resume picks the interrupt source per codec

    audio_start(current); // autoplay the first track

    uint8_t prev = 0;
    while (1) {
        uint8_t j = joypad();
        uint8_t pressed = j & ~prev;

        if (pressed & J_LEFT) {
            switch_track((current == 0) ? (NUM_SONGS - 1) : (current - 1));
        }
        if (pressed & J_RIGHT) {
            switch_track((current + 1) %% NUM_SONGS);
        }
        if (pressed & (J_A | J_START)) {  // A / Start: pause <-> resume (keeps position)
            if (playing) audio_pause(); else audio_resume();
        }
        if (pressed & J_B) {              // B: restart the current track from the start
            audio_start(current);
        }

        prev = j; // tight poll, no vsync (audio is the only timing-critical task)
    }
}
`
	return fmt.Sprintf(tmpl, name, loadCover, cpuInit, freqTables)
}

func cU16Table(name string, vals []uint16) string {
	var b strings.Builder
	fmt.Fprintf(&b, "static const uint16_t %s[%d] = {", name, len(vals))
	for _, v := range vals {
		fmt.Fprintf(&b, "%d,", v)
	}
	b.WriteString("};\n")
	return b.String()
}

func placeholderCover(title string) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, coverPxW, coverPxH))
	seed := 0
	for _, r := range title {
		seed += int(r)
	}
	base := uint8(40 + seed%80)
	for y := 0; y < coverPxH; y++ {
		shade := uint8(int(base) + y/4)
		for x := 0; x < coverPxW; x++ {
			img.Set(x, y, color.RGBA{shade, shade / 2, uint8(120 - y/2), 255})
		}
	}
	return img
}

func nextPow2(n int) int {
	p := 4
	for p < n {
		p <<= 1
	}
	return p
}
