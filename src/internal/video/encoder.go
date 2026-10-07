// Copyright (c) 2015-2019 Lior Halphon (GBVideoPlayer2)
// Copyright (c) 2026 Ilyashdev
// MIT License; the full text is in THIRD_PARTY.md.

package video

// The colour core of the video encoder: CGB palettes fitted with k-means and
// the 256 8-pixel combinations the player can show. Ported from
// GBVideoPlayer2's encoder.c (MIT, Lior Halphon); the stream is gbvp3enc.go.

import (
	"errors"
	"runtime"
	"slices"
	"sync"

	"MediaBoy/internal/safe"
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

type encoder struct {
	output        []byte
	pos           int
	frameCountPos int
	holdrand      uint32
	combos        [256 * 8]uint8
	workers       int
	maxBanks      int
	cache         *PaletteCache
	tiles         []tileBest
	// fixedBlack keeps colour 0 at pure black, and every all-black tile row
	// on the combination of that colour alone, so letterbox bars never change
	// (see hasBars).
	fixedBlack bool
	blackComb  uint8
}

// PaletteCache memoizes the per-frame palette fits, the bulk of an encode,
// across encodes of the same frames: estimates, the fit's passes, other
// qualities. An entry is only reused when the encoder reaches the frame in
// exactly the recorded state, so the output is unchanged. It is safe for
// concurrent encodes.
type PaletteCache struct {
	mu      sync.Mutex
	entries map[int]paletteEntry
}

type paletteEntry struct {
	inPalette, outPalette [gbPaletteLen]encColor
	inRand, outRand       uint32
	fixedBlack            bool
}

func NewPaletteCache() *PaletteCache {
	return &PaletteCache{entries: map[int]paletteEntry{}}
}

func (c *PaletteCache) get(fi int) (paletteEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[fi]
	return e, ok
}

func (c *PaletteCache) put(fi int, e paletteEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[fi] = e
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

func newEncoder(maxBanks int) *encoder {
	if maxBanks < 2 || maxBanks > mbc5MaxBanks {
		maxBanks = mbc5MaxBanks
	}
	e := &encoder{
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

func (e *encoder) rand() int {
	e.holdrand = e.holdrand*214013 + 2531011
	return int((e.holdrand >> 16) & 0x7FFF)
}

func (e *encoder) buildCombinations() {
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
	for c := 255; c >= 0; c-- {
		if [8]uint8(e.combos[c*8:c*8+8]) == ([8]uint8{}) {
			e.blackComb = uint8(c)
		}
	}
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

func (e *encoder) scoreForCombination(d *pixelDists, combination int) uint32 {
	c := e.combos[combination*8 : combination*8+8]
	return d[0][c[0]] + d[1][c[1]] + d[2][c[2]] + d[3][c[3]] +
		d[4][c[4]] + d[5][c[5]] + d[6][c[6]] + d[7][c[7]]
}

// bestCombinationForPixels returns the first combination with the lowest score.
func (e *encoder) bestCombinationForPixels(d *pixelDists) (int, uint32) {
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
func (e *encoder) scoreDirect(r, g, b []uint8, rp *roundedPalette, combination int) uint32 {
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
// the stream encoder then walks the result in order.
func (e *encoder) bestTiles(r, g, b []uint8, rBase int, rp *roundedPalette) []tileBest {
	const n = 288 * 20
	if len(e.tiles) != n {
		e.tiles = make([]tileBest, n)
	}
	nw := max(e.workers, 1)
	chunk := (n + nw - 1) / nw
	var wg safe.Group
	for lo := 0; lo < n; lo += chunk {
		hi := min(lo+chunk, n)
		wg.Go(func() {
			var d pixelDists
			for i := lo; i < hi; i++ {
				p := rBase + i*8
				if e.fixedBlack && allBlack(r[p:p+8], g[p:p+8], b[p:p+8]) {
					e.tiles[i] = tileBest{e.blackComb, 0}
					continue
				}
				d.fill(r[p:p+8], g[p:p+8], b[p:p+8], rp)
				comb, score := e.bestCombinationForPixels(&d)
				e.tiles[i] = tileBest{uint8(comb), score}
			}
		})
	}
	wg.Wait()
	return e.tiles
}

// gbvp3Worst is how many badly matched pixels each worker keeps to restart
// unused colours from.
const gbvp3Worst = 8

type worstPixel struct {
	dist    uint32
	r, g, b uint8
}

// keepWorst adds p to list, which holds the worst pixels so far.
func keepWorst(list []worstPixel, p worstPixel) []worstPixel {
	if len(list) < gbvp3Worst {
		return append(list, p)
	}
	least := 0
	for i := range list {
		if list[i].dist < list[least].dist {
			least = i
		}
	}
	if p.dist > list[least].dist {
		list[least] = p
	}
	return list
}

// paletteStep scores palette on the frame (each 8-pixel row matched to its
// best combination) and writes the next guess into next: every colour moves
// to the per-channel median of the pixels matched to it, which is what
// minimises the summed |Δ| the score counts. A colour no pixel uses restarts
// at one of the worst matched pixels.
func (e *encoder) paletteStep(r, g, b []uint8, palette, next []encColor, nRows int) uint64 {
	type partial struct {
		hist  [gbPaletteLen][3][256]uint32
		score uint64
		worst []worstPixel
	}
	nw := max(e.workers, 1)
	parts := make([]partial, nw)
	rp := roundPalette(palette)
	var wg safe.Group
	chunk := (nRows + nw - 1) / nw
	for w := 0; w < nw; w++ {
		lo, hi := w*chunk, min(w*chunk+chunk, nRows)
		if lo >= hi {
			continue
		}
		wg.Go(func() {
			p := &parts[w]
			var d pixelDists
			for row := lo; row < hi; row++ {
				off := row * 8
				d.fill(r[off:off+8], g[off:off+8], b[off:off+8], &rp)
				idx, cs := e.bestCombinationForPixels(&d)
				p.score += uint64(cs)
				for x, c := range e.combos[idx*8 : idx*8+8] {
					p.hist[c][0][r[off+x]]++
					p.hist[c][1][g[off+x]]++
					p.hist[c][2][b[off+x]]++
					p.worst = keepWorst(p.worst, worstPixel{d[x][c], r[off+x], g[off+x], b[off+x]})
				}
			}
		})
	}
	wg.Wait()

	var score uint64
	var worst []worstPixel
	for w := range parts {
		score += parts[w].score
		worst = append(worst, parts[w].worst...)
	}
	slices.SortFunc(worst, func(a, b worstPixel) int { return int(b.dist) - int(a.dist) })
	for i := range next {
		var hist [3][256]uint32
		var n uint32
		for w := range parts {
			for ch := range hist {
				for v, k := range parts[w].hist[i][ch] {
					hist[ch][v] += k
				}
			}
		}
		for _, k := range hist[0] {
			n += k
		}
		if n == 0 {
			if len(worst) > 0 {
				next[i] = encColor{b: worst[0].b, g: worst[0].g, r: worst[0].r}
				worst = worst[1:]
			} else {
				next[i] = palette[i]
			}
			continue
		}
		var med [3]uint8
		for ch := range hist {
			var acc uint32
			for v, k := range hist[ch] {
				if acc += k; acc*2 >= n {
					med[ch] = uint8(v)
					break
				}
			}
		}
		next[i] = encColor{b: med[2], g: med[1], r: med[0]}
	}
	// Round each colour to the nearest one the CGB shows rather than
	// truncating it to 5 bits.
	snapColours(next)
	if e.fixedBlack {
		next[0] = encColor{}
	}
	return score
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
func (e *encoder) finish() []byte {
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
func (e *encoder) optimizePalette(fi int, r, g, b []uint8, palette []encColor) {
	var in [gbPaletteLen]encColor
	copy(in[:], palette)
	inRand := e.holdrand
	if e.cache != nil {
		if c, ok := e.cache.get(fi); ok && c.inPalette == in && c.inRand == inRand && c.fixedBlack == e.fixedBlack {
			copy(palette, c.outPalette[:])
			e.holdrand = c.outRand
			return
		}
	}

	// Step while the palette improves and keep the best one seen: a step can
	// make it worse (the matching is not the plain nearest colour). The
	// original encoder.c kept the last palette instead, sometimes the worse
	// one, used means (made for squared error) and restarted unused colours
	// at random RGB; medians and restarts at the worst pixels measured
	// +0.02-0.03 SSIM.
	best, bestScore := slices.Clone(palette), ^uint64(0)
	cur, next := slices.Clone(palette), make([]encColor, gbPaletteLen)
	for i := 128; i > 0; i-- {
		s := e.paletteStep(r, g, b, cur, next, gbScyData)
		if s >= bestScore {
			break
		}
		copy(best, cur)
		bestScore = s
		cur, next = next, cur
	}
	copy(palette, best)

	if e.cache != nil {
		c := paletteEntry{inPalette: in, inRand: inRand, outRand: e.holdrand, fixedBlack: e.fixedBlack}
		copy(c.outPalette[:], palette)
		e.cache.put(fi, c)
	}
}
