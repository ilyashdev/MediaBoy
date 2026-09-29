package video

import (
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
}

const mbc5MaxBanks = 512

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

func roundedColorDiff(r1, g1, b1, r2, g2, b2 int) int {
	r2 &= 0xF8
	r2 |= r2 >> 5
	g2 &= 0xF8
	g2 |= g2 >> 5
	b2 &= 0xF8
	b2 |= b2 >> 5
	return abs8(r1-r2) + abs8(g1-g2) + abs8(b1-b2)
}

func abs8(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func (e *gbvp2enc) scoreForCombination(r, g, b []uint8, palette []encColor, combination int) uint32 {
	var score uint32
	base := combination * 8
	for x := 0; x < 8; x++ {
		ciIdx := e.combos[base+x]
		p := palette[ciIdx]
		score += uint32(roundedColorDiff(int(r[x]), int(g[x]), int(b[x]), int(p.r), int(p.g), int(p.b)))
	}
	return score
}

func (e *gbvp2enc) bestCombinationForPixels(r, g, b []uint8, palette []encColor) (int, uint32) {
	best := ^uint32(0)
	bestIndex := 0
	for combination := 0; combination < 256; combination++ {
		s := e.scoreForCombination(r, g, b, palette, combination)
		if s < best {
			best = s
			bestIndex = combination
		}
	}
	return bestIndex, best
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
			for row := lo; row < hi; row++ {
				off := row * 8
				idx, cs := e.bestCombinationForPixels(r[off:off+8], g[off:off+8], b[off:off+8], palette)
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
	p := rBase
	for y := 0; y < 288; y++ {
		ndiffs := 0
		for j := 0; j < 20; j++ {
			comb, score := e.bestCombinationForPixels(r[p:p+8], g[p:p+8], b[p:p+8], palette)
			lineBuffer[j] = uint8(comb)
			lossyLineBuffer[j] = uint8(comb)
			if e.prevLine[j] != uint8(comb) {
				lossyScore := e.scoreForCombination(r[p:p+8], g[p:p+8], b[p:p+8], palette, int(e.prevLine[j]))
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

	fi := 0
	total := len(framePaths)
	for {
		if progress != nil {
			progress(fi, total)
		}

		truncate := e.pos/0x4000 >= e.maxBanks-1
		if fi >= len(framePaths) || truncate {
			if (e.pos/0x4000)&0xFF == 0xFF {
				e.pos &= ^0x3fff
				e.pos += 0x4000
			}
			e.output[e.pos] = 0
			e.pos++
			e.pos += 0x3fff
			e.pos &= ^0x3fff
			return e.output[0x4000:e.pos], fi, nil
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

		score := ^uint32(0)
		for i := 128; i > 0; i-- {
			var ok bool
			score, ok = e.optimizePaletteStep(r, g, b, palette, score, gbScyData)
			if !ok {
				break
			}
		}

		e.encodeImage(r, g, b, 0, palette, quality, frameLength)
		id++
	}
}

func runGoEncoder(sourceFPS float64, quality, maxBanks int, audio []byte, framePaths []string, progress func(done, total int)) (data []byte, framesUsed int, err error) {
	return newGBVP2Enc(maxBanks).Encode(sourceFPS, quality, audio, framePaths, progress)
}
