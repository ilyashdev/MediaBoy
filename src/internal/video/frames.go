package video

import (
	"context"
	"image"
	"runtime"
	"sync/atomic"

	"MediaBoy/internal/core"
	"MediaBoy/internal/imaging"
	"MediaBoy/internal/safe"
)

// FPSPresets are the frame rates a video is decoded at.
var FPSPresets = []int{12, 15, 24, 30, 60}

// CapFPS is the highest preset that does not exceed the source frame rate.
func CapFPS(sourceFPS float64) int {
	best := FPSPresets[0]
	for _, p := range FPSPresets {
		if float64(p) <= sourceFPS+0.5 {
			best = p
		}
	}
	return best
}

// ProcessFrames runs the GB pipeline on every frame in parallel.
func ProcessFrames(ctx context.Context, src []*image.RGBA, cfg core.ConvertConfig, progress func(done, total int)) ([]image.Image, error) {
	n := len(src)
	out := make([]image.Image, n)
	var done int64
	var firstErr atomic.Value
	sem := make(chan struct{}, runtime.NumCPU())
	var wg safe.Group
	for i := range src {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			p, err := imaging.RunGBPipelineFrame(src[i], cfg)
			if err != nil {
				firstErr.Store(err)
				return
			}
			out[i] = p
			progress(int(atomic.AddInt64(&done, 1)), n)
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e := firstErr.Load(); e != nil {
		return nil, e.(error)
	}
	return out, nil
}
