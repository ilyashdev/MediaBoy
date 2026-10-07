package video

import (
	"context"
	"testing"
)

// Temporary probe (to be deleted): part lengths of BuildParts at 1 MB.
func TestZZPartsProbe(t *testing.T) {
	frames, audio, _, q := clipEnv(t)
	frames = frames[:min(len(frames), 24*40)]
	parts, err := BuildParts(context.Background(), frames, audio, clipFPS, q, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range parts {
		t.Logf("part %d: first %d, frames %d (%.1f s), used %d KB, rom %d KB, banks %d",
			i+1, p.FirstFrame, p.FramesUsed, float64(p.FramesUsed)/clipFPS, p.Used>>10, len(p.ROM)>>10, p.Banks)
	}
}
