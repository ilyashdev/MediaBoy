package music

import (
	"math"
	"math/cmplx"
	"math/rand"
	"testing"
)

// TestPCMLevels checks the level table against the mixer: each level's two
// sides add up to it and stay within 5 of each other.
func TestPCMLevels(t *testing.T) {
	side := func(vol, mask uint8) int {
		n := 0
		for ; mask != 0; mask >>= 1 {
			n += int(mask & 1)
		}
		return (int(vol) + 1) * n
	}
	for lvl, p := range pcmLevels {
		if p.nr50&0x88 != 0 {
			t.Errorf("level %d: NR50 %#x sets a Vin bit", lvl, p.nr50)
		}
		l, r := side(p.nr50>>4, p.nr51>>4), side(p.nr50&7, p.nr51&15)
		if l+r != lvl || abs(l-r) > 5 {
			t.Errorf("level %d: sides %d + %d", lvl, l, r)
		}
	}
}

func TestPCMTimer(t *testing.T) {
	for _, r := range PCMRatesCGB {
		if _, a := pcmTimer(r, false); abs(a-r) > 1 {
			t.Errorf("CGB %d Hz plays at %d", r, a)
		}
	}
	for _, r := range PCMRatesDMG {
		if _, a := pcmTimer(r, true); a != r {
			t.Errorf("DMG %d Hz plays at %d", r, a)
		}
	}
}

func TestPCMBanks(t *testing.T) {
	levels := make([]uint8, 2*pcmBankData+5)
	banks := streamBanks(levels, pcmNextBank, pcmEnd)
	if len(banks) != 3 || banks[0][pcmBankData] != pcmNextBank || banks[1][pcmBankData] != pcmNextBank || banks[2][5] != pcmEnd {
		t.Fatalf("bad layout")
	}
	if got := pcmBanksFor(float64(pcmBankData*2)/8192, 8192, true); got != 2 {
		t.Errorf("pcmBanksFor: %d banks", got)
	}
}

// TestDPCMRoundTrip decodes the banks of a DPCM track the way the player
// does and checks it gets the encoder's levels, in range, and that a track
// cut to fewer banks still ends properly.
func TestDPCMRoundTrip(t *testing.T) {
	const rate = 16384.0
	x := testMusic(rate)
	conditionPCM(x, rate, 80)
	nibbles := quantizeDPCM(pcmTargets(x, rate), rate)
	want := dpcmLevels(nibbles)
	for _, maxBanks := range []int{100, 2} {
		banks, samples := encodeTrack(x, rate, true, maxBanks)
		if len(banks) > maxBanks {
			t.Fatalf("%d banks for a cap of %d", len(banks), maxBanks)
		}
		lvl, n := 0, 0
	play:
		for _, b := range banks {
			for i, v := range b {
				switch {
				case v == dpcmNext:
					if i != pcmBankData {
						t.Fatalf("next-bank code at %#x", i)
					}
					continue play
				case v == dpcmEnd:
					break play
				case v>>4 == 8:
					t.Fatalf("byte %#x would be read as a code", v)
				}
				for _, nb := range []uint8{v >> 4, v & 15} {
					if n >= samples {
						break
					}
					lvl += dpcmSteps[nb]
					if lvl < 0 || lvl > pcmTopLevel {
						t.Fatalf("level %d at sample %d", lvl, n)
					}
					if maxBanks == 100 && uint8(lvl) != want[n] {
						t.Fatalf("sample %d: level %d, encoder had %d", n, lvl, want[n])
					}
					n++
				}
			}
		}
		if n != samples {
			t.Fatalf("cap %d: played %d samples of %d", maxBanks, n, samples)
		}
		if lvl > 2 {
			t.Errorf("cap %d: track ends at level %d, not silence", maxBanks, lvl)
		}
	}
}

// testMusic is a few seconds of something music-like: chords, a melody,
// noise bursts, quiet and loud parts.
func testMusic(rate float64) []float64 {
	rng := rand.New(rand.NewSource(1))
	n := int(4 * rate)
	x := make([]float64, n)
	for i := range x {
		ts := float64(i) / rate
		env := 0.3 + 0.7*math.Abs(math.Sin(ts*0.8))
		v := 0.3*math.Sin(2*math.Pi*110*ts) + 0.2*math.Sin(2*math.Pi*277*ts) + 0.15*math.Sin(2*math.Pi*330*ts)
		v += 0.2 * math.Sin(2*math.Pi*(880+200*math.Sin(ts*3))*ts)
		if int(ts*4)%2 == 0 {
			v += 0.1 * rng.NormFloat64() * math.Exp(-math.Mod(ts, 0.25)*20)
		}
		x[i] = v * env * 0.8
	}
	return x
}

// weightedSNR is the A-weighted signal to noise ratio of y (levels) against
// the ideal x scaled to the same range, in dB, over 4096-sample blocks.
func weightedSNR(x []float64, y []float64, rate float64) float64 {
	const n = 4096
	var sig, noise float64
	for off := 0; off+n <= len(x); off += n {
		a := make([]complex128, n)
		b := make([]complex128, n)
		for i := range n {
			w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/n)
			a[i] = complex(x[off+i]*w, 0)
			b[i] = complex((y[off+i]-x[off+i])*w, 0)
		}
		fa, fb := fft(a), fft(b)
		for k := 1; k < n/2; k++ {
			wt := aWeight(float64(k) * rate / n)
			wt *= wt
			sig += wt * cmplx.Abs(fa[k]) * cmplx.Abs(fa[k])
			noise += wt * cmplx.Abs(fb[k]) * cmplx.Abs(fb[k])
		}
	}
	return 10 * math.Log10(sig/noise)
}

func fft(a []complex128) []complex128 {
	n := len(a)
	if n == 1 {
		return []complex128{a[0]}
	}
	even, odd := make([]complex128, n/2), make([]complex128, n/2)
	for i := range n / 2 {
		even[i], odd[i] = a[2*i], a[2*i+1]
	}
	fe, fo := fft(even), fft(odd)
	out := make([]complex128, n)
	for k := range n / 2 {
		t := cmplx.Exp(complex(0, -2*math.Pi*float64(k)/float64(n))) * fo[k]
		out[k], out[k+n/2] = fe[k]+t, fe[k]-t
	}
	return out
}

// TestPCMQuality compares the speaker output of the old 3-bit stereo
// encoding with 15 levels (as in video ROMs) and the 43-level stream, with
// and without noise shaping. Only logs the numbers, apart from requiring the
// new encoding to win.
func TestPCMQuality(t *testing.T) {
	for _, rate := range []float64{8192, 16384} {
		x := testMusic(rate)
		conditionPCM(x, rate, 80)
		levels := func(steps int, y func(i int) float64) []float64 {
			out := make([]float64, len(x))
			for i := range x {
				// back to [-1, 1] on the same scale as x
				out[i] = y(i)/float64(steps)*2 - 1
			}
			return out
		}
		flat := func(steps int) []float64 {
			return levels(steps, func(i int) float64 {
				return math.Round((x[i] + 1) / 2 * float64(steps))
			})
		}
		ramp := int(pcmFade * rate)
		shaped := quantizePCM(pcmTargets(x, rate), rate)[ramp:]
		dpcm := dpcmLevels(quantizeDPCM(pcmTargets(x, rate), rate))[ramp:]
		snrOld := weightedSNR(x, flat(7), rate)
		snr15 := weightedSNR(x, flat(14), rate)
		snr43 := weightedSNR(x, flat(pcmTopLevel), rate)
		snrNew := weightedSNR(x, levels(pcmTopLevel, func(i int) float64 { return float64(shaped[i]) }), rate)
		snrDPCM := weightedSNR(x, levels(pcmTopLevel, func(i int) float64 { return float64(dpcm[i]) }), rate)
		t.Logf("%5.0f Hz: A-weighted SNR  8 levels %.1f dB, 15 levels %.1f dB, 43 levels %.1f dB, 43 shaped %.1f dB, DPCM %.1f dB",
			rate, snrOld, snr15, snr43, snrNew, snrDPCM)
		if snrNew < snr43 || snr43 < snr15+6 || snrDPCM < snr15+5 {
			t.Errorf("%v Hz: the new encoding does not win", rate)
		}
	}
}
