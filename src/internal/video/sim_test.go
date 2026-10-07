package video

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"image"
	"math"
	"os"
	"testing"
)

// simFrame is one video frame as the player shows it: both fields as indices
// into the frame's 32 colours, and how many video frames it is held for.
type simFrame struct {
	count   int
	palette [2][gbPaletteLen][3]int // per field, displayed RGB, 8 bits per channel
	fields  [2][144][160]uint8
}

// Cycle model of video3.asm, in M-cycles from the timer wake-up of the
// scanline that decodes a line. The renderer (prologue + 20 SCY writes) is\n// GBVideoPlayer2's; 220 is its own worst case and the limit kept here.
const (
	gbvp3CycleLimit    = 220
	gbvp3CyclesPatch   = 130 // + 6 per patch
	gbvp3CyclesRaw     = 217
	gbvp3CyclesBank    = 147 // + 6 per patch
	gbvp3CyclesRun     = 155
	gbvp3CyclesRunBank = 172
	gbvp3CyclesReturn  = 173 // + 6 per patch
	gbvp3CyclesPerHit  = 6
	gbvp3CyclesPage    = 1 // the render pointer skips to the next page

	gbvp3OpReturn = 8
)

type gbvp3SimStats struct {
	frames, rawLines, bankLines, runLines, palettesKept int
	patchLines                                          [gbvp3MaxPatches + 1]int
	maxCycles                                           int
	videoBytes, audioBytes                              int
}

// applyPatches reads n (SCY value, slot address) pairs at pos into line y's
// slot and returns the position after them.
func applyPatches(t testing.TB, rd func(int) uint8, pos, y, n int, slot *[20]uint8) int {
	low := gbvp3SlotLow(y)
	for k := 0; k < n; k++ {
		v, a := rd(pos), rd(pos+1)
		pos += 2
		if a < low || a >= low+20 {
			t.Fatalf("line %d: patch address %#x outside its slot", y, a)
		}
		slot[a-low] = v
	}
	return pos
}

// simulateGBVP3 plays a GBVP3 stream (data starts at ROM 0x4000) the way
// video3.asm does and returns the frames, the audio levels of every GB frame
// shown, and stream statistics.
func simulateGBVP3(t testing.TB, data []byte) ([]simFrame, []uint8, gbvp3SimStats) {
	t.Helper()
	e := &encoder{}
	e.buildCombinations()
	var st gbvp3SimStats
	rd := func(pos int) uint8 {
		if i := pos - 0x4000; i >= 0 && i < len(data) {
			return data[i]
		}
		t.Fatalf("read outside stream at %#x", pos)
		return 0
	}
	nextBank := func(pos int) int { return pos&^0x3fff + 0x4000 }
	// Every bank the player maps must be one MBC1 can map too.
	mapped := func(what string, pos int) {
		if mbc1Unmapped(pos / 0x4000) {
			t.Fatalf("%s in bank %#x, which MBC1 cannot map", what, pos/0x4000)
		}
	}

	videoStart := int(rd(0x4000)) * 0x4000
	mapped("video start", videoStart)
	st.audioBytes = videoStart - 0x4001

	// Ops of a run's lines come from RunTable in ROM0.
	op := func(pos int) uint8 {
		switch {
		case pos == gbvp3RunReturn:
			return gbvp3OpReturn
		case pos >= gbvp3RunReturn-(gbvp3RunMax-1) && pos < gbvp3RunReturn:
			return gbvp3PatchHeader(0)
		}
		return rd(pos)
	}
	patchN := func(y int, h uint8) int {
		n := gbvp3MaxPatches - (int(h>>1)-7)/3
		if n < 0 || n > gbvp3MaxPatches || gbvp3PatchHeader(n) != h {
			t.Fatalf("line %d: bad op %#x", y, h)
		}
		return n
	}

	var slots [gbvp3Lines][20]uint8 // SCY values, all zero at start
	pos := videoStart
	var out []simFrame
	var palette [gbPaletteLen][3]int
	runReturn := -1
	for {
		if (pos/0x4000)&0xFF == 0xFF {
			pos = nextBank(pos)
		}
		count := int(rd(pos))
		pos++
		if count == 0 {
			break
		}
		f := simFrame{count: count &^ gbvp3PaletteSame}
		if count&gbvp3PaletteSame != 0 {
			if len(out) == 0 {
				t.Fatalf("first frame keeps the colours of no frame")
			}
			st.palettesKept++
		} else {
			if (pos+gbPaletteLen*2-1)/0x4000 != (pos-1)/0x4000 {
				t.Fatalf("frame %d: header crosses into bank %d", len(out), (pos+gbPaletteLen*2-1)/0x4000)
			}
			for i := range palette {
				c := int(rd(pos))<<8 | int(rd(pos+1))
				pos += 2
				ex := func(v int) int { v &= 31; return v<<3 | v>>2 }
				palette[i] = [3]int{ex(c), ex(c >> 5), ex(c >> 10)}
			}
		}
		f.palette[0], f.palette[1] = palette, palette
		for y := 0; y < gbvp3Lines; y++ {
			if y == 144 && runReturn >= 0 {
				t.Fatalf("frame %d: field A ends inside a run", len(out))
			}
			if y == 144 {
				// FieldB: the stream goes on at the same offset past a hole.
				pos += (mbc1Skip(pos/0x4000, len(data)/0x4000+1) - pos/0x4000) * 0x4000
			}
			cycles := 0
			h := op(pos)
			pos++
			bank := false
			if h == 1 {
				pos = nextBank(pos)
				mapped(fmt.Sprintf("frame %d line %d", len(out), y), pos)
				h = op(pos)
				pos++
				st.bankLines++
				bank = true
				if h <= 1 {
					t.Fatalf("line %d: op %#x after a bank switch", y, h)
				}
			}
			switch h {
			case 0:
				for j := range slots[y] {
					slots[y][j] = rd(pos)
					pos++
				}
				st.rawLines++
				cycles = gbvp3CyclesRaw
			case gbvp3OpRun:
				if runReturn >= 0 {
					t.Fatalf("line %d: run inside a run", y)
				}
				addr := int(rd(pos)) | int(rd(pos+1))<<8
				runReturn = pos + 2
				pos = addr
				if k := gbvp3RunReturn - addr + 1; k < 1 || k > gbvp3RunMax || y%144+k > 143 {
					t.Fatalf("line %d: run of %d lines", y, k)
				}
				st.runLines++
				cycles = gbvp3CyclesRun
				if bank {
					cycles = gbvp3CyclesRunBank
				}
			case gbvp3OpReturn:
				if runReturn < 0 || bank {
					t.Fatalf("line %d: return outside a run", y)
				}
				pos, runReturn = runReturn, -1
				h = rd(pos)
				pos++
				n := patchN(y, h)
				if n > gbvp3MaxPatchesReturn {
					t.Fatalf("line %d: %d patches after a run", y, n)
				}
				st.patchLines[n]++
				cycles = gbvp3CyclesReturn + gbvp3CyclesPerHit*n
				pos = applyPatches(t, rd, pos, y, n, &slots[y])
			default:
				n := patchN(y, h)
				st.patchLines[n]++
				cycles = gbvp3CyclesPatch + gbvp3CyclesPerHit*n
				if bank {
					cycles = gbvp3CyclesBank + gbvp3CyclesPerHit*n
				}
				if pos >= 0x4000 {
					pos = applyPatches(t, rd, pos, y, n, &slots[y])
				}
			}
			if y%gbvp3LinesPerPage == 0 {
				cycles += gbvp3CyclesPage
			}
			st.maxCycles = max(st.maxCycles, cycles)
			if cycles > gbvp3CycleLimit {
				t.Fatalf("line %d: %d cycles, over the %d limit", y, cycles, gbvp3CycleLimit)
			}
			ly := y % 144
			for j := 0; j < 20; j++ {
				comb := int(slots[y][j]+uint8(ly)) & 0xFF
				copy(f.fields[y/144][ly][j*8:j*8+8], e.combos[comb*8:comb*8+8])
			}
		}
		if runReturn >= 0 {
			t.Fatalf("frame %d ends inside a run", len(out))
		}
		st.frames++
		out = append(out, f)
	}
	st.videoBytes = pos - videoStart

	// Audio: one chunk per GB frame, two GB frames per video frame shown.
	gbFrames := 0
	for _, f := range out {
		gbFrames += 2 * f.count
	}
	var levels []uint8
	ap := 0x4001
	for fr := 0; fr < gbFrames && ap < videoStart; fr++ {
		if ap&0x3fff+gbvp3ChunkBytes > 0x4000 {
			if ap = nextBank(ap); mbc1Unmapped(ap / 0x4000) {
				ap = nextBank(ap)
			}
		}
		if ap >= videoStart {
			break
		}
		var s [gbvp3ChunkSamples]uint8
		for i := 0; i < gbvp3ChunkSamples; i += 4 {
			lo, hi := rd(ap), rd(ap+1)
			ap += 2
			s[i], s[i+1], s[i+2], s[i+3] = lo&15, lo>>4, hi>>4, hi&15
		}
		for _, k := range s[:gbvp3FrameSamples] {
			if k >= gbvp3AudioLevels {
				t.Fatalf("audio level %d out of range", k)
			}
		}
		levels = append(levels, s[:gbvp3FrameSamples]...)
	}
	return out, levels, st
}

// timeline maps every video frame on screen (29.86 per second) to the index
// of the encoded frame shown.
func simTimeline(sim []simFrame) []int {
	var shown []int
	for i, f := range sim {
		for k := 0; k < f.count; k++ {
			shown = append(shown, i)
		}
	}
	return shown
}

func sourceTimeline(n int, fps float64) []int {
	mult := gbFPSConst / 2 / fps
	var shown []int
	track := 0.0
	for i := 0; i < n; i++ {
		track += mult
		l := int(track)
		track -= float64(l)
		for k := 0; k < l; k++ {
			shown = append(shown, i)
		}
	}
	return shown
}

// simPixel is what the eye sees at (x, y): both fields blended.
func simPixel(f *simFrame, x, y int) [3]int {
	a := f.palette[0][f.fields[0][y][x]]
	b := a
	if x+3 < 160 {
		b = f.palette[1][f.fields[1][y][x+3]]
	}
	return [3]int{(a[0] + b[0]) / 2, (a[1] + b[1]) / 2, (a[2] + b[2]) / 2}
}

// writeWAV writes levels (0..14 at 9198 Hz) as 16-bit mono at 44100 Hz with
// zero-order hold, close to how the Game Boy outputs them.
func writeWAV(path string, levels []uint8) error {
	const rate = 44100
	n := len(levels) * rate / audioRate
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	hdr := []any{
		[4]byte{'R', 'I', 'F', 'F'}, uint32(36 + n*2), [4]byte{'W', 'A', 'V', 'E'},
		[4]byte{'f', 'm', 't', ' '}, uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate * 2), uint16(2), uint16(16),
		[4]byte{'d', 'a', 't', 'a'}, uint32(n * 2),
	}
	for _, v := range hdr {
		binary.Write(w, binary.LittleEndian, v)
	}
	for i := 0; i < n; i++ {
		k := float64(levels[i*audioRate/rate])
		binary.Write(w, binary.LittleEndian, int16((k/14*2-1)*30000))
	}
	return w.Flush()
}

func writeWAVSource(path string, pcm []byte) error {
	// Full 8-bit source, scaled to the same 0..14 range but with fractional
	// levels kept: reuse the writer through a finer table.
	const rate = 44100
	n := len(pcm) / 2 * rate / audioRate
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	hdr := []any{
		[4]byte{'R', 'I', 'F', 'F'}, uint32(36 + n*2), [4]byte{'W', 'A', 'V', 'E'},
		[4]byte{'f', 'm', 't', ' '}, uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate * 2), uint16(2), uint16(16),
		[4]byte{'d', 'a', 't', 'a'}, uint32(n * 2),
	}
	for _, v := range hdr {
		binary.Write(w, binary.LittleEndian, v)
	}
	for i := 0; i < n; i++ {
		j := i * audioRate / rate
		v := (float64(pcm[2*j])+float64(pcm[2*j+1]))/2/128 - 1
		binary.Write(w, binary.LittleEndian, int16(v*30000))
	}
	return w.Flush()
}

// simQuality compares what the player shows against the source frames over
// time. A source frame is compared with the video frame on screen when it
// starts. Field 1 shows the source shifted right by 3 pixels.
type simQuality struct {
	psnrAvg, psnrA, psnrB float64 // average of both fields, field 0, field 1
	flicker               float64 // mean |A-B| per channel
	// ssim is the structural similarity of the luma of both fields blended,
	// over 8×8 windows (step 4): closer to what the eye notices than PSNR,
	// since errors on flat areas weigh more than in texture.
	ssim float64
}

// ssim8 adds up the SSIM of 8×8 windows of a and b (144×160 luma) and
// returns the sum and the number of windows.
func ssim8(a, b *[144][160]float64) (sum float64, n int) {
	const c1, c2 = (0.01 * 255) * (0.01 * 255), (0.03 * 255) * (0.03 * 255)
	for y := 0; y+8 <= 144; y += 4 {
		for x := 0; x+8 <= 160; x += 4 {
			var ma, mb, va, vb, cov float64
			for dy := 0; dy < 8; dy++ {
				for dx := 0; dx < 8; dx++ {
					ma += a[y+dy][x+dx]
					mb += b[y+dy][x+dx]
				}
			}
			ma, mb = ma/64, mb/64
			for dy := 0; dy < 8; dy++ {
				for dx := 0; dx < 8; dx++ {
					da, db := a[y+dy][x+dx]-ma, b[y+dy][x+dx]-mb
					va += da * da
					vb += db * db
					cov += da * db
				}
			}
			va, vb, cov = va/63, vb/63, cov/63
			sum += (2*ma*mb + c1) * (2*cov + c2) / ((ma*ma + mb*mb + c1) * (va + vb + c2))
			n++
		}
	}
	return sum, n
}

func measureSim(frames []image.Image, fps float64, sim []simFrame) simQuality {
	// Display timeline in video frames.
	var shown []int
	for i, f := range sim {
		for k := 0; k < f.count; k++ {
			shown = append(shown, i)
		}
	}
	mult := gbFPSConst / 2 / fps
	track, unit := 0.0, 0
	var seA, seB, seAvg, flick float64
	var n float64
	var ssimSum float64
	var ssimN int
	var ya, yb [144][160]float64
	luma := func(c [3]float64) float64 { return 0.299*c[0] + 0.587*c[1] + 0.114*c[2] }
	for _, src := range frames {
		track += mult
		l := int(track)
		if l == 0 {
			continue
		}
		track -= float64(l)
		if unit >= len(shown) {
			break
		}
		f := &sim[shown[unit]]
		unit += l
		b := src.Bounds()
		for y := 0; y < 144; y++ {
			for x := 0; x < 160; x++ {
				r, g, bl, _ := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
				s := [3]int{int(r >> 8), int(g >> 8), int(bl >> 8)}
				ca := f.palette[0][f.fields[0][y][x]]
				var cb [3]int
				if x+3 < 160 {
					cb = f.palette[1][f.fields[1][y][x+3]]
				} else {
					cb = ca
				}
				var src, blend [3]float64
				for c := 0; c < 3; c++ {
					da := float64(ca[c] - s[c])
					db := float64(cb[c] - s[c])
					dv := float64(ca[c]+cb[c])/2 - float64(s[c])
					seA += da * da
					seB += db * db
					seAvg += dv * dv
					flick += math.Abs(float64(ca[c] - cb[c]))
					src[c], blend[c] = float64(s[c]), float64(ca[c]+cb[c])/2
				}
				ya[y][x], yb[y][x] = luma(src), luma(blend)
				n += 3
			}
		}
		s, k := ssim8(&ya, &yb)
		ssimSum, ssimN = ssimSum+s, ssimN+k
	}
	psnr := func(se float64) float64 { return 10 * math.Log10(255*255/(se/n)) }
	return simQuality{psnr(seAvg), psnr(seA), psnr(seB), flick / n, ssimSum / float64(max(ssimN, 1))}
}
