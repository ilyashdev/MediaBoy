package video

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
)

const (
	gbScreenSize = 160 * 144
	gbPixels     = gbScreenSize * 2
	gbScyData    = gbPixels / 8
	gbPaletteLen = 8 * 4
	gbDiffThresh = 0x8000
)

const gbFPSConst = float64(1024*1024*4) / 70224.0

type encColor struct{ b, g, r uint8 }

type gbvp2enc struct {
	output        []byte
	pos           int
	frameCountPos int
	holdrand      uint32
	prevLine      [20]uint8
	combos        [256 * 8]uint8
	workers       int
	maxBanks      int
	cache         *GBVP2Cache
	tiles         []tileBest
}

// GBVP2Cache memoizes the per-frame palette optimisation across encodes of
// the same frames (auto quality encodes them several times). Which frames get
// a palette pass, and so the palette and random state each frame starts from,
// can depend on quality, so an entry is only reused when the encoder reaches
// the frame in exactly the recorded state; the output is unchanged.
type GBVP2Cache struct {
	entries map[int]paletteEntry
}

type paletteEntry struct {
	inPalette, outPalette [gbPaletteLen]encColor
	inRand, outRand       uint32
}

func NewGBVP2Cache() *GBVP2Cache {
	return &GBVP2Cache{entries: map[int]paletteEntry{}}
}

const mbc5MaxBanks = 512

// ErrAudioOverflow means the audio stream leaves no room for any frame.
var ErrAudioOverflow = errors.New("the audio alone does not fit into the ROM size limit")

func BanksFromMB(mb int) int {
	if mb <= 0 {
		return mbc5MaxBanks
	}
	banks := mb * 64
	if banks < 2 {
		banks = 2
	}
	if banks > mbc5MaxBanks {
		banks = mbc5MaxBanks
	}
	return banks
}

func newGBVP2Enc(maxBanks int) *gbvp2enc {
	if maxBanks < 2 || maxBanks > mbc5MaxBanks {
		maxBanks = mbc5MaxBanks
	}
	e := &gbvp2enc{
		output:   make([]byte, 1024*1024*8+0x4000),
		pos:      0x4001,
		holdrand: 1,
		workers:  runtime.NumCPU(),
		maxBanks: maxBanks,
	}
	for i := range e.output {
		e.output[i] = 0xFF
	}
	e.buildCombinations()
	return e
}

func (e *gbvp2enc) rand() int {
	e.holdrand = e.holdrand*214013 + 2531011
	return int((e.holdrand >> 16) & 0x7FFF)
}

func (e *gbvp2enc) buildCombinations() {
	ci := func(pal, ind int) uint8 { return uint8(pal*4 + ind) }
	out := e.combos[:0]
	emit := func(vals ...uint8) { out = append(out, vals...) }

	leftComb := func(left, firstRight uint8) {
		for k := uint8(0); k < 4; k++ {
			fr := firstRight + k
			emit(left, left, left, fr, fr, fr, fr, fr)
		}
	}
	rightComb := func(left, right uint8) {
		eq := left == right
		c2 := left
		if eq {
			c2 = left ^ 1
		}
		var c4l, c4r, c6l, c6r uint8
		if eq {
			c4l, c4r = left^2, left^2
			c6l, c6r = left^3, left^3
		} else {
			c4l, c4r = left, right
			c6l, c6r = right, right
		}
		emit(left, left, c2, c2, c4l, c4r, c6l, c6r)
	}
	leftForPalette := func(pal int) {
		leftComb(ci(pal, 0), ci(pal, 0))
		leftComb(ci(pal, 1), ci(pal, 0))
		leftComb(ci(pal, 2), ci(pal, 0))
		leftComb(ci(pal, 3), ci(pal, 0))
	}
	rightCombs := func(left, firstRight uint8) {
		rightComb(left, firstRight+0)
		rightComb(left, firstRight+1)
		rightComb(left, firstRight+2)
		rightComb(left, firstRight+3)
	}
	rightForPalette := func(pal int) {
		rightCombs(ci(pal, 0), ci(pal, 0))
		rightCombs(ci(pal, 1), ci(pal, 0))
		rightCombs(ci(pal, 2), ci(pal, 0))
		rightCombs(ci(pal, 3), ci(pal, 0))
	}
	for pal := 7; pal >= 0; pal-- {
		leftForPalette(pal)
		rightForPalette(pal)
	}
	copy(e.combos[:], out)
}

func abs8(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// round555 is the RGB555 rounding the original encoder applied to the palette
// side of every colour comparison.
func round555(v uint8) int {
	r := int(v) & 0xF8
	return r | r>>5
}

type roundedPalette [gbPaletteLen][3]int

func roundPalette(palette []encColor) (rp roundedPalette) {
	for i := range rp {
		p := palette[i]
		rp[i] = [3]int{round555(p.r), round555(p.g), round555(p.b)}
	}
	return rp
}

// pixelDists holds the colour distance of each of the 8 pixels of a tile row
// to every palette entry. A combination's score is the sum of 8 lookups, so
// the 256 combinations share 256 distance computations instead of 2048.
type pixelDists [8][gbPaletteLen]uint32

func (d *pixelDists) fill(r, g, b []uint8, rp *roundedPalette) {
	for x := 0; x < 8; x++ {
		pr, pg, pb := int(r[x]), int(g[x]), int(b[x])
		row := &d[x]
		for c := range rp {
			row[c] = uint32(abs8(pr-rp[c][0]) + abs8(pg-rp[c][1]) + abs8(pb-rp[c][2]))
		}
	}
}

func (e *gbvp2enc) scoreForCombination(d *pixelDists, combination int) uint32 {
	c := e.combos[combination*8 : combination*8+8]
	return d[0][c[0]] + d[1][c[1]] + d[2][c[2]] + d[3][c[3]] +
		d[4][c[4]] + d[5][c[5]] + d[6][c[6]] + d[7][c[7]]
}

// bestCombinationForPixels returns the first combination with the lowest score.
func (e *gbvp2enc) bestCombinationForPixels(d *pixelDists) (int, uint32) {
	best := ^uint32(0)
	bestIndex := 0
	for combination := 0; combination < 256; combination++ {
		s := e.scoreForCombination(d, combination)
		if s < best {
			best = s
			bestIndex = combination
		}
	}
	return bestIndex, best
}

// scoreDirect is scoreForCombination for a single combination, without
// building the whole distance table.
func (e *gbvp2enc) scoreDirect(r, g, b []uint8, rp *roundedPalette, combination int) uint32 {
	var score uint32
	for x, c := range e.combos[combination*8 : combination*8+8] {
		score += uint32(abs8(int(r[x])-rp[c][0]) + abs8(int(g[x])-rp[c][1]) + abs8(int(b[x])-rp[c][2]))
	}
	return score
}

type tileBest struct {
	comb  uint8
	score uint32
}

// bestTiles finds the best combination of every 8-pixel tile row of one image
// (288 lines × 20) in parallel. It depends only on the pixels and the palette;
// encodeImage then walks the result in order, so the stream is unchanged.
func (e *gbvp2enc) bestTiles(r, g, b []uint8, rBase int, rp *roundedPalette) []tileBest {
	const n = 288 * 20
	if len(e.tiles) != n {
		e.tiles = make([]tileBest, n)
	}
	nw := max(e.workers, 1)
	chunk := (n + nw - 1) / nw
	var wg sync.WaitGroup
	for lo := 0; lo < n; lo += chunk {
		hi := min(lo+chunk, n)
		wg.Add(1)
		go func(lo, hi int) {
			defer wg.Done()
			var d pixelDists
			for i := lo; i < hi; i++ {
				p := rBase + i*8
				d.fill(r[p:p+8], g[p:p+8], b[p:p+8], rp)
				comb, score := e.bestCombinationForPixels(&d)
				e.tiles[i] = tileBest{uint8(comb), score}
			}
		}(lo, hi)
	}
	wg.Wait()
	return e.tiles
}

func (e *gbvp2enc) optimizePaletteStep(r, g, b []uint8, palette []encColor, oldScore uint32, nRows int) (uint32, bool) {
	type partial struct {
		rSum, gSum, bSum, count [gbPaletteLen]uint64
		score                   uint64
	}
	nw := e.workers
	if nw < 1 {
		nw = 1
	}
	parts := make([]partial, nw)
	rp := roundPalette(palette)
	var wg sync.WaitGroup
	chunk := (nRows + nw - 1) / nw
	for w := 0; w < nw; w++ {
		lo := w * chunk
		hi := lo + chunk
		if hi > nRows {
			hi = nRows
		}
		if lo >= hi {
			continue
		}
		wg.Add(1)
		go func(w, lo, hi int) {
			defer wg.Done()
			p := &parts[w]
			var d pixelDists
			for row := lo; row < hi; row++ {
				off := row * 8
				d.fill(r[off:off+8], g[off:off+8], b[off:off+8], &rp)
				idx, cs := e.bestCombinationForPixels(&d)
				p.score += uint64(cs)
				base := idx * 8
				for x := 0; x < 8; x++ {
					colorIndex := e.combos[base+x]
					p.rSum[colorIndex] += uint64(r[off+x])
					p.gSum[colorIndex] += uint64(g[off+x])
					p.bSum[colorIndex] += uint64(b[off+x])
					p.count[colorIndex]++
				}
			}
		}(w, lo, hi)
	}
	wg.Wait()

	var rSum, gSum, bSum, count [gbPaletteLen]uint64
	var score uint64
	for w := range parts {
		score += parts[w].score
		for i := 0; i < gbPaletteLen; i++ {
			rSum[i] += parts[w].rSum[i]
			gSum[i] += parts[w].gSum[i]
			bSum[i] += parts[w].bSum[i]
			count[i] += parts[w].count[i]
		}
	}

	oldPalette := make([]encColor, gbPaletteLen)
	copy(oldPalette, palette)
	for i := 0; i < gbPaletteLen; i++ {
		if count[i] != 0 {
			palette[i].r = uint8(rSum[i] / count[i])
			palette[i].g = uint8(gSum[i] / count[i])
			palette[i].b = uint8(bSum[i] / count[i])
		} else {
			palette[i].r = uint8(e.rand())
			palette[i].g = uint8(e.rand())
			palette[i].b = uint8(e.rand())
		}
	}

	if oldScore <= uint32(score) {
		copy(palette, oldPalette)
		return oldScore, false
	}
	return uint32(score), true
}

func (e *gbvp2enc) encodeImage(r, g, b []uint8, rBase int, palette []encColor, quality, count int) {
	if count > 255 {
		e.encodeImage(r, g, b, rBase, palette, quality, count-255)
		count = 255
	}
	quality += 8

	if e.pos/0x4000 == 0xFF {
		e.pos &= ^0x3fff
		e.pos += 0x4000
	}

	e.frameCountPos = e.pos
	e.output[e.pos] = uint8(count)
	e.pos++

	if (e.pos & 0x3fff) >= 0x4000-33 {
		e.output[e.pos] = 0xFF
		e.pos &= ^0x3fff
		e.pos += 0x4000
	}
	for i := 0; i < gbPaletteLen/2; i++ {
		color := uint16(palette[i].r>>3) | (uint16(palette[i].g>>3) << 5) | (uint16(palette[i].b>>3) << 10)
		e.output[e.pos] = uint8(color >> 8)
		e.pos++
		e.output[e.pos] = uint8(color)
		e.pos++
	}
	if (e.pos & 0x3fff) >= 0x4000-33 {
		e.output[e.pos] = 0xFF
		e.pos &= ^0x3fff
		e.pos += 0x4000
	}
	for i := gbPaletteLen / 2; i < gbPaletteLen; i++ {
		color := uint16(palette[i].r>>3) | (uint16(palette[i].g>>3) << 5) | (uint16(palette[i].b>>3) << 10)
		e.output[e.pos] = uint8(color >> 8)
		e.pos++
		e.output[e.pos] = uint8(color)
		e.pos++
	}

	const maxDiffs = 3
	var lineBuffer, lossyLineBuffer [20]uint8
	rp := roundPalette(palette)
	tiles := e.bestTiles(r, g, b, rBase, &rp)
	p := rBase
	for y := 0; y < 288; y++ {
		ndiffs := 0
		for j := 0; j < 20; j++ {
			t := tiles[y*20+j]
			comb, score := int(t.comb), t.score
			lineBuffer[j] = uint8(comb)
			lossyLineBuffer[j] = uint8(comb)
			if e.prevLine[j] != uint8(comb) {
				lossyScore := e.scoreDirect(r[p:p+8], g[p:p+8], b[p:p+8], &rp, int(e.prevLine[j]))
				if lossyScore > score*uint32(quality)/8 {
					ndiffs++
				} else {
					lossyLineBuffer[j] = e.prevLine[j]
				}
			}
			p += 8
		}

		if y == 0 {
			ndiffs = 20
		}

		if (e.pos & 0x3fff) < 0x4000-22 {
			if ndiffs > maxDiffs {
				e.output[e.pos] = 0
				e.pos++
			} else {
				e.output[e.pos] = uint8(((9-ndiffs)*3 + 1) << 1)
				e.pos++
			}
		} else {
			e.output[e.pos] = 1
			e.pos &= ^0x3fff
			e.pos += 0x4000
			ndiffs = 20
		}

		if ndiffs > maxDiffs {
			copy(e.prevLine[:], lineBuffer[:])
			copy(e.output[e.pos:e.pos+20], lineBuffer[:])
			for i := 0; i < 20; i++ {
				e.output[e.pos] -= uint8(y % 144)
				e.pos++
			}
		} else {
			for i := 0; i < 20; i++ {
				if e.prevLine[i] != lossyLineBuffer[i] {
					e.output[e.pos] = lossyLineBuffer[i] - uint8(y%144) + 1
					e.pos++
					e.output[e.pos] = uint8(i) + 0xc1
					e.pos++
				}
			}
			copy(e.prevLine[:], lossyLineBuffer[:])
		}
	}

	if count != 1 && e.pos/0x4000 == 0xFF && rBase >= 46080 {
		e.output[e.frameCountPos] = 1
		e.encodeImage(r, g, b, rBase-46080, palette, quality, count-1)
	}
}

func gbQuantize(sample int) uint8 {
	switch {
	case sample < 18:
		return 0
	case sample < 55:
		return 1
	case sample < 91:
		return 2
	case sample < 130:
		return 3
	case sample < 164:
		return 4
	case sample < 200:
		return 5
	case sample < 237:
		return 6
	default:
		return 7
	}
}

func (e *gbvp2enc) Encode(sourceFPS float64, quality int, audio []byte, framePaths []string, progress func(done, total int)) (data []byte, framesUsed int, err error) {
	frameMultiplier := gbFPSConst / 2 / sourceFPS
	fpsTracking := 0.0

	r := make([]uint8, gbPixels)
	g := make([]uint8, gbPixels)
	b := make([]uint8, gbPixels)
	rgb := make([]uint8, gbScreenSize*3)
	palette := make([]encColor, gbPaletteLen)
	paletteInited := false
	id := 0

	ai := 0
	done := false
	for !done {
		if e.pos/0x4000 >= e.maxBanks-1 {
			return nil, 0, fmt.Errorf("%w (%d MB); raise Max ROM or trim the clip", ErrAudioOverflow, e.maxBanks/64)
		}
		for i := 0; i < 154; i++ {
			var left, right uint8 = 0x80, 0x80
			if ai+1 < len(audio) {
				left, right = audio[ai], audio[ai+1]
				ai += 2
			} else {
				done = true
			}
			e.output[e.pos] = gbQuantize(int(left)) | (gbQuantize(int(right)) << 4)
			e.pos++
		}
		if (e.pos&0x3FFF)+154 > 0x4000 {
			e.pos &= ^0x3fff
			e.pos += 0x4000
		}
	}
	e.pos--
	e.pos &= ^0x3fff
	e.pos += 0x4000
	e.output[0x4000] = uint8(e.pos / 0x4000)
	if e.pos/0x4000 >= e.maxBanks-1 {
		return nil, 0, fmt.Errorf("%w (%d MB); raise Max ROM or trim the clip", ErrAudioOverflow, e.maxBanks/64)
	}

	fi := 0
	total := len(framePaths)
	for {
		if progress != nil {
			progress(fi, total)
		}

		truncate := e.pos/0x4000 >= e.maxBanks-1
		if fi >= len(framePaths) || truncate {
			return e.finish(), fi, nil
		}

		fpsTracking += frameMultiplier
		frameLength := int(fpsTracking)
		if frameLength == 0 {
			fi++
			continue
		}
		fpsTracking -= float64(frameLength)

		raw, err := os.ReadFile(framePaths[fi])
		fi++
		if err != nil {
			return nil, 0, err
		}
		if len(raw) < 18+len(rgb) {
			return nil, 0, fmt.Errorf("frame %s too small / not a TGA", framePaths[fi-1])
		}
		copy(rgb, raw[18:18+len(rgb)])

		var diff int64
		for i := 0; i < gbScreenSize; i++ {
			diff += int64(abs8(int(b[i]) - int(rgb[i*3])))
			diff += int64(abs8(int(g[i]) - int(rgb[i*3+1])))
			diff += int64(abs8(int(r[i]) - int(rgb[i*3+2])))
			b[i] = rgb[i*3]
			g[i] = rgb[i*3+1]
			r[i] = rgb[i*3+2]
		}

		if id != 0 && diff < gbDiffThresh &&
			int(e.output[e.frameCountPos])+frameLength < 0xFF &&
			e.frameCountPos/0x4000 != 0xFE {
			e.output[e.frameCountPos] += uint8(frameLength)
			id++
			continue
		}

		copy(r[gbScreenSize+3:gbScreenSize+3+gbScreenSize-3], r[0:gbScreenSize-3])
		copy(g[gbScreenSize+3:gbScreenSize+3+gbScreenSize-3], g[0:gbScreenSize-3])
		copy(b[gbScreenSize+3:gbScreenSize+3+gbScreenSize-3], b[0:gbScreenSize-3])
		for y := 0; y < 144; y++ {
			base := y*160 + gbScreenSize
			r[base], r[base+1], r[base+2] = r[base+3], r[base+3], r[base+3]
			g[base], g[base+1], g[base+2] = g[base+3], g[base+3], g[base+3]
			b[base], b[base+1], b[base+2] = b[base+3], b[base+3], b[base+3]
		}

		if !paletteInited {
			for i := 0; i < gbPaletteLen; i++ {
				pixel := e.rand() % gbPixels
				palette[i].r = r[pixel]
				palette[i].g = g[pixel]
				palette[i].b = b[pixel]
			}
			paletteInited = true
		}

		e.optimizePalette(fi-1, r, g, b, palette)
		start, startCountPos := e.pos, e.frameCountPos
		e.encodeImage(r, g, b, 0, palette, quality, frameLength)
		if endBank(e.pos) >= e.maxBanks {
			// The end marker would land past the ROM limit (it skips banks
			// numbered xxFF): drop this frame and stop at the previous one.
			for i := start; i < e.pos; i++ {
				e.output[i] = 0xFF
			}
			e.pos, e.frameCountPos = start, startCountPos
			return e.finish(), fi - 1, nil
		}
		id++
	}
}

// endBank is the bank the end marker lands in when the stream stops at pos.
func endBank(pos int) int {
	bank := pos / 0x4000
	if bank&0xFF == 0xFF {
		bank++
	}
	return bank
}

// finish writes the end marker and returns the stream padded to whole banks.
func (e *gbvp2enc) finish() []byte {
	if (e.pos/0x4000)&0xFF == 0xFF {
		e.pos &= ^0x3fff
		e.pos += 0x4000
	}
	e.output[e.pos] = 0
	e.pos++
	e.pos += 0x3fff
	e.pos &= ^0x3fff
	return e.output[0x4000:e.pos]
}

// optimizePalette refines palette for frame fi in place. The result depends
// only on the frame's pixels, the starting palette and the random state.
func (e *gbvp2enc) optimizePalette(fi int, r, g, b []uint8, palette []encColor) {
	var in [gbPaletteLen]encColor
	copy(in[:], palette)
	inRand := e.holdrand
	if e.cache != nil {
		if c, ok := e.cache.entries[fi]; ok && c.inPalette == in && c.inRand == inRand {
			copy(palette, c.outPalette[:])
			e.holdrand = c.outRand
			return
		}
	}

	score := ^uint32(0)
	for i := 128; i > 0; i-- {
		var ok bool
		score, ok = e.optimizePaletteStep(r, g, b, palette, score, gbScyData)
		if !ok {
			break
		}
	}

	if e.cache != nil {
		c := paletteEntry{inPalette: in, inRand: inRand, outRand: e.holdrand}
		copy(c.outPalette[:], palette)
		e.cache.entries[fi] = c
	}
}

func runGoEncoder(sourceFPS float64, quality, maxBanks int, audio []byte, framePaths []string, cache *GBVP2Cache, progress func(done, total int)) (data []byte, framesUsed int, err error) {
	e := newGBVP2Enc(maxBanks)
	e.cache = cache
	return e.Encode(sourceFPS, quality, audio, framePaths, progress)
}
