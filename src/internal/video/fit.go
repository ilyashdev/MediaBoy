package video

import (
	"context"
	"errors"
	"fmt"
	"image"
	"sort"
)

// audioLen is the length of the PCM (stereo u8) that plays alongside the
// first n frames at fps.
func audioLen(audio []byte, n int, fps float64) int {
	return min(int(float64(n)/fps*audioRate)*2, len(audio))
}

// FitProgress reports a Trim/Split encode: each part may take several encode
// passes, each restarting at the part's first frame. Done and Total count
// frames of the whole clip.
type FitProgress func(part, pass, done, total int)

// BuildTrimmed encodes the longest start of the clip, video and audio cut
// together, that fits into maxMB. A clip that fits is encoded exactly as by
// BuildROM. FramesTotal is the whole clip, so Truncated reports a trim.
func BuildTrimmed(ctx context.Context, frames []image.Image, audio []byte, fps float64, quality, maxMB int, progress FitProgress) (Result, error) {
	return buildTrimmed(ctx, frames, audio, fps, quality, maxMB, func(pass, done int) {
		if progress != nil {
			progress(1, pass, done, len(frames))
		}
	})
}

func buildTrimmed(ctx context.Context, frames []image.Image, audio []byte, fps float64, quality, maxMB int, report func(pass, done int)) (Result, error) {
	cache := NewPaletteCache()
	pass := 0
	progress := func() func(done, total int) {
		pass++
		p := pass
		return func(done, _ int) { report(p, done) }
	}
	build := func(fr []image.Image, au []byte) (Result, error) {
		return BuildROM(ctx, fr, au, fps, quality, maxMB, cache, progress())
	}

	// No start longer than the one whose audio alone fills the ROM can fit;
	// an unlimited encode of that start tells where the trim falls.
	banks := BanksFromMB(maxMB)
	n := sort.Search(len(frames), func(n int) bool { return audioVideoBank(n+1, fps) >= banks-1 })
	if n == 0 {
		return Result{}, fmt.Errorf("%w (%d MB)", ErrAudioOverflow, maxMB)
	}
	est, err := estimate(ctx, frames[:n], audio[:audioLen(audio, n, fps)], fps, quality, cache, progress())
	if err != nil {
		return Result{}, err
	}
	n = est.TrimFramesFor(maxMB)

	// The estimate is exact; shrinking on a surprise is only a safeguard.
	// Every truncated attempt keeps fewer frames than it was given, so this
	// terminates.
	for n > 0 {
		res, err := build(frames[:n], audio[:audioLen(audio, n, fps)])
		switch {
		case errors.Is(err, ErrAudioOverflow):
			n /= 2
		case err != nil:
			return res, err
		case res.Truncated():
			n = res.FramesUsed
		default:
			res.FramesTotal = len(frames)
			return res, nil
		}
	}
	return Result{}, fmt.Errorf("not even one frame fits into %d MB", maxMB)
}

// Part is one ROM of a clip split by BuildParts.
type Part struct {
	Result
	FirstFrame int
}

// BuildParts splits the clip into consecutive ROMs that each fit into maxMB,
// every part carrying its own stretch of the audio.
func BuildParts(ctx context.Context, frames []image.Image, audio []byte, fps float64, quality, maxMB int, progress FitProgress) ([]Part, error) {
	var parts []Part
	for start := 0; start < len(frames); {
		a := audioLen(audio, start, fps)
		part := len(parts) + 1
		res, err := buildTrimmed(ctx, frames[start:], audio[a:], fps, quality, maxMB, func(pass, done int) {
			if progress != nil {
				progress(part, pass, start+done, len(frames))
			}
		})
		if err != nil {
			return nil, fmt.Errorf("part %d: %w", part, err)
		}
		res.FramesTotal = res.FramesUsed
		parts = append(parts, Part{res, start})
		start += res.FramesUsed
	}
	return parts, nil
}
