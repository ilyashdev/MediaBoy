package music

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"

	"MediaBoy/internal/ffmpeg"
)

// PCM playback. The player keeps every channel DAC on and every channel idle:
// an idle channel outputs digital 0, which its DAC turns into a constant
// "analog 1", and the mixer adds the channels NR51 routes to each side, scaled
// by NR50 (volume+1). Writing both registers per sample makes a side's level
// (volume+1) × (channels routed to it), so the speaker, which adds both sides,
// reaches every level from 0 to 50. Up to pcmTopLevel the two sides stay
// within 5 of each other, so headphones get nearly the same signal in both
// ears; above it they drift apart, so the stream stops there: 43 even levels
// where NR50 alone gives 8 per side.
//
// Two stream formats. Plain: a sample is one byte, its level. DPCM (the
// default, half the size): a byte holds two samples, high nibble first, each
// a step from dpcmSteps added to the last level. Each track's bytes fill
// whole banks from $4000; the last byte of a full bank says "next bank" and
// the track ends with an end byte. Plain levels stay below $F0 and DPCM never
// uses nibble 8, so the codes cannot be data.
const (
	pcmTopLevel = 42
	pcmNextBank = 0xF0
	pcmEnd      = 0xFF
	dpcmNext    = 0x80
	dpcmEnd     = 0x8F
	// pcmBankData is the bytes a bank holds besides its next-bank byte.
	pcmBankData = 0x4000 - 1
)

// dpcmSteps is the level change of each nibble; nibble 8 is reserved. The
// steps were picked by A-weighted SNR on real music: at 16384 Hz DPCM loses
// about 2.4 dB to plain bytes, at half the size.
var dpcmSteps = [16]int{0, 1, -1, 2, -2, 3, -3, 4, 0, -4, 6, -6, 9, -9, 14, -14}

// pcmLevel is how the player sets one output level.
type pcmLevel struct{ nr50, nr51 uint8 }

// pcmLevels maps each level 0..pcmTopLevel to NR50 and NR51, the two sides
// as close as they get.
var pcmLevels = func() (t [pcmTopLevel + 1]pcmLevel) {
	type side struct{ vol, chans int }
	sides := map[int]side{0: {0, 0}}
	for c := 4; c >= 1; c-- {
		for v := 7; v >= 0; v-- {
			sides[(v+1)*c] = side{v, c}
		}
	}
	lefts := make([]int, 0, len(sides))
	for l := range sides {
		lefts = append(lefts, l)
	}
	sort.Ints(lefts)
	for lvl := range t {
		best := -1
		for _, l := range lefts {
			sl := sides[l]
			sr, ok := sides[lvl-l]
			if !ok || (best >= 0 && abs(l-(lvl-l)) >= abs(best-(lvl-best))) {
				continue
			}
			best = l
			t[lvl] = pcmLevel{
				nr50: uint8(sl.vol<<4 | sr.vol),
				nr51: uint8(((1<<sl.chans)-1)<<4 | (1<<sr.chans - 1)),
			}
		}
	}
	return t
}()

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// PCM sample rates: the timer divides 262144 Hz (524288 Hz in double speed).
// The DMG's single-speed CPU has time for 8192 Hz at most.
var (
	PCMRatesCGB = []int{8192, 10923, 13107, 16384}
	PCMRatesDMG = []int{4096, 8192}
)

// DefaultPCMRate is the rate for a target when none was chosen.
func DefaultPCMRate(dmg bool) int {
	if dmg {
		return 8192
	}
	return 16384
}

// pcmTimer returns the TMA value for rate and the rate it really gives.
func pcmTimer(rate int, dmg bool) (tma, actual int) {
	base := 524288
	if dmg {
		base = 262144
	}
	div := max(1, min(255, int(math.Round(float64(base)/float64(rate)))))
	return 256 - div, base / div
}

// pcmRate is the rate actually played for the configured one: one of the
// target's rates (the default when the configured one is not), as the timer
// gives it.
func pcmRate(rate int, dmg bool) int {
	rates := PCMRatesCGB
	if dmg {
		rates = PCMRatesDMG
	}
	ok := false
	for _, r := range rates {
		ok = ok || r == rate
	}
	if !ok {
		rate = DefaultPCMRate(dmg)
	}
	_, actual := pcmTimer(rate, dmg)
	return actual
}

// decodeTrack decodes path at rate and conditions it for the levels.
func decodeTrack(path string, rate int, dmg bool) ([]float64, error) {
	raw, err := ffmpeg.DecodeMonoS16HQ(path, rate)
	if err != nil {
		return nil, err
	}
	if len(raw) < 2 {
		return nil, fmt.Errorf("no audio decoded from %s", path)
	}
	x := make([]float64, len(raw)/2)
	for i := range x {
		x[i] = float64(int16(binary.LittleEndian.Uint16(raw[2*i:]))) / 32768
	}
	// Bass is cut where the hardware loses it anyway, so it does not take
	// the levels' room: the GBC's output high-pass is around 700 Hz
	// (Pan Docs: charge factor 0.998943 per clock), the DMG's speaker is tiny.
	hp := 300.0
	if dmg {
		hp = 150
	}
	conditionPCM(x, float64(rate), hp)
	return x, nil
}

// pcmCapacity is how many samples of a track fit into banks banks, the
// ramps included.
func pcmCapacity(banks int, dpcm bool) int {
	n := banks*pcmBankData - 1 // the end byte
	if dpcm {
		n *= 2
	}
	return max(0, n)
}

// pcmSamples is how many samples a track of n source samples plays.
func pcmSamples(n int, rate float64) int { return n + 2*int(pcmFade*rate) }

// encodeTrack turns conditioned x into whole banks of stream, cut (with a
// fade) to at most maxBanks. It returns the banks and the samples played.
func encodeTrack(x []float64, rate float64, dpcm bool, maxBanks int) ([][]byte, int) {
	if room := pcmCapacity(maxBanks, dpcm) - 2*int(pcmFade*rate); len(x) > room {
		x = x[:max(0, room)]
	}
	t := pcmTargets(x, rate)
	if dpcm {
		return streamBanks(dpcmStream(quantizeDPCM(t, rate)), dpcmNext, dpcmEnd), len(t)
	}
	return streamBanks(quantizePCM(t, rate), pcmNextBank, pcmEnd), len(t)
}

// conditionPCM prepares x (in [-1, 1]) for 43 levels in place: a high-pass
// at hp Hz, a firm compressor, make-up gain that puts the loudest 1 % into a
// soft clip. With only 43 levels and the console's filter, loudness is what
// is heard: this is about 6 dB louder through a GBC than leaving the peaks
// alone, for a little clipping on the loudest moments.
func conditionPCM(x []float64, rate, hp float64) {
	highPass(x, rate, hp)

	// Feed-forward compressor: 4:1 above -26 dBFS, 5 ms attack, 120 ms release.
	const threshold, ratio = 0.05, 4.0
	att := math.Exp(-1 / (0.005 * rate))
	rel := math.Exp(-1 / (0.120 * rate))
	var env float64
	for i, v := range x {
		a := math.Abs(v)
		if a > env {
			env = att*env + (1-att)*a
		} else {
			env = rel*env + (1-rel)*a
		}
		if env > threshold {
			x[i] = v * math.Pow(env/threshold, 1/ratio-1)
		}
	}

	n := len(x)
	if n == 0 {
		return
	}
	abs := make([]float64, n)
	for i, v := range x {
		abs[i] = math.Abs(v)
	}
	sort.Float64s(abs)
	peak := abs[min(n-1, n*99/100)]
	if peak < 1e-4 {
		return
	}
	gain := 1.3 / peak
	for i, v := range x {
		x[i] = math.Tanh(v * gain)
	}
}

// highPass runs a 2nd-order Butterworth high-pass at fc over x in place.
func highPass(x []float64, rate, fc float64) {
	w := math.Tan(math.Pi * fc / rate)
	k := 1 / (1 + math.Sqrt2*w + w*w)
	b0, b1, b2 := k, -2*k, k
	a1, a2 := 2*(w*w-1)*k, (1-math.Sqrt2*w+w*w)*k
	var x1, x2, y1, y2 float64
	for i, v := range x {
		y := b0*v + b1*x1 + b2*x2 - a1*y1 - a2*y2
		x2, x1, y2, y1 = x1, v, y1, y
		x[i] = y
	}
}

// pcmFade is how long a track ramps in from silence and back out, so that
// starting and stopping make no click.
const pcmFade = 0.03

// pcmTargets is x in levels: a ramp up from level 0, the signal faded in and
// out over the same length, a ramp back down.
func pcmTargets(x []float64, rate float64) []float64 {
	ramp := int(pcmFade * rate)
	mid := pcmTopLevel / 2.0
	out := make([]float64, 0, len(x)+2*ramp)
	for i := range ramp {
		out = append(out, mid*(1-math.Cos(math.Pi*float64(i)/float64(ramp)))/2)
	}
	for i, v := range x {
		g := 1.0
		if d := min(i, len(x)-1-i); d < ramp {
			g = (1 - math.Cos(math.Pi*float64(d)/float64(ramp))) / 2
		}
		out = append(out, mid+v*g*mid)
	}
	for i := range ramp {
		out = append(out, mid*(1+math.Cos(math.Pi*float64(i+1)/float64(ramp)))/2)
	}
	return out
}

// shaper feeds the quantisation error back through a filter fitted to the
// ear's sensitivity at the rate (see shapingFilter), so the noise moves to
// where it is heard least.
type shaper struct{ h, errs []float64 }

func newShaper(rate float64) *shaper {
	h := shapingFilter(rate)
	return &shaper{h, make([]float64, len(h))}
}

// target is t with the fed back error.
func (s *shaper) target(t float64) float64 {
	for k, c := range s.h {
		t += c * s.errs[k]
	}
	return t
}

// put records the error of level q against shaped target u.
func (s *shaper) put(q, u float64) {
	copy(s.errs[1:], s.errs)
	s.errs[0] = max(-1, min(1, q-u)) // an overload must not run away
}

// quantizePCM maps targets to levels 0..pcmTopLevel, noise-shaped.
func quantizePCM(t []float64, rate float64) []uint8 {
	sh := newShaper(rate)
	out := make([]uint8, len(t))
	for i, v := range t {
		u := sh.target(v)
		q := max(0, min(pcmTopLevel, math.Round(u)))
		sh.put(q, u)
		out[i] = uint8(q)
	}
	return out
}

// quantizeDPCM maps targets to DPCM nibbles from level 0, noise-shaped.
// Each step looks one sample ahead, so a jump the steps cannot make at once
// is started early.
func quantizeDPCM(t []float64, rate float64) []uint8 {
	sh := newShaper(rate)
	out := make([]uint8, len(t))
	lvl := 0
	for i, v := range t {
		u := sh.target(v)
		next := u
		if i+1 < len(t) {
			next = t[i+1]
		}
		best, bestCost := 0, math.MaxFloat64
		for n, d := range dpcmSteps {
			l := lvl + d
			if n == 8 || l < 0 || l > pcmTopLevel {
				continue
			}
			e := float64(l) - u
			cost := e * e
			if cost >= bestCost {
				continue
			}
			ahead := math.MaxFloat64
			for m, d2 := range dpcmSteps {
				if l2 := l + d2; m != 8 && l2 >= 0 && l2 <= pcmTopLevel {
					e2 := float64(l2) - next
					ahead = min(ahead, e2*e2)
				}
			}
			if cost += ahead; cost < bestCost {
				best, bestCost = n, cost
			}
		}
		lvl += dpcmSteps[best]
		sh.put(float64(lvl), u)
		out[i] = uint8(best)
	}
	return out
}

// dpcmLevels decodes nibbles back to levels, as the player does.
func dpcmLevels(nibbles []uint8) []uint8 {
	out := make([]uint8, len(nibbles))
	lvl := 0
	for i, n := range nibbles {
		lvl += dpcmSteps[n]
		out[i] = uint8(lvl)
	}
	return out
}

// dpcmStream packs nibbles two to a byte, high first.
func dpcmStream(nibbles []uint8) []byte {
	out := make([]byte, (len(nibbles)+1)/2)
	for i, n := range nibbles {
		out[i/2] |= n << (4 * (1 - i%2))
	}
	return out
}

// shapingFilter returns the error feedback taps h for rate: the noise comes
// out shaped by 1 + h1 z^-1 + ..., and the taps minimise its A-weighted
// power. That is linear prediction on the weighting's spectrum: the
// autocorrelation of A-weighting², then Levinson-Durbin. The taps are
// shrunk a little (bandwidth expansion) so the shaping cannot get too steep
// for 43 levels.
func shapingFilter(rate float64) []float64 {
	const order, bins, shrink = 6, 2048, 0.9
	var r [order + 1]float64
	for b := range bins {
		f := (float64(b) + 0.5) / bins * rate / 2
		w := aWeight(f)
		w *= w
		for k := range r {
			r[k] += w * math.Cos(2*math.Pi*f*float64(k)/rate)
		}
	}
	a := levinson(r[:])
	g := 1.0
	for k := range a {
		g *= shrink
		a[k] *= g
	}
	return a
}

// aWeight is the A-weighting gain at f Hz (linear, 1 at 1 kHz).
func aWeight(f float64) float64 {
	f2 := f * f
	ra := 12194.0 * 12194 * f2 * f2 /
		((f2 + 20.6*20.6) * math.Sqrt((f2+107.7*107.7)*(f2+737.9*737.9)) * (f2 + 12194.0*12194))
	return ra * 1.2589 // +2.0 dB
}

// levinson solves the normal equations for autocorrelation r and returns
// the prediction error filter's taps a1..ap (a0 = 1).
func levinson(r []float64) []float64 {
	p := len(r) - 1
	a := make([]float64, p+1)
	a[0] = 1
	e := r[0]
	for i := 1; i <= p; i++ {
		acc := r[i]
		for j := 1; j < i; j++ {
			acc += a[j] * r[i-j]
		}
		k := -acc / e
		prev := append([]float64(nil), a...)
		for j := 1; j < i; j++ {
			a[j] = prev[j] + k*prev[i-j]
		}
		a[i] = k
		e *= 1 - k*k
	}
	return a[1:]
}

// streamBanks lays out a track's stream as whole banks.
func streamBanks(stream []byte, next, end byte) [][]byte {
	var banks [][]byte
	for len(stream) > pcmBankData {
		b := make([]byte, 0x4000)
		copy(b, stream[:pcmBankData])
		b[pcmBankData] = next
		banks = append(banks, b)
		stream = stream[pcmBankData:]
	}
	b := make([]byte, 0x4000)
	for i := range b {
		b[i] = 0xFF
	}
	copy(b, stream)
	b[len(stream)] = end
	return append(banks, b)
}

// pcmBanksFor is how many banks a track of seconds takes.
func pcmBanksFor(seconds float64, rate int, dpcm bool) int {
	n := pcmSamples(int(seconds*float64(rate)), float64(rate))
	if dpcm {
		n = (n + 1) / 2
	}
	return n/pcmBankData + 1
}
