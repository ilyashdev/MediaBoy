package video

import (
	"context"
	"fmt"
	"image"
	"path/filepath"
	"runtime"
	"sync"

	"MediaBoy/internal/core"
	"MediaBoy/internal/ffmpeg"
	"MediaBoy/internal/imaging"
	"MediaBoy/internal/safe"
)

const audioRate = 9198

// previewMaxSide bounds the editor preview frames; the crop is still expressed
// in source pixels and applied by ffmpeg at compile time.
const previewMaxSide = 640

// ExtractPreview decodes the clip at fps into small preview frames for the
// editor and returns the full source frame size the crop is expressed in.
func ExtractPreview(path string, fps int, progress func(done, total int)) ([]*image.RGBA, image.Point, error) {
	size, err := ffmpeg.ProbeFrameSize(path)
	if err != nil {
		return nil, image.Point{}, err
	}
	vf := fmt.Sprintf("fps=%d,scale=w='min(iw,%d)':h='min(ih,%d)':force_original_aspect_ratio=decrease:flags=area",
		fps, previewMaxSide, previewMaxSide)
	total := estimateFrames(path, fps)
	var frames []*image.RGBA
	err = ffmpeg.StreamFrames(path, vf, func(img *image.RGBA) error {
		frames = append(frames, img)
		if progress != nil {
			progress(len(frames), max(total, len(frames)))
		}
		return nil
	})
	if err != nil {
		return nil, image.Point{}, err
	}
	if len(frames) == 0 {
		return nil, image.Point{}, fmt.Errorf("no frames extracted from %s", filepath.Base(path))
	}
	return frames, size, nil
}

// ExtractGBFrames decodes the clip at fps with ffmpeg doing the crop (and the
// downscale, where it has an equivalent of cfg.Scaling), then runs the rest of
// the GB pipeline on every frame. srcSize is the source frame size, used for
// the automatic crop when none is drawn. Cancelling ctx stops ffmpeg and
// returns ctx's error.
func ExtractGBFrames(ctx context.Context, path string, fps int, cfg core.ConvertConfig, srcSize image.Point, progress func(done, total int)) ([]image.Image, error) {
	crop := image.Rectangle{Max: srcSize}
	switch {
	case cfg.CropEnabled && !cfg.CropRect.Empty():
		crop = cfg.CropRect.Intersect(crop)
	case !cfg.Letterbox:
		crop = imaging.AutoGBCropRect(crop)
	}
	if crop.Empty() {
		return nil, fmt.Errorf("empty crop")
	}
	vf := fmt.Sprintf("fps=%d,crop=%d:%d:%d:%d:exact=1", fps, crop.Dx(), crop.Dy(), crop.Min.X, crop.Min.Y)
	switch {
	case cfg.Letterbox:
		// ffmpeg scales the picture to its place on screen (median has no
		// ffmpeg equivalent: area); the pipeline adds the bars.
		r := imaging.LetterboxRect(crop.Dx(), crop.Dy())
		flags := "area"
		if cfg.Scaling == core.ScalingNearest {
			flags = "neighbor"
		}
		vf += fmt.Sprintf(",scale=%d:%d:flags=%s", r.Dx(), r.Dy(), flags)
	case cfg.Scaling == core.ScalingBilinear:
		vf += fmt.Sprintf(",scale=%d:%d:flags=area", core.ScreenW, core.ScreenH)
	case cfg.Scaling == core.ScalingNearest:
		vf += fmt.Sprintf(",scale=%d:%d:flags=neighbor", core.ScreenW, core.ScreenH)
	}
	// The frames are already cropped; the pipeline's auto crop covers them whole.
	cfg.CropEnabled, cfg.CropRect = false, image.Rectangle{}

	total := estimateFrames(path, fps)
	var (
		mu       sync.Mutex
		out      []image.Image
		done     int
		firstErr error
		wg       sync.WaitGroup
	)
	type job struct {
		i   int
		img *image.RGBA
	}
	jobs := make(chan job, runtime.NumCPU())
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				// A panic becomes the job's error: a dead worker would leave
				// the decoder blocked on the jobs channel.
				var p image.Image
				err := safe.Call(func() (err error) {
					p, err = imaging.RunGBPipelineFrame(j.img, cfg)
					return err
				})
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = err
				}
				out[j.i] = p
				done++
				d := done
				mu.Unlock()
				if progress != nil {
					progress(d, max(total, d))
				}
			}
		}()
	}
	err := ffmpeg.StreamFrames(path, vf, func(img *image.RGBA) error {
		if err := ctx.Err(); err != nil {
			return err // stops and kills ffmpeg
		}
		mu.Lock()
		i := len(out)
		out = append(out, nil)
		mu.Unlock()
		jobs <- job{i, img}
		return nil
	})
	close(jobs)
	wg.Wait()
	if err != nil {
		return nil, err
	}
	if firstErr != nil {
		return nil, firstErr
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no frames extracted from %s", filepath.Base(path))
	}
	return out, nil
}

// estimateFrames is the expected frame count for progress reporting (0 if unknown).
func estimateFrames(path string, fps int) int {
	dur, err := ffmpeg.ProbeDuration(path)
	if err != nil || dur <= 0 {
		return 0
	}
	return int(dur*float64(fps) + 0.5)
}

func ExtractAudio(path string) []byte {
	audio, err := ffmpeg.DecodePCMU8(path, audioRate)
	if err != nil || len(audio) < 2 {
		return nil
	}
	return audio
}
