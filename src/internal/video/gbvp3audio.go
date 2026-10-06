package video

import (
	"math"
	"sort"
)

// GBVP3 audio is mono. The player writes NR50 once per scanline (9198 Hz) with
// every channel's DAC on and idle, so the output follows the master volume.
// The speaker sums both sides, (l+1)+(r+1), and setting l = ceil(k/2),
// r = floor(k/2) gives 15 evenly spaced levels k = 0..14 instead of 8.
//
// Samples are 4-bit levels, two per byte. A chunk holds one GB frame: 154
// samples plus 2 padding, 78 bytes. video3.asm unpacks it in VBlank in
// 4-sample groups from byte pairs (e, d): low(e), high(e), high(d), low(d).
const (
	gbvp3AudioLevels  = 15
	gbvp3FrameSamples = 154
	gbvp3ChunkSamples = 156
	gbvp3ChunkBytes   = gbvp3ChunkSamples / 2
)

// gbvp3Silence is a byte of two mid-level samples.
const gbvp3Silence = gbvp3AudioLevels / 2 * 0x11

// gbvp3NR50 is the NR50 value for level k.
func gbvp3NR50(k int) uint8 { return uint8((k+1)/2<<4 | k/2) }

// gbvp3Audio turns u8 stereo PCM at 9198 Hz into packed mono chunks.
func gbvp3Audio(pcm []byte) []byte {
	levels := gbvp3Quantize(gbvp3Condition(pcm))
	nChunks := (len(levels) + gbvp3FrameSamples - 1) / gbvp3FrameSamples
	out := make([]byte, 0, nChunks*gbvp3ChunkBytes)
	var s [gbvp3ChunkSamples]uint8
	for c := 0; c < nChunks; c++ {
		for i := range s {
			k := c*gbvp3FrameSamples + min(i, gbvp3FrameSamples-1)
			s[i] = gbvp3AudioLevels / 2
			if k < len(levels) {
				s[i] = levels[k]
			}
		}
		for i := 0; i < gbvp3ChunkSamples; i += 4 {
			out = append(out, s[i]|s[i+1]<<4, s[i+3]|s[i+2]<<4)
		}
	}
	return out
}

// gbvp3Condition downmixes to mono, removes DC and compresses the dynamics so
// quiet passages survive 15 levels. The result is in [-1, 1].
func gbvp3Condition(pcm []byte) []float64 {
	n := len(pcm) / 2
	x := make([]float64, n)
	var dc float64
	const dcPole = 1 - 2*math.Pi*20/audioRate // ~20 Hz high-pass
	for i := range x {
		v := (float64(pcm[2*i])+float64(pcm[2*i+1]))/2/128 - 1
		dc = dcPole*dc + (1-dcPole)*v
		x[i] = v - dc
	}

	// Feed-forward compressor: 3:1 above -18 dBFS, 5 ms attack, 150 ms release.
	const (
		threshold = 0.125
		ratio     = 3.0
	)
	att := math.Exp(-1 / (0.005 * audioRate))
	rel := math.Exp(-1 / (0.150 * audioRate))
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

	// Make-up gain: the 99.9th percentile peak lands at 0.9, then a soft clip
	// rounds off what is left above it.
	if n == 0 {
		return x
	}
	abs := make([]float64, n)
	for i, v := range x {
		abs[i] = math.Abs(v)
	}
	sort.Float64s(abs)
	peak := abs[min(n-1, n*999/1000)]
	if peak < 1e-4 {
		return x
	}
	gain := 0.9 / peak
	for i, v := range x {
		x[i] = math.Tanh(v * gain)
	}
	return x
}

// gbvp3Quantize maps [-1, 1] to levels 0..14 with first-order error feedback,
// which moves quantisation noise away from the low and mid frequencies.
func gbvp3Quantize(x []float64) []uint8 {
	const feedback = 0.85
	out := make([]uint8, len(x))
	var err float64
	for i, v := range x {
		u := (v+1)/2*(gbvp3AudioLevels-1) - feedback*err
		q := math.Round(u)
		q = max(0, min(gbvp3AudioLevels-1, q))
		err = q - u
		out[i] = uint8(q)
	}
	return out
}
