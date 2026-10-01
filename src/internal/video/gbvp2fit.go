package video

import (
	"errors"
	"fmt"
	"image"
)

// audioLen is the length of the PCM (stereo u8) that plays alongside the
// first n frames at fps.
func audioLen(audio []byte, n int, fps float64) int {
	return min(int(float64(n)/fps*gbvp2AudioRate)*2, len(audio))
}

// FitProgress reports a Trim/Split encode: each part may take several encode
// passes, each restarting at the part's first frame. Done and Total count
// frames of the whole clip.
type FitProgress func(part, pass, done, total int)

// BuildGBVP2Trimmed encodes the longest start of the clip, video and audio cut
// together, that fits into maxMB. A clip that fits is encoded exactly as by
// BuildGBVP2ROM. FramesTotal is the whole clip, so Truncated reports a trim.
func BuildGBVP2Trimmed(dir, playerPath string, frames []image.Image, audio []byte, fps float64, quality, maxMB int, progress FitProgress) (GBVP2Result, error) {
	return buildTrimmed(dir, playerPath, frames, audio, fps, quality, maxMB, func(pass, done int) {
		if progress != nil {
			progress(1, pass, done, len(frames))
		}
	})
}

func buildTrimmed(dir, playerPath string, frames []image.Image, audio []byte, fps float64, quality, maxMB int, report func(pass, done int)) (GBVP2Result, error) {
	cache := NewGBVP2Cache()
	pass := 0
	build := func(fr []image.Image, au []byte) (GBVP2Result, error) {
		pass++
		p := pass
		return BuildGBVP2ROM(dir, playerPath, fr, au, fps, quality, maxMB, cache, func(done, _ int) { report(p, done) })
	}

	res, err := build(frames, audio)
	if err == nil && !res.Truncated() {
		return res, nil
	}
	if err != nil && !errors.Is(err, ErrAudioOverflow) {
		return res, err
	}

	// Without audio the most frames fit; from there shrink until the frames
	// and their own stretch of audio fit together. Every truncated attempt
	// uses fewer frames than it was given, so this terminates.
	res, err = build(frames, nil)
	if err != nil {
		return res, err
	}
	n := res.FramesUsed
	for n > 0 {
		res, err = build(frames[:n], audio[:audioLen(audio, n, fps)])
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
	return GBVP2Result{}, fmt.Errorf("not even one frame fits into %d MB", maxMB)
}

// GBVP2Part is one ROM of a clip split by BuildGBVP2Parts.
type GBVP2Part struct {
	GBVP2Result
	FirstFrame int
}

// BuildGBVP2Parts splits the clip into consecutive ROMs that each fit into
// maxMB, every part carrying its own stretch of the audio.
func BuildGBVP2Parts(dir, playerPath string, frames []image.Image, audio []byte, fps float64, quality, maxMB int, progress FitProgress) ([]GBVP2Part, error) {
	var parts []GBVP2Part
	for start := 0; start < len(frames); {
		a := audioLen(audio, start, fps)
		part := len(parts) + 1
		res, err := buildTrimmed(dir, playerPath, frames[start:], audio[a:], fps, quality, maxMB, func(pass, done int) {
			if progress != nil {
				progress(part, pass, start+done, len(frames))
			}
		})
		if err != nil {
			return nil, fmt.Errorf("part %d: %w", part, err)
		}
		res.FramesTotal = res.FramesUsed
		parts = append(parts, GBVP2Part{res, start})
		start += res.FramesUsed
	}
	return parts, nil
}
