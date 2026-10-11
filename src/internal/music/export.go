package music

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"MediaBoy/internal/core"
	"MediaBoy/internal/ffmpeg"
	"MediaBoy/internal/gbdk"
)

// player.c.tmpl is the player's main.c; Build fills it in.
//
//go:embed player.c.tmpl
var playerTmpl string

// Songs' screens start in this bank, one bank each; chiptune streams follow,
// then the PCM, which Build writes into the linked ROM instead of compiling
// it (megabytes of C arrays took most of a build).
const firstSongBank = 2

const maxBanks = 512

// testPatchMain, when set by a test, edits main.c before it is compiled.
var testPatchMain func(string) string

// Codecs as the player numbers them.
const (
	codecPCM    = 0
	codecChip   = 1
	codecSilent = 2
)

// Overflow modes: what Build does when the music does not fit Max ROM.
const (
	OverflowTrim  = iota // cut the track that does not fit, drop the ones after it
	OverflowFit          // the highest sample rate at which everything fits
	OverflowSplit        // several ROMs: name_part1, name_part2…
)

// BuildResult is what Build made.
type BuildResult struct {
	ROMs    []string
	Success bool
	Output  string   // the compiler's
	Notes   []string // what was cut or changed to fit
	Rate    int
}

// chipTrack is a chiptune track's stream, cut into banks.
type chipTrack struct {
	chunks [][]byte
	frames int
}

// Build exports songs as GBDK projects in cfg.OutputDir, compiles them and
// writes the PCM into the ROMs: one ROM of at most cfg.MaxMusicMB, or several
// with OverflowSplit. onCompile reports compile progress.
func Build(cfg core.ConvertConfig, songs []*core.Song, progress func(string), onCompile func(done, total int)) (BuildResult, error) {
	var res BuildResult
	if progress == nil {
		progress = func(string) {}
	}
	if len(songs) == 0 {
		return res, fmt.Errorf("no songs")
	}
	dir := cfg.OutputDir
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return res, err
	}
	cfg.OutputDir = dir
	dmg := cfg.MusicTarget == core.TargetDMG
	dpcm := !cfg.MusicUncompressed
	capBanks := maxBanks
	if cfg.MaxMusicMB > 0 {
		capBanks = min(maxBanks, cfg.MaxMusicMB*64)
	}
	rate := pcmRate(cfg.PCMRate, dmg)

	// What every track takes: chiptune streams are encoded now, PCM is
	// estimated from its length.
	chips := map[int]chipTrack{}
	seconds := make([]float64, len(songs))
	for i, sg := range songs {
		switch codecOf(sg) {
		case codecChip:
			progress(fmt.Sprintf("Converting track %d → chiptune…", i+1))
			stream, frames, err := encodeSongChiptune(sg.Path, isMIDIFile(sg.Path))
			if err != nil {
				return res, fmt.Errorf("track %d: %w", i+1, err)
			}
			chips[i] = chipTrack{chunkChipStream(stream, 0x4000), frames}
		case codecPCM:
			seconds[i] = sg.Seconds
			if seconds[i] <= 0 {
				d, err := ffmpeg.ProbeDuration(sg.Path)
				if err != nil {
					return res, fmt.Errorf("track %d: %w", i+1, err)
				}
				seconds[i] = d
			}
		}
	}
	banksAt := func(i, rate int) int {
		if c, ok := chips[i]; ok {
			return len(c.chunks)
		}
		if codecOf(songs[i]) == codecPCM {
			return pcmBanksFor(seconds[i], rate, dpcm)
		}
		return 0
	}
	// need is the banks a ROM of these tracks takes.
	need := func(idx []int, rate int) int {
		n := firstSongBank + len(idx)
		for _, i := range idx {
			n += banksAt(i, rate)
		}
		return n
	}
	all := make([]int, len(songs))
	for i := range all {
		all[i] = i
	}

	groups := [][]int{all}
	switch cfg.MusicOverflow {
	case OverflowFit:
		rates := PCMRatesCGB
		if dmg {
			rates = PCMRatesDMG
		}
		for k := len(rates) - 1; k >= 0; k-- {
			if r := pcmRate(rates[k], dmg); r <= rate && (need(all, r) <= capBanks || k == 0) {
				if r != rate {
					res.Notes = append(res.Notes, fmt.Sprintf("sample rate lowered to %d Hz to fit %d MB", r, capBanks/64))
				}
				rate = r
				break
			}
		}
	case OverflowSplit:
		groups = nil
		var cur []int
		for _, i := range all {
			if len(cur) > 0 && need(append(cur[:len(cur):len(cur)], i), rate) > capBanks {
				groups = append(groups, cur)
				cur = nil
			}
			cur = append(cur, i)
		}
		groups = append(groups, cur)
	}
	res.Rate = rate

	for g, idx := range groups {
		c := cfg
		if len(groups) > 1 {
			c.Name = fmt.Sprintf("%s_part%d", cfg.Name, g+1)
			progress(fmt.Sprintf("ROM %d / %d…", g+1, len(groups)))
		}
		part := make([]*core.Song, len(idx))
		partChips := map[int]chipTrack{}
		for k, i := range idx {
			part[k] = songs[i]
			if ch, ok := chips[i]; ok {
				partChips[k] = ch
			}
		}
		out, notes, err := buildROM(c, part, partChips, rate, dpcm, capBanks, progress, onCompile)
		res.Output += out.Output
		res.Notes = append(res.Notes, notes...)
		if err != nil {
			return res, err
		}
		if !out.Success {
			return res, nil
		}
		res.ROMs = append(res.ROMs, out.ROMPath)
	}
	res.Success = true
	return res, nil
}

func codecOf(sg *core.Song) int {
	switch {
	case sg.Path == "":
		return codecSilent
	case sg.Codec == core.CodecChiptuneAuto:
		return codecChip
	case sg.Codec == core.CodecPCM:
		return codecPCM
	}
	return codecSilent
}

// buildROM makes one ROM of at most capBanks from songs, cutting the PCM
// track that does not fit and silencing the tracks after it.
func buildROM(cfg core.ConvertConfig, songs []*core.Song, chips map[int]chipTrack, rate int, dpcm bool, capBanks int, progress func(string), onCompile func(done, total int)) (gbdk.CompileResult, []string, error) {
	var notes []string
	dir, name := cfg.OutputDir, cfg.Name
	dmg := cfg.MusicTarget == core.TargetDMG
	tma, _ := pcmTimer(rate, dmg)
	gbdk.CleanOutputDir(dir)

	// Chiptune banks first (they are compiled and want low banks), then PCM
	// into whatever is left, in playlist order.
	n := len(songs)
	nextBank := firstSongBank + n
	codec := make([]int, n)
	secs := make([]int, n)
	pcmFirst := make([]int, n)
	chipFirst := make([]int, n)
	chipFrames := make([]int, n)
	var chipBanks, chipLens []int
	for i, sg := range songs {
		codec[i] = codecOf(sg)
		chipFirst[i] = len(chipBanks)
		if codec[i] != codecChip {
			continue
		}
		ch := chips[i]
		if nextBank+len(ch.chunks) > capBanks {
			notes = append(notes, fmt.Sprintf("%s: no room for the chiptune — silent", sg.DisplayTitle()))
			codec[i] = codecSilent
			continue
		}
		for _, chunk := range ch.chunks {
			if err := emitChipChunkFile(dir, name, len(chipBanks), nextBank, chunk); err != nil {
				return gbdk.CompileResult{}, notes, err
			}
			chipBanks = append(chipBanks, nextBank)
			chipLens = append(chipLens, len(chunk))
			nextBank++
		}
		chipFrames[i], secs[i] = ch.frames, ch.frames/chipFPS
	}
	pcm := map[int][]byte{} // bank → data, written after linking
	played := 0
	for i, sg := range songs {
		if codec[i] != codecPCM {
			continue
		}
		room := capBanks - nextBank
		if room < 1 {
			notes = append(notes, fmt.Sprintf("%s: dropped, no room left", sg.DisplayTitle()))
			codec[i] = codecSilent
			continue
		}
		progress(fmt.Sprintf("Encoding audio %d / %d…", i+1, n))
		x, err := decodeTrack(sg.Path, rate, dmg)
		if err != nil {
			return gbdk.CompileResult{}, notes, fmt.Errorf("track %d: %w", i+1, err)
		}
		full := pcmSamples(len(x), float64(rate))
		banks, samples := encodeTrack(x, float64(rate), dpcm, room)
		if samples < full {
			notes = append(notes, fmt.Sprintf("%s: cut to %s of %s", sg.DisplayTitle(),
				formatTime(samples/rate), formatTime(full/rate)))
		}
		pcmFirst[i] = nextBank
		for _, b := range banks {
			pcm[nextBank] = b
			nextBank++
		}
		secs[i] = (samples + rate/2) / rate
		played++
	}
	if played == 0 && len(chipBanks) == 0 {
		return gbdk.CompileResult{}, notes, fmt.Errorf("no track fits into %d MB", capBanks/64)
	}
	// Tracks that were dropped leave the playlist (their screen banks stay
	// unused).
	var keep []int
	for i, sg := range songs {
		if codec[i] != codecSilent || codecOf(sg) == codecSilent {
			keep = append(keep, i)
		}
	}
	pick := func(v []int) []int {
		out := make([]int, len(keep))
		for k, i := range keep {
			out[k] = v[i]
		}
		return out
	}
	kept := make([]*core.Song, len(keep))
	for k, i := range keep {
		kept[k] = songs[i]
	}
	songs, n = kept, len(keep)
	codec, secs, pcmFirst, chipFirst, chipFrames = pick(codec), pick(secs), pick(pcmFirst), pick(chipFirst), pick(chipFrames)

	ntiles := make([]int, n)
	for i, sg := range songs {
		progress(fmt.Sprintf("Building screen %d / %d…", i+1, n))
		scr := buildScreen(sg.Cover, sg.DisplayTitle(), dmg)
		ntiles[i] = len(scr.tiles)
		src := emitSongBank(name, i, firstSongBank+i, scr)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s_song%02d.c", name, i)), []byte(src), 0644); err != nil {
			return gbdk.CompileResult{}, notes, err
		}
	}

	if err := writeHeader(dir, name, n); err != nil {
		return gbdk.CompileResult{}, notes, err
	}
	var tbl bytes.Buffer
	fmt.Fprintf(&tbl, "#include \"%s.h\"\n\n", name)
	cArray(&tbl, "uint8_t", "song_bank", n, func(i int) int { return firstSongBank + i })
	cArray(&tbl, "uint8_t", "song_ntiles", n, func(i int) int { return ntiles[i] })
	cArray(&tbl, "uint8_t", "song_codec", n, func(i int) int { return codec[i] })
	cArray(&tbl, "uint16_t", "song_pcm_bank", n, func(i int) int { return pcmFirst[i] })
	cArray(&tbl, "uint16_t", "song_secs", n, func(i int) int { return min(secs[i], 0xFFFF) })
	cArray(&tbl, "uint16_t", "song_chip_first", n, func(i int) int { return chipFirst[i] })
	cArray(&tbl, "uint16_t", "song_chip_frames", n, func(i int) int { return chipFrames[i] })
	for _, f := range []struct{ typ, field string }{
		{"uint8_t", "tiles"}, {"uint8_t", "map"}, {"uint8_t", "attr"}, {"palette_color_t", "pal"},
	} {
		fmt.Fprintf(&tbl, "const %s* const song_%s[NUM_SONGS] = {", f.typ, f.field)
		for i := range n {
			fmt.Fprintf(&tbl, "song%02d_%s,", i, f.field)
		}
		fmt.Fprintf(&tbl, "};\n")
	}
	nc := max(len(chipBanks), 1) // C wants no empty arrays
	cArray(&tbl, "uint16_t", "chip_chunk_bank", nc, func(i int) int { return at(chipBanks, i) })
	cArray(&tbl, "uint16_t", "chip_chunk_len", nc, func(i int) int { return at(chipLens, i) })
	for k := range chipBanks {
		fmt.Fprintf(&tbl, "extern const uint8_t chip_chunk%d[];\n", k)
	}
	fmt.Fprintf(&tbl, "const uint8_t* const chip_chunk_ptr[] = {")
	for k := range chipBanks {
		fmt.Fprintf(&tbl, "chip_chunk%d,", k)
	}
	if len(chipBanks) == 0 {
		fmt.Fprintf(&tbl, "0,")
	}
	fmt.Fprintf(&tbl, "};\n")
	if err := os.WriteFile(filepath.Join(dir, name+"_tables.c"), tbl.Bytes(), 0644); err != nil {
		return gbdk.CompileResult{}, notes, err
	}

	mainC, err := playerSource(name, dmg, rate, tma, dpcm)
	if err != nil {
		return gbdk.CompileResult{}, notes, err
	}
	if testPatchMain != nil {
		mainC = testPatchMain(mainC)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.c"), []byte(mainC), 0644); err != nil {
		return gbdk.CompileResult{}, notes, err
	}

	cfg.ROMBanks = gbdk.NextPow2(nextBank)
	cfg.HiColor = false
	cfg.Mode = core.ModeCGB
	if dmg {
		cfg.Mode = core.ModeDMG
	}
	progress("Compiling ROM…")
	res := gbdk.CompileGBWithProgress(cfg, onCompile)
	if !res.Success {
		return res, notes, nil
	}
	if err := writePCM(res.ROMPath, pcm); err != nil {
		return res, notes, err
	}
	format := "8-bit"
	if dpcm {
		format = "DPCM"
	}
	res.Output += fmt.Sprintf("[OK] %d track(s), %d PCM bank(s), %s at %d Hz, %d ROM banks\n", n, len(pcm), format, rate, cfg.ROMBanks)
	return res, notes, nil
}

// dpcmStepBytes is dpcmSteps as the bytes the player adds.
func dpcmStepBytes() []uint8 {
	out := make([]uint8, len(dpcmSteps))
	for i, d := range dpcmSteps {
		out[i] = uint8(int8(d))
	}
	return out
}

func at(s []int, i int) int {
	if i < len(s) {
		return s[i]
	}
	return 0
}

func cArray(b *bytes.Buffer, typ, name string, n int, v func(int) int) {
	fmt.Fprintf(b, "const %s %s[] = {", typ, name)
	for i := range n {
		fmt.Fprintf(b, "%d,", v(i))
	}
	fmt.Fprintf(b, "};\n")
}

func writeHeader(dir, name string, n int) error {
	upper := strings.ToUpper(name)
	var h bytes.Buffer
	fmt.Fprintf(&h, "#ifndef %s_H\n#define %s_H\n\n", upper, upper)
	fmt.Fprintf(&h, "#include <gbdk/platform.h>\n#include <stdint.h>\n\n")
	fmt.Fprintf(&h, "#define NUM_SONGS %d\n\n", n)
	for i := range n {
		fmt.Fprintf(&h, "extern const uint8_t song%02d_tiles[];\n", i)
		fmt.Fprintf(&h, "extern const uint8_t song%02d_map[];\n", i)
		fmt.Fprintf(&h, "extern const uint8_t song%02d_attr[];\n", i)
		fmt.Fprintf(&h, "extern const palette_color_t song%02d_pal[];\n", i)
	}
	for _, d := range []string{
		"uint8_t song_bank[NUM_SONGS]", "uint8_t song_ntiles[NUM_SONGS]", "uint8_t song_codec[NUM_SONGS]",
		"uint16_t song_pcm_bank[NUM_SONGS]", "uint16_t song_secs[NUM_SONGS]",
		"uint16_t song_chip_first[NUM_SONGS]", "uint16_t song_chip_frames[NUM_SONGS]",
		"uint8_t* const song_tiles[NUM_SONGS]", "uint8_t* const song_map[NUM_SONGS]",
		"uint8_t* const song_attr[NUM_SONGS]", "palette_color_t* const song_pal[NUM_SONGS]",
		"uint16_t chip_chunk_bank[]", "uint16_t chip_chunk_len[]", "uint8_t* const chip_chunk_ptr[]",
	} {
		fmt.Fprintf(&h, "extern const %s;\n", d)
	}
	fmt.Fprintf(&h, "\n#endif\n")
	return os.WriteFile(filepath.Join(dir, name+".h"), h.Bytes(), 0644)
}

// playerSource fills in player.c.tmpl.
func playerSource(name string, dmg bool, rate, tma int, dpcm bool) (string, error) {
	t, err := template.New("player").Parse(playerTmpl)
	if err != nil {
		return "", err
	}
	tileBytes := func(t core.Tile) []uint8 {
		b := gbdk.EncodeTile(t)
		return b[:]
	}
	var nr50, nr51 []uint8
	for _, l := range pcmLevels {
		nr50 = append(nr50, l.nr50)
		nr51 = append(nr51, l.nr51)
	}
	var fixed []uint8
	for i := glyphBase; i <= tileBlank; i++ {
		fixed = append(fixed, tileBytes(fixedTile(i, dmg))...)
	}
	var bar [][]uint8
	for f := range 9 {
		bar = append(bar, tileBytes(barTile(f, dmg)))
	}
	rgb5 := func(p core.Palette) (out []uint16) {
		for _, c := range p.Colors {
			out = append(out, uint16(core.ToRGB5(c)))
		}
		return out
	}
	spr := core.Palette{Colors: [4]core.RGB{uiPalette.Colors[0], uiPalette.Colors[3], uiPalette.Colors[3], uiPalette.Colors[3]}}
	cgbTiles, cgbMap := cgbOnlyScreen()
	var cgbTileBytes, cgbMapBytes []uint8
	for _, t := range cgbTiles {
		cgbTileBytes = append(cgbTileBytes, tileBytes(t)...)
	}
	for _, row := range cgbMap {
		cgbMapBytes = append(cgbMapBytes, row[:]...)
	}
	var b bytes.Buffer
	err = t.Execute(&b, map[string]any{
		"CGBOnlyTiles": cgbTileBytes,
		"CGBOnlyCount": len(cgbTiles),
		"CGBOnlyMap":   cgbMapBytes,
		"Name":         name,
		"DPCM":         dpcm,
		"DPCMSteps":    dpcmStepBytes(),
		"DMG":          dmg,
		"Rate":         rate,
		"TMA":          tma,
		"BankData":     pcmBankData,
		"TileBarPart":  tileBarPart,
		"TileBarFull":  tileBarFull,
		"TileBarEmpty": tileBarEmpty,
		"GlyphBase":    glyphBase,
		"FixedCount":   tileBlank - glyphBase + 1,
		"BarX":         barX,
		"BarY":         barY,
		"BarTiles":     barTiles,
		"BarRow":       barRow0,
		"TextLeft":     coverX * 8,
		"TextRight":    (coverX + coverTilesW) * 8,
		"NR50":         nr50,
		"NR51":         nr51,
		"FixedTiles":   fixed,
		"BarParts":     bar,
		"UIPal":        rgb5(uiPalette),
		"SprPal":       rgb5(spr),
		"FreqTables":   cU16Table("pulse_freq", gbFreqValues(false)) + cU16Table("wave_freq", gbFreqValues(true)),
	})
	return b.String(), err
}

// writePCM puts the PCM banks into the linked ROM and fixes its global
// checksum.
func writePCM(romPath string, banks map[int][]byte) error {
	rom, err := os.ReadFile(romPath)
	if err != nil {
		return err
	}
	for bank, data := range banks {
		off := bank * 0x4000
		if off+len(data) > len(rom) {
			return fmt.Errorf("ROM is %d banks, PCM needs bank %d", len(rom)/0x4000, bank)
		}
		copy(rom[off:], data)
	}
	var sum uint16
	for i, v := range rom {
		if i != 0x14E && i != 0x14F {
			sum += uint16(v)
		}
	}
	rom[0x14E], rom[0x14F] = byte(sum>>8), byte(sum)
	return os.WriteFile(romPath, rom, 0644)
}

func isMIDIFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".mid" || ext == ".midi"
}

func emitChipChunkFile(dir, name string, k, bank int, data []byte) error {
	var b bytes.Buffer
	fmt.Fprintf(&b, "#pragma bank %d\n#include <stdint.h>\n\n", bank)
	fmt.Fprintf(&b, "const uint8_t chip_chunk%d[%d] = {\n", k, len(data))
	writeBytes(&b, data)
	fmt.Fprintf(&b, "};\n")
	return os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s_chip%d.c", name, k)), b.Bytes(), 0644)
}

func writeBytes(b *bytes.Buffer, data []byte) {
	for i, v := range data {
		fmt.Fprintf(b, "0x%02X,", v)
		if i%24 == 23 {
			b.WriteByte('\n')
		}
	}
	b.WriteByte('\n')
}

// emitSongBank is song idx's screen, in its own bank.
func emitSongBank(name string, idx, bank int, s *screen) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "#pragma bank %d\n", bank)
	fmt.Fprintf(&b, "#include \"%s.h\"\n\n", name)

	var tiles []byte
	for _, t := range s.tiles {
		e := gbdk.EncodeTile(t)
		tiles = append(tiles, e[:]...)
	}
	if len(tiles) == 0 {
		tiles = []byte{0}
	}
	fmt.Fprintf(&b, "const uint8_t song%02d_tiles[] = {\n", idx)
	writeBytes(&b, tiles)
	fmt.Fprintf(&b, "};\n\n")

	var m, a []byte
	for y := range scrH {
		for x := range scrW {
			m = append(m, s.tileMap[y][x])
			a = append(a, s.attrs[y][x])
		}
	}
	fmt.Fprintf(&b, "const uint8_t song%02d_map[] = {\n", idx)
	writeBytes(&b, m)
	fmt.Fprintf(&b, "};\n\n")
	fmt.Fprintf(&b, "const uint8_t song%02d_attr[] = {\n", idx)
	writeBytes(&b, a)
	fmt.Fprintf(&b, "};\n\n")

	fmt.Fprintf(&b, "const palette_color_t song%02d_pal[] = {\n", idx)
	for _, p := range s.palettes[:uiPaletteIdx] {
		for _, c := range p.Colors {
			fmt.Fprintf(&b, "0x%04X,", uint16(core.ToRGB5(c)))
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "};\n")
	return b.String()
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
