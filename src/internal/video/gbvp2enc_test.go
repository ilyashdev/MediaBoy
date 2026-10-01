package video

import (
	"math/rand"
	"testing"
)

// Reference copies of the original per-pixel scoring, kept to check that the
// distance-table version picks the same combination with the same score.

func refRoundedColorDiff(r1, g1, b1, r2, g2, b2 int) int {
	r2 &= 0xF8
	r2 |= r2 >> 5
	g2 &= 0xF8
	g2 |= g2 >> 5
	b2 &= 0xF8
	b2 |= b2 >> 5
	return abs8(r1-r2) + abs8(g1-g2) + abs8(b1-b2)
}

func refScore(e *gbvp2enc, r, g, b []uint8, palette []encColor, combination int) uint32 {
	var score uint32
	base := combination * 8
	for x := 0; x < 8; x++ {
		p := palette[e.combos[base+x]]
		score += uint32(refRoundedColorDiff(int(r[x]), int(g[x]), int(b[x]), int(p.r), int(p.g), int(p.b)))
	}
	return score
}

func refBest(e *gbvp2enc, r, g, b []uint8, palette []encColor) (int, uint32) {
	best := ^uint32(0)
	bestIndex := 0
	for c := 0; c < 256; c++ {
		if s := refScore(e, r, g, b, palette, c); s < best {
			best, bestIndex = s, c
		}
	}
	return bestIndex, best
}

func TestBestCombinationMatchesReference(t *testing.T) {
	e := newGBVP2Enc(0)
	rng := rand.New(rand.NewSource(1))
	r, g, b := make([]uint8, 8), make([]uint8, 8), make([]uint8, 8)
	palette := make([]encColor, gbPaletteLen)
	for iter := 0; iter < 200000; iter++ {
		// Few distinct values make ties and duplicate palette entries common.
		levels := []int{2, 4, 256}[iter%3]
		v := func() uint8 { return uint8(rng.Intn(levels) * (255 / max(levels-1, 1))) }
		for i := range palette {
			palette[i] = encColor{v(), v(), v()}
		}
		if iter%5 == 0 {
			for i := range palette {
				palette[i] = palette[0]
			}
		}
		for x := 0; x < 8; x++ {
			r[x], g[x], b[x] = v(), v(), v()
		}

		rp := roundPalette(palette)
		var d pixelDists
		d.fill(r, g, b, &rp)
		gotI, gotS := e.bestCombinationForPixels(&d)
		wantI, wantS := refBest(e, r, g, b, palette)
		if gotI != wantI || gotS != wantS {
			t.Fatalf("iter %d: got (%d,%d) want (%d,%d)", iter, gotI, gotS, wantI, wantS)
		}
		c := rng.Intn(256)
		want := refScore(e, r, g, b, palette, c)
		if got := e.scoreForCombination(&d, c); got != want {
			t.Fatalf("iter %d combo %d: score %d want %d", iter, c, got, want)
		}
		if got := e.scoreDirect(r, g, b, &rp, c); got != want {
			t.Fatalf("iter %d combo %d: direct score %d want %d", iter, c, got, want)
		}
	}
}

func TestRound555MatchesReference(t *testing.T) {
	for v := 0; v < 256; v++ {
		want := v & 0xF8
		want |= want >> 5
		if got := round555(uint8(v)); got != want {
			t.Fatalf("round555(%d) = %d, want %d", v, got, want)
		}
	}
}
