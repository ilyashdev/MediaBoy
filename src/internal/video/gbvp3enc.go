package video

import (
	"errors"
	"fmt"
	"image"
	"math"
	"slices"
)

// GBVP3 is MediaBoy's successor of the GBVideoPlayer2 (GBVP2) stream, played by
// player/video3.asm (embedded as video3.gbc).
//
// Rendering is GBVP2's: on every scanline 20 SCY writes pick rows of a 256-row
// tile atlas, so each 8-pixel strip shows one of 256 colour combinations. What
// changed is where the SCY values live. GBVP2 keeps one 20-byte line buffer,
// codes every line against the line above and spends ~80 cycles per line
// re-basing that buffer. GBVP3 gives each of the 288 lines (two fields of 144)
// its own WRAM slot, so a line is coded against what the same line showed in
// the previous frame, and the re-basing pass is gone.
//
// Stream, from ROM 1:4000:
//
//	byte 0       first video bank
//	audio        one 78-byte chunk per GB frame (see gbvp3audio.go); a chunk
//	             never crosses a bank
//	video frames count (0 ends the stream), 32 colours, 288 line ops
//
// The count is the number of video frames (2 GB frames each) the frame is
// shown for, 1-127; with gbvp3PaletteSame set no colours follow and the last
// ones stay. Colours are 32 big-endian RGB555 values. A frame never starts in
// a bank numbered xxFF, and the player reads its header without bank checks,
// so the header never crosses a bank: the last line of the previous frame
// switches bank when it would not fit.
//
// Line op, decoded during the previous scanline:
//
//	$00          raw: 20 SCY values follow
//	$01          next bank, then any op but raw from $4000 (no time for raw)
//	patch n      ((9-n)*3+7)<<1, then n pairs (SCY value, slot address low)
//	run k        2, then a word: gbvp3RunReturn-(k-1). This line and the next
//	             k-1 are unchanged: the player reads their ops from a table in
//	             ROM0 that ends with a return to the stream, whose next op is
//	             the following line's, a patch op with at most 7 patches.
//
// The SCY value for combination c on line y is c - y%144. Line y's slot is at
// page $C0+y/12, offset y%12*20; every slot starts as zeros.
const (
	gbvp3MaxPatches       = 9
	gbvp3MaxPatchesReturn = 7 // after a run's return op: less time left
	gbvp3RawSize          = 21
	gbvp3LinesPerPage     = 12
	gbvp3Lines            = 288
	// count + 32 colours, plus the 2 bytes kept after every op
	gbvp3HeaderSize  = 1 + gbPaletteLen*2 + 2
	gbvp3MaxCount    = 127
	gbvp3PaletteSame = 0x80

	gbvp3OpRun   = 2
	gbvp3RunSize = 3
	// gbvp3MinRun is the shortest run worth its 3 bytes.
	gbvp3MinRun = 4
	// gbvp3RunMax is the longest run: RunTable holds 142 "no change" ops.
	gbvp3RunMax = 143
	// gbvp3RunReturn is RunReturn in video3.sym.
	gbvp3RunReturn = 0x0774
)

func gbvp3SlotLow(y int) uint8 { return uint8(y % gbvp3LinesPerPage * 20) }

func gbvp3PatchHeader(n int) uint8 { return uint8(((gbvp3MaxPatches-n)*3 + 7) << 1) }

// gbvp3Snap maps an 8-bit channel to the nearest colour the CGB can show
// (5 bits, expanded as v<<3 | v>>2).
var gbvp3Snap = func() (t [256]uint8) {
	for v := range t {
		best := 0
		for x := 0; x < 32; x++ {
			if abs8(x<<3|x>>2-v) < abs8(best<<3|best>>2-v) {
				best = x
			}
		}
		t[v] = uint8(best<<3 | best>>2)
	}
	return t
}()

func snapColours(palette []encColor) {
	for i := range palette {
		c := &palette[i]
		c.r, c.g, c.b = gbvp3Snap[c.r], gbvp3Snap[c.g], gbvp3Snap[c.b]
	}
}

type gbvp3Change struct {
	col  int
	gain uint32
}

// gbvp3Line is what one line needs: raw, or n patches.
type gbvp3Line struct {
	raw bool
	n   int
	ch  [20]gbvp3Change // the n changes, biggest gain first
}

func (l *gbvp3Line) same() bool { return !l.raw && l.n == 0 }

func (l *gbvp3Line) size() int {
	if l.raw {
		return gbvp3RawSize
	}
	return 1 + 2*l.n
}

// limit keeps the max biggest changes as patches.
func (l *gbvp3Line) limit(max int) {
	if l.raw || l.n > max {
		l.raw = false
		l.n = min(l.n, max)
	}
}

// gbvp3MinFrameBytes is the most an unchanged frame costs (count, a run and
// its return op per field), reserved per frame left in fit mode.
//
// The tile decisions use a tolerance relative to the best match (see decide).
// Also tried, and dropped after viewing them at low quality: weighing changes
// against their bytes (better PSNR and SSIM, but it leaves stale patches on
// flat areas; masked by texture it was closer, still not better to the eye),
// pricing the palette in bytes (wrong colours for seconds after a cut),
// changing both fields together, ageing stale tiles, restarting the palette
// fit at scene cuts.
const gbvp3MinFrameBytes = 12

// gbvp3Fit steers the quality so that the whole clip lands inside the ROM. A
// first encode of the same frames gives the plan: how the stream grows frame
// by frame, scaled to the room there is. The quality starts where that encode
// says the clip fits and moves only as far as the stream strays from the
// plan, so it stays nearly constant (which, for a given size, looks best).
// It is continuous here: the tolerance takes fractional steps.
type gbvp3Fit struct {
	start, end int       // video stream from start, stopping before end
	plan       []float64 // share of the video bytes spent after each frame
	slope      float64   // d log(bytes) / d log(q+1), measured
	q          float64   // for the next frame
	qSum       float64   // quality × video frames, for the average
	units      int       // video frames so far
	// recent bytes spent and planned, decaying, from the last frame's pos and
	// plan share
	recentSpent, recentPlanned float64
	lastPos                    int
	lastPlan                   float64
}

const (
	// gbvp3FitDecay keeps about 30 frames in the recent rate.
	gbvp3FitDecay = 1 - 1.0/30
	// gbvp3FitStep is how much of the needed correction a frame makes.
	gbvp3FitStep = 0.25
	// gbvp3FitMargin is kept free at the end of the ROM for the end marker
	// and bank switches.
	gbvp3FitMargin = 0x200
)

// newGBVP3Fit plans from est, an encode of the same frames, starting at
// quality q; slope is how the stream size follows the quality.
func newGBVP3Fit(est *Estimate, frames, maxBanks int, q, slope float64) *gbvp3Fit {
	f := &gbvp3Fit{
		start: est.videoBank * 0x4000,
		end:   maxBanks*0x4000 - gbvp3FitMargin,
		plan:  make([]float64, frames),
	}
	// Bytes after each frame of the estimate; held frames add none, frames it
	// did not reach (past 8 MB) continue at its average.
	spent, k := 0.0, 0
	for fi := range f.plan {
		for k < len(est.frameIdx) && est.frameIdx[k] <= fi {
			spent = float64(est.frameEnds[k] - f.start)
			k++
		}
		if fi >= est.Fits8MB && est.Fits8MB > 0 {
			spent = float64(est.frameEnds[len(est.frameEnds)-1]-f.start) * float64(fi+1) / float64(est.Fits8MB)
		}
		f.plan[fi] = spent
	}
	total := max(f.plan[frames-1], 1)
	for fi := range f.plan {
		f.plan[fi] /= total
	}
	f.q, f.slope, f.lastPos = q, slope, f.start
	return f
}

// videoBytes is the video stream of est, extended to the whole clip when it
// stopped at 8 MB.
func (est *Estimate) videoBytes() float64 {
	b := float64(est.Bytes - est.videoBank*0x4000)
	if est.Fits8MB < est.Frames {
		b *= float64(est.Frames) / float64(max(est.Fits8MB, 1))
	}
	return b
}

// fitQuality guesses the quality at which the video takes budget bytes, and
// how the size follows the quality, from encodes at one or two qualities: the
// stream shrinks as a power of (quality + 1), taken as -0.5 when one encode
// leaves nothing to measure it by. The fit goes no lower than MinPercent:
// what does not fit there is cut.
func fitQuality(budget int, ests ...*Estimate) (q, slope float64) {
	a := ests[len(ests)-1]
	x := math.Log(float64(a.Quality) + 1)
	slope = -0.5
	if len(ests) > 1 {
		xb := math.Log(float64(ests[0].Quality) + 1)
		if s := (math.Log(a.videoBytes()) - math.Log(ests[0].videoBytes())) / (x - xb); x != xb && s < -0.05 {
			slope = s
		}
	}
	x += (math.Log(float64(max(budget, 1))) - math.Log(a.videoBytes())) / slope
	return max(0, min(math.Exp(x)-1, MaxQuality)), slope
}

// step follows frame fi, shown for units video frames at quality q, with
// the stream now at pos and reserve bytes kept for the frames after it. The
// rest of the clip must fit the room left: the bytes it will take are its
// planned share at the scale the recent frames ran at, and the quality moves
// toward where that scale meets the room, a step at a time.
func (f *gbvp3Fit) step(fi, units int, q float64, pos, reserve int) {
	f.units += units
	f.qSum += q * float64(units)
	budget := float64(f.end - f.start)
	f.recentSpent = f.recentSpent*gbvp3FitDecay + float64(pos-f.lastPos)
	f.recentPlanned = f.recentPlanned*gbvp3FitDecay + budget*(f.plan[fi]-f.lastPlan)
	f.lastPos, f.lastPlan = pos, f.plan[fi]
	rest := budget * (1 - f.plan[fi])
	if rest < 1 || f.recentPlanned < 1 || f.recentSpent < 1 {
		return
	}
	scale := f.recentSpent / f.recentPlanned
	need := max(float64(f.end-pos-reserve), 1) / rest
	// bytes ∝ (q+1)^slope: to scale them by need/scale, (q+1) by its 1/slope.
	k := math.Pow(need/scale, gbvp3FitStep/f.slope)
	f.q = max(0, min((f.q+1)*max(0.7, min(1.4, k))-1, MaxQuality))
}

// quality is the average quality the fit settled on.
func (f *gbvp3Fit) quality() int {
	return int(math.Round(f.qSum / float64(max(f.units, 1))))
}

type gbvp3enc struct {
	*encoder
	// lines holds the combination every line slot shows now.
	lines [gbvp3Lines][20]uint8
	ops   [gbvp3Lines]gbvp3Line
	// dither is how much of field A's error field B makes up for (0: none).
	dither float64
	target [3][]uint8 // field B's aim, see fieldB
	rate   *gbvp3Fit  // nil: fixed quality

	stop    func() bool  // optional: abandon the encode
	onFrame func(fi int) // optional: frame fi is in the stream, ending at pos
}

var errStopped = errors.New("encode stopped")

func newGBVP3Enc(maxBanks int) *gbvp3enc {
	e := &gbvp3enc{encoder: newEncoder(maxBanks)}
	for y := range e.lines {
		for j := range e.lines[y] {
			e.lines[y][j] = uint8(y % 144) // slot value 0
		}
	}
	return e
}

func (e *gbvp3enc) nextBank() {
	e.pos &^= 0x3fff
	e.pos += 0x4000
}

func (e *gbvp3enc) room() int { return 0x4000 - e.pos&0x3fff }

func (e *gbvp3enc) writePalette(palette []encColor) {
	for _, c := range palette {
		v := uint16(c.r>>3) | uint16(c.g>>3)<<5 | uint16(c.b>>3)<<10
		e.output[e.pos] = uint8(v >> 8)
		e.output[e.pos+1] = uint8(v)
		e.pos += 2
	}
}

// decide fills e.ops with what each line needs to show tiles: a tile changes
// when the combination its slot holds is worse than the best by more than the
// tolerance, (q+8)/8 times. From 10 changes a raw line is no larger, and it
// is exact.
func (e *gbvp3enc) decide(r, g, b []uint8, rp *roundedPalette, tiles []tileBest, q float64, y0, y1 int) {
	thr := (q + 8) / 8
	for y := y0; y < y1; y++ {
		l := &e.ops[y]
		l.n = 0
		for j := 0; j < 20; j++ {
			t := tiles[y*20+j]
			if e.lines[y][j] == t.comb {
				continue
			}
			px := (y*20 + j) * 8
			s := e.scoreDirect(r[px:px+8], g[px:px+8], b[px:px+8], rp, int(e.lines[y][j]))
			if float64(s) <= float64(t.score)*thr {
				continue
			}
			l.ch[l.n] = gbvp3Change{j, s - t.score}
			l.n++
		}
		slices.SortStableFunc(l.ch[:l.n], func(a, b gbvp3Change) int { return int(b.gain) - int(a.gain) })
		l.raw = l.n > gbvp3MaxPatches
	}
}

// fieldB decides field B (lines 144-287) once field A's ops are known. The
// eye blends the two fields, which alternate at 60 Hz, so with dither field
// B aims at the source plus that share of what field A gets wrong: more
// shades, more 30 Hz flicker. (Measured at 0.5: SSIM +0.010-0.015, flicker
// +35 %. A palette of field B's own on top added only +0.001-0.007.)
// tiles holds field A's best combinations; field B's are written into it.
func (e *gbvp3enc) fieldB(r, g, b []uint8, rp *roundedPalette, tiles []tileBest, q float64) {
	if e.dither == 0 {
		e.decide(r, g, b, rp, tiles, q, 144, gbvp3Lines)
		return
	}
	if e.target[0] == nil {
		e.target = [3][]uint8{make([]uint8, gbPixels), make([]uint8, gbPixels), make([]uint8, gbPixels)}
	}
	tr, tg, tb := e.target[0], e.target[1], e.target[2]
	copy(tr, r)
	copy(tg, g)
	copy(tb, b)
	// Field B pixel x pairs on screen with field A pixel x-3.
	for y := 0; y < 144; y++ {
		l := &e.ops[y]
		comb := e.lines[y]
		for _, c := range l.ch[:l.n] {
			comb[c.col] = tiles[y*20+c.col].comb
		}
		if l.raw {
			for j := range comb {
				comb[j] = tiles[y*20+j].comb
			}
		}
		for x := 3; x < 160; x++ {
			a := rp[e.combos[int(comb[(x-3)/8])*8+(x-3)%8]]
			i := gbScreenSize + y*160 + x
			for ch, t := range [3][]uint8{tr, tg, tb} {
				v := float64(t[i])
				t[i] = uint8(max(0, min(255, math.Round(v+e.dither*(v-float64(a[ch]))))))
			}
		}
	}
	copy(tiles[144*20:], e.bestTiles(tr, tg, tb, 0, rp)[144*20:])
	e.decide(tr, tg, tb, rp, tiles, q, 144, gbvp3Lines)
}

// writeOp writes line y's op and updates its slot.
func (e *gbvp3enc) writeOp(y int, l *gbvp3Line, tiles []tileBest) {
	ly := uint8(y % 144)
	cur := &e.lines[y]
	if l.raw {
		e.output[e.pos] = 0
		e.pos++
		for j := range cur {
			cur[j] = tiles[y*20+j].comb
			e.output[e.pos] = cur[j] - ly
			e.pos++
		}
		return
	}
	e.output[e.pos] = gbvp3PatchHeader(l.n)
	e.pos++
	for _, c := range l.ch[:l.n] {
		cur[c.col] = tiles[y*20+c.col].comb
		e.output[e.pos] = cur[c.col] - ly
		e.output[e.pos+1] = gbvp3SlotLow(y) + uint8(c.col)
		e.pos += 2
	}
}

// fit switches bank when the next size bytes do not fit. Ops are read as
// words, so 2 bytes stay free after each; what the player reads after line
// y without bank checks must fit too (see tail). A line after a bank switch
// has no time for a raw copy, so a bank is left early on a line that
// patches; otherwise first is limited to 9 patches.
func (e *gbvp3enc) fit(size, y int, raw bool, first *gbvp3Line) {
	need := size + e.tail(y)
	if room := e.room(); room < need || (room < 2*(gbvp3RawSize+2) && !raw) {
		e.output[e.pos] = 1
		e.nextBank()
		first.limit(gbvp3MaxPatches)
	}
}

// tail is what must stay in the bank after line y's op: after the last line,
// the next frame's header, which the player reads in VBlank without bank
// checks; else the 2 bytes an op read takes.
func (e *gbvp3enc) tail(y int) int {
	if y == gbvp3Lines-1 {
		return gbvp3HeaderSize
	}
	return 2
}

// runLength is how many lines from y a run op covers, or 0. A run stays in
// its field, and the line after it is decoded by the return op.
func (e *gbvp3enc) runLength(y int) int {
	if !e.ops[y].same() {
		return 0
	}
	last := y - y%144 + 142
	k := 0
	for k < gbvp3RunMax && y+k <= last && e.ops[y+k].same() {
		k++
	}
	if a := &e.ops[y+k]; a.raw || a.n > gbvp3MaxPatchesReturn {
		k--
	}
	if k < gbvp3MinRun {
		return 0
	}
	return k
}

// frameBytes is what encodeFrame writes for e.ops, bank switches aside.
func (e *gbvp3enc) frameBytes(paletteSame bool) int {
	n := 1
	if !paletteSame {
		n += gbPaletteLen * 2
	}
	for y := 0; y < gbvp3Lines; {
		if k := e.runLength(y); k > 0 {
			n += gbvp3RunSize + e.ops[y+k].size()
			y += k + 1
			continue
		}
		n += e.ops[y].size()
		y++
	}
	return n
}

// encodeFrame writes one video frame shown for count video frames.
func (e *gbvp3enc) encodeFrame(tiles []tileBest, palette []encColor, paletteSame bool, count int) {
	if count > gbvp3MaxCount {
		// The repeat costs a few bytes: every slot already holds the frame.
		e.encodeFrame(tiles, palette, paletteSame, count-gbvp3MaxCount)
		count, paletteSame = gbvp3MaxCount, true
		for y := range e.ops {
			e.ops[y].n, e.ops[y].raw = 0, false
		}
	}
	if (e.pos/0x4000)&0xFF == 0xFF {
		e.nextBank()
	}
	e.frameCountPos = e.pos
	e.output[e.pos] = uint8(count)
	e.pos++
	if paletteSame {
		e.output[e.frameCountPos] |= gbvp3PaletteSame
	} else {
		e.writePalette(palette)
	}

	for y := 0; y < gbvp3Lines; {
		if k := e.runLength(y); k > 0 {
			after := &e.ops[y+k]
			e.fit(gbvp3RunSize+after.size(), y+k, false, after)
			addr := gbvp3RunReturn - (k - 1)
			e.output[e.pos], e.output[e.pos+1], e.output[e.pos+2] = gbvp3OpRun, uint8(addr), uint8(addr>>8)
			e.pos += gbvp3RunSize
			e.writeOp(y+k, after, tiles)
			y += k + 1
			continue
		}
		l := &e.ops[y]
		e.fit(l.size(), y, l.raw, l)
		e.writeOp(y, l, tiles)
		y++
	}
}

// loadFrame fills the first field of r, g, b with img (160×144).
func loadFrame(img image.Image, r, g, b []uint8) {
	bd := img.Bounds()
	if rgba, ok := img.(*image.RGBA); ok {
		for y := 0; y < 144; y++ {
			row := rgba.Pix[(bd.Min.Y+y-rgba.Rect.Min.Y)*rgba.Stride+(bd.Min.X-rgba.Rect.Min.X)*4:]
			for x := 0; x < 160; x++ {
				i := y*160 + x
				r[i], g[i], b[i] = row[x*4], row[x*4+1], row[x*4+2]
			}
		}
		return
	}
	for y := 0; y < 144; y++ {
		for x := 0; x < 160; x++ {
			rr, gg, bb, _ := img.At(bd.Min.X+x, bd.Min.Y+y).RGBA()
			i := y*160 + x
			r[i], g[i], b[i] = uint8(rr>>8), uint8(gg>>8), uint8(bb>>8)
		}
	}
}

// buildSecondField copies the first field shifted right by 3 pixels into the
// second, as GBVP2 does: the player shows it with SCX=4.
func buildSecondField(r, g, b []uint8) {
	for _, c := range [][]uint8{r, g, b} {
		copy(c[gbScreenSize+3:], c[:gbScreenSize-3])
		for y := 0; y < 144; y++ {
			base := y*160 + gbScreenSize
			c[base], c[base+1], c[base+2] = c[base+3], c[base+3], c[base+3]
		}
	}
}

func tilesScore(tiles []tileBest) (s uint64) {
	for _, t := range tiles {
		s += uint64(t.score)
	}
	return s
}

// Encode writes the audio and as many frames as fit into maxBanks.
func (e *gbvp3enc) Encode(sourceFPS float64, quality int, audio []byte, frames []image.Image, progress func(done, total int)) (data []byte, framesUsed int, err error) {
	// The player reads a chunk every GB frame whatever the audio length, so
	// pad with silence to the length of the clip.
	chunks := gbvp3Audio(audio)
	need := int(float64(len(frames))/sourceFPS*gbFPSConst+2) * gbvp3ChunkBytes
	for len(chunks) < need {
		chunks = append(chunks, gbvp3Silence)
	}
	e.pos = 0x4001
	for off := 0; off < len(chunks); off += gbvp3ChunkBytes {
		if e.pos&0x3fff+gbvp3ChunkBytes > 0x4000 {
			e.nextBank()
		}
		if e.pos/0x4000 >= e.maxBanks-1 {
			return nil, 0, fmt.Errorf("%w (%d MB); raise Max ROM or trim the clip", ErrAudioOverflow, e.maxBanks/64)
		}
		copy(e.output[e.pos:], chunks[off:off+gbvp3ChunkBytes])
		e.pos += gbvp3ChunkBytes
	}
	e.nextBank()
	e.output[0x4000] = uint8(e.pos / 0x4000)
	if e.pos/0x4000 >= e.maxBanks-1 {
		return nil, 0, fmt.Errorf("%w (%d MB); raise Max ROM or trim the clip", ErrAudioOverflow, e.maxBanks/64)
	}

	frameMultiplier := gbFPSConst / 2 / sourceFPS
	fpsTracking := 0.0
	r := make([]uint8, gbPixels)
	g := make([]uint8, gbPixels)
	b := make([]uint8, gbPixels)
	// last is the first field of the last encoded frame: a frame is held
	// instead of encoded only when it is close to what is on screen, so slow
	// changes cannot drift away unnoticed.
	last := make([]uint8, gbScreenSize*3)
	fitted := make([]encColor, gbPaletteLen) // the k-means chain
	shown := make([]encColor, gbPaletteLen)  // the colours on screen
	newTiles := make([]tileBest, 288*20)
	frameTiles := make([]tileBest, 288*20) // field A at its palette, field B at its own
	encoded := 0

	for fi := 0; ; fi++ {
		if progress != nil {
			progress(fi, len(frames))
		}
		if e.stop != nil && e.stop() {
			return nil, 0, errStopped
		}
		if fi >= len(frames) {
			return e.finish(), fi, nil
		}
		fpsTracking += frameMultiplier
		frameLength := int(fpsTracking)
		if frameLength == 0 {
			continue
		}
		fpsTracking -= float64(frameLength)

		loadFrame(frames[fi], r, g, b)
		var diff int64
		for i := 0; i < gbScreenSize; i++ {
			diff += int64(abs8(int(r[i])-int(last[i*3]))) +
				int64(abs8(int(g[i])-int(last[i*3+1]))) +
				int64(abs8(int(b[i])-int(last[i*3+2])))
		}
		if encoded != 0 && diff < gbDiffThresh &&
			int(e.output[e.frameCountPos]&^gbvp3PaletteSame)+frameLength <= gbvp3MaxCount {
			e.output[e.frameCountPos] += uint8(frameLength)
			if e.rate != nil {
				e.rate.step(fi, frameLength, e.rate.q, e.pos, (len(frames)-fi-1)*gbvp3MinFrameBytes)
			}
			continue
		}
		for i := 0; i < gbScreenSize; i++ {
			last[i*3], last[i*3+1], last[i*3+2] = r[i], g[i], b[i]
		}
		buildSecondField(r, g, b)

		// The palettes are fitted frame after frame from the previous fit,
		// whatever was shown, so the fits do not depend on the quality and the
		// palette cache serves every encode of the clip.
		if encoded == 0 {
			for i := range fitted {
				p := e.rand() % gbPixels
				fitted[i] = encColor{b: b[p], g: g[p], r: r[p]}
			}
		}
		e.optimizePalette(fi, r, g, b, fitted)
		rp := roundPalette(fitted)
		copy(newTiles, e.bestTiles(r, g, b, 0, &rp))
		var old []tileBest
		var oldRP roundedPalette
		if encoded != 0 && !slices.Equal(fitted, shown) {
			oldRP = roundPalette(shown)
			old = e.bestTiles(r, g, b, 0, &oldRP)
		}

		q := float64(quality)
		if e.rate != nil {
			q = e.rate.q
		}
		var palette []encColor
		var paletteSame bool
		decide := func() {
			// Keep the colours on screen unless the new ones are better by
			// more than quality/256: unchanged lines then stay exact.
			palette, paletteSame = fitted, false
			rp, tiles := rp, newTiles
			switch {
			case encoded == 0:
			case old == nil:
				paletteSame = true
			case float64(tilesScore(old))*256 <= float64(tilesScore(newTiles))*(256+q):
				palette, tiles, paletteSame, rp = shown, old, true, oldRP
			}
			copy(frameTiles, tiles)
			e.decide(r, g, b, &rp, frameTiles, q, 0, 144)
			e.fieldB(r, g, b, &rp, frameTiles, q)
		}
		decide()
		if f := e.rate; f != nil {
			// This frame and the least the frames after it can cost must fit:
			// lower the quality until they do.
			reserve := (len(frames) - fi - 1) * gbvp3MinFrameBytes
			for e.pos+e.frameBytes(paletteSame)+reserve > f.end && q < MaxQuality {
				q = min(max(q*2, 1), MaxQuality)
				decide()
			}
		}

		start, startCountPos, startLines := e.pos, e.frameCountPos, e.lines
		e.encodeFrame(frameTiles, palette, paletteSame, frameLength)
		if endBank(e.pos) >= e.maxBanks {
			for i := start; i < e.pos; i++ {
				e.output[i] = 0xFF
			}
			e.pos, e.frameCountPos, e.lines = start, startCountPos, startLines
			return e.finish(), fi, nil
		}
		copy(shown, palette)
		if e.rate != nil {
			e.rate.step(fi, frameLength, q, e.pos, (len(frames)-fi-1)*gbvp3MinFrameBytes)
		}
		encoded++
		if e.onFrame != nil {
			e.onFrame(fi)
		}
	}
}
